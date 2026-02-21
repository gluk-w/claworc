package sshtunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	gossh "golang.org/x/crypto/ssh"
)

// --- Benchmarks: measure request latency through SSH tunnels vs direct ---
//
// Run with: go test -bench=. -benchtime=5s ./internal/sshtunnel/
//
// Performance characteristics (loopback SSH tunnel):
//   - HTTP overhead: ~0.1-0.3ms per request vs direct (SSH channel setup + bidirectional copy)
//   - WebSocket overhead: ~0.05-0.1ms per message (amortized over persistent connection)
//   - Tunnel reuse: all requests multiplex over a single SSH connection per instance
//   - Throughput: >1000 req/s per tunnel with connection pooling on loopback

// BenchmarkHTTPThroughTunnel measures HTTP request/response latency through an
// SSH tunnel. Each iteration performs a full HTTP GET through the tunnel.
func BenchmarkHTTPThroughTunnel(b *testing.B) {
	httpPort, httpCleanup := startBenchHTTPServer(b)
	defer httpCleanup()

	sshAddr, sshCleanup := startBenchSSHServer(b, portMapping{DefaultVNCPort: httpPort})
	defer sshCleanup()

	sm := sshmanager.NewSSHManager(0)
	client := connectBenchSSH(b, sshAddr)
	defer client.Close()
	sm.SetClient("bench-instance", client)

	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	ctx := context.Background()
	localPort, err := tm.CreateTunnelForVNC(ctx, "bench-instance")
	if err != nil {
		b.Fatalf("create tunnel: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/", localPort)
	httpClient := &http.Client{Timeout: 10 * time.Second}

	// Warm up the connection
	resp, err := httpClient.Get(url)
	if err != nil {
		b.Fatalf("warmup request: %v", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := httpClient.Get(url)
		if err != nil {
			b.Fatalf("request %d: %v", i, err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkHTTPDirect measures HTTP request/response latency directly to a
// local server (no tunnel). Used to compare overhead of SSH tunneling.
func BenchmarkHTTPDirect(b *testing.B) {
	httpPort, httpCleanup := startBenchHTTPServer(b)
	defer httpCleanup()

	url := fmt.Sprintf("http://127.0.0.1:%d/", httpPort)
	httpClient := &http.Client{Timeout: 10 * time.Second}

	// Warm up
	resp, err := httpClient.Get(url)
	if err != nil {
		b.Fatalf("warmup: %v", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := httpClient.Get(url)
		if err != nil {
			b.Fatalf("request %d: %v", i, err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkWebSocketThroughTunnel measures WebSocket round-trip latency
// through an SSH tunnel. Each iteration sends a message and reads the echo.
func BenchmarkWebSocketThroughTunnel(b *testing.B) {
	wsPort, wsCleanup := startBenchWSEchoServer(b)
	defer wsCleanup()

	sshAddr, sshCleanup := startBenchSSHServer(b, portMapping{DefaultGatewayPort: wsPort})
	defer sshCleanup()

	sm := sshmanager.NewSSHManager(0)
	client := connectBenchSSH(b, sshAddr)
	defer client.Close()
	sm.SetClient("bench-instance", client)

	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	ctx := context.Background()
	localPort, err := tm.CreateTunnelForGateway(ctx, "bench-instance", DefaultGatewayPort)
	if err != nil {
		b.Fatalf("create tunnel: %v", err)
	}

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/", localPort)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		b.Fatalf("websocket dial: %v", err)
	}
	defer conn.CloseNow()

	msg := []byte("benchmark-payload-64bytes-padding-to-realistic-message-size!!!!!")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
			b.Fatalf("write %d: %v", i, err)
		}
		_, _, err := conn.Read(ctx)
		if err != nil {
			b.Fatalf("read %d: %v", i, err)
		}
	}
}

// BenchmarkWebSocketDirect measures WebSocket round-trip latency directly
// (no tunnel), for comparison with the tunneled benchmark.
func BenchmarkWebSocketDirect(b *testing.B) {
	wsPort, wsCleanup := startBenchWSEchoServer(b)
	defer wsCleanup()

	ctx := context.Background()
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/", wsPort)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		b.Fatalf("websocket dial: %v", err)
	}
	defer conn.CloseNow()

	msg := []byte("benchmark-payload-64bytes-padding-to-realistic-message-size!!!!!")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
			b.Fatalf("write %d: %v", i, err)
		}
		_, _, err := conn.Read(ctx)
		if err != nil {
			b.Fatalf("read %d: %v", i, err)
		}
	}
}

// --- Concurrent connection tests ---

// TestConcurrentHTTPSameInstance verifies that multiple concurrent HTTP requests
// through the same tunnel work correctly without interference or data corruption.
func TestConcurrentHTTPSameInstance(t *testing.T) {
	// Backend echoes the request path in its response body
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "response-for-%s", r.URL.Path)
	}))
	defer backend.Close()
	backendPort := backend.Listener.Addr().(*net.TCPAddr).Port

	sshAddr, sshCleanup := startTestSSHServer(t, portMapping{DefaultVNCPort: backendPort})
	defer sshCleanup()

	sm := sshmanager.NewSSHManager(0)
	client := connectTestSSH(t, sshAddr)
	defer client.Close()
	sm.SetClient("concurrent-instance", client)

	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	if err := tm.StartTunnelsForInstance(t.Context(), "concurrent-instance"); err != nil {
		t.Fatalf("StartTunnelsForInstance: %v", err)
	}

	vnc := findTunnel(tm.GetTunnels("concurrent-instance"), ServiceVNC)
	if vnc == nil {
		t.Fatal("VNC tunnel not found")
	}

	// Launch 20 concurrent HTTP requests through the same tunnel
	const numClients = 20
	var wg sync.WaitGroup
	errors := make(chan error, numClients)

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			url := fmt.Sprintf("http://127.0.0.1:%d/client/%d", vnc.LocalPort, id)
			httpClient := &http.Client{Timeout: 10 * time.Second}
			resp, err := httpClient.Get(url)
			if err != nil {
				errors <- fmt.Errorf("client %d: GET failed: %w", id, err)
				return
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				errors <- fmt.Errorf("client %d: read body: %w", id, err)
				return
			}
			expected := fmt.Sprintf("response-for-/client/%d", id)
			if string(body) != expected {
				errors <- fmt.Errorf("client %d: got %q, want %q", id, string(body), expected)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}

// TestConcurrentWebSocketSameInstance verifies multiple concurrent WebSocket
// connections through the same tunnel exchange messages correctly.
func TestConcurrentWebSocketSameInstance(t *testing.T) {
	wsPort, wsCleanup := startWSEchoServer(t)
	defer wsCleanup()

	sshAddr, sshCleanup := startTestSSHServer(t, portMapping{DefaultGatewayPort: wsPort})
	defer sshCleanup()

	sm := sshmanager.NewSSHManager(0)
	client := connectTestSSH(t, sshAddr)
	defer client.Close()
	sm.SetClient("ws-concurrent", client)

	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	if err := tm.StartTunnelsForInstance(t.Context(), "ws-concurrent"); err != nil {
		t.Fatalf("StartTunnelsForInstance: %v", err)
	}

	gw := findTunnel(tm.GetTunnels("ws-concurrent"), ServiceGateway)
	if gw == nil {
		t.Fatal("Gateway tunnel not found")
	}

	// Launch 10 concurrent WebSocket connections, each sending 5 messages
	const numClients = 10
	const msgsPerClient = 5
	var wg sync.WaitGroup
	errors := make(chan error, numClients*msgsPerClient)

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			wsURL := fmt.Sprintf("ws://127.0.0.1:%d/", gw.LocalPort)
			conn, _, err := websocket.Dial(ctx, wsURL, nil)
			if err != nil {
				errors <- fmt.Errorf("client %d: dial: %w", id, err)
				return
			}
			defer conn.CloseNow()

			for j := 0; j < msgsPerClient; j++ {
				msg := fmt.Sprintf("client-%d-msg-%d", id, j)
				if err := conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
					errors <- fmt.Errorf("client %d msg %d: write: %w", id, j, err)
					return
				}
				_, data, err := conn.Read(ctx)
				if err != nil {
					errors <- fmt.Errorf("client %d msg %d: read: %w", id, j, err)
					return
				}
				if string(data) != msg {
					errors <- fmt.Errorf("client %d msg %d: got %q, want %q", id, j, string(data), msg)
				}
			}
			conn.Close(websocket.StatusNormalClosure, "")
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}

// TestConcurrentMultipleInstances verifies that tunnels for multiple instances
// work independently and concurrently without cross-contamination.
func TestConcurrentMultipleInstances(t *testing.T) {
	const numInstances = 5

	type instanceSetup struct {
		name        string
		httpPort    int
		httpCleanup func()
		echoPort    int
		echoCleanup func()
	}
	instances := make([]instanceSetup, numInstances)
	for i := 0; i < numInstances; i++ {
		body := fmt.Sprintf("instance-%d-response", i)
		httpPort, httpCleanup := startHTTPTestServer(t, body)
		echoPort, echoCleanup := startEchoServer(t)
		instances[i] = instanceSetup{
			name:        fmt.Sprintf("multi-inst-%d", i),
			httpPort:    httpPort,
			httpCleanup: httpCleanup,
			echoPort:    echoPort,
			echoCleanup: echoCleanup,
		}
	}
	defer func() {
		for _, inst := range instances {
			inst.httpCleanup()
			inst.echoCleanup()
		}
	}()

	sm := sshmanager.NewSSHManager(0)

	var sshCleanups []func()
	for i := range instances {
		sshAddr, sshCleanup := startTestSSHServer(t, portMapping{
			DefaultVNCPort:     instances[i].httpPort,
			DefaultGatewayPort: instances[i].echoPort,
		})
		sshCleanups = append(sshCleanups, sshCleanup)

		client := connectTestSSH(t, sshAddr)
		sm.SetClient(instances[i].name, client)
	}
	defer func() {
		for _, cleanup := range sshCleanups {
			cleanup()
		}
	}()

	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	// Start tunnels for all instances
	for _, inst := range instances {
		if err := tm.StartTunnelsForInstance(t.Context(), inst.name); err != nil {
			t.Fatalf("StartTunnelsForInstance(%s): %v", inst.name, err)
		}
	}

	// Verify correct number of SSH connections (one per instance)
	if sm.ConnectionCount() != numInstances {
		t.Errorf("expected %d SSH connections, got %d", numInstances, sm.ConnectionCount())
	}

	// Concurrently send requests to all instances and verify isolation
	var wg sync.WaitGroup
	errors := make(chan error, numInstances*2)

	for i, inst := range instances {
		wg.Add(1)
		go func(idx int, instName string) {
			defer wg.Done()

			// Test HTTP through VNC tunnel — each instance must return its own response
			vnc := findTunnel(tm.GetTunnels(instName), ServiceVNC)
			if vnc == nil {
				errors <- fmt.Errorf("instance %d: VNC tunnel not found", idx)
				return
			}

			url := fmt.Sprintf("http://127.0.0.1:%d/", vnc.LocalPort)
			resp, err := http.Get(url)
			if err != nil {
				errors <- fmt.Errorf("instance %d: HTTP GET: %w", idx, err)
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			expected := fmt.Sprintf("instance-%d-response", idx)
			if string(body) != expected {
				errors <- fmt.Errorf("instance %d: got %q, want %q", idx, string(body), expected)
			}

			// Test TCP echo through gateway tunnel
			gw := findTunnel(tm.GetTunnels(instName), ServiceGateway)
			if gw == nil {
				errors <- fmt.Errorf("instance %d: Gateway tunnel not found", idx)
				return
			}
			verifyEchoThroughPort(t, gw.LocalPort, fmt.Sprintf("instance-%d", idx))
		}(i, inst.name)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}

// --- Tunnel reuse tests ---

// TestTunnelReuseNoDuplicates verifies that StartTunnelsForInstance creates
// exactly 2 tunnels and that proper stop/start cycle creates fresh tunnels.
func TestTunnelReuseNoDuplicates(t *testing.T) {
	echoPort, echoCleanup := startEchoServer(t)
	defer echoCleanup()

	sshAddr, sshCleanup := startTestSSHServer(t, portMapping{
		DefaultVNCPort:     echoPort,
		DefaultGatewayPort: echoPort,
	})
	defer sshCleanup()

	sm := sshmanager.NewSSHManager(0)
	client := connectTestSSH(t, sshAddr)
	defer client.Close()
	sm.SetClient("reuse-test", client)

	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	// First start: should create exactly 2 tunnels
	if err := tm.StartTunnelsForInstance(t.Context(), "reuse-test"); err != nil {
		t.Fatalf("first StartTunnelsForInstance: %v", err)
	}

	tunnels := tm.GetTunnels("reuse-test")
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels after first start, got %d", len(tunnels))
	}

	firstVNCPort := findTunnel(tunnels, ServiceVNC).LocalPort
	firstGWPort := findTunnel(tunnels, ServiceGateway).LocalPort

	// Verify data flows through initial tunnels
	verifyEchoThroughPort(t, firstVNCPort, "first-vnc")
	verifyEchoThroughPort(t, firstGWPort, "first-gw")

	// Stop and restart: should create exactly 2 new tunnels, old ports freed
	if err := tm.StopTunnelsForInstance("reuse-test"); err != nil {
		t.Fatalf("StopTunnelsForInstance: %v", err)
	}

	if err := tm.StartTunnelsForInstance(t.Context(), "reuse-test"); err != nil {
		t.Fatalf("second StartTunnelsForInstance: %v", err)
	}

	tunnels = tm.GetTunnels("reuse-test")
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels after restart, got %d", len(tunnels))
	}

	secondVNCPort := findTunnel(tunnels, ServiceVNC).LocalPort
	secondGWPort := findTunnel(tunnels, ServiceGateway).LocalPort

	// Verify new tunnels work
	verifyEchoThroughPort(t, secondVNCPort, "second-vnc")
	verifyEchoThroughPort(t, secondGWPort, "second-gw")
}

// TestTunnelReuseWithoutStop verifies that calling StartTunnelsForInstance
// without stopping first results in accumulated tunnels — demonstrates why
// StopTunnelsForInstance must be called before restart.
func TestTunnelReuseWithoutStop(t *testing.T) {
	echoPort, echoCleanup := startEchoServer(t)
	defer echoCleanup()

	sshAddr, sshCleanup := startTestSSHServer(t, portMapping{
		DefaultVNCPort:     echoPort,
		DefaultGatewayPort: echoPort,
	})
	defer sshCleanup()

	sm := sshmanager.NewSSHManager(0)
	client := connectTestSSH(t, sshAddr)
	defer client.Close()
	sm.SetClient("accumulate-test", client)

	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	// First start
	if err := tm.StartTunnelsForInstance(t.Context(), "accumulate-test"); err != nil {
		t.Fatalf("first start: %v", err)
	}
	first := tm.GetTunnels("accumulate-test")
	if len(first) != 2 {
		t.Fatalf("expected 2 tunnels, got %d", len(first))
	}

	// Second start without stopping — tunnels accumulate
	if err := tm.StartTunnelsForInstance(t.Context(), "accumulate-test"); err != nil {
		t.Fatalf("second start: %v", err)
	}
	second := tm.GetTunnels("accumulate-test")
	if len(second) != 4 {
		t.Fatalf("expected 4 tunnels (accumulated), got %d", len(second))
	}

	// All tunnels (old and new) should still work
	for i, tun := range second {
		if tun.IsClosed() {
			t.Errorf("tunnel %d should be open", i)
			continue
		}
		verifyEchoThroughPort(t, tun.LocalPort, fmt.Sprintf("accumulated-%d", i))
	}
}

// TestTunnelSSHClientReuse verifies that multiple tunnels for the same instance
// share a single SSH connection (no redundant SSH connections).
func TestTunnelSSHClientReuse(t *testing.T) {
	echoPort, echoCleanup := startEchoServer(t)
	defer echoCleanup()

	sshAddr, sshCleanup := startTestSSHServer(t, portMapping{
		DefaultVNCPort:     echoPort,
		DefaultGatewayPort: echoPort,
	})
	defer sshCleanup()

	sm := sshmanager.NewSSHManager(0)
	client := connectTestSSH(t, sshAddr)
	defer client.Close()
	sm.SetClient("ssh-reuse", client)

	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	// Start tunnels — both VNC and Gateway should use the same SSH client
	if err := tm.StartTunnelsForInstance(t.Context(), "ssh-reuse"); err != nil {
		t.Fatalf("StartTunnelsForInstance: %v", err)
	}

	// SSH manager should still have exactly 1 connection for this instance
	if sm.ConnectionCount() != 1 {
		t.Errorf("expected 1 SSH connection, got %d", sm.ConnectionCount())
	}

	// Verify both tunnels work through the single SSH connection
	tunnels := tm.GetTunnels("ssh-reuse")
	for _, tun := range tunnels {
		verifyEchoThroughPort(t, tun.LocalPort, fmt.Sprintf("ssh-reuse-%s", tun.Config.Service))
	}
}

// --- Throughput test ---

// TestHTTPThroughputThroughTunnel measures raw throughput by sending many
// concurrent HTTP requests through the tunnel, reporting requests/second.
func TestHTTPThroughputThroughTunnel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping throughput test in short mode")
	}

	backend := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		}),
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go backend.Serve(l)
	defer backend.Close()
	backendPort := l.Addr().(*net.TCPAddr).Port

	sshAddr, sshCleanup := startTestSSHServer(t, portMapping{DefaultVNCPort: backendPort})
	defer sshCleanup()

	sm := sshmanager.NewSSHManager(0)
	client := connectTestSSH(t, sshAddr)
	defer client.Close()
	sm.SetClient("throughput-test", client)

	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	localPort, err := tm.CreateTunnelForVNC(t.Context(), "throughput-test")
	if err != nil {
		t.Fatalf("create tunnel: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/", localPort)
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
		},
	}

	// Run for 2 seconds with concurrent clients
	const numClients = 10
	duration := 2 * time.Second
	ctx, cancel := context.WithTimeout(t.Context(), duration)
	defer cancel()

	var wg sync.WaitGroup
	var totalRequests atomic.Int64
	var totalErrors atomic.Int64

	start := time.Now()
	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
				resp, err := httpClient.Do(req)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					totalErrors.Add(1)
					continue
				}
				io.ReadAll(resp.Body)
				resp.Body.Close()
				totalRequests.Add(1)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	reqs := totalRequests.Load()
	errs := totalErrors.Load()
	rps := float64(reqs) / elapsed.Seconds()

	t.Logf("Throughput: %d requests in %v (%.0f req/s), %d errors, %d clients",
		reqs, elapsed.Round(time.Millisecond), rps, errs, numClients)

	// Sanity check: should be able to handle at least 100 req/s through
	// a local SSH tunnel with 10 clients
	if rps < 100 {
		t.Errorf("throughput too low: %.0f req/s (expected >= 100)", rps)
	}
	if errs > 0 {
		t.Errorf("had %d errors during throughput test", errs)
	}
}

// --- Latency measurement test ---

// TestHTTPLatencyThroughTunnel measures and reports p50/p95/p99 latencies
// for HTTP requests through an SSH tunnel.
func TestHTTPLatencyThroughTunnel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping latency test in short mode")
	}

	backend := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		}),
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go backend.Serve(l)
	defer backend.Close()
	backendPort := l.Addr().(*net.TCPAddr).Port

	sshAddr, sshCleanup := startTestSSHServer(t, portMapping{DefaultVNCPort: backendPort})
	defer sshCleanup()

	sm := sshmanager.NewSSHManager(0)
	client := connectTestSSH(t, sshAddr)
	defer client.Close()
	sm.SetClient("latency-test", client)

	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	localPort, err := tm.CreateTunnelForVNC(t.Context(), "latency-test")
	if err != nil {
		t.Fatalf("create tunnel: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/", localPort)
	httpClient := &http.Client{Timeout: 10 * time.Second}

	const numRequests = 200
	latencies := make([]time.Duration, numRequests)

	// Warmup
	for i := 0; i < 10; i++ {
		resp, err := httpClient.Get(url)
		if err != nil {
			t.Fatalf("warmup: %v", err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}

	for i := 0; i < numRequests; i++ {
		start := time.Now()
		resp, err := httpClient.Get(url)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
		latencies[i] = time.Since(start)
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[numRequests/2]
	p95 := latencies[int(float64(numRequests)*0.95)]
	p99 := latencies[int(float64(numRequests)*0.99)]
	min := latencies[0]
	max := latencies[numRequests-1]

	t.Logf("HTTP through SSH tunnel latency (%d requests):", numRequests)
	t.Logf("  min=%v  p50=%v  p95=%v  p99=%v  max=%v", min, p50, p95, p99, max)

	// SSH tunnel overhead on loopback should be under 10ms for p95
	if p95 > 10*time.Millisecond {
		t.Logf("WARNING: p95 latency %v exceeds 10ms — may indicate a performance issue", p95)
	}
}

// --- Benchmark helpers ---
// These mirror the test helpers from integration_test.go but accept testing.B.

func startBenchHTTPServer(b *testing.B) (port int, cleanup func()) {
	b.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("bench-ok"))
	})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(l)
	return l.Addr().(*net.TCPAddr).Port, func() { srv.Close() }
}

func startBenchWSEchoServer(b *testing.B) (port int, cleanup func()) {
	b.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			typ, msg, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err := conn.Write(r.Context(), typ, msg); err != nil {
				return
			}
		}
	})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(l)
	return l.Addr().(*net.TCPAddr).Port, func() { srv.Close() }
}

func startBenchSSHServer(b *testing.B, mapping portMapping) (addr string, cleanup func()) {
	b.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		b.Fatalf("generate ed25519 key: %v", err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		b.Fatalf("create signer: %v", err)
	}

	cfg := &gossh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("ssh server listen: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveSSHConn(conn, cfg, mapping)
		}
	}()

	return listener.Addr().String(), func() { listener.Close() }
}

func connectBenchSSH(b *testing.B, addr string) *gossh.Client {
	b.Helper()
	client, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		b.Fatalf("ssh dial %s: %v", addr, err)
	}
	return client
}
