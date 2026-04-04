package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gluk-w/claworc/control-plane/internal/backup"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/go-chi/chi/v5"
)

type scheduleCreateRequest struct {
	InstanceIDs    string   `json:"instance_ids"`
	CronExpression string   `json:"cron_expression"`
	Paths          []string `json:"paths"`
}

type scheduleUpdateRequest struct {
	InstanceIDs    *string  `json:"instance_ids,omitempty"`
	CronExpression *string  `json:"cron_expression,omitempty"`
	Paths          []string `json:"paths,omitempty"`
}

// CreateBackupSchedule creates a new backup schedule.
func CreateBackupSchedule(w http.ResponseWriter, r *http.Request) {
	var req scheduleCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.CronExpression == "" {
		writeError(w, http.StatusBadRequest, "cron_expression is required")
		return
	}

	if req.InstanceIDs == "" {
		writeError(w, http.StatusBadRequest, "instance_ids is required")
		return
	}

	nextRun, err := backup.ComputeNextRun(req.CronExpression)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid cron expression: "+err.Error())
		return
	}

	pathsJSON, _ := json.Marshal(req.Paths)
	if len(req.Paths) == 0 {
		pathsJSON = []byte(`["HOME"]`)
	}

	s := &database.BackupSchedule{
		InstanceIDs:    req.InstanceIDs,
		CronExpression: req.CronExpression,
		Paths:          string(pathsJSON),
		NextRunAt:      &nextRun,
	}

	if err := database.CreateBackupSchedule(s); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create schedule")
		return
	}

	writeJSON(w, http.StatusCreated, s)
}

// ListBackupSchedules returns all backup schedules.
func ListBackupSchedules(w http.ResponseWriter, r *http.Request) {
	schedules, err := database.ListBackupSchedules()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list schedules")
		return
	}

	writeJSON(w, http.StatusOK, schedules)
}

// UpdateBackupSchedule updates an existing backup schedule.
func UpdateBackupSchedule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid schedule ID")
		return
	}

	if _, err := database.GetBackupSchedule(uint(id)); err != nil {
		writeError(w, http.StatusNotFound, "Schedule not found")
		return
	}

	var req scheduleUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	updates := map[string]interface{}{}

	if req.InstanceIDs != nil {
		updates["instance_ids"] = *req.InstanceIDs
	}

	if len(req.Paths) > 0 {
		pathsJSON, _ := json.Marshal(req.Paths)
		updates["paths"] = string(pathsJSON)
	}

	if req.CronExpression != nil {
		nextRun, err := backup.ComputeNextRun(*req.CronExpression)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid cron expression: "+err.Error())
			return
		}
		updates["cron_expression"] = *req.CronExpression
		updates["next_run_at"] = &nextRun
	}

	if len(updates) == 0 {
		writeError(w, http.StatusBadRequest, "No fields to update")
		return
	}

	if err := database.UpdateBackupSchedule(uint(id), updates); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update schedule")
		return
	}

	updated, _ := database.GetBackupSchedule(uint(id))
	writeJSON(w, http.StatusOK, updated)
}

// DeleteBackupSchedule removes a backup schedule.
func DeleteBackupSchedule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid schedule ID")
		return
	}

	if _, err := database.GetBackupSchedule(uint(id)); err != nil {
		writeError(w, http.StatusNotFound, "Schedule not found")
		return
	}

	if err := database.DeleteBackupSchedule(uint(id)); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete schedule")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Schedule deleted"})
}
