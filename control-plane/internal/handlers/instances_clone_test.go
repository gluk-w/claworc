package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/taskmanager"
)

// blockingMockOrchestrator embeds the default mock but blocks CreateInstance
// on a channel so a clone test can keep the work goroutine paused mid-flight,
// observe the in-flight task, and trigger cancellation while still inside
// CreateInstance. Counts also let us assert which cleanup paths fired.
type blockingMockOrchestrator struct {
	mockOrchestrator

	createBlock           chan struct{}
	deleteCalls           atomic.Int32
	deleteBrowserPodCalls atomic.Int32
}

func (m *blockingMockOrchestrator) CreateInstance(ctx context.Context, _ orchestrator.CreateParams) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.createBlock:
		return nil
	}
}

func (m *blockingMockOrchestrator) DeleteInstance(_ context.Context, _ string) error {
	m.deleteCalls.Add(1)
	return nil
}

func (m *blockingMockOrchestrator) DeleteBrowserPod(_ context.Context, _ uint) error {
	m.deleteBrowserPodCalls.Add(1)
	return nil
}

// withTaskMgr installs a fresh TaskManager for the test and tears it down on
// cleanup. The cleanup also cancels any still-running tasks and waits for
// them to terminate before returning, so a goroutine leaked from one test
// can't write to the next test's freshly-recreated database.
func withTaskMgr(t *testing.T) *taskmanager.Manager {
	t.Helper()
	tm := taskmanager.New(taskmanager.Config{
		RetainTerminal:   500 * time.Millisecond,
		SubscriberBuffer: 8,
		GCInterval:       100 * time.Millisecond,
	})
	TaskMgr = tm
	t.Cleanup(func() {
		for _, task := range tm.List(taskmanager.Filter{OnlyActive: true}) {
			_ = tm.Cancel(task.ID)
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if len(tm.List(taskmanager.Filter{OnlyActive: true})) == 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		TaskMgr = nil
		tm.Close()
	})
	return tm
}

// TestCloneOnCancel_DeletesDestRow_PreservesSrc unit-tests the OnCancel
// closure in isolation: given a source row and a (partially-created)
// destination row, invoking the closure must remove the destination from
// the DB and call DeleteInstance / DeleteBrowserPod on the orchestrator,
// while leaving the source row untouched.
func TestCloneOnCancel_DeletesDestRow_PreservesSrc(t *testing.T) {
	setupTestDB(t)

	mock := &blockingMockOrchestrator{}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	src := createTestInstance(t, "bot-src", "Src")
	dst := createTestInstance(t, "bot-dst", "Dst")

	cloneOnCancel(dst.ID, dst.Name)(context.Background())

	var srcReloaded database.Instance
	if err := database.DB.First(&srcReloaded, src.ID).Error; err != nil {
		t.Fatalf("source row should still exist: %v", err)
	}
	var dstReloaded database.Instance
	if err := database.DB.First(&dstReloaded, dst.ID).Error; err == nil {
		t.Fatalf("destination row should have been deleted, still found: %+v", dstReloaded)
	}
	if mock.deleteCalls.Load() != 1 {
		t.Errorf("DeleteInstance calls = %d, want 1", mock.deleteCalls.Load())
	}
	if mock.deleteBrowserPodCalls.Load() != 1 {
		t.Errorf("DeleteBrowserPod calls = %d, want 1", mock.deleteBrowserPodCalls.Load())
	}
}

// TestCloneInstance_RegistersCancellableTask drives the HTTP handler and
// verifies the resulting TaskManager task is wired with the right title,
// description, instance type, and is cancellable.
func TestCloneInstance_RegistersCancellableTask(t *testing.T) {
	setupTestDB(t)

	mock := &blockingMockOrchestrator{createBlock: make(chan struct{})}
	defer close(mock.createBlock) // unblock pending Run when test ends
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	SSHMgr = sshproxy.NewSSHManager(nil, "")
	defer func() { SSHMgr = nil }()

	tm := withTaskMgr(t)

	src := createTestInstance(t, "bot-src", "Source Bot")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "POST", "/api/v1/instances/{id}/clone", user, map[string]string{"id": fmt.Sprintf("%d", src.ID)})
	w := httptest.NewRecorder()
	CloneInstance(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("clone HTTP status = %d, want 201; body=%s", w.Code, w.Body.String())
	}

	// Wait for the task to register and Run to hit the blocking CreateInstance.
	var task taskmanager.Task
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tasks := tm.List(taskmanager.Filter{Type: taskmanager.TaskInstanceClone})
		if len(tasks) == 1 {
			task = tasks[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if task.ID == "" {
		t.Fatalf("clone task was never registered with TaskManager")
	}
	if !task.Cancellable {
		t.Errorf("clone task should be cancellable, got Cancellable=false")
	}
	if task.Title != "Cloning instance" {
		t.Errorf("title = %q, want %q", task.Title, "Cloning instance")
	}
	// Task message is set via h.UpdateMessage as the first thing Run does;
	// give the goroutine a tick to apply it before we assert.
	deadline = time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		t2, _ := tm.Get(task.ID)
		if t2.Message != "" {
			task = t2
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(task.Message, "Source Bot") || !strings.Contains(task.Message, " to ") {
		t.Errorf("description = %q, want it to contain source name and ' to '", task.Message)
	}

	// Cancel the in-flight task so its work goroutine exits before the test
	// returns. Otherwise it would keep running into the next test's freshly-
	// migrated database and produce flaky cross-test failures.
	if err := tm.Cancel(task.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := tm.Get(task.ID)
		if got.State == taskmanager.StateCanceled {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestCloneInstance_Cancel_TransitionsToCanceled drives the HTTP handler,
// cancels the in-flight task, and verifies the task transitions to the
// terminal "canceled" state with the OnCancel cleanup callback firing
// (DeleteInstance / DeleteBrowserPod observed on the orchestrator). The
// DB-cleanup behavior of OnCancel itself is covered by the unit test
// TestCloneOnCancel_DeletesDestRow_PreservesSrc; we deliberately don't
// assert DB rows here because GORM's Update("status","error") in the work
// goroutine races with OnCancel's Delete on the same connection and the
// observable order is non-deterministic on in-memory SQLite.
func TestCloneInstance_Cancel_TransitionsToCanceled(t *testing.T) {
	setupTestDB(t)
	if err := database.DB.AutoMigrate(&database.LLMProvider{}, &database.LLMGatewayKey{}, &database.BrowserSession{}); err != nil {
		t.Fatalf("migrate ancillary tables: %v", err)
	}

	mock := &blockingMockOrchestrator{createBlock: make(chan struct{})}
	defer close(mock.createBlock)
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	SSHMgr = sshproxy.NewSSHManager(nil, "")
	defer func() { SSHMgr = nil }()

	tm := withTaskMgr(t)

	src := createTestInstance(t, "bot-src2", "Src2")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "POST", "/api/v1/instances/{id}/clone", user, map[string]string{"id": fmt.Sprintf("%d", src.ID)})
	w := httptest.NewRecorder()
	CloneInstance(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("clone HTTP status = %d, want 201; body=%s", w.Code, w.Body.String())
	}

	var taskID string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tasks := tm.List(taskmanager.Filter{Type: taskmanager.TaskInstanceClone})
		if len(tasks) == 1 {
			taskID = tasks[0].ID
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if taskID == "" {
		t.Fatalf("clone task never registered")
	}
	if err := tm.Cancel(taskID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := tm.Get(taskID)
		if got.State == taskmanager.StateCanceled {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := tm.Get(taskID)
	if got.State != taskmanager.StateCanceled {
		t.Fatalf("task state = %s, want canceled", got.State)
	}
	if mock.deleteCalls.Load() < 1 {
		t.Errorf("OnCancel must call orchestrator.DeleteInstance, got 0 calls")
	}
	if mock.deleteBrowserPodCalls.Load() < 1 {
		t.Errorf("OnCancel must call orchestrator.DeleteBrowserPod, got 0 calls")
	}
}

// cloneSetup is shared scaffolding for the §1 row-fidelity tests below: it
// installs the default mock orchestrator (fast returns), an in-memory SSH
// manager, and a TaskMgr — so the async clone goroutine can run without
// panicking on nil dependencies. We don't care about the goroutine's
// outcome here; the assertions are on the immediate handler response and
// the dst DB row.
//
// The post-setup `SetMaxOpenConns(1)` is load-bearing: SQLite `:memory:` is
// per-connection, so without pinning the pool to a single conn the async
// clone goroutine and the test's main goroutine can grab different (empty)
// in-memory databases and the test sees "no such table: instances" on a
// row the handler just created.
func cloneSetup(t *testing.T) {
	t.Helper()
	setupTestDB(t)
	if sqlDB, err := database.DB.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	mock := &mockOrchestrator{}
	orchestrator.Set(mock)
	t.Cleanup(func() { orchestrator.Set(nil) })
	SSHMgr = sshproxy.NewSSHManager(nil, "")
	t.Cleanup(func() { SSHMgr = nil })
	withTaskMgr(t)
}

// reqClone builds an authenticated POST /api/v1/instances/{id}/clone request
// with the chi route param wired so the handler resolves {id}.
func reqClone(t *testing.T, srcID uint, user *database.User) *http.Request {
	t.Helper()
	return buildRequest(t, "POST", "/api/v1/instances/{id}/clone", user, map[string]string{"id": fmt.Sprintf("%d", srcID)})
}

// TestCloneInstance_CopiesBrowserConfig verifies the on-demand browser
// configuration carries over to the clone — the fix that motivated this
// test. Each browser field is set to a non-default value on src; we then
// fetch the dst row from the DB and confirm each field matches.
func TestCloneInstance_CopiesBrowserConfig(t *testing.T) {
	cloneSetup(t)

	idle := 17
	src := createTestInstance(t, "bot-bcfg", "BCfg")
	if err := database.DB.Model(&src).Updates(map[string]interface{}{
		"browser_provider":     "docker",
		"browser_image":        "registry.example.com/custom-browser:1.2.3",
		"browser_idle_minutes": idle,
		"browser_storage":      "20Gi",
		"browser_active":       false,
	}).Error; err != nil {
		t.Fatalf("seed src browser fields: %v", err)
	}

	user := createTestUser(t, "admin")
	w := httptest.NewRecorder()
	CloneInstance(w, reqClone(t, src.ID, user))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	dstID := uint(resp["id"].(float64))

	var dst database.Instance
	if err := database.DB.First(&dst, dstID).Error; err != nil {
		t.Fatalf("load dst row: %v", err)
	}
	if dst.BrowserProvider != "docker" {
		t.Errorf("BrowserProvider = %q, want %q", dst.BrowserProvider, "docker")
	}
	if dst.BrowserImage != "registry.example.com/custom-browser:1.2.3" {
		t.Errorf("BrowserImage = %q", dst.BrowserImage)
	}
	if dst.BrowserIdleMinutes == nil || *dst.BrowserIdleMinutes != idle {
		t.Errorf("BrowserIdleMinutes = %v, want *int=%d", dst.BrowserIdleMinutes, idle)
	}
	if dst.BrowserStorage != "20Gi" {
		t.Errorf("BrowserStorage = %q", dst.BrowserStorage)
	}
	if dst.BrowserActive != false {
		t.Errorf("BrowserActive = %v, want false", dst.BrowserActive)
	}
}

// TestCloneInstance_AssignsNewGatewayToken verifies the clone gets its own
// (different) encrypted gateway token. Carrying over the same token would
// let one instance impersonate another at the LLM gateway.
func TestCloneInstance_AssignsNewGatewayToken(t *testing.T) {
	cloneSetup(t)
	src := createTestInstance(t, "bot-gw", "GW")
	// Seed a known encrypted token on src so we have something to compare.
	if err := database.DB.Model(&src).Update("gateway_token", "src-token-blob").Error; err != nil {
		t.Fatalf("seed src token: %v", err)
	}

	user := createTestUser(t, "admin")
	w := httptest.NewRecorder()
	CloneInstance(w, reqClone(t, src.ID, user))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d", w.Code)
	}
	resp := parseResponse(t, w)
	dstID := uint(resp["id"].(float64))

	var dst database.Instance
	if err := database.DB.First(&dst, dstID).Error; err != nil {
		t.Fatalf("load dst: %v", err)
	}
	if dst.GatewayToken == "" {
		t.Fatalf("dst.GatewayToken is empty")
	}
	if dst.GatewayToken == "src-token-blob" {
		t.Errorf("dst inherited src's gateway token; expected a fresh one")
	}
}

// TestCloneInstance_AssignsNextSortOrder verifies the new clone slots in
// AFTER every existing instance in the dashboard ordering. Pre-seed two
// rows with non-contiguous sort_order values; clone src; assert dst gets
// max+1.
func TestCloneInstance_AssignsNextSortOrder(t *testing.T) {
	cloneSetup(t)
	src := createTestInstance(t, "bot-so", "SO")
	if err := database.DB.Model(&src).Update("sort_order", 3).Error; err != nil {
		t.Fatalf("seed src sort_order: %v", err)
	}
	other := createTestInstance(t, "bot-other", "Other")
	if err := database.DB.Model(&other).Update("sort_order", 7).Error; err != nil {
		t.Fatalf("seed other sort_order: %v", err)
	}

	user := createTestUser(t, "admin")
	w := httptest.NewRecorder()
	CloneInstance(w, reqClone(t, src.ID, user))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d", w.Code)
	}
	resp := parseResponse(t, w)
	dstID := uint(resp["id"].(float64))

	var dst database.Instance
	if err := database.DB.First(&dst, dstID).Error; err != nil {
		t.Fatalf("load dst: %v", err)
	}
	if dst.SortOrder != 8 {
		t.Errorf("SortOrder = %d, want 8 (max=7 + 1)", dst.SortOrder)
	}
}

// TestCloneInstance_NameCollisionGetsSuffix exercises the duplicate-name
// path: when generateName() produces a name that already exists, the
// handler appends a hex suffix. Pre-seed a row at the auto-generated name
// to force the collision.
func TestCloneInstance_NameCollisionGetsSuffix(t *testing.T) {
	cloneSetup(t)

	src := createTestInstance(t, "bot-cn", "CN")
	// Compute the name CloneInstance will try first, then plant a row at it
	// so the handler is forced into the suffix branch.
	collidingName := generateName(src.DisplayName + " (Copy)")
	if _, err := database.DB.DB(); err != nil {
		t.Fatalf("DB handle: %v", err)
	}
	colliding := database.Instance{Name: collidingName, DisplayName: "placeholder", Status: "running"}
	if err := database.DB.Create(&colliding).Error; err != nil {
		t.Fatalf("seed colliding row: %v", err)
	}

	user := createTestUser(t, "admin")
	w := httptest.NewRecorder()
	CloneInstance(w, reqClone(t, src.ID, user))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	dstID := uint(resp["id"].(float64))

	var dst database.Instance
	if err := database.DB.First(&dst, dstID).Error; err != nil {
		t.Fatalf("load dst: %v", err)
	}
	if dst.Name == collidingName {
		t.Fatalf("dst name = %q matches colliding name; expected suffix", dst.Name)
	}
	if !strings.HasPrefix(dst.Name, collidingName+"-") {
		t.Errorf("dst name = %q, expected prefix %q + '-' + hex", dst.Name, collidingName)
	}
	// Suffix is 6 hex chars from 3 random bytes.
	suffix := strings.TrimPrefix(dst.Name, collidingName+"-")
	if len(suffix) != 6 {
		t.Errorf("suffix length = %d, want 6 (hex of 3 bytes); name=%q", len(suffix), dst.Name)
	}
}

// TestCloneInstance_NotFound — non-existent source ID must yield 404.
func TestCloneInstance_NotFound(t *testing.T) {
	cloneSetup(t)
	user := createTestUser(t, "admin")
	w := httptest.NewRecorder()
	CloneInstance(w, reqClone(t, 9999, user))
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

// TestCloneInstance_InvalidID — non-numeric {id} must yield 400.
func TestCloneInstance_InvalidID(t *testing.T) {
	cloneSetup(t)
	user := createTestUser(t, "admin")
	req := buildRequest(t, "POST", "/api/v1/instances/{id}/clone", user, map[string]string{"id": "not-a-number"})
	w := httptest.NewRecorder()
	CloneInstance(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// TestCloneOnCancel_StopsTunnels exercises the TunnelMgr cleanup branch in
// cloneOnCancel. We install a real TunnelManager (no SSH backend, so no
// tunnels exist), trigger the cleanup, and confirm it ran without error
// and the destination instance has zero tunnels. Mirrors the
// "verify clean state after teardown" pattern used in
// TestStopInstance_StopsTunnels.
func TestCloneOnCancel_StopsTunnels(t *testing.T) {
	setupTestDB(t)
	mock := &mockOrchestrator{}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	sshMgr := sshproxy.NewSSHManager(nil, "")
	SSHMgr = sshMgr
	defer func() { SSHMgr = nil }()
	tm := sshproxy.NewTunnelManager(sshMgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	dst := createTestInstance(t, "bot-tn", "TN")
	cloneOnCancel(dst.ID, dst.Name)(context.Background())

	if got := tm.GetTunnelsForInstance(dst.ID); len(got) != 0 {
		t.Errorf("tunnels after cleanup = %d, want 0", len(got))
	}
}

// TestCloneOnCancel_DeletesBrowserSessionRow seeds a BrowserSession row for
// the destination instance, triggers cloneOnCancel, and verifies the row
// is gone. Catches a regression where the OnCancel callback forgets to
// sweep this table — a partial cleanup would leave a stale row pointing at
// a deleted instance ID.
func TestCloneOnCancel_DeletesBrowserSessionRow(t *testing.T) {
	setupTestDB(t)
	if err := database.DB.AutoMigrate(&database.BrowserSession{}); err != nil {
		t.Fatalf("migrate BrowserSession: %v", err)
	}
	mock := &mockOrchestrator{}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	dst := createTestInstance(t, "bot-bs", "BS")
	if err := database.UpsertBrowserSession(&database.BrowserSession{
		InstanceID: dst.ID,
		Provider:   "docker",
		Status:     "running",
		Image:      "img",
	}); err != nil {
		t.Fatalf("seed BrowserSession: %v", err)
	}
	if _, err := database.GetBrowserSession(dst.ID); err != nil {
		t.Fatalf("seed sanity check failed: %v", err)
	}

	cloneOnCancel(dst.ID, dst.Name)(context.Background())

	if got, err := database.GetBrowserSession(dst.ID); err == nil {
		t.Errorf("BrowserSession row still present after cleanup: %+v", got)
	}
}

// TestCloneOnCancel_DeletesProviderAndGatewayRows seeds LLMProvider and
// LLMGatewayKey rows tied to the destination instance and confirms both
// are removed by the OnCancel sweep. These rows must follow the instance
// row to avoid foreign-key-like dangling references.
func TestCloneOnCancel_DeletesProviderAndGatewayRows(t *testing.T) {
	setupTestDB(t)
	if err := database.DB.AutoMigrate(&database.LLMProvider{}, &database.LLMGatewayKey{}); err != nil {
		t.Fatalf("migrate provider/gateway tables: %v", err)
	}
	mock := &mockOrchestrator{}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	dst := createTestInstance(t, "bot-pg", "PG")
	dstIDp := dst.ID
	provider := database.LLMProvider{
		InstanceID: &dstIDp,
		Key:        "openai-x",
		Provider:   "openai",
		Name:       "X",
		BaseURL:    "https://example.com",
	}
	if err := database.DB.Create(&provider).Error; err != nil {
		t.Fatalf("seed LLMProvider: %v", err)
	}
	if err := database.DB.Create(&database.LLMGatewayKey{
		InstanceID: dst.ID,
		ProviderID: provider.ID,
		GatewayKey: "claworc-vk-test",
	}).Error; err != nil {
		t.Fatalf("seed LLMGatewayKey: %v", err)
	}

	cloneOnCancel(dst.ID, dst.Name)(context.Background())

	var providers int64
	database.DB.Model(&database.LLMProvider{}).Where("instance_id = ?", dst.ID).Count(&providers)
	if providers != 0 {
		t.Errorf("LLMProvider rows for dst after cleanup = %d, want 0", providers)
	}
	var gateway int64
	database.DB.Model(&database.LLMGatewayKey{}).Where("instance_id = ?", dst.ID).Count(&gateway)
	if gateway != 0 {
		t.Errorf("LLMGatewayKey rows for dst after cleanup = %d, want 0", gateway)
	}
}
