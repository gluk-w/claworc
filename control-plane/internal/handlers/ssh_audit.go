package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
)

// GetSSHAuditLogs returns paginated SSH audit log entries.
// Admin-only endpoint.
//
// Query parameters:
//
//	instance_id   - filter by instance ID
//	instance_name - filter by instance name
//	event_type    - filter by event type
//	username      - filter by username
//	since         - RFC3339 timestamp, only entries after this time
//	until         - RFC3339 timestamp, only entries before this time
//	limit         - max entries to return (default 50, max 1000)
//	offset        - pagination offset
func GetSSHAuditLogs(w http.ResponseWriter, r *http.Request) {
	auditor := sshaudit.GetAuditor()
	if auditor == nil {
		writeError(w, http.StatusServiceUnavailable, "Audit system not initialized")
		return
	}

	opts := sshaudit.QueryOptions{}

	if v := r.URL.Query().Get("instance_id"); v != "" {
		id, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid instance_id")
			return
		}
		opts.InstanceID = uint(id)
	}
	if v := r.URL.Query().Get("instance_name"); v != "" {
		opts.InstanceName = v
	}
	if v := r.URL.Query().Get("event_type"); v != "" {
		opts.EventType = v
	}
	if v := r.URL.Query().Get("username"); v != "" {
		opts.Username = v
	}
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid since timestamp (use RFC3339)")
			return
		}
		opts.Since = &t
	}
	if v := r.URL.Query().Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid until timestamp (use RFC3339)")
			return
		}
		opts.Until = &t
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "Invalid limit")
			return
		}
		opts.Limit = n
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "Invalid offset")
			return
		}
		opts.Offset = n
	}

	result, err := auditor.Query(opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to query audit logs")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// PurgeSSHAuditLogs manually triggers audit log purge.
// Admin-only endpoint.
//
// Query parameters:
//
//	days - number of days to retain (uses configured default if omitted)
func PurgeSSHAuditLogs(w http.ResponseWriter, r *http.Request) {
	auditor := sshaudit.GetAuditor()
	if auditor == nil {
		writeError(w, http.StatusServiceUnavailable, "Audit system not initialized")
		return
	}

	days := 0
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "Invalid days parameter")
			return
		}
		days = n
	}

	deleted, err := auditor.PurgeOlderThan(days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to purge audit logs")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"deleted":        deleted,
		"retention_days": auditor.RetentionDays(),
	})
}
