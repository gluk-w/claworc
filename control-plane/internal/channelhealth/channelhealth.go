// Package channelhealth implements a background monitor that polls each
// running instance's OpenClaw gateway for per-channel/per-account runtime
// state (via the channels.status RPC), evaluates health, persists the
// latest status to the database, and keeps an in-memory snapshot for
// cheap reads by HTTP handlers.
package channelhealth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Per-account health statuses.
const (
	StatusHealthy      = "healthy"
	StatusDisconnected = "disconnected"
	StatusNotRunning   = "not_running"
	StatusStale        = "stale"
	StatusDisabled     = "disabled"
	StatusUnknown      = "unknown"
)

// Instance-level overall statuses.
const (
	OverallHealthy     = "healthy"
	OverallDegraded    = "degraded"
	OverallUnhealthy   = "unhealthy"
	OverallUnreachable = "unreachable"
	OverallNoChannels  = "no_channels"
	OverallUnknown     = "unknown"
)

// StaleThreshold is how long a connected persistent-socket channel may go
// without any event before it is considered stale.
const StaleThreshold = 30 * time.Minute

// AccountState is the parsed runtime state of one channel account as
// reported by the gateway's channels.status RPC.
type AccountState struct {
	AccountID         string
	Enabled           bool
	Configured        bool
	Running           bool
	Connected         bool
	Mode              string
	LastEventAt       *time.Time
	LastInboundAt     *time.Time
	LastOutboundAt    *time.Time
	LastError         string
	ReconnectAttempts int
}

// ChannelState is the evaluated health of one channel account, ready for
// persistence and for serving to the frontend.
type ChannelState struct {
	Channel           string
	AccountID         string
	Status            string
	Enabled           bool
	Running           bool
	Connected         bool
	Mode              string
	LastEventAt       *time.Time
	LastInboundAt     *time.Time
	LastOutboundAt    *time.Time
	LastError         string
	ReconnectAttempts int
	CheckedAt         time.Time
}

// Snapshot is the latest known channel health for one instance.
type Snapshot struct {
	InstanceID       uint
	Overall          string
	GatewayReachable bool
	CheckedAt        time.Time
	Channels         []ChannelState
}

// --- channels.status payload parsing -----------------------------------

// wirePayload mirrors the channels.status response payload. Every field is
// optional/nullable on the wire, so everything is a pointer or raw JSON.
type wirePayload struct {
	ChannelAccounts map[string][]wireAccount `json:"channelAccounts"`
}

type wireAccount struct {
	AccountID         *string         `json:"accountId"`
	Enabled           *bool           `json:"enabled"`
	Configured        *bool           `json:"configured"`
	Running           *bool           `json:"running"`
	Connected         *bool           `json:"connected"`
	LastEventAt       *float64        `json:"lastEventAt"`
	LastInboundAt     *float64        `json:"lastInboundAt"`
	LastOutboundAt    *float64        `json:"lastOutboundAt"`
	LastError         json.RawMessage `json:"lastError"`
	ReconnectAttempts *int            `json:"reconnectAttempts"`
	Mode              *string         `json:"mode"`
}

func (w wireAccount) toAccountState() AccountState {
	return AccountState{
		AccountID:         strOr(w.AccountID, "default"),
		Enabled:           boolOr(w.Enabled, true),
		Configured:        boolOr(w.Configured, true),
		Running:           boolOr(w.Running, false),
		Connected:         boolOr(w.Connected, false),
		Mode:              strOr(w.Mode, ""),
		LastEventAt:       msToTime(w.LastEventAt),
		LastInboundAt:     msToTime(w.LastInboundAt),
		LastOutboundAt:    msToTime(w.LastOutboundAt),
		LastError:         lastErrorString(w.LastError),
		ReconnectAttempts: intOr(w.ReconnectAttempts, 0),
	}
}

func strOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func intOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

// msToTime converts a Unix-milliseconds timestamp to *time.Time. Zero and
// negative values are treated as absent.
func msToTime(p *float64) *time.Time {
	if p == nil || *p <= 0 {
		return nil
	}
	t := time.UnixMilli(int64(*p)).UTC()
	return &t
}

// lastErrorString normalizes the lastError field, which may be absent,
// null, a plain string, or an object, into a display string.
func lastErrorString(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err == nil {
		return s
	}
	var obj map[string]any
	if err := json.Unmarshal(trimmed, &obj); err == nil {
		for _, key := range []string{"message", "error", "reason", "code"} {
			if v, ok := obj[key].(string); ok && v != "" {
				return v
			}
		}
	}
	return string(trimmed)
}

// BuildChannelStates parses a channels.status payload and evaluates the
// health of every channel account. The result is sorted by (channel,
// account_id) for deterministic output.
func BuildChannelStates(payload []byte, now time.Time) ([]ChannelState, error) {
	var wp wirePayload
	if err := json.Unmarshal(payload, &wp); err != nil {
		return nil, fmt.Errorf("parse channels.status payload: %w", err)
	}

	states := make([]ChannelState, 0, len(wp.ChannelAccounts))
	for channel, accounts := range wp.ChannelAccounts {
		for _, wa := range accounts {
			a := wa.toAccountState()
			states = append(states, ChannelState{
				Channel:           channel,
				AccountID:         a.AccountID,
				Status:            EvaluateAccount(a, now),
				Enabled:           a.Enabled,
				Running:           a.Running,
				Connected:         a.Connected,
				Mode:              a.Mode,
				LastEventAt:       a.LastEventAt,
				LastInboundAt:     a.LastInboundAt,
				LastOutboundAt:    a.LastOutboundAt,
				LastError:         a.LastError,
				ReconnectAttempts: a.ReconnectAttempts,
				CheckedAt:         now,
			})
		}
	}
	sort.Slice(states, func(i, j int) bool {
		if states[i].Channel != states[j].Channel {
			return states[i].Channel < states[j].Channel
		}
		return states[i].AccountID < states[j].AccountID
	})
	return states, nil
}

// isPersistentMode reports whether a channel mode implies a long-lived
// socket connection over which events are expected to keep flowing.
// Pull-style modes (http, webhook) never go "stale".
func isPersistentMode(mode string) bool {
	return mode != "http" && mode != "webhook"
}

// EvaluateAccount derives the health status for one channel account.
func EvaluateAccount(a AccountState, now time.Time) string {
	if !a.Enabled || !a.Configured {
		return StatusDisabled
	}
	switch {
	case a.Running && !a.Connected:
		return StatusDisconnected
	case !a.Running:
		return StatusNotRunning
	case a.Connected:
		if isPersistentMode(a.Mode) && a.LastEventAt != nil && now.Sub(*a.LastEventAt) > StaleThreshold {
			return StatusStale
		}
		return StatusHealthy
	default:
		return StatusUnknown
	}
}

// DeriveOverall computes the instance-level status from the per-channel
// statuses. checked is false when the instance has never been polled.
func DeriveOverall(gatewayReachable, checked bool, channels []ChannelState) string {
	if !checked {
		return OverallUnknown
	}
	if !gatewayReachable {
		return OverallUnreachable
	}
	var active, unhealthy, degraded, healthy int
	for _, c := range channels {
		switch c.Status {
		case StatusDisabled:
			continue
		case StatusDisconnected, StatusNotRunning:
			unhealthy++
		case StatusStale, StatusUnknown:
			degraded++
		case StatusHealthy:
			healthy++
		}
		active++
	}
	switch {
	case active == 0:
		return OverallNoChannels
	case unhealthy > 0:
		return OverallUnhealthy
	case degraded > 0:
		return OverallDegraded
	case healthy > 0:
		return OverallHealthy
	default:
		return OverallUnknown
	}
}
