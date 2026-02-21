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

func TestDesktopProxy_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/abc/desktop/", map[string]string{"id": "abc", "*": ""})
	w := httptest.NewRecorder()

	DesktopProxy(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDesktopProxy_Forbidden(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-desk", DisplayName: "Desk", Status: "running"}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/desktop/", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": ""})
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	DesktopProxy(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestDesktopProxy_NoTunnel(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-desk", DisplayName: "Desk", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/desktop/", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": ""})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	DesktopProxy(w, r)

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", w.Code)
	}
}

func TestDesktopProxy_ProxiesHTTP(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Start backend to simulate VNC service
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "<html>VNC</html>")
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	inst := database.Instance{Name: "bot-vnc-http", DisplayName: "VNC HTTP", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-vnc-http", sshtunnel.TestTunnelOpts{
		Service:    "vnc",
		Type:       "reverse",
		LocalPort:  port,
		RemotePort: 3000,
	})

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/desktop/index.html", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": "index.html"})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	DesktopProxy(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "text/html" {
		t.Errorf("expected text/html, got %s", w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	if !strings.Contains(body, "VNC") {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestDesktopProxy_ForwardsQueryString(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	var receivedQuery string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	inst := database.Instance{Name: "bot-vnc-qs", DisplayName: "VNC QS", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-vnc-qs", sshtunnel.TestTunnelOpts{
		Service:    "vnc",
		Type:       "reverse",
		LocalPort:  port,
		RemotePort: 3000,
	})

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/desktop/stream?quality=high", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": "stream"})
	r.URL.RawQuery = "quality=high"
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	DesktopProxy(w, r)

	if receivedQuery != "quality=high" {
		t.Errorf("expected query 'quality=high', got %q", receivedQuery)
	}
}

func TestDesktopProxy_ClosedTunnelReturns502(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-closed-desk", DisplayName: "Closed Desk", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-closed-desk", sshtunnel.TestTunnelOpts{
		Service:    "vnc",
		Type:       "reverse",
		LocalPort:  12345,
		RemotePort: 3000,
		Closed:     true,
	})

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/desktop/", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": ""})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	DesktopProxy(w, r)

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", w.Code)
	}
}

func TestDesktopProxy_WebSocketRelay(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Start backend WebSocket echo server simulating VNC
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

	inst := database.Instance{Name: "bot-vnc-ws", DisplayName: "VNC WS", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-vnc-ws", sshtunnel.TestTunnelOpts{
		Service:    "vnc",
		Type:       "reverse",
		LocalPort:  backendPort,
		RemotePort: 3000,
	})

	// Set up a test server with chi router so WebSocket upgrade works over real TCP
	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, middleware.WithUserForTest(r, admin))
		})
	})
	mux.Get("/api/v1/instances/{id}/desktop/*", DesktopProxy)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Connect to the desktop proxy endpoint via WebSocket
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/desktop/ws",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial desktop WS proxy: %v", err)
	}
	defer conn.CloseNow()

	// Send VNC-like binary data and verify relay
	vncData := []byte{0x00, 0x00, 0x00, 0x01, 0xFF, 0xFE}
	if err := conn.Write(ctx, websocket.MessageBinary, vncData); err != nil {
		t.Fatalf("failed to write VNC data: %v", err)
	}

	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read VNC echo: %v", err)
	}
	if msgType != websocket.MessageBinary {
		t.Errorf("expected binary message, got %v", msgType)
	}
	if string(data) != string(vncData) {
		t.Errorf("VNC data mismatch: expected %x, got %x", vncData, data)
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestDesktopProxy_WebSocketMultipleFrames(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Backend echo server
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

	inst := database.Instance{Name: "bot-vnc-frames", DisplayName: "VNC Frames", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-vnc-frames", sshtunnel.TestTunnelOpts{
		Service:    "vnc",
		Type:       "reverse",
		LocalPort:  backendPort,
		RemotePort: 3000,
	})

	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, middleware.WithUserForTest(r, admin))
		})
	})
	mux.Get("/api/v1/instances/{id}/desktop/*", DesktopProxy)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/desktop/stream",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.CloseNow()

	// Simulate multiple VNC frames (text and binary)
	frames := []struct {
		msgType websocket.MessageType
		data    []byte
	}{
		{websocket.MessageText, []byte("frame1")},
		{websocket.MessageBinary, []byte{0x01, 0x02}},
		{websocket.MessageText, []byte("frame3")},
	}

	for _, f := range frames {
		if err := conn.Write(ctx, f.msgType, f.data); err != nil {
			t.Fatalf("failed to write frame: %v", err)
		}
	}

	for i, expected := range frames {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("frame %d: read error: %v", i, err)
		}
		if msgType != expected.msgType {
			t.Errorf("frame %d: expected type %v, got %v", i, expected.msgType, msgType)
		}
		if string(data) != string(expected.data) {
			t.Errorf("frame %d: data mismatch", i)
		}
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestDesktopProxy_ViewerWithAccess(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "<html>VNC</html>")
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	inst := database.Instance{Name: "bot-viewer-desk", DisplayName: "Viewer Desk", Status: "running"}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	// Assign viewer to this instance
	database.DB.Create(&database.UserInstance{UserID: viewer.ID, InstanceID: inst.ID})

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-viewer-desk", sshtunnel.TestTunnelOpts{
		Service:    "vnc",
		Type:       "reverse",
		LocalPort:  port,
		RemotePort: 3000,
	})

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/desktop/index.html", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": "index.html"})
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	DesktopProxy(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for assigned viewer, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "VNC") {
		t.Errorf("unexpected body: %s", w.Body.String())
	}
}

func TestDesktopProxy_NoUserContext(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-nouser", DisplayName: "No User", Status: "running"}
	database.DB.Create(&inst)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	// Request without any user in context
	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/desktop/", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": ""})

	w := httptest.NewRecorder()
	DesktopProxy(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for no user context, got %d", w.Code)
	}
}

func TestDesktopProxy_NestedPath(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	var receivedPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/javascript")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "console.log('ok')")
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	inst := database.Instance{Name: "bot-path-desk", DisplayName: "Path Desk", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-path-desk", sshtunnel.TestTunnelOpts{
		Service:    "vnc",
		Type:       "reverse",
		LocalPort:  port,
		RemotePort: 3000,
	})

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/desktop/assets/js/app.js", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID), "*": "assets/js/app.js"})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	DesktopProxy(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if receivedPath != "/assets/js/app.js" {
		t.Errorf("expected path '/assets/js/app.js', got %q", receivedPath)
	}
	if w.Header().Get("Content-Type") != "application/javascript" {
		t.Errorf("expected application/javascript, got %s", w.Header().Get("Content-Type"))
	}
}
