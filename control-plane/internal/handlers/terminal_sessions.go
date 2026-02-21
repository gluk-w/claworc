package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshterminal"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
	"github.com/go-chi/chi/v5"
)

// sessionInfo is the JSON representation of a terminal session for API responses.
type sessionInfo struct {
	ID         string              `json:"id"`
	InstanceID uint                `json:"instance_id"`
	UserID     uint                `json:"user_id"`
	Shell      string              `json:"shell"`
	State      sshterminal.SessionState `json:"state"`
	CreatedAt  time.Time           `json:"created_at"`
	ClosedAt   *time.Time          `json:"closed_at,omitempty"`
}

func toSessionInfo(ms *sshterminal.ManagedSession) sessionInfo {
	return sessionInfo{
		ID:         ms.ID,
		InstanceID: ms.InstanceID,
		UserID:     ms.UserID,
		Shell:      ms.Shell,
		State:      ms.State(),
		CreatedAt:  ms.CreatedAt,
		ClosedAt:   ms.ClosedAt,
	}
}

// ListTerminalSessions returns all terminal sessions for an instance.
// GET /api/v1/instances/{id}/terminal/sessions
func ListTerminalSessions(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid instance ID", http.StatusBadRequest)
		return
	}

	if !middleware.CanAccessInstance(r, uint(id)) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}

	sessMgr := sshtunnel.GetSessionManager()
	if sessMgr == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string][]sessionInfo{"sessions": {}})
		return
	}

	activeOnly := r.URL.Query().Get("active") == "true"
	sessions := sessMgr.ListSessions(uint(id), activeOnly)

	result := make([]sessionInfo, 0, len(sessions))
	for _, ms := range sessions {
		result = append(result, toSessionInfo(ms))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]sessionInfo{"sessions": result})
}

// DeleteTerminalSession closes and removes a terminal session.
// DELETE /api/v1/instances/{id}/terminal/sessions/{sessionId}
func DeleteTerminalSession(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid instance ID", http.StatusBadRequest)
		return
	}

	if !middleware.CanAccessInstance(r, uint(id)) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	sessionID := chi.URLParam(r, "sessionId")
	if sessionID == "" {
		http.Error(w, "Missing session ID", http.StatusBadRequest)
		return
	}

	sessMgr := sshtunnel.GetSessionManager()
	if sessMgr == nil {
		http.Error(w, "Session manager not available", http.StatusServiceUnavailable)
		return
	}

	ms := sessMgr.GetSession(sessionID)
	if ms == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	if ms.InstanceID != uint(id) {
		http.Error(w, "Session belongs to different instance", http.StatusForbidden)
		return
	}

	sessMgr.CloseSession(sessionID)
	sessMgr.RemoveSession(sessionID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "closed"})
}

// GetTerminalRecording returns the recording for a terminal session.
// GET /api/v1/instances/{id}/terminal/sessions/{sessionId}/recording
func GetTerminalRecording(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid instance ID", http.StatusBadRequest)
		return
	}

	if !middleware.CanAccessInstance(r, uint(id)) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	sessionID := chi.URLParam(r, "sessionId")
	if sessionID == "" {
		http.Error(w, "Missing session ID", http.StatusBadRequest)
		return
	}

	sessMgr := sshtunnel.GetSessionManager()
	if sessMgr == nil {
		http.Error(w, "Session manager not available", http.StatusServiceUnavailable)
		return
	}

	ms := sessMgr.GetSession(sessionID)
	if ms == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	if ms.InstanceID != uint(id) {
		http.Error(w, "Session belongs to different instance", http.StatusForbidden)
		return
	}

	if ms.Recording == nil {
		http.Error(w, "Recording not enabled for this session", http.StatusNotFound)
		return
	}

	data, err := ms.Recording.ExportJSON()
	if err != nil {
		http.Error(w, "Failed to export recording", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}
