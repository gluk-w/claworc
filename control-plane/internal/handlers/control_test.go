package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
	"github.com/go-chi/chi/v5"
)

func TestControlProxy_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/abc/control/", map[string]string{"id": "abc", "*": ""})
	w := httptest.NewRecorder()

	ControlProxy(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestControlProxy_Forbidden(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-ctrl", DisplayName: "Ctrl", Status: "running"}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/control/", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": ""})
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	ControlProxy(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestControlProxy_NoTunnel(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-ctrl", DisplayName: "Ctrl", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/control/", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": ""})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	ControlProxy(w, r)

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", w.Code)
	}
}

func TestControlProxy_ProxiesHTTP(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Start backend to simulate gateway service
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"connected"}`)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	inst := database.Instance{Name: "bot-gw-http", DisplayName: "GW HTTP", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-gw-http", sshtunnel.TestTunnelOpts{
		Service:    "gateway",
		Type:       "reverse",
		LocalPort:  port,
		RemotePort: 8080,
	})

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/control/health", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": "health"})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	ControlProxy(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected application/json, got %s", w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	if !strings.Contains(body, `"status":"connected"`) {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestControlProxy_ForwardsQueryString(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	var receivedQuery string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	inst := database.Instance{Name: "bot-gw-qs", DisplayName: "GW QS", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-gw-qs", sshtunnel.TestTunnelOpts{
		Service:    "gateway",
		Type:       "reverse",
		LocalPort:  port,
		RemotePort: 8080,
	})

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/control/sessions?active=true", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": "sessions"})
	r.URL.RawQuery = "active=true"
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	ControlProxy(w, r)

	if receivedQuery != "active=true" {
		t.Errorf("expected query 'active=true', got %q", receivedQuery)
	}
}

func TestControlProxy_ClosedTunnelReturns502(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-closed-ctrl", DisplayName: "Closed Ctrl", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-closed-ctrl", sshtunnel.TestTunnelOpts{
		Service:    "gateway",
		Type:       "reverse",
		LocalPort:  12345,
		RemotePort: 8080,
		Closed:     true,
	})

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/control/", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": ""})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	ControlProxy(w, r)

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", w.Code)
	}
}

func TestControlProxy_WebSocketRelay(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Start backend WebSocket echo server simulating gateway
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

	inst := database.Instance{Name: "bot-gw-ws", DisplayName: "GW WS", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-gw-ws", sshtunnel.TestTunnelOpts{
		Service:    "gateway",
		Type:       "reverse",
		LocalPort:  backendPort,
		RemotePort: 8080,
	})

	// Real HTTP server with chi router for WebSocket upgrade
	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, middleware.WithUserForTest(r, admin))
		})
	})
	mux.Get("/api/v1/instances/{id}/control/*", ControlProxy)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/control/ws",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial control WS proxy: %v", err)
	}
	defer conn.CloseNow()

	// Send a gateway command as JSON and verify relay
	cmd := `{"action":"subscribe","channel":"events"}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(cmd)); err != nil {
		t.Fatalf("failed to write command: %v", err)
	}

	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read echo: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Errorf("expected text message, got %v", msgType)
	}
	if string(data) != cmd {
		t.Errorf("expected %q, got %q", cmd, string(data))
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestControlProxy_WebSocketBidirectional(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Backend that sends a greeting then echoes
	greetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		// Send server-initiated event (simulating gateway push)
		conn.Write(context.Background(), websocket.MessageText, []byte(`{"event":"ready"}`))
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
	defer greetServer.Close()

	backendPort := extractPort(t, greetServer.URL)

	inst := database.Instance{Name: "bot-gw-bidir", DisplayName: "GW Bidir", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-gw-bidir", sshtunnel.TestTunnelOpts{
		Service:    "gateway",
		Type:       "reverse",
		LocalPort:  backendPort,
		RemotePort: 8080,
	})

	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, middleware.WithUserForTest(r, admin))
		})
	})
	mux.Get("/api/v1/instances/{id}/control/*", ControlProxy)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/control/events",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.CloseNow()

	// Read server-initiated event
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read greeting: %v", err)
	}
	if string(data) != `{"event":"ready"}` {
		t.Errorf("expected ready event, got %q", string(data))
	}

	// Send client message and verify echo
	if err := conn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("failed to write: %v", err)
	}
	_, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read echo: %v", err)
	}
	if string(data) != "ping" {
		t.Errorf("expected 'ping', got %q", string(data))
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestControlProxy_ViewerWithAccess(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	inst := database.Instance{Name: "bot-viewer-ctrl", DisplayName: "Viewer Ctrl", Status: "running"}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	// Assign viewer to this instance
	database.DB.Create(&database.UserInstance{UserID: viewer.ID, InstanceID: inst.ID})

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-viewer-ctrl", sshtunnel.TestTunnelOpts{
		Service:    "gateway",
		Type:       "reverse",
		LocalPort:  port,
		RemotePort: 8080,
	})

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/control/health", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": "health"})
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	ControlProxy(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for assigned viewer, got %d", w.Code)
	}
}

func TestControlProxy_NoUserContext(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-nouser-ctrl", DisplayName: "No User", Status: "running"}
	database.DB.Create(&inst)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/control/", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": ""})

	w := httptest.NewRecorder()
	ControlProxy(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for no user context, got %d", w.Code)
	}
}

func TestControlProxy_NestedPath(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	var receivedPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	inst := database.Instance{Name: "bot-path-ctrl", DisplayName: "Path Ctrl", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-path-ctrl", sshtunnel.TestTunnelOpts{
		Service:    "gateway",
		Type:       "reverse",
		LocalPort:  port,
		RemotePort: 8080,
	})

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/control/api/v1/sessions/123", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": "api/v1/sessions/123"})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	ControlProxy(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if receivedPath != "/api/v1/sessions/123" {
		t.Errorf("expected path '/api/v1/sessions/123', got %q", receivedPath)
	}
}
