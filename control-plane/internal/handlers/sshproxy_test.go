package handlers

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

// --- getTunnelPort tests ---

func TestGetTunnelPort_NilTunnelMgr(t *testing.T) {
	TunnelMgr = nil

	_, err := getTunnelPort(1, "vnc")
	if err == nil {
		t.Fatal("expected error when TunnelMgr is nil")
	}
	if !strings.Contains(err.Error(), "tunnel manager not initialized") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetTunnelPort_UnknownServiceType(t *testing.T) {
	mgr := sshproxy.NewSSHManager(nil, "")
	TunnelMgr = sshproxy.NewTunnelManager(mgr)
	defer func() { TunnelMgr = nil }()

	_, err := getTunnelPort(1, "unknown")
	if err == nil {
		t.Fatal("expected error for unknown service type")
	}
	if !strings.Contains(err.Error(), "unknown service type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetTunnelPort_NoActiveTunnel(t *testing.T) {
	mgr := sshproxy.NewSSHManager(nil, "")
	TunnelMgr = sshproxy.NewTunnelManager(mgr)
	defer func() { TunnelMgr = nil }()

	_, err := getTunnelPort(1, "vnc")
	if err == nil {
		t.Fatal("expected error when no tunnel exists")
	}
	if !strings.Contains(err.Error(), "no active vnc tunnel") {
		t.Errorf("unexpected error: %v", err)
	}

	_, err = getTunnelPort(1, "gateway")
	if err == nil {
		t.Fatal("expected error when no tunnel exists")
	}
	if !strings.Contains(err.Error(), "no active gateway tunnel") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetTunnelPort_ActiveTunnel(t *testing.T) {
	setupTestDB(t)

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

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	defer mgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")

	_, err = mgr.Connect(context.Background(), inst.ID, host, port)
	if err != nil {
		t.Fatalf("SSH connect: %v", err)
	}

	tm := sshproxy.NewTunnelManager(mgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	vncPort, err := tm.CreateTunnelForVNC(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("create VNC tunnel: %v", err)
	}

	gwPort, err := tm.CreateTunnelForGateway(context.Background(), inst.ID, 0)
	if err != nil {
		t.Fatalf("create Gateway tunnel: %v", err)
	}

	// Test VNC lookup
	gotPort, err := getTunnelPort(inst.ID, "vnc")
	if err != nil {
		t.Fatalf("getTunnelPort vnc: %v", err)
	}
	if gotPort != vncPort {
		t.Errorf("expected VNC port %d, got %d", vncPort, gotPort)
	}

	// Test Gateway lookup
	gotPort, err = getTunnelPort(inst.ID, "gateway")
	if err != nil {
		t.Fatalf("getTunnelPort gateway: %v", err)
	}
	if gotPort != gwPort {
		t.Errorf("expected Gateway port %d, got %d", gwPort, gotPort)
	}

	// Test case-insensitivity
	gotPort, err = getTunnelPort(inst.ID, "VNC")
	if err != nil {
		t.Fatalf("getTunnelPort VNC (uppercase): %v", err)
	}
	if gotPort != vncPort {
		t.Errorf("expected VNC port %d, got %d", vncPort, gotPort)
	}
}

// --- proxyToLocalPort tests ---

func TestProxyToLocalPort_Success(t *testing.T) {
	// Start a local HTTP server to proxy to
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "hello from backend, path=%s, query=%s", r.URL.Path, r.URL.RawQuery)
	}))
	defer backend.Close()

	// Extract port from the backend URL
	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	req := httptest.NewRequest("GET", "/api/v1/instances/1/desktop/some/path?foo=bar", nil)
	w := httptest.NewRecorder()

	if err := proxyToLocalPort(w, req, port, "some/path"); err != nil {
		t.Fatalf("proxyToLocalPort returned error: %v", err)
	}

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hello from backend") {
		t.Errorf("unexpected body: %s", string(body))
	}
	if !strings.Contains(string(body), "path=/some/path") {
		t.Errorf("expected path in body, got: %s", string(body))
	}
	if !strings.Contains(string(body), "query=foo=bar") {
		t.Errorf("expected query in body, got: %s", string(body))
	}

	if ct := resp.Header.Get("Content-Type"); ct != "text/plain" {
		t.Errorf("expected Content-Type text/plain, got %s", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("expected Cache-Control no-cache, got %s", cc)
	}
}

func TestProxyToLocalPort_ForwardsHeaders(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo back the received Accept header
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"accept":"%s","content_type":"%s"}`, r.Header.Get("Accept"), r.Header.Get("Content-Type"))
	}))
	defer backend.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	req := httptest.NewRequest("POST", "/test", strings.NewReader("body"))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := proxyToLocalPort(w, req, port, "test"); err != nil {
		t.Fatalf("proxyToLocalPort returned error: %v", err)
	}

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"accept":"application/json"`) {
		t.Errorf("Accept header not forwarded, body: %s", string(body))
	}
	if !strings.Contains(string(body), `"content_type":"application/json"`) {
		t.Errorf("Content-Type header not forwarded, body: %s", string(body))
	}
}

func TestProxyToLocalPort_BackendDown(t *testing.T) {
	// Use a port that's not listening
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close() // close immediately so nothing is listening

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	err := proxyToLocalPort(w, req, port, "test")

	if err == nil {
		t.Error("expected non-nil error when backend is down")
	}
}

func TestProxyToLocalPort_StatusCodeForwarded(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "not found")
	}))
	defer backend.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	req := httptest.NewRequest("GET", "/missing", nil)
	w := httptest.NewRecorder()

	if err := proxyToLocalPort(w, req, port, "missing"); err != nil {
		t.Fatalf("proxyToLocalPort returned error: %v", err)
	}

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

// --- websocketProxyToLocalPort tests ---

func TestWebsocketProxyToLocalPort_Success(t *testing.T) {
	// Start a WebSocket echo server
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
			// Echo back with prefix
			if err := c.Write(ctx, msgType, append([]byte("echo:"), data...)); err != nil {
				return
			}
		}
	}))
	defer echoServer.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(echoServer.URL, "http://"))
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	// Create a proxy server that uses websocketProxyToLocalPort
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		websocketProxyToLocalPort(w, r, port, "")
	}))
	defer proxyServer.Close()

	// Connect as a WebSocket client to the proxy
	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	// Send a message
	err = conn.Write(ctx, websocket.MessageText, []byte("hello"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read echo response
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if string(data) != "echo:hello" {
		t.Errorf("expected 'echo:hello', got '%s'", string(data))
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestWebsocketProxyToLocalPort_BinaryMessages(t *testing.T) {
	// Start a WebSocket server that echoes binary messages
	echoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer c.CloseNow()

		ctx := r.Context()
		msgType, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		c.Write(ctx, msgType, data)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	binaryData := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE}
	err = conn.Write(ctx, websocket.MessageBinary, binaryData)
	if err != nil {
		t.Fatalf("write binary: %v", err)
	}

	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if msgType != websocket.MessageBinary {
		t.Errorf("expected binary message type, got %v", msgType)
	}
	if string(data) != string(binaryData) {
		t.Errorf("binary data mismatch")
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestWebsocketProxyToLocalPort_BackendDown(t *testing.T) {
	// Use a port that's not listening
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		websocketProxyToLocalPort(w, r, port, "")
	}))
	defer proxyServer.Close()

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		// Connection might fail at dial or after accept
		return
	}
	defer conn.CloseNow()

	// The proxy should close the connection with an error status
	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected error reading from proxy with dead backend")
	}
}

func TestWebsocketProxyToLocalPort_PathAndQuery(t *testing.T) {
	// Server that echoes the request path and query
	echoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer c.CloseNow()

		ctx := r.Context()
		msg := fmt.Sprintf("path=%s query=%s", r.URL.Path, r.URL.RawQuery)
		c.Write(ctx, websocket.MessageText, []byte(msg))

		// Wait for close
		c.Read(ctx)
	}))
	defer echoServer.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(echoServer.URL, "http://"))
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		websocketProxyToLocalPort(w, r, port, "some/path")
	}))
	defer proxyServer.Close()

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1) + "?key=value"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	msg := string(data)
	if !strings.Contains(msg, "path=/some/path") {
		t.Errorf("expected path=/some/path in message, got: %s", msg)
	}
	if !strings.Contains(msg, "query=key=value") {
		t.Errorf("expected query=key=value in message, got: %s", msg)
	}

	conn.Close(websocket.StatusNormalClosure, "")
}
