package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/moderator"
	"github.com/go-chi/chi/v5"
)

// ModeratorSvc is the singleton moderator service. Wired in main.go.
var ModeratorSvc *moderator.Service

// ---- Boards ------------------------------------------------------------

type boardPayload struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	EligibleInstances []uint `json:"eligible_instances"`
}

func ListKanbanBoards(w http.ResponseWriter, r *http.Request) {
	var rows []database.KanbanBoard
	if err := database.DB.Order("created_at DESC").Find(&rows).Error; err != nil {
		writeError(w, 500, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, b := range rows {
		var ids []uint
		_ = json.Unmarshal([]byte(b.EligibleInstances), &ids)
		out = append(out, map[string]any{
			"id":                 b.ID,
			"name":               b.Name,
			"description":        b.Description,
			"eligible_instances": ids,
			"created_at":         b.CreatedAt,
			"updated_at":         b.UpdatedAt,
		})
	}
	writeJSON(w, 200, out)
}

func CreateKanbanBoard(w http.ResponseWriter, r *http.Request) {
	var p boardPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil || p.Name == "" {
		writeError(w, 400, "invalid payload")
		return
	}
	idsJSON, _ := json.Marshal(p.EligibleInstances)
	row := database.KanbanBoard{
		Name: p.Name, Description: p.Description, EligibleInstances: string(idsJSON),
	}
	if err := database.DB.Create(&row).Error; err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, row)
}

func GetKanbanBoard(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var b database.KanbanBoard
	if err := database.DB.First(&b, id).Error; err != nil {
		writeError(w, 404, "not found")
		return
	}
	var tasks []database.KanbanTask
	database.DB.Where("board_id = ?", id).Order("created_at DESC").Find(&tasks)
	var ids []uint
	_ = json.Unmarshal([]byte(b.EligibleInstances), &ids)
	writeJSON(w, 200, map[string]any{
		"id":                 b.ID,
		"name":               b.Name,
		"description":        b.Description,
		"eligible_instances": ids,
		"created_at":         b.CreatedAt,
		"updated_at":         b.UpdatedAt,
		"tasks":              tasks,
	})
}

func UpdateKanbanBoard(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var p boardPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, 400, "invalid payload")
		return
	}
	idsJSON, _ := json.Marshal(p.EligibleInstances)
	if err := database.DB.Model(&database.KanbanBoard{}).Where("id = ?", id).Updates(map[string]any{
		"name":               p.Name,
		"description":        p.Description,
		"eligible_instances": string(idsJSON),
	}).Error; err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func DeleteKanbanBoard(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	database.DB.Where("board_id = ?", id).Delete(&database.KanbanTask{})
	database.DB.Delete(&database.KanbanBoard{}, id)
	w.WriteHeader(204)
}

// ---- Tasks -------------------------------------------------------------

type taskPayload struct {
	Title                string `json:"title"`
	Description          string `json:"description"`
	EvaluatorProviderKey string `json:"evaluator_provider_key"`
	EvaluatorModel       string `json:"evaluator_model"`
	Status               string `json:"status"` // "draft" or "todo"; default "todo"
}

func CreateKanbanTask(w http.ResponseWriter, r *http.Request) {
	boardID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var p taskPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil || p.Description == "" {
		writeError(w, 400, "invalid payload")
		return
	}
	title := p.Title
	if title == "" {
		title = autoTitle(p.Description)
	}
	status := "todo"
	if p.Status == "draft" {
		status = "draft"
	}
	row := database.KanbanTask{
		BoardID: uint(boardID), Title: title, Description: p.Description,
		Status:               status,
		EvaluatorProviderKey: p.EvaluatorProviderKey,
		EvaluatorModel:       p.EvaluatorModel,
	}
	if err := database.DB.Create(&row).Error; err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if status == "todo" && ModeratorSvc != nil {
		ModeratorSvc.EnqueueTask(row.ID)
	}
	writeJSON(w, 201, row)
}

func StartKanbanTask(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var t database.KanbanTask
	if err := database.DB.First(&t, id).Error; err != nil {
		writeError(w, 404, "not found")
		return
	}
	if t.Status != "draft" {
		writeError(w, 400, "task is not in draft status")
		return
	}
	if err := database.DB.Model(&t).Update("status", "todo").Error; err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if ModeratorSvc != nil {
		ModeratorSvc.EnqueueTask(t.ID)
	}
	w.WriteHeader(202)
}

func GetKanbanTask(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var t database.KanbanTask
	if err := database.DB.First(&t, id).Error; err != nil {
		writeError(w, 404, "not found")
		return
	}
	var comments []database.KanbanComment
	database.DB.Where("task_id = ?", id).Order("created_at ASC").Find(&comments)
	var artifacts []database.KanbanArtifact
	database.DB.Where("task_id = ?", id).Order("created_at ASC").Find(&artifacts)
	writeJSON(w, 200, map[string]any{
		"task":      t,
		"comments":  comments,
		"artifacts": artifacts,
	})
}

func PatchKanbanTask(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var p map[string]any
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, 400, "invalid payload")
		return
	}
	allowed := map[string]any{}
	for _, k := range []string{"status", "title", "description", "evaluator_provider_key", "evaluator_model"} {
		if v, ok := p[k]; ok {
			allowed[k] = v
		}
	}
	if err := database.DB.Model(&database.KanbanTask{}).Where("id = ?", id).Updates(allowed).Error; err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func DeleteKanbanTask(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	// Delete artifact files from the control-plane filesystem.
	baseDir := config.Cfg.DataPath + "/kanban/artifacts"
	if v, err := database.GetSetting("kanban_artifacts_dir"); err == nil && v != "" {
		baseDir = v
	}
	artifactDir := filepath.Join(baseDir, fmt.Sprintf("%d", id))
	_ = os.RemoveAll(artifactDir)
	// Delete DB records.
	database.DB.Where("task_id = ?", id).Delete(&database.KanbanComment{})
	database.DB.Where("task_id = ?", id).Delete(&database.KanbanArtifact{})
	database.DB.Delete(&database.KanbanTask{}, id)
	w.WriteHeader(204)
}

func StopKanbanTask(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if ModeratorSvc != nil {
		ModeratorSvc.Stop(uint(id))
	}
	w.WriteHeader(204)
}

type kanbanCommentPayload struct {
	Body string `json:"body"`
}

func CreateKanbanUserComment(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var p kanbanCommentPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil || p.Body == "" {
		writeError(w, 400, "invalid payload")
		return
	}
	username := "user"
	if u := middleware.GetUser(r); u != nil {
		username = u.Username
	}
	row := database.KanbanComment{
		TaskID: uint(id), Kind: "user", Author: username, Body: p.Body,
	}
	if err := database.DB.Create(&row).Error; err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, row)
}

func ReopenKanbanTask(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if ModeratorSvc != nil {
		ModeratorSvc.Reopen(uint(id))
	}
	w.WriteHeader(202)
}

func DownloadKanbanArtifact(w http.ResponseWriter, r *http.Request) {
	aid, _ := strconv.Atoi(chi.URLParam(r, "artifact_id"))
	var a database.KanbanArtifact
	if err := database.DB.First(&a, aid).Error; err != nil {
		writeError(w, 404, "not found")
		return
	}
	f, err := os.Open(a.StoragePath)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	defer f.Close()
	w.Header().Set("Content-Disposition", "attachment; filename=\""+a.Path+"\"")
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = io.Copy(w, f)
}

// autoTitle generates a short task title from the first line of the description.
func autoTitle(desc string) string {
	line := strings.SplitN(strings.TrimSpace(desc), "\n", 2)[0]
	line = strings.TrimSpace(line)
	if len(line) > 60 {
		line = line[:57] + "..."
	}
	if line == "" {
		return "Untitled task"
	}
	return line
}
