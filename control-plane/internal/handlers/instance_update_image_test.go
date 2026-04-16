package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

// updateImageMock wraps mockOrchestrator so UpdateImage and GetInstanceStatus
// can be overridden per test — the base mock exposes static implementations.
type updateImageMock struct {
	mockOrchestrator
	updateImageErr error
	liveStatus     string
}

func (m *updateImageMock) UpdateImage(_ context.Context, _ string, _ orchestrator.CreateParams) error {
	return m.updateImageErr
}

func (m *updateImageMock) GetInstanceStatus(_ context.Context, _ string) (string, error) {
	return m.liveStatus, nil
}

// waitForStatus polls the DB until the instance status matches `want` or the
// deadline passes. Returns the final status observed.
func waitForStatus(t *testing.T, id uint, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got string
	for time.Now().Before(deadline) {
		var inst database.Instance
		if err := database.DB.First(&inst, id).Error; err != nil {
			t.Fatalf("reload instance: %v", err)
		}
		got = inst.Status
		if got == want {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return got
}

// TestUpdateInstanceImage_FailureKeepsRunningStatusWhenPodHealthy reproduces
// the production incident where a failed UpdateImage left the DB row at
// status='error' even though the pod was fine, which prevented the background
// reconcile loop from restoring SSH tunnels (including the LLM proxy tunnel on
// 127.0.0.1:40001 that the embedded agent relies on).
func TestUpdateInstanceImage_FailureKeepsRunningStatusWhenPodHealthy(t *testing.T) {
	setupTestDB(t)

	sshMgr := sshproxy.NewSSHManager(nil, "")
	tm := sshproxy.NewTunnelManager(sshMgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	mock := &updateImageMock{
		updateImageErr: errors.New("simulated image pull failure"),
		liveStatus:     "running",
	}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-uimg-ok", "Update Image — Pod Healthy")
	inst.ContainerImage = "ghcr.io/example/agent:latest"
	if err := database.DB.Save(&inst).Error; err != nil {
		t.Fatalf("save instance: %v", err)
	}
	user := createTestUser(t, "admin")

	req := buildRequest(t, "POST", "/api/v1/instances/1/update-image", user,
		map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	UpdateInstanceImage(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (body: %s)", w.Code, w.Body.String())
	}

	// UpdateImage runs in a goroutine; wait for the DB write.
	final := waitForStatus(t, inst.ID, "running", 2*time.Second)
	if final != "running" {
		t.Fatalf("expected status to remain 'running' when pod is live, got %q", final)
	}
}

// TestUpdateInstanceImage_FailureMarksErrorWhenPodUnhealthy covers the
// inverse: if the pod is genuinely not running after the failed update, the
// row must transition to 'error' so the UI surfaces the problem.
func TestUpdateInstanceImage_FailureMarksErrorWhenPodUnhealthy(t *testing.T) {
	setupTestDB(t)

	sshMgr := sshproxy.NewSSHManager(nil, "")
	tm := sshproxy.NewTunnelManager(sshMgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	mock := &updateImageMock{
		updateImageErr: errors.New("simulated image pull failure"),
		liveStatus:     "stopped",
	}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-uimg-bad", "Update Image — Pod Dead")
	inst.ContainerImage = "ghcr.io/example/agent:latest"
	if err := database.DB.Save(&inst).Error; err != nil {
		t.Fatalf("save instance: %v", err)
	}
	user := createTestUser(t, "admin")

	req := buildRequest(t, "POST", "/api/v1/instances/1/update-image", user,
		map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	// The handler's precondition check requires the orchestrator to report
	// 'running' before kicking off the update. Flip the mock to 'running' for
	// that check, then back to 'stopped' once the goroutine re-reads status.
	mock.liveStatus = "running"
	UpdateInstanceImage(w, req)
	mock.liveStatus = "stopped"

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (body: %s)", w.Code, w.Body.String())
	}

	final := waitForStatus(t, inst.ID, "error", 2*time.Second)
	if final != "error" {
		t.Fatalf("expected status 'error' when pod is not running, got %q", final)
	}
}

// TestUpdateInstanceImage_SuccessMarksRunning is the happy path — a
// successful update ends with status='running' regardless of intermediate
// state.
func TestUpdateInstanceImage_SuccessMarksRunning(t *testing.T) {
	setupTestDB(t)

	sshMgr := sshproxy.NewSSHManager(nil, "")
	tm := sshproxy.NewTunnelManager(sshMgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	mock := &updateImageMock{
		updateImageErr: nil,
		liveStatus:     "running",
	}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-uimg-ok2", "Update Image — Success")
	inst.ContainerImage = "ghcr.io/example/agent:latest"
	if err := database.DB.Save(&inst).Error; err != nil {
		t.Fatalf("save instance: %v", err)
	}
	user := createTestUser(t, "admin")

	req := buildRequest(t, "POST", "/api/v1/instances/1/update-image", user,
		map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	UpdateInstanceImage(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (body: %s)", w.Code, w.Body.String())
	}

	final := waitForStatus(t, inst.ID, "running", 2*time.Second)
	if final != "running" {
		t.Fatalf("expected status 'running' after successful update, got %q", final)
	}
}
