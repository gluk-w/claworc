package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
)

func TestSSHReconnect_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("POST", "/api/v1/instances/abc/ssh-reconnect", map[string]string{"id": "abc"})
	w := httptest.NewRecorder()

	SSHReconnect(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestSSHReconnect_NotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("POST", "/api/v1/instances/999/ssh-reconnect", map[string]string{"id": "999"})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	SSHReconnect(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSSHReconnect_Forbidden(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-reconnect-test", DisplayName: "Reconnect Test", Status: "running"}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/ssh-reconnect", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	SSHReconnect(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestSSHReconnect_NoOrchestrator(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-reconnect-test", DisplayName: "Reconnect Test", Status: "running", SSHPrivateKeyPath: "/tmp/test.key"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	orchestrator.ResetForTest()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/ssh-reconnect", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	SSHReconnect(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestSSHReconnect_NoSSHManager(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-reconnect-test", DisplayName: "Reconnect Test", Status: "running", SSHPrivateKeyPath: "/tmp/test.key"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	mock := &mockOrchestrator{sshHost: "10.0.0.1", sshPort: 22}
	orchestrator.SetForTest(mock)
	defer orchestrator.ResetForTest()

	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/ssh-reconnect", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	SSHReconnect(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestSSHReconnect_NoSSHKey(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-reconnect-test", DisplayName: "Reconnect Test", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	mock := &mockOrchestrator{sshHost: "10.0.0.1", sshPort: 22}
	orchestrator.SetForTest(mock)
	defer orchestrator.ResetForTest()

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/ssh-reconnect", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	SSHReconnect(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp sshReconnectResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Success {
		t.Error("expected success=false for instance with no SSH key")
	}
	if resp.Message == "" {
		t.Error("expected non-empty message")
	}
}

func TestSSHReconnect_EndpointError(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-reconnect-test", DisplayName: "Reconnect Test", Status: "running", SSHPrivateKeyPath: "/tmp/test.key"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	mock := &mockOrchestrator{sshErr: fmt.Errorf("instance not running")}
	orchestrator.SetForTest(mock)
	defer orchestrator.ResetForTest()

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/ssh-reconnect", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	SSHReconnect(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp sshReconnectResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Success {
		t.Error("expected success=false for endpoint error")
	}
	if resp.Message == "" {
		t.Error("expected non-empty message")
	}
}

func TestSSHReconnect_ConnectionFail(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{
		Name:              "bot-reconnect-test",
		DisplayName:       "Reconnect Test",
		Status:            "running",
		SSHPrivateKeyPath: "/tmp/nonexistent-key-for-test.key",
	}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	mock := &mockOrchestrator{sshHost: "10.0.0.1", sshPort: 22}
	orchestrator.SetForTest(mock)
	defer orchestrator.ResetForTest()

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/ssh-reconnect", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	SSHReconnect(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp sshReconnectResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Success {
		t.Error("expected success=false for connection failure")
	}
	if resp.Message == "" {
		t.Error("expected non-empty error message for connection failure")
	}
}

func TestSSHReconnect_ResponseFormat(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{
		Name:              "bot-reconnect-fmt",
		DisplayName:       "Reconnect Format",
		Status:            "running",
		SSHPrivateKeyPath: "/tmp/nonexistent.key",
	}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	mock := &mockOrchestrator{sshHost: "10.0.0.1", sshPort: 22}
	orchestrator.SetForTest(mock)
	defer orchestrator.ResetForTest()

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/ssh-reconnect", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	SSHReconnect(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Verify response has expected JSON fields
	var raw map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if _, ok := raw["success"]; !ok {
		t.Error("response missing 'success' field")
	}
	if _, ok := raw["message"]; !ok {
		t.Error("response missing 'message' field")
	}
}
