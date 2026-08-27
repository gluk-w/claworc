package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/channelhealth"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/go-chi/chi/v5"
)

// ChannelHealthMon is set from main.go during init when the channel health
// monitor is enabled. nil means CLAWORC_CHANNEL_HEALTH_ENABLED=false.
var ChannelHealthMon *channelhealth.Monitor

type channelHealthResponse struct {
	InstanceID       uint                 `json:"instance_id"`
	Overall          string               `json:"overall"`
	GatewayReachable bool                 `json:"gateway_reachable"`
	CheckedAt        *string              `json:"checked_at"`
	Channels         []channelHealthEntry `json:"channels"`
}

type channelHealthEntry struct {
	Channel           string  `json:"channel"`
	AccountID         string  `json:"account_id"`
	Status            string  `json:"status"`
	Enabled           bool    `json:"enabled"`
	Running           bool    `json:"running"`
	Connected         bool    `json:"connected"`
	Mode              string  `json:"mode"`
	LastEventAt       *string `json:"last_event_at"`
	LastInboundAt     *string `json:"last_inbound_at"`
	LastOutboundAt    *string `json:"last_outbound_at"`
	LastError         string  `json:"last_error"`
	ReconnectAttempts int     `json:"reconnect_attempts"`
	CheckedAt         string  `json:"checked_at"`
}

// GetChannelHealth returns the latest per-channel health for an instance,
// as observed by the background channel health monitor.
func GetChannelHealth(w http.ResponseWriter, r *http.Request) {
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

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	// Monitor disabled entirely (CLAWORC_CHANNEL_HEALTH_ENABLED=false).
	if ChannelHealthMon == nil {
		writeJSON(w, http.StatusOK, channelHealthResponse{
			InstanceID:       inst.ID,
			Overall:          "disabled",
			GatewayReachable: false,
			CheckedAt:        nil,
			Channels:         []channelHealthEntry{},
		})
		return
	}

	snap, ok := ChannelHealthMon.Snapshot(inst.ID)
	if !ok {
		// No in-memory snapshot yet (e.g. control plane just restarted):
		// fall back to persisted rows.
		snap, ok = channelhealth.SnapshotFromDB(inst.ID)
	}
	if !ok {
		writeJSON(w, http.StatusOK, channelHealthResponse{
			InstanceID:       inst.ID,
			Overall:          channelhealth.OverallUnknown,
			GatewayReachable: false,
			CheckedAt:        nil,
			Channels:         []channelHealthEntry{},
		})
		return
	}

	channels := make([]channelHealthEntry, len(snap.Channels))
	for i, c := range snap.Channels {
		channels[i] = channelHealthEntry{
			Channel:           c.Channel,
			AccountID:         c.AccountID,
			Status:            c.Status,
			Enabled:           c.Enabled,
			Running:           c.Running,
			Connected:         c.Connected,
			Mode:              c.Mode,
			LastEventAt:       rfc3339OrNil(c.LastEventAt),
			LastInboundAt:     rfc3339OrNil(c.LastInboundAt),
			LastOutboundAt:    rfc3339OrNil(c.LastOutboundAt),
			LastError:         c.LastError,
			ReconnectAttempts: c.ReconnectAttempts,
			CheckedAt:         c.CheckedAt.UTC().Format(time.RFC3339),
		}
	}

	checkedAt := snap.CheckedAt.UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, channelHealthResponse{
		InstanceID:       inst.ID,
		Overall:          snap.Overall,
		GatewayReachable: snap.GatewayReachable,
		CheckedAt:        &checkedAt,
		Channels:         channels,
	})
}

// rfc3339OrNil formats an optional timestamp as RFC3339 UTC, or nil.
func rfc3339OrNil(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// GetChannelHealthEvents returns the escalation audit log for an instance
// (alerts sent, auto-restarts, recoveries), newest first.
// GET /api/v1/instances/{id}/channels/health/events?limit=50
func GetChannelHealthEvents(w http.ResponseWriter, r *http.Request) {
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

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	var events []database.ChannelHealthEvent
	if err := database.DB.Where("instance_id = ?", inst.ID).
		Order("created_at DESC").Limit(limit).Find(&events).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load events")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}
