package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gluk-w/claworc/control-plane/internal/analytics"
	"github.com/gluk-w/claworc/control-plane/internal/backup"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/taskmanager"
	"github.com/go-chi/chi/v5"
)

type backupCreateRequest struct {
	Paths []string `json:"paths"`
	Note  string   `json:"note"`
}

// CreateBackup starts a new backup for an instance.
func CreateBackup(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "You do not have access to this instance")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	var req backupCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	orch := orchestrator.Get()
	if orch == nil {
		writeError(w, http.StatusServiceUnavailable, "Orchestrator not available")
		return
	}

	backupID, err := backup.CreateFullBackup(r.Context(), orch, inst.Name, inst.ID, callerID(r), req.Note, req.Paths)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to start backup: %v", err))
		return
	}

	analytics.Track(r.Context(), analytics.EventBackupCreatedManual, map[string]any{
		"paths_count": len(req.Paths),
	})

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"id":      backupID,
		"message": "Backup started",
	})
}

// ListInstanceBackups returns all backups for a specific instance.
func ListInstanceBackups(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "You do not have access to this instance")
		return
	}

	backups, err := database.ListBackups(uint(id))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list backups")
		return
	}

	writeJSON(w, http.StatusOK, backups)
}

// ListAllBackups returns backups across all instances. Admins see everything;
// non-admin users only see backups for instances assigned to them.
func ListAllBackups(w http.ResponseWriter, r *http.Request) {
	backups, err := database.ListAllBackups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list backups")
		return
	}

	user := middleware.GetUser(r)
	if user != nil && user.Role != "admin" {
		filtered := backups[:0]
		for _, b := range backups {
			if database.IsUserAssignedToInstance(user.ID, b.InstanceID) {
				filtered = append(filtered, b)
			}
		}
		backups = filtered
	}

	writeJSON(w, http.StatusOK, backups)
}

// GetBackupDetail returns details for a specific backup.
func GetBackupDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "backupId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid backup ID")
		return
	}

	b, err := database.GetBackup(uint(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Backup not found")
		return
	}

	if !middleware.CanAccessInstance(r, b.InstanceID) {
		writeError(w, http.StatusForbidden, "You do not have access to this backup")
		return
	}

	writeJSON(w, http.StatusOK, b)
}

// CancelBackupHandler aborts an in-flight backup. It looks up the active
// backup.create task by resource_id, calls Manager.Cancel which fires the
// OnCancel cleanup (delete partial file, mark row canceled), and returns.
func CancelBackupHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "backupId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid backup ID")
		return
	}
	b, err := database.GetBackup(uint(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Backup not found")
		return
	}
	if !middleware.CanAccessInstance(r, b.InstanceID) {
		writeError(w, http.StatusForbidden, "You do not have access to this backup")
		return
	}
	if b.Status != "running" {
		writeError(w, http.StatusConflict, "Backup is not running")
		return
	}
	if TaskMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "Task manager not initialized")
		return
	}
	tasks := TaskMgr.List(taskmanager.Filter{
		Type:       taskmanager.TaskBackupCreate,
		ResourceID: strconv.Itoa(id),
		OnlyActive: true,
	})
	if len(tasks) == 0 {
		writeError(w, http.StatusNotFound, "No active task for this backup")
		return
	}
	if err := TaskMgr.Cancel(tasks[0].ID); err != nil {
		switch {
		case errors.Is(err, taskmanager.ErrAlreadyTerminal):
			writeError(w, http.StatusConflict, "Task already finished")
		case errors.Is(err, taskmanager.ErrNotCancellable):
			writeError(w, http.StatusMethodNotAllowed, "Task is not cancellable")
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Backup cancellation requested"})
}

// DeleteBackupHandler removes a backup and its file.
func DeleteBackupHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "backupId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid backup ID")
		return
	}

	b, err := database.GetBackup(uint(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Backup not found")
		return
	}
	if !middleware.CanAccessInstance(r, b.InstanceID) {
		writeError(w, http.StatusForbidden, "You do not have access to this backup")
		return
	}

	if err := backup.DeleteBackup(uint(id)); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Backup deleted"})
}

type restoreRequest struct {
	InstanceID uint `json:"instance_id"`
}

// RestoreBackupHandler restores a backup to a target instance.
func RestoreBackupHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "backupId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid backup ID")
		return
	}

	b, err := database.GetBackup(uint(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Backup not found")
		return
	}
	if !middleware.CanAccessInstance(r, b.InstanceID) {
		writeError(w, http.StatusForbidden, "You do not have access to this backup")
		return
	}

	var req restoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.InstanceID == 0 {
		writeError(w, http.StatusBadRequest, "instance_id is required")
		return
	}

	if !middleware.CanAccessInstance(r, req.InstanceID) {
		writeError(w, http.StatusForbidden, "You do not have access to the target instance")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, req.InstanceID).Error; err != nil {
		writeError(w, http.StatusNotFound, "Target instance not found")
		return
	}

	orch := orchestrator.Get()
	if orch == nil {
		writeError(w, http.StatusServiceUnavailable, "Orchestrator not available")
		return
	}

	// Run restore asynchronously
	go func() {
		if err := backup.RestoreBackup(r.Context(), orch, inst.Name, uint(id)); err != nil {
			fmt.Printf("restore backup %d to instance %s failed: %v\n", id, inst.Name, err)
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"message": "Restore started"})
}

// DownloadBackup streams the backup archive file to the client.
func DownloadBackup(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "backupId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid backup ID")
		return
	}

	b, err := database.GetBackup(uint(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Backup not found")
		return
	}

	if !middleware.CanAccessInstance(r, b.InstanceID) {
		writeError(w, http.StatusForbidden, "You do not have access to this backup")
		return
	}

	if b.Status != "completed" {
		writeError(w, http.StatusConflict, "Backup is not completed")
		return
	}

	absPath := filepath.Join(backup.BackupDir(), b.FilePath)
	f, err := os.Open(absPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "Backup file not found")
		return
	}
	defer f.Close()

	stat, _ := f.Stat()
	filename := filepath.Base(b.FilePath)

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	if stat != nil {
		w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	}

	http.ServeContent(w, r, filename, stat.ModTime(), f)
}
