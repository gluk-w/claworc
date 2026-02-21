package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
)

// mockOrchestrator implements only the GetInstanceSSHEndpoint method
// for testing the SSH connection test handler.
type mockOrchestrator struct {
	sshHost string
	sshPort int
	sshErr  error
}

func (m *mockOrchestrator) Initialize(ctx context.Context) error   { return nil }
func (m *mockOrchestrator) IsAvailable(ctx context.Context) bool   { return true }
func (m *mockOrchestrator) BackendName() string                    { return "mock" }
func (m *mockOrchestrator) CreateInstance(ctx context.Context, params orchestrator.CreateParams) error {
	return nil
}
func (m *mockOrchestrator) DeleteInstance(ctx context.Context, name string) error  { return nil }
func (m *mockOrchestrator) StartInstance(ctx context.Context, name string) error   { return nil }
func (m *mockOrchestrator) StopInstance(ctx context.Context, name string) error    { return nil }
func (m *mockOrchestrator) RestartInstance(ctx context.Context, name string) error { return nil }
func (m *mockOrchestrator) GetInstanceStatus(ctx context.Context, name string) (string, error) {
	return "running", nil
}
func (m *mockOrchestrator) UpdateInstanceConfig(ctx context.Context, name string, configJSON string) error {
	return nil
}
func (m *mockOrchestrator) StreamInstanceLogs(ctx context.Context, name string, tail int, follow bool) (<-chan string, error) {
	return nil, nil
}
func (m *mockOrchestrator) CloneVolumes(ctx context.Context, srcName, dstName string) error {
	return nil
}
func (m *mockOrchestrator) ExecInInstance(ctx context.Context, name string, cmd []string) (string, string, int, error) {
	return "", "", 0, nil
}
func (m *mockOrchestrator) ExecInteractive(ctx context.Context, name string, cmd []string) (*orchestrator.ExecSession, error) {
	return nil, nil
}
func (m *mockOrchestrator) ListDirectory(ctx context.Context, name string, path string) ([]orchestrator.FileEntry, error) {
	return nil, nil
}
func (m *mockOrchestrator) ReadFile(ctx context.Context, name string, path string) ([]byte, error) {
	return nil, nil
}
func (m *mockOrchestrator) CreateFile(ctx context.Context, name string, path string, content string) error {
	return nil
}
func (m *mockOrchestrator) CreateDirectory(ctx context.Context, name string, path string) error {
	return nil
}
func (m *mockOrchestrator) WriteFile(ctx context.Context, name string, path string, data []byte) error {
	return nil
}
func (m *mockOrchestrator) GetVNCBaseURL(ctx context.Context, name string, display string) (string, error) {
	return "", nil
}
func (m *mockOrchestrator) GetGatewayWSURL(ctx context.Context, name string) (string, error) {
	return "", nil
}
func (m *mockOrchestrator) GetInstanceSSHEndpoint(ctx context.Context, name string) (string, int, error) {
	return m.sshHost, m.sshPort, m.sshErr
}
func (m *mockOrchestrator) GetHTTPTransport() http.RoundTripper { return nil }

func TestSSHConnectionTest_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/abc/ssh-test", map[string]string{"id": "abc"})
	w := httptest.NewRecorder()

	SSHConnectionTest(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestSSHConnectionTest_NotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("GET", "/api/v1/instances/999/ssh-test", map[string]string{"id": "999"})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	SSHConnectionTest(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSSHConnectionTest_Forbidden(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-ssh-test", DisplayName: "SSH Test", Status: "running"}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-test", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	SSHConnectionTest(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestSSHConnectionTest_NoOrchestrator(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-ssh-test", DisplayName: "SSH Test", Status: "running", SSHPrivateKeyPath: "/tmp/test.key"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	orchestrator.ResetForTest()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-test", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	SSHConnectionTest(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestSSHConnectionTest_NoSSHManager(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-ssh-test", DisplayName: "SSH Test", Status: "running", SSHPrivateKeyPath: "/tmp/test.key"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	mock := &mockOrchestrator{sshHost: "10.0.0.1", sshPort: 22}
	orchestrator.SetForTest(mock)
	defer orchestrator.ResetForTest()

	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-test", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	SSHConnectionTest(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestSSHConnectionTest_NoSSHKey(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-ssh-test", DisplayName: "SSH Test", Status: "running"}
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

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-test", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	SSHConnectionTest(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d body: %s", w.Code, w.Body.String())
	}
}

func TestSSHConnectionTest_EndpointError(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-ssh-test", DisplayName: "SSH Test", Status: "running", SSHPrivateKeyPath: "/tmp/test.key"}
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

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-test", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	SSHConnectionTest(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp sshTestResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Status != "error" {
		t.Errorf("expected status 'error', got %q", resp.Status)
	}
	if resp.Error == "" {
		t.Error("expected non-empty error message")
	}
}

func TestSSHConnectionTest_ConnectionFail(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{
		Name:              "bot-ssh-test",
		DisplayName:       "SSH Test",
		Status:            "running",
		SSHPrivateKeyPath: "/tmp/nonexistent-key-file-for-test.key",
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

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-test", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	SSHConnectionTest(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp sshTestResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Status != "error" {
		t.Errorf("expected status 'error', got %q", resp.Status)
	}
	if resp.Error == "" {
		t.Error("expected non-empty error message for connection failure")
	}
	if resp.LatencyMs < 0 {
		t.Errorf("expected non-negative latency, got %d", resp.LatencyMs)
	}
}

func TestSSHConnectionTest_ResponseFormat(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{
		Name:              "bot-ssh-fmt",
		DisplayName:       "SSH Format",
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

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-test", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	SSHConnectionTest(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Verify response is valid JSON with expected fields
	body, _ := io.ReadAll(w.Body)
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if _, ok := raw["status"]; !ok {
		t.Error("response missing 'status' field")
	}
	if _, ok := raw["latency_ms"]; !ok {
		t.Error("response missing 'latency_ms' field")
	}
}
