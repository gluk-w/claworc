package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
)

func TestGetGlobalSSHStatus_NoInstances(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", "/api/v1/ssh-status", nil)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetGlobalSSHStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp globalSSHStatusResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.TotalCount != 0 {
		t.Errorf("expected 0 total, got %d", resp.TotalCount)
	}
	if len(resp.Instances) != 0 {
		t.Errorf("expected 0 instances, got %d", len(resp.Instances))
	}
}

func TestGetGlobalSSHStatus_WithInstances(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst1 := database.Instance{Name: "bot-one", DisplayName: "One", Status: "running"}
	inst2 := database.Instance{Name: "bot-two", DisplayName: "Two", Status: "stopped"}
	database.DB.Create(&inst1)
	database.DB.Create(&inst2)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sm.SetConnectionState("bot-one", sshmanager.StateConnected)
	sm.SetConnectionState("bot-two", sshmanager.StateDisconnected)

	r := newChiRequest("GET", "/api/v1/ssh-status", nil)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetGlobalSSHStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp globalSSHStatusResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.TotalCount != 2 {
		t.Errorf("expected 2 total, got %d", resp.TotalCount)
	}
	if resp.Connected != 1 {
		t.Errorf("expected 1 connected, got %d", resp.Connected)
	}
	if resp.Disconnected != 1 {
		t.Errorf("expected 1 disconnected, got %d", resp.Disconnected)
	}
}

func TestGetGlobalSSHStatus_MixedStates(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	instances := []database.Instance{
		{Name: "bot-a", DisplayName: "A", Status: "running"},
		{Name: "bot-b", DisplayName: "B", Status: "running"},
		{Name: "bot-c", DisplayName: "C", Status: "running"},
		{Name: "bot-d", DisplayName: "D", Status: "stopped"},
	}
	for i := range instances {
		database.DB.Create(&instances[i])
	}

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sm.SetConnectionState("bot-a", sshmanager.StateConnected)
	sm.SetConnectionState("bot-b", sshmanager.StateReconnecting)
	sm.SetConnectionState("bot-c", sshmanager.StateFailed)

	r := newChiRequest("GET", "/api/v1/ssh-status", nil)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetGlobalSSHStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp globalSSHStatusResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.TotalCount != 4 {
		t.Errorf("expected 4 total, got %d", resp.TotalCount)
	}
	if resp.Connected != 1 {
		t.Errorf("expected 1 connected, got %d", resp.Connected)
	}
	if resp.Reconnecting != 1 {
		t.Errorf("expected 1 reconnecting, got %d", resp.Reconnecting)
	}
	if resp.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", resp.Failed)
	}
	if resp.Disconnected != 1 {
		t.Errorf("expected 1 disconnected, got %d", resp.Disconnected)
	}
}

func TestGetGlobalSSHStatus_ViewerFiltered(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst1 := database.Instance{Name: "bot-one", DisplayName: "One", Status: "running"}
	inst2 := database.Instance{Name: "bot-two", DisplayName: "Two", Status: "running"}
	database.DB.Create(&inst1)
	database.DB.Create(&inst2)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	// Only assign inst1 to viewer
	database.DB.Create(&database.UserInstance{UserID: viewer.ID, InstanceID: inst1.ID})

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sm.SetConnectionState("bot-one", sshmanager.StateConnected)
	sm.SetConnectionState("bot-two", sshmanager.StateConnected)

	r := newChiRequest("GET", "/api/v1/ssh-status", nil)
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	GetGlobalSSHStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp globalSSHStatusResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.TotalCount != 1 {
		t.Errorf("viewer should see only 1 instance, got %d", resp.TotalCount)
	}
	if len(resp.Instances) != 1 {
		t.Fatalf("expected 1 instance in list, got %d", len(resp.Instances))
	}
	if resp.Instances[0].DisplayName != "One" {
		t.Errorf("expected instance 'One', got %q", resp.Instances[0].DisplayName)
	}
}

func TestGetGlobalSSHStatus_ViewerNoInstances(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-one", DisplayName: "One", Status: "running"}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", "/api/v1/ssh-status", nil)
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	GetGlobalSSHStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp globalSSHStatusResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.TotalCount != 0 {
		t.Errorf("viewer with no assignments should see 0, got %d", resp.TotalCount)
	}
}

func TestGetGlobalSSHStatus_NoManagers(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-test", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", "/api/v1/ssh-status", nil)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetGlobalSSHStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp globalSSHStatusResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.TotalCount != 1 {
		t.Errorf("expected 1 total, got %d", resp.TotalCount)
	}
	if len(resp.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(resp.Instances))
	}
	if resp.Instances[0].ConnectionState != "disconnected" {
		t.Errorf("expected disconnected state, got %q", resp.Instances[0].ConnectionState)
	}
}

func TestGetGlobalSSHStatus_WithTunnels(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-tunnel", DisplayName: "Tunnel", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sm.SetConnectionState("bot-tunnel", sshmanager.StateConnected)

	sshtunnel.AddTestTunnel(tm, "bot-tunnel", sshtunnel.TestTunnelOpts{
		Service:    "vnc",
		Type:       "reverse",
		LocalPort:  12345,
		RemotePort: 3000,
	})
	sshtunnel.AddTestTunnel(tm, "bot-tunnel", sshtunnel.TestTunnelOpts{
		Service:    "gateway",
		Type:       "reverse",
		LocalPort:  12346,
		RemotePort: 8080,
	})

	r := newChiRequest("GET", "/api/v1/ssh-status", nil)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetGlobalSSHStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp globalSSHStatusResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(resp.Instances))
	}
	if resp.Instances[0].TunnelCount != 2 {
		t.Errorf("expected 2 tunnels, got %d", resp.Instances[0].TunnelCount)
	}
}

func TestGetGlobalSSHStatus_ResponseFormat(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", "/api/v1/ssh-status", nil)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetGlobalSSHStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var raw map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	expectedFields := []string{"instances", "total_count", "connected", "reconnecting", "failed", "disconnected"}
	for _, field := range expectedFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("response missing %q field", field)
		}
	}
}
