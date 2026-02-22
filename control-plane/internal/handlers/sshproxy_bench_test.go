package handlers

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

// setupBenchTunnel sets up SSH infrastructure with a tunnel to a backend HTTP server.
// Returns the tunnel port and a cleanup function.
func setupBenchTunnel(b *testing.B, backendPort int) (int, func()) {
	b.Helper()

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		b.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		b.Fatalf("parse private key: %v", err)
	}

	addr, sshCleanup := testSSHServer(&testing.T{}, signer.PublicKey())

	host, portStr, _ := net.SplitHostPort(addr)
	var sshPort int
	fmt.Sscanf(portStr, "%d", &sshPort)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))

	_, err = mgr.Connect(context.Background(), 1, host, sshPort)
	if err != nil {
		b.Fatalf("SSH connect: %v", err)
	}

	tm := sshproxy.NewTunnelManager(mgr)

	tunnelPort, err := tm.CreateReverseTunnel(context.Background(), 1, "VNC", backendPort, 0)
	if err != nil {
		b.Fatalf("CreateReverseTunnel: %v", err)
	}

	TunnelMgr = tm

	cleanup := func() {
		TunnelMgr = nil
		tm.StopAll()
		mgr.CloseAll()
		sshCleanup()
	}

	return tunnelPort, cleanup
}

// BenchmarkProxyToLocalPort_Direct measures the raw HTTP proxy latency without SSH tunnel.
// This serves as a baseline for comparison with the tunneled proxy.
func BenchmarkProxyToLocalPort_Direct(b *testing.B) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}))
	defer backend.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		_ = proxyToLocalPort(w, req, port, "test")
		if w.Code != http.StatusOK {
			b.Fatalf("expected 200, got %d", w.Code)
		}
	}
}

// BenchmarkProxyToLocalPort_ViaTunnel measures HTTP proxy latency through an SSH tunnel.
// The difference from BenchmarkProxyToLocalPort_Direct shows the SSH tunnel overhead.
func BenchmarkProxyToLocalPort_ViaTunnel(b *testing.B) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}))
	defer backend.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	var backendPort int
	fmt.Sscanf(portStr, "%d", &backendPort)

	tunnelPort, cleanup := setupBenchTunnel(b, backendPort)
	defer cleanup()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		_ = proxyToLocalPort(w, req, tunnelPort, "test")
		if w.Code != http.StatusOK {
			b.Fatalf("expected 200, got %d", w.Code)
		}
	}
}

// BenchmarkProxyToLocalPort_Concurrent measures concurrent HTTP proxy throughput.
// Multiple goroutines proxy through the same tunnel port simultaneously.
func BenchmarkProxyToLocalPort_Concurrent(b *testing.B) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}))
	defer backend.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	var backendPort int
	fmt.Sscanf(portStr, "%d", &backendPort)

	tunnelPort, cleanup := setupBenchTunnel(b, backendPort)
	defer cleanup()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()
			_ = proxyToLocalPort(w, req, tunnelPort, "test")
			if w.Code != http.StatusOK {
				b.Errorf("expected 200, got %d", w.Code)
			}
		}
	})
}

// BenchmarkWebsocketProxy measures WebSocket round-trip latency through the proxy.
func BenchmarkWebsocketProxy(b *testing.B) {
	echoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer c.CloseNow()

		ctx := r.Context()
		for {
			msgType, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if err := c.Write(ctx, msgType, data); err != nil {
				return
			}
		}
	}))
	defer echoServer.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(echoServer.URL, "http://"))
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		websocketProxyToLocalPort(w, r, port, "")
	}))
	defer proxyServer.Close()

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			cancel()
			b.Fatalf("dial: %v", err)
		}

		err = conn.Write(ctx, websocket.MessageText, []byte("bench"))
		if err != nil {
			conn.CloseNow()
			cancel()
			b.Fatalf("write: %v", err)
		}

		_, _, err = conn.Read(ctx)
		if err != nil {
			conn.CloseNow()
			cancel()
			b.Fatalf("read: %v", err)
		}

		conn.Close(websocket.StatusNormalClosure, "")
		cancel()
	}
}

// BenchmarkGetTunnelPort measures the overhead of the getTunnelPort helper.
func BenchmarkGetTunnelPort(b *testing.B) {
	mgr := sshproxy.NewSSHManager(nil, "")
	TunnelMgr = sshproxy.NewTunnelManager(mgr)
	defer func() { TunnelMgr = nil }()

	// This will return an error (no active tunnel), but we're measuring the lookup overhead
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		getTunnelPort(1, "vnc")
	}
}

// TestConcurrentHTTPProxySameInstance verifies that multiple concurrent HTTP requests
// through the same SSH tunnel all succeed without errors.
func TestConcurrentHTTPProxySameInstance(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok:%s", r.URL.Path)
	}))
	defer backend.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	var backendPort int
	fmt.Sscanf(portStr, "%d", &backendPort)

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, cleanup := testSSHServer(t, signer.PublicKey())
	defer cleanup()

	host, sshPortStr, _ := net.SplitHostPort(addr)
	var sshPort int
	fmt.Sscanf(sshPortStr, "%d", &sshPort)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	defer mgr.CloseAll()

	_, err = mgr.Connect(context.Background(), 1, host, sshPort)
	if err != nil {
		t.Fatalf("SSH connect: %v", err)
	}

	tm := sshproxy.NewTunnelManager(mgr)
	tunnelPort, err := tm.CreateReverseTunnel(context.Background(), 1, "VNC", backendPort, 0)
	if err != nil {
		t.Fatalf("CreateReverseTunnel: %v", err)
	}

	const numConcurrent = 20
	var wg sync.WaitGroup
	errors := make([]string, 0)
	var mu sync.Mutex

	for i := 0; i < numConcurrent; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := httptest.NewRequest("GET", fmt.Sprintf("/test/%d", idx), nil)
			w := httptest.NewRecorder()

			_ = proxyToLocalPort(w, req, tunnelPort, fmt.Sprintf("test/%d", idx))

			if w.Code != http.StatusOK {
				mu.Lock()
				errors = append(errors, fmt.Sprintf("request %d: expected 200, got %d", idx, w.Code))
				mu.Unlock()
				return
			}

			body, _ := io.ReadAll(w.Result().Body)
			expected := fmt.Sprintf("ok:/test/%d", idx)
			if string(body) != expected {
				mu.Lock()
				errors = append(errors, fmt.Sprintf("request %d: expected %q, got %q", idx, expected, string(body)))
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	for _, e := range errors {
		t.Error(e)
	}

	t.Logf("All %d concurrent requests through SSH tunnel succeeded", numConcurrent)
}

// TestConcurrentWebSocketProxySameInstance verifies that multiple concurrent WebSocket
// connections through the same SSH tunnel work correctly.
func TestConcurrentWebSocketProxySameInstance(t *testing.T) {
	echoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer c.CloseNow()

		ctx := r.Context()
		for {
			msgType, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if err := c.Write(ctx, msgType, append([]byte("echo:"), data...)); err != nil {
				return
			}
		}
	}))
	defer echoServer.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(echoServer.URL, "http://"))
	var backendPort int
	fmt.Sscanf(portStr, "%d", &backendPort)

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, cleanup := testSSHServer(t, signer.PublicKey())
	defer cleanup()

	host, sshPortStr, _ := net.SplitHostPort(addr)
	var sshPort int
	fmt.Sscanf(sshPortStr, "%d", &sshPort)

	sshMgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	defer sshMgr.CloseAll()

	_, err = sshMgr.Connect(context.Background(), 1, host, sshPort)
	if err != nil {
		t.Fatalf("SSH connect: %v", err)
	}

	tm := sshproxy.NewTunnelManager(sshMgr)
	tunnelPort, err := tm.CreateReverseTunnel(context.Background(), 1, "VNC", backendPort, 0)
	if err != nil {
		t.Fatalf("CreateReverseTunnel: %v", err)
	}

	// Create proxy server
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		websocketProxyToLocalPort(w, r, tunnelPort, "")
	}))
	defer proxyServer.Close()

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)

	const numConcurrent = 10
	var wg sync.WaitGroup
	errs := make([]string, 0)
	var mu sync.Mutex

	for i := 0; i < numConcurrent; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			conn, _, err := websocket.Dial(ctx, wsURL, nil)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("ws %d dial: %v", idx, err))
				mu.Unlock()
				return
			}
			defer conn.CloseNow()

			msg := fmt.Sprintf("msg-%d", idx)
			if err := conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("ws %d write: %v", idx, err))
				mu.Unlock()
				return
			}

			_, data, err := conn.Read(ctx)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("ws %d read: %v", idx, err))
				mu.Unlock()
				return
			}

			expected := "echo:" + msg
			if string(data) != expected {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("ws %d: expected %q, got %q", idx, expected, string(data)))
				mu.Unlock()
			}

			conn.Close(websocket.StatusNormalClosure, "")
		}(i)
	}

	wg.Wait()

	for _, e := range errs {
		t.Error(e)
	}

	t.Logf("All %d concurrent WebSocket connections through SSH tunnel succeeded", numConcurrent)
}

// TestMultiInstanceHTTPProxy verifies that HTTP proxy requests to different instances
// route to the correct backend through their respective SSH tunnels.
func TestMultiInstanceHTTPProxy(t *testing.T) {
	const numInstances = 3

	// Create separate backends for each instance
	backends := make([]*httptest.Server, numInstances)
	backendPorts := make([]int, numInstances)

	for i := 0; i < numInstances; i++ {
		idx := i
		backends[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "instance-%d", idx+1)
		}))
		defer backends[i].Close()

		_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(backends[i].URL, "http://"))
		fmt.Sscanf(portStr, "%d", &backendPorts[i])
	}

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, cleanup := testSSHServer(t, signer.PublicKey())
	defer cleanup()

	host, sshPortStr, _ := net.SplitHostPort(addr)
	var sshPort int
	fmt.Sscanf(sshPortStr, "%d", &sshPort)

	sshMgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	defer sshMgr.CloseAll()

	tm := sshproxy.NewTunnelManager(sshMgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	tunnelPorts := make([]int, numInstances)
	for i := 0; i < numInstances; i++ {
		id := uint(i + 1)
		_, err := sshMgr.Connect(context.Background(), id, host, sshPort)
		if err != nil {
			t.Fatalf("SSH connect instance %d: %v", id, err)
		}
		tunnelPorts[i], err = tm.CreateReverseTunnel(context.Background(), id, "VNC", backendPorts[i], 0)
		if err != nil {
			t.Fatalf("CreateReverseTunnel instance %d: %v", id, err)
		}
	}

	// Send concurrent requests to all instances
	var wg sync.WaitGroup
	for i := 0; i < numInstances; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()

			_ = proxyToLocalPort(w, req, tunnelPorts[idx], "test")

			if w.Code != http.StatusOK {
				t.Errorf("instance %d: expected 200, got %d", idx+1, w.Code)
				return
			}

			body := w.Body.String()
			expected := fmt.Sprintf("instance-%d", idx+1)
			if body != expected {
				t.Errorf("instance %d: expected %q, got %q", idx+1, expected, body)
			}
		}()
	}

	wg.Wait()

	t.Logf("All %d instances routed correctly through their SSH tunnels", numInstances)
}
