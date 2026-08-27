package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/channelhealth"
	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// settingChannelAutoRestartEnabled is the DB settings key gating automatic
// restarts. Auto-restart is opt-in: absent or non-"true" means disabled.
const settingChannelAutoRestartEnabled = "channel_auto_restart_enabled"

// Channel health event types persisted to channel_health_events.
const (
	eventFailureDetected     = "failure_detected"
	eventAutoRestart         = "auto_restart"
	eventRestartLimitReached = "restart_limit_reached"
	eventRecovered           = "recovered"
)

// restartWindow is the rolling window the auto-restart circuit breaker
// counts restarts in.
const restartWindow = time.Hour

// ChannelEscalatorConfig holds the escalation thresholds (see
// CLAWORC_CHANNEL_HEALTH_* env vars).
type ChannelEscalatorConfig struct {
	AlertThreshold     int
	RestartThreshold   int
	MaxRestartsPerHour int
	RestartCooldown    time.Duration
}

// escalationState is the in-memory incident state for one instance. It is
// not persisted: after a control-plane restart an ongoing outage re-counts
// from zero.
type escalationState struct {
	consecutiveFails int
	failingSince     time.Time
	alertSent        bool
	breakerAlerted   bool
	cooldownUntil    time.Time
	// restartTimes is the rolling-window restart log for the circuit
	// breaker. Deliberately preserved across incident resets so a
	// restart -> briefly-healthy -> fail loop cannot restart forever.
	restartTimes []time.Time
}

// ChannelEscalator turns channel health snapshots into alerts and (opt-in)
// automatic instance restarts. It is registered as the channelhealth
// Monitor's listener; OnSnapshot does only in-memory bookkeeping
// synchronously and dispatches all I/O (DB writes, webhook, restart) in
// goroutines so the monitor loop never blocks.
type ChannelEscalator struct {
	cfg ChannelEscalatorConfig

	// Injectable for tests.
	now         func() time.Time
	restart     func(instanceID uint, title, message string)
	notify      func(p ChannelAlertPayload) string
	recordEvent func(ev database.ChannelHealthEvent)
	// dispatch runs slow work off the monitor goroutine (tests run it
	// inline for determinism).
	dispatch func(fn func())

	mu     sync.Mutex
	states map[uint]*escalationState
}

// NewChannelEscalator builds an escalator with production dependencies.
func NewChannelEscalator(cfg ChannelEscalatorConfig) *ChannelEscalator {
	e := &ChannelEscalator{
		cfg:      cfg,
		now:      time.Now,
		notify:   sendChannelAlert,
		states:   make(map[uint]*escalationState),
		dispatch: func(fn func()) { go fn() },
	}
	e.restart = e.restartInstance
	e.recordEvent = func(ev database.ChannelHealthEvent) {
		if err := database.DB.Create(&ev).Error; err != nil {
			log.Printf("[channelhealth] record event: %v", err)
		}
	}
	return e
}

// OnSnapshot is the channelhealth.Listener. It receives every stored
// snapshot, including ones whose overall status did not change.
func (e *ChannelEscalator) OnSnapshot(snap channelhealth.Snapshot) {
	failing := snap.Overall == channelhealth.OverallUnhealthy || snap.Overall == channelhealth.OverallUnreachable
	recovered := snap.Overall == channelhealth.OverallHealthy || snap.Overall == channelhealth.OverallNoChannels
	// degraded/unknown hold: neither count nor reset an open incident.
	if !failing && !recovered {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.states[snap.InstanceID]
	if st == nil {
		st = &escalationState{}
		e.states[snap.InstanceID] = st
	}
	now := e.now()

	if recovered {
		if st.alertSent {
			duration := now.Sub(st.failingSince)
			e.dispatch(func() {
				e.emit(snap, eventRecovered, map[string]any{
					"duration_seconds": int(duration.Seconds()),
				})
			})
		}
		st.consecutiveFails = 0
		st.failingSince = time.Time{}
		st.alertSent = false
		st.breakerAlerted = false
		st.cooldownUntil = time.Time{}
		return
	}

	// Failing snapshot.
	if now.Before(st.cooldownUntil) {
		return
	}
	st.consecutiveFails++
	if st.consecutiveFails == 1 {
		st.failingSince = snap.CheckedAt
		if st.failingSince.IsZero() {
			st.failingSince = now
		}
	}

	if st.consecutiveFails >= e.cfg.AlertThreshold && !st.alertSent {
		st.alertSent = true
		fails := st.consecutiveFails
		since := st.failingSince
		e.dispatch(func() {
			e.emit(snap, eventFailureDetected, map[string]any{
				"consecutive_failures": fails,
				"failing_since":        since,
			})
		})
	}

	if st.consecutiveFails < e.cfg.RestartThreshold {
		return
	}
	if !autoRestartEnabled() {
		return
	}

	// Circuit breaker: cap restarts per instance per rolling hour.
	cutoff := now.Add(-restartWindow)
	recent := st.restartTimes[:0]
	for _, t := range st.restartTimes {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	st.restartTimes = recent
	if len(st.restartTimes) >= e.cfg.MaxRestartsPerHour {
		if !st.breakerAlerted {
			st.breakerAlerted = true
			restarts := len(st.restartTimes)
			e.dispatch(func() {
				e.emit(snap, eventRestartLimitReached, map[string]any{
					"restarts_last_hour": restarts,
				})
			})
		}
		return
	}

	st.restartTimes = append(st.restartTimes, now)
	st.cooldownUntil = now.Add(e.cfg.RestartCooldown)
	fails := st.consecutiveFails
	since := st.failingSince
	e.dispatch(func() {
		e.restart(snap.InstanceID,
			"Auto-restarting agent with unhealthy channels",
			fmt.Sprintf("Channel health %s for %d consecutive checks", snap.Overall, fails))
		e.emit(snap, eventAutoRestart, map[string]any{
			"consecutive_failures": fails,
			"failing_since":        since,
		})
	})
}

// emit persists an audit event and sends the webhook alert for it. Runs
// off the monitor goroutine.
func (e *ChannelEscalator) emit(snap channelhealth.Snapshot, eventType string, extra map[string]any) {
	payload := buildChannelAlertPayload(snap, eventType, extra)
	status := e.notify(payload)

	detail := map[string]any{}
	for k, v := range extra {
		detail[k] = v
	}
	if len(payload.Channels) > 0 {
		detail["channels"] = payload.Channels
	}
	detailJSON, _ := json.Marshal(detail)
	e.recordEvent(database.ChannelHealthEvent{
		InstanceID:    snap.InstanceID,
		Type:          eventType,
		Overall:       snap.Overall,
		Detail:        string(detailJSON),
		WebhookStatus: status,
	})
}

// restartInstance is the production restart dependency: it re-fetches a
// fresh instance row (the snapshot may be up to one interval old) and
// reuses the shared async restart flow, which no-ops unless the instance
// is still running.
func (e *ChannelEscalator) restartInstance(instanceID uint, title, message string) {
	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		log.Printf("[channelhealth] auto-restart: load instance %d: %v", instanceID, err)
		return
	}
	log.Printf("[channelhealth] auto-restarting instance %d (%s): %s", inst.ID, inst.DisplayName, message)
	restartInstanceAsyncWithToast(inst, 0, title, message)
}

// autoRestartEnabled reads the opt-in toggle from the settings table.
// Settings are intentionally not cached (matches the rest of the settings
// surface), so flipping the toggle takes effect on the next check.
func autoRestartEnabled() bool {
	val, err := database.GetSetting(settingChannelAutoRestartEnabled)
	return err == nil && val == "true"
}
