package handlers

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
