package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/go-chi/chi/v5"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupKanbanDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(
		&database.KanbanBoard{},
		&database.KanbanTask{},
		&database.KanbanComment{},
		&database.KanbanArtifact{},
		&database.Setting{},
	); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	database.DB = db
	t.Cleanup(func() { database.DB = nil })
}

// chiCtx injects chi URL params into the request context.
func chiCtx(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	return r.WithContext(ctx)
}

// ---- Board CRUD -----------------------------------------------------------

func TestListKanbanBoards_Empty(t *testing.T) {
	setupKanbanDB(t)
	req := httptest.NewRequest("GET", "/api/v1/kanban/boards", nil)
	w := httptest.NewRecorder()
	ListKanbanBoards(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body []map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 0 {
		t.Errorf("expected empty list, got %d items", len(body))
	}
}

func TestCreateKanbanBoard(t *testing.T) {
	setupKanbanDB(t)
	payload := `{"name":"My Board","description":"A test board","eligible_instances":[1,2]}`
	req := httptest.NewRequest("POST", "/api/v1/kanban/boards", bytes.NewBufferString(payload))
	w := httptest.NewRecorder()
	CreateKanbanBoard(w, req)

	if w.Code != 201 {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	var board database.KanbanBoard
	json.Unmarshal(w.Body.Bytes(), &board)
	if board.Name != "My Board" {
		t.Errorf("name = %q, want 'My Board'", board.Name)
	}
	if board.ID == 0 {
		t.Error("expected non-zero board ID")
	}
}

func TestCreateKanbanBoard_InvalidPayload(t *testing.T) {
	setupKanbanDB(t)
	req := httptest.NewRequest("POST", "/api/v1/kanban/boards", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	CreateKanbanBoard(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestGetKanbanBoard(t *testing.T) {
	setupKanbanDB(t)
	database.DB.Create(&database.KanbanBoard{Name: "Board1", EligibleInstances: "[1]"})

	req := chiCtx(httptest.NewRequest("GET", "/", nil), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	GetKanbanBoard(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["name"] != "Board1" {
		t.Errorf("name = %v, want Board1", body["name"])
	}
	if body["tasks"] == nil {
		t.Error("expected tasks array in response")
	}
}

func TestGetKanbanBoard_NotFound(t *testing.T) {
	setupKanbanDB(t)
	req := chiCtx(httptest.NewRequest("GET", "/", nil), map[string]string{"id": "999"})
	w := httptest.NewRecorder()
	GetKanbanBoard(w, req)

	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestUpdateKanbanBoard(t *testing.T) {
	setupKanbanDB(t)
	database.DB.Create(&database.KanbanBoard{Name: "Old", EligibleInstances: "[]"})

	payload := `{"name":"New","description":"updated","eligible_instances":[3]}`
	req := chiCtx(httptest.NewRequest("PUT", "/", bytes.NewBufferString(payload)), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	UpdateKanbanBoard(w, req)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	var board database.KanbanBoard
	database.DB.First(&board, 1)
	if board.Name != "New" {
		t.Errorf("name = %q, want 'New'", board.Name)
	}
}

func TestDeleteKanbanBoard(t *testing.T) {
	setupKanbanDB(t)
	database.DB.Create(&database.KanbanBoard{Name: "ToDelete", EligibleInstances: "[]"})
	database.DB.Create(&database.KanbanTask{BoardID: 1, Title: "Task1", Status: "todo"})

	req := chiCtx(httptest.NewRequest("DELETE", "/", nil), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	DeleteKanbanBoard(w, req)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	var count int64
	database.DB.Model(&database.KanbanBoard{}).Count(&count)
	if count != 0 {
		t.Errorf("board should be deleted, count = %d", count)
	}
	database.DB.Model(&database.KanbanTask{}).Count(&count)
	if count != 0 {
		t.Errorf("associated tasks should be deleted, count = %d", count)
	}
}

func TestListKanbanBoards_ReturnsEligibleInstances(t *testing.T) {
	setupKanbanDB(t)
	database.DB.Create(&database.KanbanBoard{Name: "B1", EligibleInstances: "[1,2,3]"})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	ListKanbanBoards(w, req)

	var body []map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 1 {
		t.Fatalf("expected 1 board, got %d", len(body))
	}
	ids, ok := body[0]["eligible_instances"].([]any)
	if !ok || len(ids) != 3 {
		t.Errorf("eligible_instances = %v, want [1,2,3]", body[0]["eligible_instances"])
	}
}

// ---- Task CRUD ------------------------------------------------------------

func TestCreateKanbanTask(t *testing.T) {
	setupKanbanDB(t)
	database.DB.Create(&database.KanbanBoard{Name: "B1", EligibleInstances: "[]"})

	payload := `{"title":"My Task","description":"Do something"}`
	req := chiCtx(httptest.NewRequest("POST", "/", bytes.NewBufferString(payload)), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	CreateKanbanTask(w, req)

	if w.Code != 201 {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	var task database.KanbanTask
	json.Unmarshal(w.Body.Bytes(), &task)
	if task.Title != "My Task" {
		t.Errorf("title = %q, want 'My Task'", task.Title)
	}
	if task.Status != "todo" {
		t.Errorf("default status = %q, want 'todo'", task.Status)
	}
}

func TestCreateKanbanTask_DraftStatus(t *testing.T) {
	setupKanbanDB(t)
	database.DB.Create(&database.KanbanBoard{Name: "B1", EligibleInstances: "[]"})

	payload := `{"description":"Draft task","status":"draft"}`
	req := chiCtx(httptest.NewRequest("POST", "/", bytes.NewBufferString(payload)), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	CreateKanbanTask(w, req)

	if w.Code != 201 {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	var task database.KanbanTask
	json.Unmarshal(w.Body.Bytes(), &task)
	if task.Status != "draft" {
		t.Errorf("status = %q, want 'draft'", task.Status)
	}
}

func TestCreateKanbanTask_AutoTitle(t *testing.T) {
	setupKanbanDB(t)
	database.DB.Create(&database.KanbanBoard{Name: "B1", EligibleInstances: "[]"})

	payload := `{"description":"Build a REST API\nwith auth"}`
	req := chiCtx(httptest.NewRequest("POST", "/", bytes.NewBufferString(payload)), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	CreateKanbanTask(w, req)

	var task database.KanbanTask
	json.Unmarshal(w.Body.Bytes(), &task)
	if task.Title != "Build a REST API" {
		t.Errorf("auto-title = %q, want 'Build a REST API'", task.Title)
	}
}

func TestCreateKanbanTask_InvalidPayload(t *testing.T) {
	setupKanbanDB(t)
	req := chiCtx(httptest.NewRequest("POST", "/", bytes.NewBufferString(`{}`)), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	CreateKanbanTask(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestGetKanbanTask(t *testing.T) {
	setupKanbanDB(t)
	database.DB.Create(&database.KanbanTask{BoardID: 1, Title: "T1", Description: "desc", Status: "todo"})
	database.DB.Create(&database.KanbanComment{TaskID: 1, Kind: "user", Author: "alice", Body: "hello"})

	req := chiCtx(httptest.NewRequest("GET", "/", nil), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	GetKanbanTask(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	task, _ := body["task"].(map[string]any)
	if task["title"] != "T1" {
		t.Errorf("task title = %v, want T1", task["title"])
	}
	comments, _ := body["comments"].([]any)
	if len(comments) != 1 {
		t.Errorf("expected 1 comment, got %d", len(comments))
	}
}

func TestGetKanbanTask_NotFound(t *testing.T) {
	setupKanbanDB(t)
	req := chiCtx(httptest.NewRequest("GET", "/", nil), map[string]string{"id": "999"})
	w := httptest.NewRecorder()
	GetKanbanTask(w, req)

	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestPatchKanbanTask(t *testing.T) {
	setupKanbanDB(t)
	database.DB.Create(&database.KanbanTask{BoardID: 1, Title: "Old", Description: "old", Status: "todo"})

	payload := `{"title":"New Title","status":"done"}`
	req := chiCtx(httptest.NewRequest("PATCH", "/", bytes.NewBufferString(payload)), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	PatchKanbanTask(w, req)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	var task database.KanbanTask
	database.DB.First(&task, 1)
	if task.Title != "New Title" {
		t.Errorf("title = %q, want 'New Title'", task.Title)
	}
	if task.Status != "done" {
		t.Errorf("status = %q, want 'done'", task.Status)
	}
}

func TestPatchKanbanTask_IgnoresDisallowedFields(t *testing.T) {
	setupKanbanDB(t)
	database.DB.Create(&database.KanbanTask{BoardID: 1, Title: "T", Description: "d", Status: "todo"})

	// Try to change board_id which is not in the allowed list.
	payload := `{"board_id":99,"title":"Updated"}`
	req := chiCtx(httptest.NewRequest("PATCH", "/", bytes.NewBufferString(payload)), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	PatchKanbanTask(w, req)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	var task database.KanbanTask
	database.DB.First(&task, 1)
	if task.BoardID != 1 {
		t.Errorf("board_id should not change, got %d", task.BoardID)
	}
	if task.Title != "Updated" {
		t.Errorf("title should change, got %q", task.Title)
	}
}

func TestDeleteKanbanTask(t *testing.T) {
	setupKanbanDB(t)

	tmpDir := t.TempDir()
	config.Cfg.DataPath = tmpDir

	// Create artifact directory and file.
	artifactDir := filepath.Join(tmpDir, "kanban", "artifacts", "1")
	os.MkdirAll(artifactDir, 0o755)
	os.WriteFile(filepath.Join(artifactDir, "test.txt"), []byte("data"), 0o644)

	database.DB.Create(&database.KanbanTask{BoardID: 1, Title: "T1", Status: "todo"})
	database.DB.Create(&database.KanbanComment{TaskID: 1, Kind: "user", Body: "hi"})
	database.DB.Create(&database.KanbanArtifact{TaskID: 1, Path: "test.txt", StoragePath: filepath.Join(artifactDir, "test.txt")})

	req := chiCtx(httptest.NewRequest("DELETE", "/", nil), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	DeleteKanbanTask(w, req)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	var taskCount, commentCount, artifactCount int64
	database.DB.Model(&database.KanbanTask{}).Count(&taskCount)
	database.DB.Model(&database.KanbanComment{}).Count(&commentCount)
	database.DB.Model(&database.KanbanArtifact{}).Count(&artifactCount)

	if taskCount != 0 {
		t.Errorf("task should be deleted, count = %d", taskCount)
	}
	if commentCount != 0 {
		t.Errorf("comments should be deleted, count = %d", commentCount)
	}
	if artifactCount != 0 {
		t.Errorf("artifacts should be deleted, count = %d", artifactCount)
	}

	// Verify artifact directory was removed from disk.
	if _, err := os.Stat(artifactDir); !os.IsNotExist(err) {
		t.Error("artifact directory should be removed from disk")
	}
}

func TestStartKanbanTask(t *testing.T) {
	setupKanbanDB(t)
	database.DB.Create(&database.KanbanTask{BoardID: 1, Title: "Draft", Status: "draft"})

	req := chiCtx(httptest.NewRequest("POST", "/", nil), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	StartKanbanTask(w, req)

	if w.Code != 202 {
		t.Fatalf("status = %d, want 202", w.Code)
	}

	var task database.KanbanTask
	database.DB.First(&task, 1)
	if task.Status != "todo" {
		t.Errorf("status = %q, want 'todo'", task.Status)
	}
}

func TestStartKanbanTask_NotDraft(t *testing.T) {
	setupKanbanDB(t)
	database.DB.Create(&database.KanbanTask{BoardID: 1, Title: "Running", Status: "in_progress"})

	req := chiCtx(httptest.NewRequest("POST", "/", nil), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	StartKanbanTask(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400 for non-draft task", w.Code)
	}
}

func TestStartKanbanTask_NotFound(t *testing.T) {
	setupKanbanDB(t)
	req := chiCtx(httptest.NewRequest("POST", "/", nil), map[string]string{"id": "999"})
	w := httptest.NewRecorder()
	StartKanbanTask(w, req)

	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// ---- Comments -------------------------------------------------------------

func TestCreateKanbanUserComment(t *testing.T) {
	setupKanbanDB(t)
	database.DB.Create(&database.KanbanTask{BoardID: 1, Title: "T1", Status: "todo"})

	payload := `{"body":"This is a comment"}`
	req := chiCtx(httptest.NewRequest("POST", "/", bytes.NewBufferString(payload)), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	CreateKanbanUserComment(w, req)

	if w.Code != 201 {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	var comment database.KanbanComment
	json.Unmarshal(w.Body.Bytes(), &comment)
	if comment.Kind != "user" {
		t.Errorf("kind = %q, want 'user'", comment.Kind)
	}
	if comment.Body != "This is a comment" {
		t.Errorf("body = %q, want 'This is a comment'", comment.Body)
	}
	if comment.Author != "user" {
		t.Errorf("author = %q, want 'user' (default)", comment.Author)
	}
}

func TestCreateKanbanUserComment_EmptyBody(t *testing.T) {
	setupKanbanDB(t)
	req := chiCtx(httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"body":""}`)), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	CreateKanbanUserComment(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400 for empty body", w.Code)
	}
}

// ---- autoTitle (handler-level) -------------------------------------------

func TestAutoTitle_HandlerLevel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		desc string
		want string
	}{
		{"First line\nMore text", "First line"},
		{"Short", "Short"},
		{"", "Untitled task"},
	}
	for _, tt := range tests {
		got := autoTitle(tt.desc)
		if got != tt.want {
			t.Errorf("autoTitle(%q) = %q, want %q", tt.desc, got, tt.want)
		}
	}
}

// ---- Artifact download ----------------------------------------------------

func TestDownloadKanbanArtifact(t *testing.T) {
	setupKanbanDB(t)

	tmpDir := t.TempDir()
	artifactPath := filepath.Join(tmpDir, "result.txt")
	os.WriteFile(artifactPath, []byte("artifact content"), 0o644)

	database.DB.Create(&database.KanbanArtifact{
		TaskID: 1, Path: "result.txt", SizeBytes: 16,
		SHA256: "abc", StoragePath: artifactPath,
	})

	req := chiCtx(httptest.NewRequest("GET", "/", nil), map[string]string{"id": "1", "artifact_id": "1"})
	w := httptest.NewRecorder()
	DownloadKanbanArtifact(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "artifact content" {
		t.Errorf("body = %q, want 'artifact content'", w.Body.String())
	}
	cd := w.Header().Get("Content-Disposition")
	if cd == "" {
		t.Error("expected Content-Disposition header")
	}
}

func TestDownloadKanbanArtifact_NotFound(t *testing.T) {
	setupKanbanDB(t)
	req := chiCtx(httptest.NewRequest("GET", "/", nil), map[string]string{"id": "1", "artifact_id": "999"})
	w := httptest.NewRecorder()
	DownloadKanbanArtifact(w, req)

	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// ---- Stop / Reopen (handler-level, ModeratorSvc nil) ----------------------

func TestStopKanbanTask_NoModerator(t *testing.T) {
	setupKanbanDB(t)
	ModeratorSvc = nil
	req := chiCtx(httptest.NewRequest("POST", "/", nil), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	StopKanbanTask(w, req)

	if w.Code != 204 {
		t.Errorf("status = %d, want 204 even without moderator", w.Code)
	}
}

func TestReopenKanbanTask_NoModerator(t *testing.T) {
	setupKanbanDB(t)
	ModeratorSvc = nil
	req := chiCtx(httptest.NewRequest("POST", "/", nil), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	ReopenKanbanTask(w, req)

	if w.Code != 202 {
		t.Errorf("status = %d, want 202 even without moderator", w.Code)
	}
}

// ---- GetKanbanBoard includes tasks ----------------------------------------

func TestGetKanbanBoard_IncludesTasks(t *testing.T) {
	setupKanbanDB(t)
	database.DB.Create(&database.KanbanBoard{Name: "B", EligibleInstances: "[]"})
	database.DB.Create(&database.KanbanTask{BoardID: 1, Title: "T1", Description: "d1", Status: "todo"})
	database.DB.Create(&database.KanbanTask{BoardID: 1, Title: "T2", Description: "d2", Status: "done"})

	req := chiCtx(httptest.NewRequest("GET", "/", nil), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	GetKanbanBoard(w, req)

	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	tasks, _ := body["tasks"].([]any)
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

// ---- GetKanbanTask includes artifacts -------------------------------------

func TestGetKanbanTask_IncludesArtifacts(t *testing.T) {
	setupKanbanDB(t)
	database.DB.Create(&database.KanbanTask{BoardID: 1, Title: "T1", Status: "done"})
	database.DB.Create(&database.KanbanArtifact{TaskID: 1, Path: "out.txt", SizeBytes: 100})

	req := chiCtx(httptest.NewRequest("GET", "/", nil), map[string]string{"id": "1"})
	w := httptest.NewRecorder()
	GetKanbanTask(w, req)

	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	artifacts, _ := body["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Errorf("expected 1 artifact, got %d", len(artifacts))
	}
}
