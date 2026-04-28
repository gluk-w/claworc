package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gluk-w/claworc/control-plane/internal/analytics"
	"github.com/gluk-w/claworc/control-plane/internal/backup"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/go-chi/chi/v5"
)

// scheduleInstanceIDs parses the schedule's instance_ids field, which may be a
// JSON array (e.g. "[1,2]") or a single numeric string for legacy callers.
func scheduleInstanceIDs(raw string) []uint {
	if raw == "" {
		return nil
	}
	var ids []uint
	if err := json.Unmarshal([]byte(raw), &ids); err == nil {
		return ids
	}
	// Fallback: single integer
	if n, err := strconv.Atoi(raw); err == nil {
		return []uint{uint(n)}
	}
	return nil
}

// canAccessAllScheduleInstances returns true if the caller has access to every
// instance referenced by the schedule's instance_ids field.
func canAccessAllScheduleInstances(r *http.Request, raw string) bool {
	ids := scheduleInstanceIDs(raw)
	if len(ids) == 0 {
		// A schedule with no resolvable instances is admin-only by default.
		user := middleware.GetUser(r)
		return user != nil && user.Role == "admin"
	}
	for _, id := range ids {
		if !middleware.CanAccessInstance(r, id) {
			return false
		}
	}
	return true
}

type scheduleCreateRequest struct {
	InstanceIDs    string   `json:"instance_ids"`
	CronExpression string   `json:"cron_expression"`
	Paths          []string `json:"paths"`
	RetentionDays  *int     `json:"retention_days,omitempty"`
}

type scheduleUpdateRequest struct {
	InstanceIDs    *string  `json:"instance_ids,omitempty"`
	CronExpression *string  `json:"cron_expression,omitempty"`
	Paths          []string `json:"paths,omitempty"`
	RetentionDays  *int     `json:"retention_days,omitempty"`
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

	if !canAccessAllScheduleInstances(r, req.InstanceIDs) {
		writeError(w, http.StatusForbidden, "You do not have access to all instances in this schedule")
		return
	}

	nextRun, err := backup.ComputeNextRun(req.CronExpression)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid cron expression: "+err.Error())
		return
	}

	retentionDays := 0
	if req.RetentionDays != nil {
		if *req.RetentionDays < 0 {
			writeError(w, http.StatusBadRequest, "retention_days must be >= 0")
			return
		}
		retentionDays = *req.RetentionDays
	}

	pathsJSON, _ := json.Marshal(req.Paths)
	if len(req.Paths) == 0 {
		pathsJSON = []byte(`["HOME"]`)
	}

	s := &database.BackupSchedule{
		InstanceIDs:    req.InstanceIDs,
		CronExpression: req.CronExpression,
		Paths:          string(pathsJSON),
		RetentionDays:  retentionDays,
		NextRunAt:      &nextRun,
	}

	if err := database.CreateBackupSchedule(s); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create schedule")
		return
	}

	instCount := len(scheduleInstanceIDs(req.InstanceIDs))
	if instCount == 0 {
		instCount = -1 // sentinel for "all"
	}
	withinDataDir := true
	for _, p := range req.Paths {
		if p != "HOME" && !strings.HasPrefix(p, "/data/") && p != "/data" {
			withinDataDir = false
			break
		}
	}
	analytics.Track(r.Context(), analytics.EventBackupScheduleCreated, map[string]any{
		"cron":            req.CronExpression,
		"instances_count": instCount,
		"retention_days":  retentionDays,
		"within_data_dir": withinDataDir,
		"paths_count":     len(req.Paths),
	})

	writeJSON(w, http.StatusCreated, s)
}

// ListBackupSchedules returns all backup schedules. Non-admin users only see
// schedules whose instances are all assigned to them.
func ListBackupSchedules(w http.ResponseWriter, r *http.Request) {
	schedules, err := database.ListBackupSchedules()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list schedules")
		return
	}

	user := middleware.GetUser(r)
	if user != nil && user.Role != "admin" {
		filtered := schedules[:0]
		for _, s := range schedules {
			if canAccessAllScheduleInstances(r, s.InstanceIDs) {
				filtered = append(filtered, s)
			}
		}
		schedules = filtered
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

	existing, err := database.GetBackupSchedule(uint(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Schedule not found")
		return
	}
	if !canAccessAllScheduleInstances(r, existing.InstanceIDs) {
		writeError(w, http.StatusForbidden, "You do not have access to this schedule")
		return
	}

	var req scheduleUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	updates := map[string]interface{}{}

	if req.InstanceIDs != nil {
		if !canAccessAllScheduleInstances(r, *req.InstanceIDs) {
			writeError(w, http.StatusForbidden, "You do not have access to all instances in this schedule")
			return
		}
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

	if req.RetentionDays != nil {
		if *req.RetentionDays < 0 {
			writeError(w, http.StatusBadRequest, "retention_days must be >= 0")
			return
		}
		updates["retention_days"] = *req.RetentionDays
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

	existing, err := database.GetBackupSchedule(uint(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Schedule not found")
		return
	}
	if !canAccessAllScheduleInstances(r, existing.InstanceIDs) {
		writeError(w, http.StatusForbidden, "You do not have access to this schedule")
		return
	}

	if err := database.DeleteBackupSchedule(uint(id)); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete schedule")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Schedule deleted"})
}
