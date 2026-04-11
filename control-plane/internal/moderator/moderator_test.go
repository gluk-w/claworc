package moderator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ---- Mocks ----------------------------------------------------------------

type mockStore struct {
	mu       sync.Mutex
	tasks    map[uint]Task
	boards   map[uint]Board
	comments []Comment
	souls    map[uint]Soul
	nextID   uint
}

func newMockStore() *mockStore {
	return &mockStore{
		tasks:  map[uint]Task{},
		boards: map[uint]Board{},
		souls:  map[uint]Soul{},
		nextID: 1,
	}
}

func (s *mockStore) GetTask(_ context.Context, id uint) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, fmt.Errorf("task %d not found", id)
	}
	return t, nil
}

func (s *mockStore) UpdateTask(_ context.Context, id uint, fields map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return fmt.Errorf("task %d not found", id)
	}
	if v, ok := fields["status"]; ok {
		t.Status = v.(string)
	}
	if v, ok := fields["assigned_instance_id"]; ok {
		uid := v.(uint)
		t.AssignedInstanceID = &uid
	}
	if v, ok := fields["open_claw_session_id"]; ok {
		t.OpenClawSessionID = v.(string)
	}
	s.tasks[id] = t
	return nil
}

func (s *mockStore) GetBoard(_ context.Context, id uint) (Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.boards[id]
	if !ok {
		return Board{}, fmt.Errorf("board %d not found", id)
	}
	return b, nil
}

func (s *mockStore) InsertComment(_ context.Context, c Comment) (uint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	c.ID = s.nextID
	s.comments = append(s.comments, c)
	return c.ID, nil
}

func (s *mockStore) SetCommentBody(_ context.Context, id uint, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.comments {
		if c.ID == id {
			s.comments[i].Body = body
			return nil
		}
	}
	return nil
}

func (s *mockStore) ListComments(_ context.Context, taskID uint) ([]Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Comment
	for _, c := range s.comments {
		if c.TaskID == taskID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *mockStore) InsertArtifact(_ context.Context, _ Artifact) error { return nil }
func (s *mockStore) ListTaskArtifacts(_ context.Context, _ uint) ([]Artifact, error) {
	return nil, nil
}

func (s *mockStore) GetSouls(_ context.Context, ids []uint) ([]Soul, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Soul
	for _, id := range ids {
		if sl, ok := s.souls[id]; ok {
			out = append(out, sl)
		}
	}
	return out, nil
}

func (s *mockStore) UpsertSoul(_ context.Context, soul Soul) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.souls[soul.InstanceID] = soul
	return nil
}

func (s *mockStore) commentsOfKind(taskID uint, kind string) []Comment {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Comment
	for _, c := range s.comments {
		if c.TaskID == taskID && c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

type mockLLM struct {
	response string
	err      error
}

func (m *mockLLM) Complete(_ context.Context, _, _, _ string) (string, error) {
	return m.response, m.err
}

type mockSettings struct{}

func (m *mockSettings) ModeratorProvider() (string, string)  { return "test-key", "test-model" }
func (m *mockSettings) SummaryInterval() time.Duration       { return time.Minute }
func (m *mockSettings) ArtifactMaxBytes() int64              { return 5 * 1024 * 1024 }
func (m *mockSettings) ArtifactStorageDir() string           { return "/tmp/test-artifacts" }
func (m *mockSettings) WorkspaceDir() string                 { return "/home/claworc/.openclaw/workspace" }
func (m *mockSettings) TaskOutcomeDir() string               { return "/home/claworc/tasks" }

type mockInstances struct {
	ids   []uint
	names map[uint]string
}

func (m *mockInstances) ListInstanceIDs(_ context.Context) ([]uint, error) {
	return m.ids, nil
}

func (m *mockInstances) InstanceName(_ context.Context, id uint) (string, error) {
	if n, ok := m.names[id]; ok {
		return n, nil
	}
	return "", fmt.Errorf("not found")
}

type mockDialer struct {
	err error
}

func (m *mockDialer) Dial(_ context.Context, _ uint, _ string) (GatewayConn, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &mockConn{}, nil
}

type mockConn struct {
	sent   [][]byte
	recvCh chan []byte
}

func (m *mockConn) Send(_ context.Context, frame []byte) error {
	m.sent = append(m.sent, frame)
	return nil
}
func (m *mockConn) Recv(ctx context.Context) ([]byte, error) {
	if m.recvCh == nil {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case data := <-m.recvCh:
		return data, nil
	}
}
func (m *mockConn) Close() error { return nil }

type mockWorkspaceFS struct{}

func (m *mockWorkspaceFS) List(_ context.Context, _ uint, _ string) ([]FileEntry, error) {
	return nil, nil
}
func (m *mockWorkspaceFS) Read(_ context.Context, _ uint, _ string) ([]byte, error) {
	return nil, fmt.Errorf("not found")
}
func (m *mockWorkspaceFS) Write(_ context.Context, _ uint, _ string, _ []byte) error { return nil }
func (m *mockWorkspaceFS) MkdirAll(_ context.Context, _ uint, _ string) error        { return nil }
func (m *mockWorkspaceFS) RemoveAll(_ context.Context, _ uint, _ string) error        { return nil }

// ---- Tests ----------------------------------------------------------------

func newTestService(store *mockStore) *Service {
	return New(Options{
		Dialer:    &mockDialer{},
		Workspace: &mockWorkspaceFS{},
		LLM:       &mockLLM{},
		Store:     store,
		Settings:  &mockSettings{},
		Instances: &mockInstances{
			ids:   []uint{1, 2},
			names: map[uint]string{1: "agent-alpha", 2: "agent-beta"},
		},
	})
}

func TestNew(t *testing.T) {
	t.Parallel()
	svc := newTestService(newMockStore())
	if svc == nil {
		t.Fatal("New returned nil")
	}
	if svc.running == nil {
		t.Fatal("running map not initialized")
	}
}

func TestStop_NotRunning(t *testing.T) {
	t.Parallel()
	svc := newTestService(newMockStore())
	if svc.Stop(999) {
		t.Error("Stop should return false for non-running task")
	}
}

func TestStop_RunningTask(t *testing.T) {
	t.Parallel()
	svc := newTestService(newMockStore())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc.mu.Lock()
	svc.running[42] = cancel
	svc.mu.Unlock()

	if !svc.Stop(42) {
		t.Error("Stop should return true for running task")
	}

	// Verify context was canceled.
	select {
	case <-ctx.Done():
		// ok
	default:
		t.Error("context should be canceled after Stop")
	}
}

func TestMarkStopped(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	store.tasks[1] = Task{ID: 1, BoardID: 1, Status: "in_progress"}
	svc := newTestService(store)

	svc.markStopped(1)

	task := store.tasks[1]
	if task.Status != "todo" {
		t.Errorf("status = %q, want todo", task.Status)
	}
	comments := store.commentsOfKind(1, "moderator")
	if len(comments) != 1 || comments[0].Body != "Task stopped." {
		t.Errorf("expected 'Task stopped.' comment, got %v", comments)
	}
}

func TestMarkFailed(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	store.tasks[1] = Task{ID: 1, BoardID: 1, Status: "in_progress"}
	svc := newTestService(store)

	svc.markFailed(context.Background(), 1, errors.New("something broke"))

	task := store.tasks[1]
	if task.Status != "failed" {
		t.Errorf("status = %q, want failed", task.Status)
	}
	errComments := store.commentsOfKind(1, "error")
	if len(errComments) != 1 || errComments[0].Body != "something broke" {
		t.Errorf("expected error comment, got %v", errComments)
	}
}

func TestReopen(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	store.tasks[1] = Task{ID: 1, BoardID: 1, Status: "done"}
	store.boards[1] = Board{ID: 1, EligibleInstances: []uint{1}}
	svc := newTestService(store)

	svc.Reopen(1)

	// Give goroutine time to start.
	time.Sleep(50 * time.Millisecond)

	store.mu.Lock()
	status := store.tasks[1].Status
	store.mu.Unlock()

	// After reopen, task should have been set to "todo" then picked up by EnqueueTask.
	// It will either be dispatching/in_progress or failed (since mock dialer returns empty conn).
	if status == "done" {
		t.Errorf("task should not still be in done status after Reopen")
	}
}

// ---- Dispatch tests -------------------------------------------------------

func TestDispatch_SingleInstance(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	store.tasks[1] = Task{ID: 1, BoardID: 1, Title: "Test", Description: "Do something", Status: "todo"}
	store.boards[1] = Board{ID: 1, EligibleInstances: []uint{1}}

	svc := newTestService(store)
	if err := svc.Dispatch(context.Background(), 1); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	task := store.tasks[1]
	if task.Status != "dispatching" {
		t.Errorf("status = %q, want dispatching", task.Status)
	}
	if task.AssignedInstanceID == nil || *task.AssignedInstanceID != 1 {
		t.Errorf("assigned_instance_id = %v, want 1", task.AssignedInstanceID)
	}

	routing := store.commentsOfKind(1, "routing")
	if len(routing) != 1 {
		t.Fatalf("expected 1 routing comment, got %d", len(routing))
	}
	if routing[0].Body == "" {
		t.Error("routing comment should not be empty")
	}
}

func TestDispatch_MultipleInstances_LLMPick(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	store.tasks[1] = Task{ID: 1, BoardID: 1, Title: "Code review", Description: "Review PR #42", Status: "todo"}
	store.boards[1] = Board{ID: 1, EligibleInstances: []uint{1, 2}}
	store.souls[1] = Soul{InstanceID: 1, Summary: "Python expert"}
	store.souls[2] = Soul{InstanceID: 2, Summary: "Go expert"}

	svc := New(Options{
		Dialer:    &mockDialer{},
		Workspace: &mockWorkspaceFS{},
		LLM:       &mockLLM{response: `{"instance_id": 2, "reason": "Go expert is better for this task"}`},
		Store:     store,
		Settings:  &mockSettings{},
		Instances: &mockInstances{ids: []uint{1, 2}, names: map[uint]string{1: "alpha", 2: "beta"}},
	})

	if err := svc.Dispatch(context.Background(), 1); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	task := store.tasks[1]
	if task.AssignedInstanceID == nil || *task.AssignedInstanceID != 2 {
		t.Errorf("assigned_instance_id = %v, want 2", task.AssignedInstanceID)
	}
}

func TestDispatch_LLMFailure_FallsBackToFirst(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	store.tasks[1] = Task{ID: 1, BoardID: 1, Title: "Test", Description: "test", Status: "todo"}
	store.boards[1] = Board{ID: 1, EligibleInstances: []uint{1, 2}}

	svc := New(Options{
		Dialer:    &mockDialer{},
		Workspace: &mockWorkspaceFS{},
		LLM:       &mockLLM{err: errors.New("API error")},
		Store:     store,
		Settings:  &mockSettings{},
		Instances: &mockInstances{ids: []uint{1, 2}, names: map[uint]string{1: "a", 2: "b"}},
	})

	if err := svc.Dispatch(context.Background(), 1); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	task := store.tasks[1]
	if task.AssignedInstanceID == nil || *task.AssignedInstanceID != 1 {
		t.Errorf("should fall back to first instance, got %v", task.AssignedInstanceID)
	}
}

func TestDispatch_NoEligibleInstances(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	store.tasks[1] = Task{ID: 1, BoardID: 1, Status: "todo"}
	store.boards[1] = Board{ID: 1, EligibleInstances: []uint{}}

	svc := newTestService(store)
	err := svc.Dispatch(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error for empty eligible instances")
	}
}

func TestDispatch_LLMGarbageResponse_FallsBack(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	store.tasks[1] = Task{ID: 1, BoardID: 1, Title: "Test", Description: "desc", Status: "todo"}
	store.boards[1] = Board{ID: 1, EligibleInstances: []uint{1, 2}}

	svc := New(Options{
		Dialer:    &mockDialer{},
		Workspace: &mockWorkspaceFS{},
		LLM:       &mockLLM{response: "I'm not sure, maybe instance 2?"},
		Store:     store,
		Settings:  &mockSettings{},
		Instances: &mockInstances{ids: []uint{1, 2}, names: map[uint]string{1: "a", 2: "b"}},
	})

	if err := svc.Dispatch(context.Background(), 1); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	task := store.tasks[1]
	if task.AssignedInstanceID == nil || *task.AssignedInstanceID != 1 {
		t.Errorf("garbage LLM response should fall back to first instance, got %v", task.AssignedInstanceID)
	}
}

// ---- parseRankReply -------------------------------------------------------

func TestParseRankReply(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		wantID   uint
		wantOK   bool
	}{
		{"valid json", `{"instance_id": 5, "reason": "best fit"}`, 5, true},
		{"json in text", `Sure! {"instance_id": 3, "reason": "because"} ok?`, 3, true},
		{"numeric string id", `{"instance_id": "7", "reason": "ok"}`, 7, true},
		{"no json", "I think instance 2 is best", 0, false},
		{"empty", "", 0, false},
		{"malformed json", `{"instance_id":}`, 0, false},
		{"missing id", `{"reason": "no id"}`, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, _, ok := parseRankReply(tt.input)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && id != tt.wantID {
				t.Errorf("id = %d, want %d", id, tt.wantID)
			}
		})
	}
}

func TestContainsUint(t *testing.T) {
	t.Parallel()
	if !containsUint([]uint{1, 2, 3}, 2) {
		t.Error("expected true for 2 in [1,2,3]")
	}
	if containsUint([]uint{1, 2, 3}, 4) {
		t.Error("expected false for 4 in [1,2,3]")
	}
	if containsUint(nil, 1) {
		t.Error("expected false for nil slice")
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
	if got := truncate("hello world", 5); got != "hello…" {
		t.Errorf("got %q, want hello…", got)
	}
	if got := truncate("", 5); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ---- EnqueueTask integration (dispatch fails → markFailed) ----------------

func TestEnqueueTask_DispatchFails_MarksFailed(t *testing.T) {
	store := newMockStore()
	store.tasks[1] = Task{ID: 1, BoardID: 99, Status: "todo"} // board 99 doesn't exist

	svc := newTestService(store)
	svc.EnqueueTask(1)

	// Wait for goroutine to complete.
	time.Sleep(100 * time.Millisecond)

	store.mu.Lock()
	task := store.tasks[1]
	store.mu.Unlock()

	if task.Status != "failed" {
		t.Errorf("status = %q, want failed", task.Status)
	}
	errComments := store.commentsOfKind(1, "error")
	if len(errComments) == 0 {
		t.Error("expected error comment after dispatch failure")
	}
}

func TestEnqueueTask_StopDuringDispatch_MarksAsTodo(t *testing.T) {
	// Create a store where GetBoard blocks until canceled.
	store := newMockStore()
	store.tasks[1] = Task{ID: 1, BoardID: 1, Status: "todo"}
	store.boards[1] = Board{ID: 1, EligibleInstances: []uint{1}}

	// Use an LLM that blocks forever (simulating slow dispatch).
	blockingLLM := &blockingMockLLM{ch: make(chan struct{})}
	svc := New(Options{
		Dialer:    &mockDialer{},
		Workspace: &mockWorkspaceFS{},
		LLM:       blockingLLM,
		Store:     store,
		Settings:  &mockSettings{},
		Instances: &mockInstances{ids: []uint{1, 2}, names: map[uint]string{1: "a", 2: "b"}},
	})

	// Use 2 instances so dispatch calls LLM for ranking.
	store.boards[1] = Board{ID: 1, EligibleInstances: []uint{1, 2}}

	svc.EnqueueTask(1)
	time.Sleep(20 * time.Millisecond)

	svc.Stop(1)
	close(blockingLLM.ch) // unblock
	time.Sleep(100 * time.Millisecond)

	store.mu.Lock()
	task := store.tasks[1]
	store.mu.Unlock()

	if task.Status != "todo" {
		t.Errorf("stopped task status = %q, want todo", task.Status)
	}
}

type blockingMockLLM struct {
	ch chan struct{}
}

func (b *blockingMockLLM) Complete(ctx context.Context, _, _, _ string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-b.ch:
		return `{"instance_id": 1, "reason": "test"}`, nil
	}
}

// ---- Dispatch with context cancellation -----------------------------------

func TestDispatch_ContextCanceled_RankFallsBack(t *testing.T) {
	t.Parallel()
	// When the LLM call fails due to context cancellation, rank() gracefully
	// falls back to the first candidate. Dispatch still succeeds — context
	// cancellation is only surfaced when store calls fail.
	store := newMockStore()
	store.tasks[1] = Task{ID: 1, BoardID: 1, Title: "T", Description: "d", Status: "todo"}
	store.boards[1] = Board{ID: 1, EligibleInstances: []uint{1, 2}}

	svc := New(Options{
		Dialer:    &mockDialer{},
		Workspace: &mockWorkspaceFS{},
		LLM:       &mockLLM{err: context.Canceled},
		Store:     store,
		Settings:  &mockSettings{},
		Instances: &mockInstances{ids: []uint{1, 2}, names: map[uint]string{1: "a", 2: "b"}},
	})

	err := svc.Dispatch(context.Background(), 1)
	if err != nil {
		t.Fatalf("Dispatch should succeed with LLM fallback, got %v", err)
	}

	task := store.tasks[1]
	if task.AssignedInstanceID == nil || *task.AssignedInstanceID != 1 {
		t.Errorf("should fall back to first candidate, got %v", task.AssignedInstanceID)
	}
}

// ---- Dispatch uses task-level evaluator overrides -------------------------

func TestDispatch_UsesTaskEvaluatorOverride(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	store.tasks[1] = Task{
		ID: 1, BoardID: 1, Title: "Test", Description: "desc", Status: "todo",
		EvaluatorProviderKey: "custom-provider",
		EvaluatorModel:       "custom-model",
	}
	store.boards[1] = Board{ID: 1, EligibleInstances: []uint{1, 2}}

	captureLLM := &capturingMockLLM{}
	svc := New(Options{
		Dialer:    &mockDialer{},
		Workspace: &mockWorkspaceFS{},
		LLM:       captureLLM,
		Store:     store,
		Settings:  &mockSettings{},
		Instances: &mockInstances{ids: []uint{1, 2}, names: map[uint]string{1: "a", 2: "b"}},
	})

	_ = svc.Dispatch(context.Background(), 1)

	if captureLLM.lastProviderKey != "custom-provider" {
		t.Errorf("provider = %q, want custom-provider", captureLLM.lastProviderKey)
	}
	if captureLLM.lastModel != "custom-model" {
		t.Errorf("model = %q, want custom-model", captureLLM.lastModel)
	}
}

type capturingMockLLM struct {
	lastProviderKey string
	lastModel       string
}

func (c *capturingMockLLM) Complete(_ context.Context, providerKey, model, _ string) (string, error) {
	c.lastProviderKey = providerKey
	c.lastModel = model
	return fmt.Sprintf(`{"instance_id": 1, "reason": "test"}`), nil
}

// ---- LLM picks invalid instance ID → fallback ----------------------------

func TestDispatch_LLMPicksInvalidID_FallsBack(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	store.tasks[1] = Task{ID: 1, BoardID: 1, Title: "T", Description: "d", Status: "todo"}
	store.boards[1] = Board{ID: 1, EligibleInstances: []uint{1, 2}}

	// LLM picks instance 99 which is not in eligible list.
	svc := New(Options{
		Dialer:    &mockDialer{},
		Workspace: &mockWorkspaceFS{},
		LLM:       &mockLLM{response: `{"instance_id": 99, "reason": "I like 99"}`},
		Store:     store,
		Settings:  &mockSettings{},
		Instances: &mockInstances{ids: []uint{1, 2}, names: map[uint]string{1: "a", 2: "b"}},
	})

	if err := svc.Dispatch(context.Background(), 1); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	task := store.tasks[1]
	if task.AssignedInstanceID == nil || *task.AssignedInstanceID != 1 {
		t.Errorf("should fall back to first eligible, got %v", task.AssignedInstanceID)
	}
}

// ---- JSON extraction from wrapped response --------------------------------

func TestParseRankReply_WrappedInMarkdown(t *testing.T) {
	t.Parallel()
	input := "```json\n{\"instance_id\": 2, \"reason\": \"Go expert\"}\n```"
	id, reason, ok := parseRankReply(input)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if id != 2 {
		t.Errorf("id = %d, want 2", id)
	}
	if reason != "Go expert" {
		t.Errorf("reason = %q, want 'Go expert'", reason)
	}
}

// ---- Verify cleanup of running map ----------------------------------------

func TestEnqueueTask_CleansUpRunningMap(t *testing.T) {
	store := newMockStore()
	store.tasks[1] = Task{ID: 1, BoardID: 99, Status: "todo"} // will fail dispatch
	svc := newTestService(store)

	svc.EnqueueTask(1)
	time.Sleep(100 * time.Millisecond)

	svc.mu.Lock()
	_, stillRunning := svc.running[1]
	svc.mu.Unlock()

	if stillRunning {
		t.Error("task should be removed from running map after completion")
	}
}

// ---- Verify rank builds prompt with candidate info -------------------------

func TestRank_BuildsPromptWithSouls(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	captureLLM := &promptCapturingLLM{}

	svc := New(Options{
		Dialer:    &mockDialer{},
		Workspace: &mockWorkspaceFS{},
		LLM:       captureLLM,
		Store:     store,
		Settings:  &mockSettings{},
		Instances: &mockInstances{ids: []uint{1, 2}, names: map[uint]string{1: "a", 2: "b"}},
	})

	task := Task{ID: 1, Title: "Build API", Description: "REST API in Go"}
	souls := []Soul{
		{InstanceID: 1, Summary: "Python ML specialist"},
		{InstanceID: 2, Summary: "Go backend developer"},
	}

	chosen, _, err := svc.rank(context.Background(), task, []uint{1, 2}, souls)
	if err != nil {
		t.Fatal(err)
	}
	_ = chosen // valid either way since mock returns first

	prompt := captureLLM.lastPrompt
	if prompt == "" {
		t.Fatal("no prompt captured")
	}
	// Verify prompt contains task info and candidate souls.
	for _, substr := range []string{"Build API", "REST API in Go", "instance_id=1", "instance_id=2", "Python ML specialist", "Go backend developer"} {
		if !contains(prompt, substr) {
			t.Errorf("prompt missing %q", substr)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

type promptCapturingLLM struct {
	lastPrompt string
}

func (p *promptCapturingLLM) Complete(_ context.Context, _, _, prompt string) (string, error) {
	p.lastPrompt = prompt
	return `{"instance_id": 1, "reason": "test"}`, nil
}

// Helpers to verify comment JSON is valid where needed.
func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
