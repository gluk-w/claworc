package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
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
