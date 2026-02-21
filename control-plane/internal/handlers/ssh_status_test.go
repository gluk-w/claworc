package handlers

import (
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
)

func TestGetSSHStatus_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/abc/ssh-status", map[string]string{"id": "abc"})
	w := httptest.NewRecorder()

	GetSSHStatus(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGetSSHStatus_NotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("GET", "/api/v1/instances/999/ssh-status", map[string]string{"id": "999"})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHStatus(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetSSHStatus_Forbidden(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-test", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-status", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	GetSSHStatus(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestGetSSHStatus_NoManagers(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-test", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-status", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp sshStatusResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.ConnectionState != "disconnected" {
		t.Errorf("expected state disconnected, got %q", resp.ConnectionState)
	}
	if resp.Health != nil {
		t.Errorf("expected nil health, got %+v", resp.Health)
	}
	if len(resp.Tunnels) != 0 {
		t.Errorf("expected 0 tunnels, got %d", len(resp.Tunnels))
	}
	if len(resp.RecentEvents) != 0 {
		t.Errorf("expected 0 events, got %d", len(resp.RecentEvents))
	}
}

func TestGetSSHStatus_WithSSHConnection(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-connected", DisplayName: "Connected", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	// Set connection state to connected
	sm.SetConnectionState("bot-connected", sshmanager.StateConnected)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-status", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp sshStatusResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.ConnectionState != "connected" {
		t.Errorf("expected state connected, got %q", resp.ConnectionState)
	}
}

func TestGetSSHStatus_WithStateTransitions(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-events", DisplayName: "Events", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	// Generate state transitions
	sm.SetConnectionState("bot-events", sshmanager.StateConnecting)
	sm.SetConnectionState("bot-events", sshmanager.StateConnected)
	sm.SetConnectionState("bot-events", sshmanager.StateReconnecting)
	sm.SetConnectionState("bot-events", sshmanager.StateConnected)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-status", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp sshStatusResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.RecentEvents) != 4 {
		t.Fatalf("expected 4 events, got %d", len(resp.RecentEvents))
	}

	// Verify first transition: disconnected -> connecting
	if resp.RecentEvents[0].From != "disconnected" || resp.RecentEvents[0].To != "connecting" {
		t.Errorf("event 0: expected disconnected->connecting, got %s->%s", resp.RecentEvents[0].From, resp.RecentEvents[0].To)
	}

	// Verify last transition: reconnecting -> connected
	last := resp.RecentEvents[3]
	if last.From != "reconnecting" || last.To != "connected" {
		t.Errorf("event 3: expected reconnecting->connected, got %s->%s", last.From, last.To)
	}

	// Verify timestamps are non-empty
	for i, e := range resp.RecentEvents {
		if e.Timestamp == "" {
			t.Errorf("event %d: expected non-empty timestamp", i)
		}
	}
}

func TestGetSSHStatus_EventsLimitedTo10(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-many", DisplayName: "Many", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	// Generate more than 10 transitions
	for i := 0; i < 15; i++ {
		if i%2 == 0 {
			sm.SetConnectionState("bot-many", sshmanager.StateReconnecting)
		} else {
			sm.SetConnectionState("bot-many", sshmanager.StateConnected)
		}
	}

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-status", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp sshStatusResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.RecentEvents) != 10 {
		t.Errorf("expected 10 events (capped), got %d", len(resp.RecentEvents))
	}
}

func TestGetSSHStatus_WithTunnels(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-tunnels", DisplayName: "Tunnels", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sshtunnel.AddTestTunnel(tm, "bot-tunnels", sshtunnel.TestTunnelOpts{
		Service:    "vnc",
		Type:       "reverse",
		LocalPort:  12345,
		RemotePort: 3000,
	})
	sshtunnel.AddTestTunnel(tm, "bot-tunnels", sshtunnel.TestTunnelOpts{
		Service:       "gateway",
		Type:          "reverse",
		LocalPort:     12346,
		RemotePort:    8080,
		LastCheckTime: time.Now().Add(-10 * time.Second),
		LastCheckErr:  fmt.Errorf("connection refused"),
	})

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-status", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp sshStatusResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Tunnels) != 2 {
		t.Fatalf("expected 2 tunnels, got %d", len(resp.Tunnels))
	}

	// Find VNC and gateway tunnels
	var vnc, gw *sshTunnelStatus
	for i := range resp.Tunnels {
		switch resp.Tunnels[i].Service {
		case "vnc":
			vnc = &resp.Tunnels[i]
		case "gateway":
			gw = &resp.Tunnels[i]
		}
	}

	if vnc == nil {
		t.Fatal("VNC tunnel not found")
	}
	if vnc.LocalPort != 12345 {
		t.Errorf("VNC local port: expected 12345, got %d", vnc.LocalPort)
	}
	if vnc.RemotePort != 3000 {
		t.Errorf("VNC remote port: expected 3000, got %d", vnc.RemotePort)
	}

	if gw == nil {
		t.Fatal("Gateway tunnel not found")
	}
	if gw.LocalPort != 12346 {
		t.Errorf("Gateway local port: expected 12346, got %d", gw.LocalPort)
	}
	if gw.LastError != "connection refused" {
		t.Errorf("Gateway last error: expected 'connection refused', got %q", gw.LastError)
	}
}

func TestGetSSHStatus_ResponseFormat(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-format", DisplayName: "Format", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-status", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Verify JSON has expected top-level fields
	var raw map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	expectedFields := []string{"connection_state", "health", "tunnels", "recent_events"}
	for _, field := range expectedFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("response missing %q field", field)
		}
	}

	if raw["connection_state"] != "disconnected" {
		t.Errorf("expected connection_state 'disconnected', got %v", raw["connection_state"])
	}
}

func TestGetSSHStatus_ViewerAssigned(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-viewer", DisplayName: "Viewer", Status: "running"}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	// Assign viewer to instance
	database.DB.Create(&database.UserInstance{UserID: viewer.ID, InstanceID: inst.ID})

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-status", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	GetSSHStatus(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for assigned viewer, got %d", w.Code)
	}
}
