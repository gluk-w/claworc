package handlers

import (
	"net/http"
	"strconv"

	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
)

// AuditLog is set from main.go during init.
var AuditLog *sshaudit.Auditor

// GetAuditLogs handles GET /api/v1/audit-logs (admin only).
// Query parameters:
//   - instance_id (optional): filter by instance
//   - event_type (optional): filter by event type
//   - limit (optional): number of entries per page (default 100)
//   - offset (optional): pagination offset
func GetAuditLogs(w http.ResponseWriter, r *http.Request) {
	if AuditLog == nil {
		writeError(w, http.StatusServiceUnavailable, "Audit logging not initialized")
		return
	}

	opts := sshaudit.QueryOptions{}

	if idStr := r.URL.Query().Get("instance_id"); idStr != "" {
		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid instance_id")
			return
		}
		uid := uint(id)
		opts.InstanceID = &uid
	}

	if et := r.URL.Query().Get("event_type"); et != "" {
		eventType := sshaudit.EventType(et)
		opts.EventType = &eventType
	}

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit < 1 {
			writeError(w, http.StatusBadRequest, "Invalid limit")
			return
		}
		if limit > 1000 {
			limit = 1000
		}
		opts.Limit = limit
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		offset, err := strconv.Atoi(offsetStr)
		if err != nil || offset < 0 {
			writeError(w, http.StatusBadRequest, "Invalid offset")
			return
		}
		opts.Offset = offset
	}

	entries, total, err := AuditLog.Query(opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to query audit logs")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries": entries,
		"total":   total,
	})
}
