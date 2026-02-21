package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
	"github.com/go-chi/chi/v5"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupTestDB initialises an in-memory SQLite DB for testing and returns a
// cleanup function that should be deferred.
func setupTestDB(t *testing.T) func() {
	t.Helper()
	var err error
	database.DB, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	if err := database.DB.AutoMigrate(&database.Instance{}, &database.Setting{}, &database.User{}, &database.UserInstance{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	return func() {
		sqlDB, _ := database.DB.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
}

// newChiRequest creates an *http.Request with chi URL params set.
func newChiRequest(method, path string, params map[string]string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestGetTunnelStatus_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/abc/tunnels", map[string]string{"id": "abc"})
	w := httptest.NewRecorder()

	GetTunnelStatus(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGetTunnelStatus_NotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("GET", "/api/v1/instances/999/tunnels", map[string]string{"id": "999"})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetTunnelStatus(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetTunnelStatus_Forbidden(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-test", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/tunnels", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	GetTunnelStatus(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestGetTunnelStatus_NoTunnelManager(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-test", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/tunnels", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetTunnelStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp map[string][]tunnelInfo
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp["tunnels"]) != 0 {
		t.Errorf("expected empty tunnels, got %d", len(resp["tunnels"]))
	}
}

func TestGetTunnelStatus_EmptyTunnels(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-test", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sshtunnel.InitGlobal()
	defer sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/tunnels", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetTunnelStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp map[string][]tunnelInfo
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp["tunnels"]) != 0 {
		t.Errorf("expected 0 tunnels, got %d", len(resp["tunnels"]))
	}
}

func TestGetTunnelStatus_WithTunnels(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-test", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-test", sshtunnel.TestTunnelOpts{
		Service:    "vnc",
		Type:       "reverse",
		LocalPort:  12345,
		RemotePort: 3000,
	})
	sshtunnel.AddTestTunnel(tm, "bot-test", sshtunnel.TestTunnelOpts{
		Service:    "gateway",
		Type:       "reverse",
		LocalPort:  12346,
		RemotePort: 8080,
	})

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/tunnels", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetTunnelStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp map[string][]tunnelInfo
	json.NewDecoder(w.Body).Decode(&resp)
	tunnels := resp["tunnels"]

	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels, got %d", len(tunnels))
	}

	var vnc, gw *tunnelInfo
	for i := range tunnels {
		switch tunnels[i].Service {
		case "vnc":
			vnc = &tunnels[i]
		case "gateway":
			gw = &tunnels[i]
		}
	}

	if vnc == nil {
		t.Fatal("VNC tunnel not found in response")
	}
	if vnc.LocalPort != 12345 {
		t.Errorf("VNC local port: expected 12345, got %d", vnc.LocalPort)
	}
	if vnc.RemotePort != 3000 {
		t.Errorf("VNC remote port: expected 3000, got %d", vnc.RemotePort)
	}
	if vnc.Status != "active" {
		t.Errorf("VNC status: expected active, got %s", vnc.Status)
	}
	if vnc.Type != "reverse" {
		t.Errorf("VNC type: expected reverse, got %s", vnc.Type)
	}

	if gw == nil {
		t.Fatal("Gateway tunnel not found in response")
	}
	if gw.LocalPort != 12346 {
		t.Errorf("Gateway local port: expected 12346, got %d", gw.LocalPort)
	}
	if gw.RemotePort != 8080 {
		t.Errorf("Gateway remote port: expected 8080, got %d", gw.RemotePort)
	}
	if gw.Status != "active" {
		t.Errorf("Gateway status: expected active, got %s", gw.Status)
	}
}

func TestGetTunnelStatus_ClosedTunnel(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-closed", DisplayName: "Closed", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-closed", sshtunnel.TestTunnelOpts{
		Service:    "vnc",
		Type:       "reverse",
		LocalPort:  11111,
		RemotePort: 3000,
		Closed:     true,
	})

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/tunnels", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetTunnelStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string][]tunnelInfo
	json.NewDecoder(w.Body).Decode(&resp)
	tunnels := resp["tunnels"]
	if len(tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(tunnels))
	}
	if tunnels[0].Status != "closed" {
		t.Errorf("expected status 'closed', got %q", tunnels[0].Status)
	}
}

func TestGetTunnelStatus_HealthMetrics(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-health", DisplayName: "Health", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-health", sshtunnel.TestTunnelOpts{
		Service:       "vnc",
		Type:          "reverse",
		LocalPort:     22222,
		RemotePort:    3000,
		LastCheckTime: time.Now().Add(-30 * time.Second),
		LastCheckErr:  fmt.Errorf("connection refused"),
	})

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/tunnels", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetTunnelStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string][]tunnelInfo
	json.NewDecoder(w.Body).Decode(&resp)
	tunnels := resp["tunnels"]
	if len(tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(tunnels))
	}

	ti := tunnels[0]
	if ti.LastCheck == "" {
		t.Error("expected non-empty LastCheck")
	}
	if ti.LastError != "connection refused" {
		t.Errorf("expected LastError 'connection refused', got %q", ti.LastError)
	}
}
