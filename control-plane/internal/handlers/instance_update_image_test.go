package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

// updateImageMock wraps mockOrchestrator so UpdateImage and GetInstanceStatus
// can be overridden per test. The WaitGroup lets tests wait until the handler's
// background goroutine fully completes (including the DB write after UpdateImage
// returns), preventing cross-test races on the global database.DB.
type updateImageMock struct {
	mockOrchestrator
	updateImageErr error

	mu         sync.Mutex
	liveStatus string

	// gate, when non-nil, blocks UpdateImage until closed. This lets the test
	// change mock state (e.g. liveStatus) before the goroutine proceeds past
	// UpdateImage to its post-failure GetInstanceStatus check.
	gate chan struct{}

	wg sync.WaitGroup
}

func (m *updateImageMock) UpdateImage(_ context.Context, _ string, _ orchestrator.CreateParams) error {
	defer m.wg.Done()
	if m.gate != nil {
		<-m.gate
	}
	return m.updateImageErr
}

func (m *updateImageMock) GetInstanceStatus(_ context.Context, _ string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.liveStatus, nil
}

func (m *updateImageMock) setLiveStatus(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.liveStatus = s
}

// ensureStatusMessageColumn adds the `status_message` column to the test DB
// so GORM's map-based Updates in UpdateInstanceImage don't fail on SQLite
// with "no such column" (production has this column via migration, but the
// Instance struct used by AutoMigrate in setupTestDB does not declare it).
func ensureStatusMessageColumn(t *testing.T) {
	t.Helper()
	database.DB.Exec("ALTER TABLE instances ADD COLUMN status_message TEXT")
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
	ensureStatusMessageColumn(t)

	sshMgr := sshproxy.NewSSHManager(nil, "")
	tm := sshproxy.NewTunnelManager(sshMgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	mock := &updateImageMock{
		updateImageErr: errors.New("simulated image pull failure"),
		liveStatus:     "running",
	}
	mock.wg.Add(1)
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

	final := waitForStatus(t, inst.ID, "running", 5*time.Second)
	if final != "running" {
		t.Fatalf("expected status to remain 'running' when pod is live, got %q", final)
	}

	mock.wg.Wait()
}

// TestUpdateInstanceImage_FailureMarksErrorWhenPodUnhealthy covers the
// inverse: if the pod is genuinely not running after the failed update, the
// row must transition to 'error' so the UI surfaces the problem.
func TestUpdateInstanceImage_FailureMarksErrorWhenPodUnhealthy(t *testing.T) {
	setupTestDB(t)
	ensureStatusMessageColumn(t)

	sshMgr := sshproxy.NewSSHManager(nil, "")
	tm := sshproxy.NewTunnelManager(sshMgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	gate := make(chan struct{})
	mock := &updateImageMock{
		updateImageErr: errors.New("simulated image pull failure"),
		liveStatus:     "running",
		gate:           gate,
	}
	mock.wg.Add(1)
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

	UpdateInstanceImage(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Flip status BEFORE unblocking the goroutine so that the post-failure
	// GetInstanceStatus check inside the goroutine sees "stopped".
	mock.setLiveStatus("stopped")
	close(gate)

	final := waitForStatus(t, inst.ID, "error", 5*time.Second)
	if final != "error" {
		t.Fatalf("expected status 'error' when pod is not running, got %q", final)
	}

	mock.wg.Wait()
}

// TestUpdateInstanceImage_SuccessMarksRunning is the happy path — a
// successful update ends with status='running' regardless of intermediate
// state.
func TestUpdateInstanceImage_SuccessMarksRunning(t *testing.T) {
	setupTestDB(t)
	ensureStatusMessageColumn(t)

	sshMgr := sshproxy.NewSSHManager(nil, "")
	tm := sshproxy.NewTunnelManager(sshMgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	mock := &updateImageMock{
		updateImageErr: nil,
		liveStatus:     "running",
	}
	mock.wg.Add(1)
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

	final := waitForStatus(t, inst.ID, "running", 5*time.Second)
	if final != "running" {
		t.Fatalf("expected status 'running' after successful update, got %q", final)
	}

	mock.wg.Wait()
}
