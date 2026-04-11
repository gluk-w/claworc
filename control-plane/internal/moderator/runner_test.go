package moderator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- truncateForHistory ---------------------------------------------------

func TestTruncateForHistory(t *testing.T) {
	t.Parallel()
	short := "hello"
	if got := truncateForHistory(short, 10); got != short {
		t.Errorf("got %q, want %q", got, short)
	}
	long := strings.Repeat("x", 100)
	got := truncateForHistory(long, 10)
	if !strings.HasPrefix(got, "xxxxxxxxxx") {
		t.Errorf("should start with 10 x's, got %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("should end with ellipsis, got %q", got)
	}
}

// ---- buildCommentHistory --------------------------------------------------

func TestBuildCommentHistory_Empty(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	svc := newTestService(store)

	result := svc.buildCommentHistory(context.Background(), 999)
	if result != "" {
		t.Errorf("expected empty for task with no comments, got %q", result)
	}
}

func TestBuildCommentHistory_SkipsToolComments(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	store.comments = []Comment{
		{ID: 1, TaskID: 1, Kind: "user", Author: "alice", Body: "Please fix the bug"},
		{ID: 2, TaskID: 1, Kind: "tool", Author: "agent:bot", Body: `{"tool":"read_file"}`},
		{ID: 3, TaskID: 1, Kind: "assistant", Author: "agent:bot", Body: "I fixed the bug"},
	}
	svc := newTestService(store)

	result := svc.buildCommentHistory(context.Background(), 1)

	if strings.Contains(result, "read_file") {
		t.Error("tool comments should be excluded from history")
	}
	if !strings.Contains(result, "Please fix the bug") {
		t.Error("user comment should be included")
	}
	if !strings.Contains(result, "I fixed the bug") {
		t.Error("assistant comment should be included")
	}
}

func TestBuildCommentHistory_SkipsEmptyBodies(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	store.comments = []Comment{
		{ID: 1, TaskID: 1, Kind: "assistant", Author: "bot", Body: ""},
		{ID: 2, TaskID: 1, Kind: "user", Author: "alice", Body: "hello"},
	}
	svc := newTestService(store)

	result := svc.buildCommentHistory(context.Background(), 1)

	lines := strings.Split(strings.TrimSpace(result), "\n")
	nonEmpty := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty++
		}
	}
	if nonEmpty != 1 {
		t.Errorf("expected 1 non-empty line, got %d in:\n%s", nonEmpty, result)
	}
}

func TestBuildCommentHistory_Truncates(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	// Create many long comments to exceed the 8000 char cap.
	for i := 0; i < 50; i++ {
		store.comments = append(store.comments, Comment{
			ID: uint(i + 1), TaskID: 1, Kind: "user", Author: "alice",
			Body: strings.Repeat("A", 500),
		})
	}
	svc := newTestService(store)

	result := svc.buildCommentHistory(context.Background(), 1)

	if len(result) > 8100 { // some slack for the truncation message
		t.Errorf("history should be capped around 8000 chars, got %d", len(result))
	}
	if !strings.Contains(result, "truncated") {
		t.Error("truncated history should contain truncation marker")
	}
}

func TestBuildCommentHistory_FormatsCorrectly(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	store.comments = []Comment{
		{ID: 1, TaskID: 1, Kind: "routing", Author: "moderator", Body: "Routed to agent-alpha."},
		{ID: 2, TaskID: 1, Kind: "user", Author: "alice", Body: "Try a different approach"},
	}
	svc := newTestService(store)

	result := svc.buildCommentHistory(context.Background(), 1)

	if !strings.Contains(result, "[routing] moderator:") {
		t.Errorf("expected formatted routing comment in history, got:\n%s", result)
	}
	if !strings.Contains(result, "[user] alice:") {
		t.Errorf("expected formatted user comment in history, got:\n%s", result)
	}
}

// ---- injectPriorArtifacts -------------------------------------------------

func TestInjectPriorArtifacts_NoArtifacts(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	svc := newTestService(store)

	desc := svc.injectPriorArtifacts(context.Background(), 1, 1)
	if desc != "" {
		t.Errorf("expected empty for no prior artifacts, got %q", desc)
	}
}

func TestInjectPriorArtifacts_WithArtifacts(t *testing.T) {
	t.Parallel()

	// Create a temp file to serve as the stored artifact.
	tmpDir := t.TempDir()
	artifactPath := filepath.Join(tmpDir, "report.py")
	if err := os.WriteFile(artifactPath, []byte("print('hello')"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := &artifactMockStore{
		mockStore: newMockStore(),
		artifacts: map[uint][]Artifact{
			1: {{ID: 1, TaskID: 1, Path: "report.py", SizeBytes: 14, StoragePath: artifactPath}},
		},
	}

	writeFS := &trackingWorkspaceFS{}
	svc := New(Options{
		Dialer:    &mockDialer{},
		Workspace: writeFS,
		LLM:       &mockLLM{},
		Store:     store,
		Settings:  &mockSettings{},
		Instances: &mockInstances{ids: []uint{1}, names: map[uint]string{1: "bot"}},
	})

	desc := svc.injectPriorArtifacts(context.Background(), 1, 1)

	if !strings.Contains(desc, "report.py") {
		t.Errorf("description should mention artifact file, got %q", desc)
	}
	if len(writeFS.written) == 0 {
		t.Error("expected artifact to be written to instance")
	}
}

func TestInjectPriorArtifacts_SkipsOversized(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	artifactPath := filepath.Join(tmpDir, "big.bin")
	if err := os.WriteFile(artifactPath, make([]byte, 6*1024*1024), 0o644); err != nil { // 6MB > 5MB max
		t.Fatal(err)
	}

	store := &artifactMockStore{
		mockStore: newMockStore(),
		artifacts: map[uint][]Artifact{
			1: {{ID: 1, TaskID: 1, Path: "big.bin", SizeBytes: 6 * 1024 * 1024, StoragePath: artifactPath}},
		},
	}

	writeFS := &trackingWorkspaceFS{}
	svc := New(Options{
		Dialer:    &mockDialer{},
		Workspace: writeFS,
		LLM:       &mockLLM{},
		Store:     store,
		Settings:  &mockSettings{},
		Instances: &mockInstances{ids: []uint{1}, names: map[uint]string{1: "bot"}},
	})

	desc := svc.injectPriorArtifacts(context.Background(), 1, 1)

	if desc != "" {
		t.Errorf("oversized artifacts should be skipped, got desc %q", desc)
	}
	if len(writeFS.written) != 0 {
		t.Error("oversized artifact should not be uploaded")
	}
}

// ---- collectOutcomes (directory vs. mention fallback) ----------------------

func TestCollectOutcomes_FallsBackToMentionBased(t *testing.T) {
	t.Parallel()

	// WorkspaceFS.List returns empty → triggers mention-based fallback.
	store := newMockStore()
	svc := New(Options{
		Dialer:    &mockDialer{},
		Workspace: &mockWorkspaceFS{}, // List returns nil
		LLM:       &mockLLM{},
		Store:     store,
		Settings:  &mockSettings{},
		Instances: &mockInstances{ids: []uint{1}, names: map[uint]string{1: "bot"}},
	})

	// No files on instance and no mentions → empty results.
	pulled, skipped := svc.collectOutcomes(context.Background(), 1, 1, "No files mentioned")
	if len(pulled) != 0 || len(skipped) != 0 {
		t.Errorf("expected empty results, got pulled=%v skipped=%v", pulled, skipped)
	}
}

func TestCollectOutcomes_DirectoryBased(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := newMockStore()

	dirFS := &dirBasedWorkspaceFS{
		files: map[string][]byte{
			"/home/claworc/tasks/1/output.txt": []byte("result"),
		},
		listing: map[string][]FileEntry{
			"/home/claworc/tasks/1": {
				{Path: "/home/claworc/tasks/1/output.txt", Size: 6, IsDir: false},
			},
		},
	}

	svc := New(Options{
		Dialer:    &mockDialer{},
		Workspace: dirFS,
		LLM:       &mockLLM{},
		Store:     store,
		Settings:  &mockSettingsWithDir{artifactDir: tmpDir},
		Instances: &mockInstances{ids: []uint{1}, names: map[uint]string{1: "bot"}},
	})

	pulled, _ := svc.collectOutcomes(context.Background(), 1, 1, "")

	if len(pulled) != 1 || pulled[0] != "output.txt" {
		t.Errorf("expected [output.txt], got %v", pulled)
	}

	// Verify file was saved to disk.
	savedPath := filepath.Join(tmpDir, "1", "output.txt")
	data, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("artifact not saved to disk: %v", err)
	}
	if string(data) != "result" {
		t.Errorf("artifact content = %q, want 'result'", string(data))
	}
}

// ---- randomSuffix ---------------------------------------------------------

func TestRandomSuffix_Length(t *testing.T) {
	t.Parallel()
	s := randomSuffix(6)
	if len(s) != 6 {
		t.Errorf("len = %d, want 6", len(s))
	}
}

func TestRandomSuffix_Unique(t *testing.T) {
	t.Parallel()
	a := randomSuffix(12)
	b := randomSuffix(12)
	if a == b {
		t.Errorf("two random suffixes should differ: %q == %q", a, b)
	}
}

// ---- Test helpers / mocks for runner tests --------------------------------

type artifactMockStore struct {
	*mockStore
	artifacts map[uint][]Artifact
}

func (s *artifactMockStore) ListTaskArtifacts(_ context.Context, taskID uint) ([]Artifact, error) {
	return s.artifacts[taskID], nil
}

type trackingWorkspaceFS struct {
	written []struct {
		path string
		data []byte
	}
}

func (f *trackingWorkspaceFS) List(_ context.Context, _ uint, _ string) ([]FileEntry, error) {
	return nil, nil
}
func (f *trackingWorkspaceFS) Read(_ context.Context, _ uint, _ string) ([]byte, error) {
	return nil, fmt.Errorf("not found")
}
func (f *trackingWorkspaceFS) Write(_ context.Context, _ uint, path string, data []byte) error {
	f.written = append(f.written, struct {
		path string
		data []byte
	}{path, data})
	return nil
}
func (f *trackingWorkspaceFS) MkdirAll(_ context.Context, _ uint, _ string) error { return nil }
func (f *trackingWorkspaceFS) RemoveAll(_ context.Context, _ uint, _ string) error { return nil }

type dirBasedWorkspaceFS struct {
	files   map[string][]byte
	listing map[string][]FileEntry
}

func (f *dirBasedWorkspaceFS) List(_ context.Context, _ uint, dir string) ([]FileEntry, error) {
	if entries, ok := f.listing[dir]; ok {
		return entries, nil
	}
	return nil, fmt.Errorf("dir not found: %s", dir)
}

func (f *dirBasedWorkspaceFS) Read(_ context.Context, _ uint, path string) ([]byte, error) {
	if data, ok := f.files[path]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("file not found: %s", path)
}

func (f *dirBasedWorkspaceFS) Write(_ context.Context, _ uint, _ string, _ []byte) error {
	return nil
}
func (f *dirBasedWorkspaceFS) MkdirAll(_ context.Context, _ uint, _ string) error { return nil }
func (f *dirBasedWorkspaceFS) RemoveAll(_ context.Context, _ uint, _ string) error { return nil }

type mockSettingsWithDir struct {
	mockSettings
	artifactDir string
}

func (s *mockSettingsWithDir) ArtifactStorageDir() string { return s.artifactDir }
