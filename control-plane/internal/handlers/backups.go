package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gluk-w/claworc/control-plane/internal/backup"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
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

	backupID, err := backup.CreateFullBackup(r.Context(), orch, inst.Name, inst.ID, req.Note, req.Paths)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to start backup: %v", err))
		return
	}

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

	backups, err := database.ListBackups(uint(id))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list backups")
		return
	}

	writeJSON(w, http.StatusOK, backups)
}

// ListAllBackups returns all backups across all instances.
func ListAllBackups(w http.ResponseWriter, r *http.Request) {
	backups, err := database.ListAllBackups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list backups")
		return
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

	writeJSON(w, http.StatusOK, b)
}

// DeleteBackupHandler removes a backup and its file.
func DeleteBackupHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "backupId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid backup ID")
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

	var req restoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.InstanceID == 0 {
		writeError(w, http.StatusBadRequest, "instance_id is required")
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
