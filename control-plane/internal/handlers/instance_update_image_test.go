package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"gorm.io/gorm"
)

// updateImageMock wraps mockOrchestrator so UpdateImage and GetInstanceStatus
// can be overridden per test.
type updateImageMock struct {
	mockOrchestrator
	updateImageErr error

	mu         sync.Mutex
	liveStatus string

	// gate, when non-nil, blocks UpdateImage until closed.
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

// waitForStatusDB polls the given DB handle until the instance status matches
// `want` or the deadline passes. Uses a captured DB reference instead of the
// global database.DB to avoid races with parallel tests that call setupTestDB.
func waitForStatusDB(t *testing.T, db *gorm.DB, id uint, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got string
	for time.Now().Before(deadline) {
		var inst database.Instance
		if err := db.First(&inst, id).Error; err != nil {
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

// awaitGoroutine waits for UpdateImage to return (via wg.Wait), then yields
// the CPU and sleeps briefly to let the handler's goroutine execute the DB
// write that follows UpdateImage. Returns the DB handle captured before the
// handler was called — using this (rather than database.DB) insulates the
// polling loop from concurrent setupTestDB calls in parallel tests.
func awaitGoroutine(mock *updateImageMock) {
	mock.wg.Wait()
	runtime.Gosched()
	time.Sleep(100 * time.Millisecond)
}

// TestUpdateInstanceImage_FailureKeepsRunningStatusWhenPodHealthy reproduces
// the production incident where a failed UpdateImage left the DB row at
// status='error' even though the pod was fine, which prevented the background
// reconcile loop from restoring SSH tunnels (including the LLM proxy tunnel on
// 127.0.0.1:40001 that the embedded agent relies on).
func TestUpdateInstanceImage_FailureKeepsRunningStatusWhenPodHealthy(t *testing.T) {
	setupTestDB(t)
	ensureStatusMessageColumn(t)
	db := database.DB // capture before handler fires its goroutine

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

	awaitGoroutine(mock)

	final := waitForStatusDB(t, db, inst.ID, "running", 5*time.Second)
	if final != "running" {
		t.Fatalf("expected status to remain 'running' when pod is live, got %q", final)
	}
}

// TestUpdateInstanceImage_FailureMarksErrorWhenPodUnhealthy covers the
// inverse: if the pod is genuinely not running after the failed update, the
// row must transition to 'error' so the UI surfaces the problem.
func TestUpdateInstanceImage_FailureMarksErrorWhenPodUnhealthy(t *testing.T) {
	setupTestDB(t)
	ensureStatusMessageColumn(t)
	db := database.DB

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

	awaitGoroutine(mock)

	final := waitForStatusDB(t, db, inst.ID, "error", 5*time.Second)
	if final != "error" {
		t.Fatalf("expected status 'error' when pod is not running, got %q", final)
	}
}

// TestUpdateInstanceImage_SuccessMarksRunning is the happy path — a
// successful update ends with status='running' regardless of intermediate
// state.
func TestUpdateInstanceImage_SuccessMarksRunning(t *testing.T) {
	setupTestDB(t)
	ensureStatusMessageColumn(t)
	db := database.DB

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

	awaitGoroutine(mock)

	final := waitForStatusDB(t, db, inst.ID, "running", 5*time.Second)
	if final != "running" {
		t.Fatalf("expected status 'running' after successful update, got %q", final)
	}
}
