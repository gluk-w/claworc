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
	"github.com/gluk-w/claworc/control-plane/internal/sshkeys"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
)

func TestRotateSSHKey_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("POST", "/api/v1/instances/abc/rotate-ssh-key", map[string]string{"id": "abc"})
	w := httptest.NewRecorder()

	RotateSSHKey(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestRotateSSHKey_NotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("POST", "/api/v1/instances/999/rotate-ssh-key", map[string]string{"id": "999"})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	RotateSSHKey(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestRotateSSHKey_Forbidden(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-rotate-test", DisplayName: "Rotate Test", Status: "running"}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/rotate-ssh-key", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	RotateSSHKey(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestRotateSSHKey_NoOrchestrator(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	pubKey, _, _ := sshkeys.GenerateKeyPair()
	inst := database.Instance{
		Name:              "bot-rotate-test",
		DisplayName:       "Rotate Test",
		Status:            "running",
		SSHPublicKey:      string(pubKey),
		SSHPrivateKeyPath: "/tmp/test.key",
	}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	orchestrator.ResetForTest()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/rotate-ssh-key", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	RotateSSHKey(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestRotateSSHKey_NoSSHManager(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	pubKey, _, _ := sshkeys.GenerateKeyPair()
	inst := database.Instance{
		Name:              "bot-rotate-test",
		DisplayName:       "Rotate Test",
		Status:            "running",
		SSHPublicKey:      string(pubKey),
		SSHPrivateKeyPath: "/tmp/test.key",
	}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	mock := &mockOrchestrator{sshHost: "10.0.0.1", sshPort: 22}
	orchestrator.SetForTest(mock)
	defer orchestrator.ResetForTest()

	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/rotate-ssh-key", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	RotateSSHKey(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestRotateSSHKey_NoSSHKeyConfigured(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{
		Name:        "bot-rotate-test",
		DisplayName: "Rotate Test",
		Status:      "running",
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

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/rotate-ssh-key", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	RotateSSHKey(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d body: %s", w.Code, w.Body.String())
	}
}

func TestRotateSSHKey_NoActiveSSHConnection(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	pubKey, _, _ := sshkeys.GenerateKeyPair()
	inst := database.Instance{
		Name:              "bot-rotate-test",
		DisplayName:       "Rotate Test",
		Status:            "running",
		SSHPublicKey:      string(pubKey),
		SSHPrivateKeyPath: "/tmp/test.key",
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

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/rotate-ssh-key", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	RotateSSHKey(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d body: %s", w.Code, w.Body.String())
	}
}

func TestRotateSSHKey_ResponseFormat(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	pubKey, _, _ := sshkeys.GenerateKeyPair()
	inst := database.Instance{
		Name:              "bot-rotate-fmt",
		DisplayName:       "Rotate Format",
		Status:            "running",
		SSHPublicKey:      string(pubKey),
		SSHPrivateKeyPath: "/tmp/test.key",
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

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/rotate-ssh-key", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	RotateSSHKey(w, r)

	// Without an active SSH connection, we expect 503
	if w.Code != http.StatusServiceUnavailable {
		// If it returns 200, check the JSON format
		var raw map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
			t.Fatalf("response is not valid JSON: %v", err)
		}
		if _, ok := raw["success"]; !ok {
			t.Error("response missing 'success' field")
		}
	}
}

func TestRotateSSHKey_EndpointError(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	pubKey, _, _ := sshkeys.GenerateKeyPair()
	inst := database.Instance{
		Name:              "bot-rotate-test",
		DisplayName:       "Rotate Test",
		Status:            "running",
		SSHPublicKey:      string(pubKey),
		SSHPrivateKeyPath: "/tmp/test.key",
	}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	mock := &mockOrchestrator{sshErr: fmt.Errorf("instance not running")}
	orchestrator.SetForTest(mock)
	defer orchestrator.ResetForTest()

	sm := sshmanager.NewSSHManager(0)
	// Set a nil client so GetClient returns an error
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/rotate-ssh-key", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	RotateSSHKey(w, r)

	// Should fail at GetClient step since no SSH connection exists
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d body: %s", w.Code, w.Body.String())
	}
}

func TestRotateSSHKey_ViewerAssigned(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	pubKey, _, _ := sshkeys.GenerateKeyPair()
	inst := database.Instance{
		Name:              "bot-rotate-viewer",
		DisplayName:       "Rotate Viewer",
		Status:            "running",
		SSHPublicKey:      string(pubKey),
		SSHPrivateKeyPath: "/tmp/test.key",
	}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	// Assign viewer to instance
	database.DB.Create(&database.UserInstance{UserID: viewer.ID, InstanceID: inst.ID})

	mock := &mockOrchestrator{sshHost: "10.0.0.1", sshPort: 22}
	orchestrator.SetForTest(mock)
	defer orchestrator.ResetForTest()

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/rotate-ssh-key", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	RotateSSHKey(w, r)

	// Assigned viewer should have access, but will hit 503 because no SSH connection
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 (no SSH connection), got %d body: %s", w.Code, w.Body.String())
	}
}

func TestRotateSSHKey_SuccessResponseFields(t *testing.T) {
	// Test that the sshRotateResponse struct has the expected JSON field names
	resp := sshRotateResponse{
		Success:     true,
		Fingerprint: "SHA256:abc123",
		RotatedAt:   "2024-01-01T00:00:00Z",
		Message:     "SSH key rotation completed successfully",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	expectedFields := []string{"success", "fingerprint", "rotated_at", "message"}
	for _, field := range expectedFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("response missing '%s' field", field)
		}
	}

	if raw["success"] != true {
		t.Errorf("expected success=true, got %v", raw["success"])
	}
	if raw["fingerprint"] != "SHA256:abc123" {
		t.Errorf("expected fingerprint 'SHA256:abc123', got %v", raw["fingerprint"])
	}
}

func TestRotateSSHKey_FailureResponseOmitsEmptyFields(t *testing.T) {
	resp := sshRotateResponse{
		Success: false,
		Message: "Key rotation failed: some error",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if raw["success"] != false {
		t.Errorf("expected success=false, got %v", raw["success"])
	}

	// fingerprint should be present but empty (not omitted since no omitempty)
	if raw["fingerprint"] != "" {
		t.Errorf("expected empty fingerprint, got %v", raw["fingerprint"])
	}
}
