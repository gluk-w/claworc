package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
)

// --- getTunnelPort tests ---

func TestGetTunnelPort_InstanceNotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	sshtunnel.InitGlobal()
	defer sshtunnel.ResetGlobalForTest()

	_, err := getTunnelPort(999, "vnc")
	if err == nil {
		t.Fatal("expected error for non-existent instance")
	}
	if !strings.Contains(err.Error(), "instance not found") {
		t.Errorf("expected 'instance not found' error, got: %v", err)
	}
}

func TestGetTunnelPort_NoTunnelManager(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-test", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	sshtunnel.ResetGlobalForTest()

	_, err := getTunnelPort(inst.ID, "vnc")
	if err == nil {
		t.Fatal("expected error when tunnel manager is nil")
	}
	if !strings.Contains(err.Error(), "tunnel manager not available") {
		t.Errorf("expected 'tunnel manager not available' error, got: %v", err)
	}
}

func TestGetTunnelPort_NoActiveTunnel(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-test", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	_, err := getTunnelPort(inst.ID, "vnc")
	if err == nil {
		t.Fatal("expected error when no tunnel exists")
	}
	if !strings.Contains(err.Error(), "no active vnc tunnel") {
		t.Errorf("expected 'no active vnc tunnel' error, got: %v", err)
	}
}

func TestGetTunnelPort_SkipsClosedTunnel(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-closed", DisplayName: "Closed", Status: "running"}
	database.DB.Create(&inst)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-closed", sshtunnel.TestTunnelOpts{
		Service:    "vnc",
		Type:       "reverse",
		LocalPort:  12345,
		RemotePort: 3000,
		Closed:     true,
	})

	_, err := getTunnelPort(inst.ID, "vnc")
	if err == nil {
		t.Fatal("expected error when tunnel is closed")
	}
}

func TestGetTunnelPort_ReturnsVNCPort(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-vnc", DisplayName: "VNC", Status: "running"}
	database.DB.Create(&inst)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-vnc", sshtunnel.TestTunnelOpts{
		Service:    "vnc",
		Type:       "reverse",
		LocalPort:  54321,
		RemotePort: 3000,
	})

	port, err := getTunnelPort(inst.ID, "vnc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if port != 54321 {
		t.Errorf("expected port 54321, got %d", port)
	}
}

func TestGetTunnelPort_ReturnsGatewayPort(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-gw", DisplayName: "GW", Status: "running"}
	database.DB.Create(&inst)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-gw", sshtunnel.TestTunnelOpts{
		Service:    "gateway",
		Type:       "reverse",
		LocalPort:  65432,
		RemotePort: 8080,
	})

	port, err := getTunnelPort(inst.ID, "gateway")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if port != 65432 {
		t.Errorf("expected port 65432, got %d", port)
	}
}

func TestGetTunnelPort_CorrectServiceMatch(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-multi", DisplayName: "Multi", Status: "running"}
	database.DB.Create(&inst)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-multi", sshtunnel.TestTunnelOpts{
		Service:    "vnc",
		Type:       "reverse",
		LocalPort:  11111,
		RemotePort: 3000,
	})
	sshtunnel.AddTestTunnel(tm, "bot-multi", sshtunnel.TestTunnelOpts{
		Service:    "gateway",
		Type:       "reverse",
		LocalPort:  22222,
		RemotePort: 8080,
	})

	vncPort, err := getTunnelPort(inst.ID, "vnc")
	if err != nil {
		t.Fatalf("unexpected error for vnc: %v", err)
	}
	if vncPort != 11111 {
		t.Errorf("expected vnc port 11111, got %d", vncPort)
	}

	gwPort, err := getTunnelPort(inst.ID, "gateway")
	if err != nil {
		t.Fatalf("unexpected error for gateway: %v", err)
	}
	if gwPort != 22222 {
		t.Errorf("expected gateway port 22222, got %d", gwPort)
	}
}

// --- proxyToLocalPort tests ---

func TestProxyToLocalPort_Success(t *testing.T) {
	// Start a local HTTP server to act as the tunnel endpoint
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	defer backend.Close()

	// Extract port from the test server
	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("GET", "/test/path", nil)
	w := httptest.NewRecorder()

	proxyToLocalPort(w, req, port, "test/path")

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected application/json, got %s", w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestProxyToLocalPort_ForwardsQueryString(t *testing.T) {
	var receivedQuery string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("GET", "/test?foo=bar&baz=1", nil)
	w := httptest.NewRecorder()

	proxyToLocalPort(w, req, port, "test")

	if receivedQuery != "foo=bar&baz=1" {
		t.Errorf("expected query 'foo=bar&baz=1', got %q", receivedQuery)
	}
}

func TestProxyToLocalPort_ForwardsHeaders(t *testing.T) {
	var receivedAccept string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAccept = r.Header.Get("Accept")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()

	proxyToLocalPort(w, req, port, "test")

	if receivedAccept != "text/html" {
		t.Errorf("expected Accept 'text/html', got %q", receivedAccept)
	}
}

func TestProxyToLocalPort_BackendError(t *testing.T) {
	// Use a port that nothing is listening on
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	proxyToLocalPort(w, req, 1, "test")

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", w.Code)
	}
}

func TestProxyToLocalPort_ForwardsResponseStatus(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "not found")
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("GET", "/missing", nil)
	w := httptest.NewRecorder()

	proxyToLocalPort(w, req, port, "missing")

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestProxyToLocalPort_PostRequest(t *testing.T) {
	var receivedBody string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("POST", "/submit", strings.NewReader("hello"))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()

	proxyToLocalPort(w, req, port, "submit")

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", w.Code)
	}
	if receivedBody != "hello" {
		t.Errorf("expected body 'hello', got %q", receivedBody)
	}
}

// --- websocketProxyToLocalPort tests ---

func TestWebsocketProxyToLocalPort_BackendNotListening(t *testing.T) {
	// Create a chi request context
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	w := httptest.NewRecorder()

	// Should handle gracefully when nothing is listening
	websocketProxyToLocalPort(w, req, 1, "ws")

	// The websocket.Accept call may fail or the dial may fail;
	// either way, no panic
}

func TestWebsocketProxyToLocalPort_BidirectionalRelay(t *testing.T) {
	// Start a backend WebSocket echo server
	echoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			msgType, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			if err := conn.Write(context.Background(), msgType, data); err != nil {
				return
			}
		}
	}))
	defer echoServer.Close()

	backendPort := extractPort(t, echoServer.URL)

	// Set up a test server that proxies WebSocket to the echo backend
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		websocketProxyToLocalPort(w, r, backendPort, "")
	}))
	defer proxyServer.Close()

	// Connect to the proxy server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(proxyServer.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial proxy: %v", err)
	}
	defer conn.CloseNow()

	// Send a text message and verify echo
	testMsg := "hello via SSH tunnel"
	if err := conn.Write(ctx, websocket.MessageText, []byte(testMsg)); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read echo: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Errorf("expected text message, got %v", msgType)
	}
	if string(data) != testMsg {
		t.Errorf("expected %q, got %q", testMsg, string(data))
	}

	// Send a binary message and verify echo
	binMsg := []byte{0x00, 0x01, 0x02, 0xFF}
	if err := conn.Write(ctx, websocket.MessageBinary, binMsg); err != nil {
		t.Fatalf("failed to write binary: %v", err)
	}

	msgType, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read binary echo: %v", err)
	}
	if msgType != websocket.MessageBinary {
		t.Errorf("expected binary message, got %v", msgType)
	}
	if string(data) != string(binMsg) {
		t.Errorf("binary data mismatch")
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestWebsocketProxyToLocalPort_MultipleMessages(t *testing.T) {
	// Backend that echoes messages
	echoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			msgType, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			if err := conn.Write(context.Background(), msgType, data); err != nil {
				return
			}
		}
	}))
	defer echoServer.Close()

	backendPort := extractPort(t, echoServer.URL)

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		websocketProxyToLocalPort(w, r, backendPort, "")
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(proxyServer.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial proxy: %v", err)
	}
	defer conn.CloseNow()

	// Send multiple messages rapidly to verify relay stability
	messages := []string{"msg1", "msg2", "msg3", "msg4", "msg5"}
	for _, msg := range messages {
		if err := conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
			t.Fatalf("failed to write %q: %v", msg, err)
		}
	}

	for _, expected := range messages {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("failed to read echo for %q: %v", expected, err)
		}
		if string(data) != expected {
			t.Errorf("expected %q, got %q", expected, string(data))
		}
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestWebsocketProxyToLocalPort_UpstreamSendsFirst(t *testing.T) {
	// Backend that sends a greeting immediately upon connection
	greetingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		// Send greeting first
		conn.Write(context.Background(), websocket.MessageText, []byte("welcome"))
		// Then echo
		for {
			msgType, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			if err := conn.Write(context.Background(), msgType, data); err != nil {
				return
			}
		}
	}))
	defer greetingServer.Close()

	backendPort := extractPort(t, greetingServer.URL)

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		websocketProxyToLocalPort(w, r, backendPort, "")
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(proxyServer.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial proxy: %v", err)
	}
	defer conn.CloseNow()

	// Read the upstream-initiated greeting
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read greeting: %v", err)
	}
	if string(data) != "welcome" {
		t.Errorf("expected 'welcome', got %q", string(data))
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestProxyToLocalPort_ForwardsPath(t *testing.T) {
	var receivedPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("GET", "/deep/nested/path/file.js", nil)
	w := httptest.NewRecorder()

	proxyToLocalPort(w, req, port, "deep/nested/path/file.js")

	if receivedPath != "/deep/nested/path/file.js" {
		t.Errorf("expected path '/deep/nested/path/file.js', got %q", receivedPath)
	}
}

func TestProxyToLocalPort_EmptyPath(t *testing.T) {
	var receivedPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	proxyToLocalPort(w, req, port, "")

	if receivedPath != "/" {
		t.Errorf("expected path '/', got %q", receivedPath)
	}
}

func TestProxyToLocalPort_BinaryResponse(t *testing.T) {
	binaryData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A} // PNG header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write(binaryData)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("GET", "/image.png", nil)
	w := httptest.NewRecorder()

	proxyToLocalPort(w, req, port, "image.png")

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "image/png" {
		t.Errorf("expected image/png, got %s", w.Header().Get("Content-Type"))
	}
	if w.Body.Len() != len(binaryData) {
		t.Errorf("expected %d bytes, got %d", len(binaryData), w.Body.Len())
	}
}

func TestProxyToLocalPort_CacheHeaders(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", `"abc123"`)
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2026 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "body{}")
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("GET", "/style.css", nil)
	w := httptest.NewRecorder()

	proxyToLocalPort(w, req, port, "style.css")

	if w.Header().Get("Cache-Control") != "public, max-age=3600" {
		t.Errorf("Cache-Control not forwarded: %q", w.Header().Get("Cache-Control"))
	}
	if w.Header().Get("ETag") != `"abc123"` {
		t.Errorf("ETag not forwarded: %q", w.Header().Get("ETag"))
	}
	if w.Header().Get("Last-Modified") != "Thu, 01 Jan 2026 00:00:00 GMT" {
		t.Errorf("Last-Modified not forwarded: %q", w.Header().Get("Last-Modified"))
	}
}

// --- helpers ---

func extractPort(t *testing.T, serverURL string) int {
	t.Helper()
	// serverURL format: http://127.0.0.1:PORT
	parts := strings.Split(serverURL, ":")
	if len(parts) < 3 {
		t.Fatalf("unexpected server URL format: %s", serverURL)
	}
	var port int
	fmt.Sscanf(parts[2], "%d", &port)
	return port
}
