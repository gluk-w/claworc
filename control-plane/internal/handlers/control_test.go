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
