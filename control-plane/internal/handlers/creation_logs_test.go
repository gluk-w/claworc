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
