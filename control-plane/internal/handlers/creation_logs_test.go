package handlers_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/auth"
	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/handlers"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/go-chi/chi/v5"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// mockOrchestrator implements orchestrator.ContainerOrchestrator for testing.
type mockOrchestrator struct {
	creationLogsCh  chan string
	creationLogsErr error
}

func (m *mockOrchestrator) Initialize(_ context.Context) error                          { return nil }
func (m *mockOrchestrator) IsAvailable(_ context.Context) bool                          { return true }
func (m *mockOrchestrator) BackendName() string                                         { return "mock" }
func (m *mockOrchestrator) CreateInstance(_ context.Context, _ orchestrator.CreateParams) error {
	return nil
}
func (m *mockOrchestrator) DeleteInstance(_ context.Context, _ string) error             { return nil }
func (m *mockOrchestrator) StartInstance(_ context.Context, _ string) error              { return nil }
func (m *mockOrchestrator) StopInstance(_ context.Context, _ string) error               { return nil }
func (m *mockOrchestrator) RestartInstance(_ context.Context, _ string) error            { return nil }
func (m *mockOrchestrator) GetInstanceStatus(_ context.Context, _ string) (string, error) {
	return "creating", nil
}
func (m *mockOrchestrator) UpdateInstanceConfig(_ context.Context, _ string, _ string) error {
	return nil
}
func (m *mockOrchestrator) StreamInstanceLogs(_ context.Context, _ string, _ int, _ bool) (<-chan string, error) {
	return nil, nil
}
func (m *mockOrchestrator) StreamCreationLogs(_ context.Context, _ string) (<-chan string, error) {
	if m.creationLogsErr != nil {
		return nil, m.creationLogsErr
	}
	return m.creationLogsCh, nil
}
func (m *mockOrchestrator) CloneVolumes(_ context.Context, _, _ string) error { return nil }
func (m *mockOrchestrator) ExecInInstance(_ context.Context, _ string, _ []string) (string, string, int, error) {
	return "", "", 0, nil
}
func (m *mockOrchestrator) ExecInteractive(_ context.Context, _ string, _ []string) (*orchestrator.ExecSession, error) {
	return nil, nil
}
func (m *mockOrchestrator) ListDirectory(_ context.Context, _ string, _ string) ([]orchestrator.FileEntry, error) {
	return nil, nil
}
func (m *mockOrchestrator) ReadFile(_ context.Context, _ string, _ string) ([]byte, error) {
	return nil, nil
}
func (m *mockOrchestrator) CreateFile(_ context.Context, _ string, _ string, _ string) error {
	return nil
}
func (m *mockOrchestrator) CreateDirectory(_ context.Context, _ string, _ string) error { return nil }
func (m *mockOrchestrator) WriteFile(_ context.Context, _ string, _ string, _ []byte) error {
	return nil
}
func (m *mockOrchestrator) GetVNCBaseURL(_ context.Context, _ string, _ string) (string, error) {
	return "", nil
}
func (m *mockOrchestrator) GetGatewayWSURL(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (m *mockOrchestrator) GetHTTPTransport() http.RoundTripper { return nil }

var sessionStore *auth.SessionStore

func setupTestDB(t *testing.T) {
	t.Helper()
	var err error
	database.DB, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	// Limit to 1 connection so all goroutines share the same in-memory DB
	// (SQLite :memory: databases are per-connection by default).
	sqlDB, _ := database.DB.DB()
	sqlDB.SetMaxOpenConns(1)

	database.DB.AutoMigrate(&database.Instance{}, &database.Setting{}, &database.User{}, &database.UserInstance{})

	// Enable auth-disabled mode so RequireAuth injects the admin user from DB
	config.Cfg.AuthDisabled = true

	// Create session store for middleware
	sessionStore = auth.NewSessionStore()
	handlers.SessionStore = sessionStore
}

func createTestInstance(t *testing.T, name string) database.Instance {
	t.Helper()
	return createTestInstanceWithStatus(t, name, "creating")
}

func createTestInstanceWithStatus(t *testing.T, name, status string) database.Instance {
	t.Helper()
	inst := database.Instance{Name: name, DisplayName: name, Status: status}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create test instance: %v", err)
	}
	return inst
}

func createTestAdmin(t *testing.T) *database.User {
	t.Helper()
	user := &database.User{Username: "admin", PasswordHash: "test", Role: "admin"}
	if err := database.DB.Create(user).Error; err != nil {
		t.Fatalf("create test admin: %v", err)
	}
	return user
}

func newRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequireAuth(sessionStore))
	r.Get("/instances/{id}/creation-logs", handlers.StreamCreationLogs)
	return r
}

func TestStreamCreationLogs_Success(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test")
	createTestAdmin(t)

	ch := make(chan string, 10)
	ch <- "Waiting for pod creation..."
	ch <- "Pod scheduled to node xyz"
	ch <- "Pod is running and ready"
	close(ch)

	mock := &mockOrchestrator{creationLogsCh: ch}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))

	expected := []string{"Waiting for pod creation...", "Pod scheduled to node xyz", "Pod is running and ready"}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d events, got %d: %v", len(expected), len(lines), lines)
	}
	for i, exp := range expected {
		if lines[i] != exp {
			t.Errorf("event %d: expected %q, got %q", i, exp, lines[i])
		}
	}
}

func TestStreamCreationLogs_InstanceNotFound(t *testing.T) {
	setupTestDB(t)
	createTestAdmin(t)

	mock := &mockOrchestrator{}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", "/instances/9999/creation-logs", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestStreamCreationLogs_InvalidID(t *testing.T) {
	setupTestDB(t)
	createTestAdmin(t)

	mock := &mockOrchestrator{}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", "/instances/abc/creation-logs", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestStreamCreationLogs_OrchestratorError(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-err")
	createTestAdmin(t)

	mock := &mockOrchestrator{creationLogsErr: fmt.Errorf("connection refused")}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestStreamCreationLogs_NoOrchestrator(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-no-orch")
	createTestAdmin(t)

	orchestrator.SetForTest(nil)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestStreamCreationLogs_StoppedInstance(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstanceWithStatus(t, "bot-stopped", "stopped")
	createTestAdmin(t)

	mock := &mockOrchestrator{}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))
	if len(lines) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(lines), lines)
	}
	expectedMsg := "Instance is not in creation phase. Switch to Runtime logs or restart the instance to see creation logs."
	if lines[0] != expectedMsg {
		t.Errorf("expected %q, got %q", expectedMsg, lines[0])
	}
}

func TestStreamCreationLogs_FailedInstance(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstanceWithStatus(t, "bot-failed", "failed")
	createTestAdmin(t)

	mock := &mockOrchestrator{}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))
	if len(lines) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(lines), lines)
	}
	expectedMsg := "Instance is not in creation phase. Switch to Runtime logs or restart the instance to see creation logs."
	if lines[0] != expectedMsg {
		t.Errorf("expected %q, got %q", expectedMsg, lines[0])
	}
}

func TestStreamCreationLogs_ErrorInstance(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstanceWithStatus(t, "bot-error", "error")
	createTestAdmin(t)

	mock := &mockOrchestrator{}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))
	if len(lines) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(lines), lines)
	}
	expectedMsg := "Instance is not in creation phase. Switch to Runtime logs or restart the instance to see creation logs."
	if lines[0] != expectedMsg {
		t.Errorf("expected %q, got %q", expectedMsg, lines[0])
	}
}

// TestStreamCreationLogs_RunningInstance verifies that an already-running instance
// returns the "not in creation phase" message instead of streaming from the orchestrator.
func TestStreamCreationLogs_RunningInstance(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstanceWithStatus(t, "bot-running", "running")
	createTestAdmin(t)

	mock := &mockOrchestrator{}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))
	if len(lines) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(lines), lines)
	}
	expectedMsg := "Instance is not in creation phase. Switch to Runtime logs or restart the instance to see creation logs."
	if lines[0] != expectedMsg {
		t.Errorf("expected %q, got %q", expectedMsg, lines[0])
	}
}

// TestStreamCreationLogs_FastStartup verifies that even a very fast startup
// (channel closes quickly) still delivers all log lines.
func TestStreamCreationLogs_FastStartup(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-fast")
	createTestAdmin(t)

	ch := make(chan string, 10)
	ch <- "Pod is running and ready"
	close(ch)

	mock := &mockOrchestrator{creationLogsCh: ch}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))
	if len(lines) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(lines), lines)
	}
	if lines[0] != "Pod is running and ready" {
		t.Errorf("expected %q, got %q", "Pod is running and ready", lines[0])
	}
}

// TestStreamCreationLogs_SlowImagePull verifies that pulling progress messages
// are streamed correctly during a slow image pull.
func TestStreamCreationLogs_SlowImagePull(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-slow-pull")
	createTestAdmin(t)

	ch := make(chan string, 10)
	ch <- "[2026-02-20 14:23:01] [STATUS] Waiting for pod creation..."
	ch <- "[2026-02-20 14:23:03] [EVENT] kubelet: Pulling image ghcr.io/openclaw/agent:latest"
	ch <- "[2026-02-20 14:23:15] [EVENT] kubelet: Still pulling image ghcr.io/openclaw/agent:latest"
	ch <- "[2026-02-20 14:23:45] [EVENT] kubelet: Successfully pulled image ghcr.io/openclaw/agent:latest"
	ch <- "[2026-02-20 14:23:46] [STATUS] Pod is running and ready"
	close(ch)

	mock := &mockOrchestrator{creationLogsCh: ch}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))
	if len(lines) != 5 {
		t.Fatalf("expected 5 events for slow image pull, got %d: %v", len(lines), lines)
	}
	// Verify pulling progress appears in log stream
	if !strings.Contains(lines[1], "Pulling image") {
		t.Errorf("expected pulling progress in event 1, got %q", lines[1])
	}
	if !strings.Contains(lines[3], "Successfully pulled") {
		t.Errorf("expected pull success in event 3, got %q", lines[3])
	}
}

// TestStreamCreationLogs_ImagePullFailure verifies that image pull failure errors
// are correctly streamed to the client.
func TestStreamCreationLogs_ImagePullFailure(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-pull-fail")
	createTestAdmin(t)

	ch := make(chan string, 10)
	ch <- "[2026-02-20 14:23:01] [STATUS] Waiting for pod creation..."
	ch <- "[2026-02-20 14:23:03] [EVENT] kubelet: Pulling image ghcr.io/openclaw/agent:nonexistent"
	ch <- "[2026-02-20 14:23:10] [STATUS] Container main: ImagePullBackOff - Back-off pulling image ghcr.io/openclaw/agent:nonexistent"
	ch <- "[2026-02-20 14:23:30] [EVENT] kubelet: Failed to pull image ghcr.io/openclaw/agent:nonexistent: rpc error: code = NotFound"
	close(ch)

	mock := &mockOrchestrator{creationLogsCh: ch}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))
	if len(lines) != 4 {
		t.Fatalf("expected 4 events for image pull failure, got %d: %v", len(lines), lines)
	}
	// Verify error events appear
	if !strings.Contains(lines[2], "ImagePullBackOff") {
		t.Errorf("expected ImagePullBackOff in event 2, got %q", lines[2])
	}
	if !strings.Contains(lines[3], "Failed to pull image") {
		t.Errorf("expected pull failure in event 3, got %q", lines[3])
	}
}

// TestStreamCreationLogs_ContextCancellation verifies that the handler
// stops streaming when the client disconnects (context cancelled).
func TestStreamCreationLogs_ContextCancellation(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-cancel")
	createTestAdmin(t)

	ch := make(chan string, 10)
	ch <- "First log line"
	// Don't close the channel — simulate a long-running stream

	mock := &mockOrchestrator{creationLogsCh: ch}
	orchestrator.SetForTest(mock)

	router := newRouter()

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		router.ServeHTTP(w, req)
		close(done)
	}()

	// Cancel the context to simulate client disconnect
	cancel()

	// Handler should return promptly
	<-done

	body, _ := io.ReadAll(w.Result().Body)
	lines := parseSSEData(string(body))
	// Should have received at most the first line (may or may not have been flushed)
	if len(lines) > 1 {
		t.Errorf("expected at most 1 event after cancellation, got %d: %v", len(lines), lines)
	}
}

// TestStreamCreationLogs_EmptyChannel verifies that an immediately-closed channel
// (no log lines) results in a valid SSE response with no data events.
func TestStreamCreationLogs_EmptyChannel(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-empty")
	createTestAdmin(t)

	ch := make(chan string, 10)
	close(ch)

	mock := &mockOrchestrator{creationLogsCh: ch}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))
	if len(lines) != 0 {
		t.Errorf("expected 0 events for empty channel, got %d: %v", len(lines), lines)
	}
}

// TestStreamCreationLogs_AllNonCreatingStatuses verifies that all terminal statuses
// short-circuit with the "not in creation phase" message.
func TestStreamCreationLogs_AllNonCreatingStatuses(t *testing.T) {
	statuses := []string{"running", "stopped", "failed", "error"}
	expectedMsg := "Instance is not in creation phase. Switch to Runtime logs or restart the instance to see creation logs."

	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			setupTestDB(t)
			inst := createTestInstanceWithStatus(t, fmt.Sprintf("bot-%s", status), status)
			createTestAdmin(t)

			mock := &mockOrchestrator{}
			orchestrator.SetForTest(mock)

			router := newRouter()

			req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			resp := w.Result()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status %q: expected 200, got %d: %s", status, resp.StatusCode, string(body))
			}

			body, _ := io.ReadAll(resp.Body)
			lines := parseSSEData(string(body))
			if len(lines) != 1 {
				t.Fatalf("status %q: expected 1 event, got %d: %v", status, len(lines), lines)
			}
			if lines[0] != expectedMsg {
				t.Errorf("status %q: expected %q, got %q", status, expectedMsg, lines[0])
			}
		})
	}
}

// TestStreamCreationLogs_MultipleInstances verifies that multiple instances
// stream their own independent creation log events without cross-contamination.
func TestStreamCreationLogs_MultipleInstances(t *testing.T) {
	setupTestDB(t)
	inst1 := createTestInstance(t, "bot-multi-1")
	inst2 := createTestInstance(t, "bot-multi-2")
	inst3 := createTestInstance(t, "bot-multi-3")
	createTestAdmin(t)

	ch1 := make(chan string, 10)
	ch2 := make(chan string, 10)
	ch3 := make(chan string, 10)

	// concurrentMockOrchestrator routes channels per instance name
	mock := &concurrentMockOrchestrator{
		channels: map[string]chan string{
			"bot-multi-1": ch1,
			"bot-multi-2": ch2,
			"bot-multi-3": ch3,
		},
	}
	orchestrator.SetForTest(mock)

	router := newRouter()

	// Pre-fill channels with instance-specific events
	ch1 <- "Instance 1: Scheduling"
	ch1 <- "Instance 1: Pulling image"
	ch1 <- "Instance 1: Ready"
	close(ch1)

	ch2 <- "Instance 2: Scheduling"
	ch2 <- "Instance 2: Ready"
	close(ch2)

	ch3 <- "Instance 3: Scheduling"
	ch3 <- "Instance 3: Pulling image"
	ch3 <- "Instance 3: Container creating"
	ch3 <- "Instance 3: Ready"
	close(ch3)

	// Stream each instance sequentially and verify isolation
	type testCase struct {
		inst          database.Instance
		expectedCount int
		prefix        string
	}
	cases := []testCase{
		{inst1, 3, "Instance 1:"},
		{inst2, 2, "Instance 2:"},
		{inst3, 4, "Instance 3:"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", tc.inst.ID), nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("%s: expected 200, got %d: %s", tc.inst.Name, resp.StatusCode, string(body))
		}

		body, _ := io.ReadAll(resp.Body)
		lines := parseSSEData(string(body))
		if len(lines) != tc.expectedCount {
			t.Fatalf("%s: expected %d events, got %d: %v", tc.inst.Name, tc.expectedCount, len(lines), lines)
		}
		for _, line := range lines {
			if !strings.HasPrefix(line, tc.prefix) {
				t.Errorf("%s: unexpected event %q (expected prefix %q)", tc.inst.Name, line, tc.prefix)
			}
		}
	}
}

// TestStreamCreationLogs_AccessDenied verifies that a non-admin user without
// instance assignment gets 403 when accessing creation logs.
func TestStreamCreationLogs_AccessDenied(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-denied")

	// Create a regular user (not admin) without instance assignment
	user := &database.User{Username: "regular", PasswordHash: "test", Role: "user"}
	if err := database.DB.Create(user).Error; err != nil {
		t.Fatalf("create test user: %v", err)
	}

	// Disable auth-disabled mode to use proper auth flow
	config.Cfg.AuthDisabled = false

	// Create session for the regular user
	token, err := sessionStore.Create(user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	mock := &mockOrchestrator{}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}

	// Restore auth-disabled for other tests
	config.Cfg.AuthDisabled = true
}

// TestStreamCreationLogs_Unauthenticated verifies that requests without
// authentication credentials get 401.
func TestStreamCreationLogs_Unauthenticated(t *testing.T) {
	setupTestDB(t)
	createTestInstance(t, "bot-unauth")

	// Disable auth-disabled mode
	config.Cfg.AuthDisabled = false

	mock := &mockOrchestrator{}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", "/instances/1/creation-logs", nil)
	// No cookie or session
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	// Restore auth-disabled for other tests
	config.Cfg.AuthDisabled = true
}

// TestStreamCreationLogs_SSEHeaders verifies all required SSE headers are set.
func TestStreamCreationLogs_SSEHeaders(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-headers")
	createTestAdmin(t)

	ch := make(chan string, 1)
	ch <- "test"
	close(ch)

	mock := &mockOrchestrator{creationLogsCh: ch}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("expected Cache-Control no-cache, got %q", resp.Header.Get("Cache-Control"))
	}
	if resp.Header.Get("Connection") != "keep-alive" {
		t.Errorf("expected Connection keep-alive, got %q", resp.Header.Get("Connection"))
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Errorf("expected X-Accel-Buffering no, got %q", resp.Header.Get("X-Accel-Buffering"))
	}
}

// TestStreamCreationLogs_AdditionalStatuses verifies creation logs work correctly
// for additional statuses like "stopping" and "restarting".
func TestStreamCreationLogs_AdditionalStatuses(t *testing.T) {
	// These statuses are not in the short-circuit list, so they should
	// attempt to stream from the orchestrator
	statuses := []string{"stopping", "restarting"}

	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			setupTestDB(t)
			inst := createTestInstanceWithStatus(t, fmt.Sprintf("bot-%s", status), status)
			createTestAdmin(t)

			ch := make(chan string, 1)
			ch <- "Stream event during " + status
			close(ch)

			mock := &mockOrchestrator{creationLogsCh: ch}
			orchestrator.SetForTest(mock)

			router := newRouter()

			req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			resp := w.Result()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status %q: expected 200, got %d: %s", status, resp.StatusCode, string(body))
			}
		})
	}
}

// --- Docker-specific E2E verification tests ---
// These tests verify that Docker creation log event patterns flow correctly
// through the SSE handler, matching the DockerOrchestrator.StreamCreationLogs output.

// TestStreamCreationLogs_DockerFullLifecycle simulates a complete Docker container
// creation lifecycle: container not found → created → running → health starting → healthy + logs.
func TestStreamCreationLogs_DockerFullLifecycle(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-docker-lifecycle")
	createTestAdmin(t)

	ch := make(chan string, 20)
	ch <- "Waiting for container creation..."
	ch <- "Container status: created"
	ch <- "Container status: running"
	ch <- "Health: starting"
	ch <- "Health: healthy"
	ch <- "systemd[1]: Started OpenClaw Agent Service"
	ch <- "clawd[42]: Listening on port 8080"
	ch <- "Container is running and healthy"
	close(ch)

	mock := &mockOrchestrator{creationLogsCh: ch}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))

	expected := []string{
		"Waiting for container creation...",
		"Container status: created",
		"Container status: running",
		"Health: starting",
		"Health: healthy",
		"systemd[1]: Started OpenClaw Agent Service",
		"clawd[42]: Listening on port 8080",
		"Container is running and healthy",
	}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d events, got %d: %v", len(expected), len(lines), lines)
	}
	for i, exp := range expected {
		if lines[i] != exp {
			t.Errorf("event %d: expected %q, got %q", i, exp, lines[i])
		}
	}
}

// TestStreamCreationLogs_DockerContainerNotFound simulates the scenario where the
// Docker container hasn't been created yet (container not found polling).
func TestStreamCreationLogs_DockerContainerNotFound(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-docker-notfound")
	createTestAdmin(t)

	ch := make(chan string, 10)
	ch <- "Waiting for container creation..."
	ch <- "Waiting for container creation..."
	ch <- "Container status: created"
	ch <- "Container status: running"
	ch <- "Container is running and healthy"
	close(ch)

	mock := &mockOrchestrator{creationLogsCh: ch}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))
	if len(lines) != 5 {
		t.Fatalf("expected 5 events, got %d: %v", len(lines), lines)
	}
	// Both "Waiting for container creation..." messages should come through
	if lines[0] != "Waiting for container creation..." {
		t.Errorf("event 0: expected waiting message, got %q", lines[0])
	}
	if lines[4] != "Container is running and healthy" {
		t.Errorf("event 4: expected healthy message, got %q", lines[4])
	}
}

// TestStreamCreationLogs_DockerInspectError simulates errors from Docker
// ContainerInspect (e.g., daemon connectivity issues).
func TestStreamCreationLogs_DockerInspectError(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-docker-inspect-err")
	createTestAdmin(t)

	ch := make(chan string, 10)
	ch <- "Waiting for container creation..."
	ch <- "Error inspecting container: connection refused"
	ch <- "Container status: created"
	ch <- "Container status: running"
	ch <- "Container is running and healthy"
	close(ch)

	mock := &mockOrchestrator{creationLogsCh: ch}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))
	if len(lines) != 5 {
		t.Fatalf("expected 5 events, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[1], "Error inspecting container") {
		t.Errorf("event 1: expected inspect error, got %q", lines[1])
	}
}

// TestStreamCreationLogs_DockerTimeout simulates a Docker container that fails
// to become healthy within the timeout period.
func TestStreamCreationLogs_DockerTimeout(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-docker-timeout")
	createTestAdmin(t)

	ch := make(chan string, 10)
	ch <- "Waiting for container creation..."
	ch <- "Container status: created"
	ch <- "Container status: running"
	ch <- "Health: starting"
	ch <- "Timed out waiting for container to become ready"
	close(ch)

	mock := &mockOrchestrator{creationLogsCh: ch}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))
	if len(lines) != 5 {
		t.Fatalf("expected 5 events, got %d: %v", len(lines), lines)
	}
	if lines[4] != "Timed out waiting for container to become ready" {
		t.Errorf("expected timeout message, got %q", lines[4])
	}
}

// TestStreamCreationLogs_DockerHealthFailure simulates a container that starts
// but health checks report unhealthy status.
func TestStreamCreationLogs_DockerHealthFailure(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-docker-unhealthy")
	createTestAdmin(t)

	ch := make(chan string, 10)
	ch <- "Container status: created"
	ch <- "Container status: running"
	ch <- "Health: starting"
	ch <- "Health: unhealthy"
	ch <- "Timed out waiting for container to become ready"
	close(ch)

	mock := &mockOrchestrator{creationLogsCh: ch}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))
	if len(lines) != 5 {
		t.Fatalf("expected 5 events, got %d: %v", len(lines), lines)
	}
	if lines[3] != "Health: unhealthy" {
		t.Errorf("event 3: expected unhealthy status, got %q", lines[3])
	}
}

// TestStreamCreationLogs_DockerFastStartup simulates a Docker container that
// starts almost instantly (no waiting phase, immediate health).
func TestStreamCreationLogs_DockerFastStartup(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-docker-fast")
	createTestAdmin(t)

	ch := make(chan string, 10)
	ch <- "Container status: running"
	ch <- "Health: healthy"
	ch <- "Container is running and healthy"
	close(ch)

	mock := &mockOrchestrator{creationLogsCh: ch}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))
	if len(lines) != 3 {
		t.Fatalf("expected 3 events for fast Docker startup, got %d: %v", len(lines), lines)
	}
	if lines[0] != "Container status: running" {
		t.Errorf("event 0: expected running status, got %q", lines[0])
	}
	if lines[2] != "Container is running and healthy" {
		t.Errorf("event 2: expected healthy message, got %q", lines[2])
	}
}

// TestStreamCreationLogs_DockerWithContainerLogs simulates Docker creation logs
// that include container stdout/stderr lines (stripped of Docker mux headers).
func TestStreamCreationLogs_DockerWithContainerLogs(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-docker-logs")
	createTestAdmin(t)

	ch := make(chan string, 20)
	ch <- "Container status: created"
	ch <- "Container status: running"
	ch <- "Health: starting"
	ch <- "Health: healthy"
	// These simulate container stdout lines after Docker log header stripping
	ch <- "Starting services..."
	ch <- "VNC server started on display :1"
	ch <- "noVNC listening on port 6080"
	ch <- "Chrome remote debugging on port 9222"
	ch <- "OpenClaw agent ready"
	ch <- "Container is running and healthy"
	close(ch)

	mock := &mockOrchestrator{creationLogsCh: ch}
	orchestrator.SetForTest(mock)

	router := newRouter()

	req := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst.ID), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	lines := parseSSEData(string(body))
	if len(lines) != 10 {
		t.Fatalf("expected 10 events, got %d: %v", len(lines), lines)
	}
	// Verify container logs are interleaved correctly
	if lines[4] != "Starting services..." {
		t.Errorf("event 4: expected container log, got %q", lines[4])
	}
	if lines[8] != "OpenClaw agent ready" {
		t.Errorf("event 8: expected container log, got %q", lines[8])
	}
}

// TestStreamCreationLogs_DockerMultipleInstances verifies that multiple Docker
// instances stream independent creation events without cross-contamination.
func TestStreamCreationLogs_DockerMultipleInstances(t *testing.T) {
	setupTestDB(t)
	inst1 := createTestInstance(t, "bot-docker-1")
	inst2 := createTestInstance(t, "bot-docker-2")
	createTestAdmin(t)

	ch1 := make(chan string, 10)
	ch2 := make(chan string, 10)

	mock := &concurrentMockOrchestrator{
		channels: map[string]chan string{
			"bot-docker-1": ch1,
			"bot-docker-2": ch2,
		},
	}
	orchestrator.SetForTest(mock)

	// Docker instance 1: fast startup
	ch1 <- "Container status: running"
	ch1 <- "Health: healthy"
	ch1 <- "Container is running and healthy"
	close(ch1)

	// Docker instance 2: slow with health retries
	ch2 <- "Waiting for container creation..."
	ch2 <- "Container status: created"
	ch2 <- "Container status: running"
	ch2 <- "Health: starting"
	ch2 <- "Health: starting"
	ch2 <- "Health: healthy"
	ch2 <- "Container is running and healthy"
	close(ch2)

	router := newRouter()

	// Stream instance 1
	req1 := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst1.ID), nil)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	body1, _ := io.ReadAll(w1.Result().Body)
	lines1 := parseSSEData(string(body1))
	if len(lines1) != 3 {
		t.Fatalf("instance 1: expected 3 events, got %d: %v", len(lines1), lines1)
	}
	for _, line := range lines1 {
		if strings.Contains(line, "Waiting for") || strings.Contains(line, "created") {
			t.Errorf("instance 1: unexpected slow-path event leaked: %q", line)
		}
	}

	// Stream instance 2
	req2 := httptest.NewRequest("GET", fmt.Sprintf("/instances/%d/creation-logs", inst2.ID), nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	body2, _ := io.ReadAll(w2.Result().Body)
	lines2 := parseSSEData(string(body2))
	if len(lines2) != 7 {
		t.Fatalf("instance 2: expected 7 events, got %d: %v", len(lines2), lines2)
	}
	if lines2[0] != "Waiting for container creation..." {
		t.Errorf("instance 2 event 0: expected waiting msg, got %q", lines2[0])
	}
}

// concurrentMockOrchestrator routes StreamCreationLogs to per-instance channels.
type concurrentMockOrchestrator struct {
	mockOrchestrator
	channels map[string]chan string
}

func (m *concurrentMockOrchestrator) StreamCreationLogs(_ context.Context, name string) (<-chan string, error) {
	if ch, ok := m.channels[name]; ok {
		return ch, nil
	}
	return nil, fmt.Errorf("no channel for instance %s", name)
}

// --- Concurrent instance creation logging tests ---
// These tests verify that multiple instances can stream creation logs simultaneously
// with truly parallel HTTP connections, without cross-contamination or blocking.

// TestStreamCreationLogs_ConcurrentStreaming verifies that 3 instances stream
// creation logs in parallel via a real HTTP server, each receiving only its own
// events without cross-contamination.
func TestStreamCreationLogs_ConcurrentStreaming(t *testing.T) {
	setupTestDB(t)
	inst1 := createTestInstance(t, "bot-conc-1")
	inst2 := createTestInstance(t, "bot-conc-2")
	inst3 := createTestInstance(t, "bot-conc-3")
	createTestAdmin(t)

	ch1 := make(chan string, 20)
	ch2 := make(chan string, 20)
	ch3 := make(chan string, 20)

	mock := &concurrentMockOrchestrator{
		channels: map[string]chan string{
			"bot-conc-1": ch1,
			"bot-conc-2": ch2,
			"bot-conc-3": ch3,
		},
	}
	orchestrator.SetForTest(mock)

	// Pre-fill channels with interleaved events (simulates events arriving
	// in mixed order across instances) then close them.
	ch1 <- "Instance 1: Scheduling pod"
	ch2 <- "Instance 2: Scheduling pod"
	ch3 <- "Instance 3: Scheduling pod"
	ch2 <- "Instance 2: Pulling image"
	ch1 <- "Instance 1: Pulling image"
	ch3 <- "Instance 3: Pulling image"
	ch3 <- "Instance 3: Container creating"
	ch1 <- "Instance 1: Pod running"
	ch2 <- "Instance 2: Pod running"
	ch3 <- "Instance 3: Pod running"
	close(ch1)
	close(ch2)
	close(ch3)

	router := newRouter()
	ts := httptest.NewServer(router)
	defer ts.Close()

	type result struct {
		instName string
		lines    []string
		err      error
	}
	results := make(chan result, 3)

	// Launch 3 concurrent SSE readers against the real HTTP server
	for _, tc := range []struct {
		inst database.Instance
		name string
	}{
		{inst1, "bot-conc-1"},
		{inst2, "bot-conc-2"},
		{inst3, "bot-conc-3"},
	} {
		go func(inst database.Instance, name string) {
			resp, err := http.Get(fmt.Sprintf("%s/instances/%d/creation-logs", ts.URL, inst.ID))
			if err != nil {
				results <- result{name, nil, err}
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			results <- result{name, parseSSEData(string(body)), nil}
		}(tc.inst, tc.name)
	}

	// Collect results from all 3 streams
	collected := map[string][]string{}
	for i := 0; i < 3; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("%s: HTTP error: %v", r.instName, r.err)
		}
		collected[r.instName] = r.lines
	}

	// Verify instance 1: 3 events, all prefixed with "Instance 1:"
	if len(collected["bot-conc-1"]) != 3 {
		t.Fatalf("bot-conc-1: expected 3 events, got %d: %v", len(collected["bot-conc-1"]), collected["bot-conc-1"])
	}
	for _, line := range collected["bot-conc-1"] {
		if !strings.HasPrefix(line, "Instance 1:") {
			t.Errorf("bot-conc-1: unexpected event %q (expected prefix 'Instance 1:')", line)
		}
	}

	// Verify instance 2: 3 events, all prefixed with "Instance 2:"
	if len(collected["bot-conc-2"]) != 3 {
		t.Fatalf("bot-conc-2: expected 3 events, got %d: %v", len(collected["bot-conc-2"]), collected["bot-conc-2"])
	}
	for _, line := range collected["bot-conc-2"] {
		if !strings.HasPrefix(line, "Instance 2:") {
			t.Errorf("bot-conc-2: unexpected event %q (expected prefix 'Instance 2:')", line)
		}
	}

	// Verify instance 3: 4 events, all prefixed with "Instance 3:"
	if len(collected["bot-conc-3"]) != 4 {
		t.Fatalf("bot-conc-3: expected 4 events, got %d: %v", len(collected["bot-conc-3"]), collected["bot-conc-3"])
	}
	for _, line := range collected["bot-conc-3"] {
		if !strings.HasPrefix(line, "Instance 3:") {
			t.Errorf("bot-conc-3: unexpected event %q (expected prefix 'Instance 3:')", line)
		}
	}
}

// TestStreamCreationLogs_ConcurrentNoBlocking verifies that a slow-streaming instance
// does not block a fast-streaming instance — the fast one completes independently
// while the slow one continues streaming.
func TestStreamCreationLogs_ConcurrentNoBlocking(t *testing.T) {
	setupTestDB(t)
	instFast := createTestInstance(t, "bot-fast-conc")
	instSlow := createTestInstance(t, "bot-slow-conc")
	createTestAdmin(t)

	chFast := make(chan string, 10)
	chSlow := make(chan string, 10)

	mock := &concurrentMockOrchestrator{
		channels: map[string]chan string{
			"bot-fast-conc": chFast,
			"bot-slow-conc": chSlow,
		},
	}
	orchestrator.SetForTest(mock)

	// Pre-fill fast channel with all events and close it
	chFast <- "Fast: Pod ready"
	close(chFast)

	// Pre-fill slow channel with first event only (don't close yet)
	chSlow <- "Slow: Still pulling image..."

	router := newRouter()
	ts := httptest.NewServer(router)
	defer ts.Close()

	fastDone := make(chan []string, 1)
	slowDone := make(chan []string, 1)

	// Start both streams concurrently
	go func() {
		resp, err := http.Get(fmt.Sprintf("%s/instances/%d/creation-logs", ts.URL, instFast.ID))
		if err != nil {
			t.Errorf("fast stream error: %v", err)
			fastDone <- nil
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		fastDone <- parseSSEData(string(body))
	}()

	go func() {
		resp, err := http.Get(fmt.Sprintf("%s/instances/%d/creation-logs", ts.URL, instSlow.ID))
		if err != nil {
			t.Errorf("slow stream error: %v", err)
			slowDone <- nil
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		slowDone <- parseSSEData(string(body))
	}()

	// Fast stream should complete without waiting for slow stream
	fastLines := <-fastDone
	if len(fastLines) != 1 {
		t.Fatalf("fast stream: expected 1 event, got %d: %v", len(fastLines), fastLines)
	}
	if fastLines[0] != "Fast: Pod ready" {
		t.Errorf("fast stream: expected 'Fast: Pod ready', got %q", fastLines[0])
	}

	// Now complete the slow stream (send remaining events and close)
	chSlow <- "Slow: Image pulled"
	chSlow <- "Slow: Pod running"
	close(chSlow)

	slowLines := <-slowDone
	if len(slowLines) != 3 {
		t.Fatalf("slow stream: expected 3 events, got %d: %v", len(slowLines), slowLines)
	}
	for _, line := range slowLines {
		if !strings.HasPrefix(line, "Slow:") {
			t.Errorf("slow stream: unexpected event %q (expected prefix 'Slow:')", line)
		}
	}
}

// TestStreamCreationLogs_ConcurrentClientDisconnect verifies that one client disconnecting
// does not affect other concurrent streams.
func TestStreamCreationLogs_ConcurrentClientDisconnect(t *testing.T) {
	setupTestDB(t)
	inst1 := createTestInstance(t, "bot-disc-1")
	inst2 := createTestInstance(t, "bot-disc-2")
	createTestAdmin(t)

	ch1 := make(chan string, 10)
	ch2 := make(chan string, 10)

	mock := &concurrentMockOrchestrator{
		channels: map[string]chan string{
			"bot-disc-1": ch1,
			"bot-disc-2": ch2,
		},
	}
	orchestrator.SetForTest(mock)

	// Pre-fill stream 2 with all events and close it
	ch2 <- "Stream 2: First event"
	ch2 <- "Stream 2: Second event"
	ch2 <- "Stream 2: Third event"
	close(ch2)

	// Stream 1 gets an event but stays open (will be cancelled)
	ch1 <- "Stream 1: First event"

	router := newRouter()
	ts := httptest.NewServer(router)
	defer ts.Close()

	stream2Done := make(chan []string, 1)

	// Start stream 1 with a cancellable context
	ctx1, cancel1 := context.WithCancel(context.Background())
	req1, _ := http.NewRequestWithContext(ctx1, "GET", fmt.Sprintf("%s/instances/%d/creation-logs", ts.URL, inst1.ID), nil)

	go func() {
		resp, err := http.DefaultClient.Do(req1)
		if err != nil {
			return // Expected after cancel
		}
		resp.Body.Close()
	}()

	// Start stream 2
	go func() {
		resp, err := http.Get(fmt.Sprintf("%s/instances/%d/creation-logs", ts.URL, inst2.ID))
		if err != nil {
			t.Errorf("stream 2 error: %v", err)
			stream2Done <- nil
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		stream2Done <- parseSSEData(string(body))
	}()

	// Cancel stream 1 (simulate client disconnect)
	cancel1()
	close(ch1)

	// Stream 2 should complete with all 3 events despite stream 1 disconnect
	lines2 := <-stream2Done
	if len(lines2) != 3 {
		t.Fatalf("stream 2: expected 3 events, got %d: %v", len(lines2), lines2)
	}
	for _, line := range lines2 {
		if !strings.HasPrefix(line, "Stream 2:") {
			t.Errorf("stream 2: unexpected event %q (expected prefix 'Stream 2:')", line)
		}
	}
}

// parseSSEData extracts "data: ..." lines from SSE output.
func parseSSEData(body string) []string {
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			lines = append(lines, strings.TrimPrefix(line, "data: "))
		}
	}
	return lines
}
