package channelhealth

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
	"gorm.io/gorm/clause"
)

const (
	// maxConcurrentChecks bounds how many instances are polled in parallel.
	maxConcurrentChecks = 5
	// perInstanceTimeout bounds one instance's dial+RPC round trip.
	perInstanceTimeout = 15 * time.Second
	// gatewayTunnelLabel is the tunnel manager's label for the OpenClaw
	// gateway tunnel (see sshproxy tunnel provisioning).
	gatewayTunnelLabel = "Gateway"
)

// Listener receives every stored snapshot, including ones whose overall
// status did not change — consumers that count consecutive results depend
// on non-transition snapshots too. Listeners run synchronously on the
// check goroutine and must not block.
type Listener func(snap Snapshot)

// Monitor periodically polls the OpenClaw gateway of every running
// instance for channel health, persists the results, and keeps an
// in-memory snapshot per instance for cheap reads by handlers.
type Monitor struct {
	tunnels  *sshproxy.TunnelManager
	interval time.Duration
	listener Listener

	mu        sync.RWMutex
	snapshots map[uint]Snapshot
}

// New builds a Monitor. tunnels is used to resolve the local port of each
// instance's Gateway SSH tunnel; interval is the polling period (<=0 falls
// back to 60s).
func New(tunnels *sshproxy.TunnelManager, interval time.Duration) *Monitor {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &Monitor{
		tunnels:   tunnels,
		interval:  interval,
		snapshots: make(map[uint]Snapshot),
	}
}

// SetListener registers the snapshot listener. Must be called before
// Start; the field is not synchronized.
func (m *Monitor) SetListener(fn Listener) {
	m.listener = fn
}

// Start launches the background polling loop. It returns immediately; the
// goroutine exits when ctx is canceled.
func (m *Monitor) Start(ctx context.Context) {
	go m.loop(ctx)
}

func (m *Monitor) loop(ctx context.Context) {
	t := time.NewTicker(m.interval)
	defer t.Stop()

	// Run once on startup so the UI has data quickly.
	m.checkAll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.checkAll(ctx)
		}
	}
}

func (m *Monitor) checkAll(ctx context.Context) {
	var instances []database.Instance
	if err := database.DB.Where("status = ?", "running").Find(&instances).Error; err != nil {
		log.Printf("[channelhealth] list instances: %v", err)
		return
	}

	sem := make(chan struct{}, maxConcurrentChecks)
	var wg sync.WaitGroup
	for i := range instances {
		if ctx.Err() != nil {
			break
		}
		inst := instances[i]
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, perInstanceTimeout)
			defer cancel()
			m.checkInstance(cctx, inst)
		}()
	}
	wg.Wait()
}

func (m *Monitor) checkInstance(ctx context.Context, inst database.Instance) {
	now := time.Now().UTC()

	port, ok := m.gatewayPort(inst.ID)
	if !ok {
		m.recordUnreachable(inst.ID, now)
		return
	}

	var gatewayToken string
	if inst.GatewayToken != "" {
		if tok, err := utils.Decrypt(inst.GatewayToken); err == nil {
			gatewayToken = tok
		}
	}

	payload, err := queryChannelsStatus(ctx, port, gatewayToken)
	if err != nil {
		log.Printf("[channelhealth] instance %d: channels.status: %v", inst.ID, err)
		m.recordUnreachable(inst.ID, now)
		return
	}

	states, err := BuildChannelStates(payload, now)
	if err != nil {
		// The gateway responded but with an unparseable payload; keep the
		// previous snapshot rather than flapping to unreachable.
		log.Printf("[channelhealth] instance %d: %v", inst.ID, err)
		return
	}

	if err := persistStates(inst.ID, states); err != nil {
		log.Printf("[channelhealth] instance %d: persist: %v", inst.ID, err)
	}

	m.store(Snapshot{
		InstanceID:       inst.ID,
		Overall:          DeriveOverall(true, true, states),
		GatewayReachable: true,
		CheckedAt:        now,
		Channels:         states,
	})
}

// gatewayPort resolves the local port of the instance's active Gateway
// tunnel.
func (m *Monitor) gatewayPort(instanceID uint) (int, bool) {
	if m.tunnels == nil {
		return 0, false
	}
	for _, t := range m.tunnels.GetTunnelsForInstance(instanceID) {
		if t.Label == gatewayTunnelLabel && t.Status == "active" {
			return t.LocalPort, true
		}
	}
	return 0, false
}

// recordUnreachable marks the instance-level state unreachable while
// keeping the previously known channel rows (from the prior snapshot or,
// failing that, the database) so the UI can still show the last state.
func (m *Monitor) recordUnreachable(instanceID uint, now time.Time) {
	m.mu.RLock()
	prev, had := m.snapshots[instanceID]
	m.mu.RUnlock()

	channels := prev.Channels
	if !had {
		if dbSnap, ok := SnapshotFromDB(instanceID); ok {
			channels = dbSnap.Channels
		}
	}

	m.store(Snapshot{
		InstanceID:       instanceID,
		Overall:          OverallUnreachable,
		GatewayReachable: false,
		CheckedAt:        now,
		Channels:         channels,
	})
}

// store swaps in the new snapshot and logs notable overall-status
// transitions (to unhealthy/unreachable, and recovery back to healthy).
func (m *Monitor) store(snap Snapshot) {
	m.mu.Lock()
	prev, had := m.snapshots[snap.InstanceID]
	m.snapshots[snap.InstanceID] = snap
	m.mu.Unlock()

	if m.listener != nil {
		m.listener(snap)
	}

	prevOverall := OverallUnknown
	if had {
		prevOverall = prev.Overall
	}
	if prevOverall == snap.Overall {
		return
	}
	switch {
	case snap.Overall == OverallUnhealthy || snap.Overall == OverallUnreachable:
		log.Printf("[channelhealth] instance %d: channel health %s -> %s", snap.InstanceID, prevOverall, snap.Overall)
	case snap.Overall == OverallHealthy && (prevOverall == OverallUnhealthy || prevOverall == OverallUnreachable):
		log.Printf("[channelhealth] instance %d: channel health recovered: %s -> %s", snap.InstanceID, prevOverall, snap.Overall)
	}
}

// Snapshot returns a copy of the latest snapshot for the instance, or
// ok=false when the instance has never been checked.
func (m *Monitor) Snapshot(instanceID uint) (Snapshot, bool) {
	m.mu.RLock()
	snap, ok := m.snapshots[instanceID]
	m.mu.RUnlock()
	if !ok {
		return Snapshot{}, false
	}
	out := snap
	out.Channels = append([]ChannelState(nil), snap.Channels...)
	return out, true
}

// SnapshotFromDB reconstructs a snapshot from persisted rows. Used as a
// fallback when no in-memory snapshot exists yet (e.g. right after a
// control-plane restart). ok=false when no rows exist.
func SnapshotFromDB(instanceID uint) (Snapshot, bool) {
	var rows []database.ChannelHealthStatus
	if err := database.DB.Where("instance_id = ?", instanceID).
		Order("channel ASC, account_id ASC").Find(&rows).Error; err != nil || len(rows) == 0 {
		return Snapshot{}, false
	}

	channels := make([]ChannelState, len(rows))
	var latest time.Time
	for i, r := range rows {
		channels[i] = ChannelState{
			Channel:           r.Channel,
			AccountID:         r.AccountID,
			Status:            r.Status,
			Enabled:           r.Enabled,
			Running:           r.Running,
			Connected:         r.Connected,
			Mode:              r.Mode,
			LastEventAt:       r.LastEventAt,
			LastInboundAt:     r.LastInboundAt,
			LastOutboundAt:    r.LastOutboundAt,
			LastError:         r.LastError,
			ReconnectAttempts: r.ReconnectAttempts,
			CheckedAt:         r.CheckedAt,
		}
		if r.CheckedAt.After(latest) {
			latest = r.CheckedAt
		}
	}

	return Snapshot{
		InstanceID:       instanceID,
		Overall:          DeriveOverall(true, true, channels),
		GatewayReachable: true,
		CheckedAt:        latest,
		Channels:         channels,
	}, true
}

// persistStates upserts one row per (instance, channel, account) and
// deletes rows for accounts that disappeared from the gateway's config.
func persistStates(instanceID uint, states []ChannelState) error {
	var existing []database.ChannelHealthStatus
	if err := database.DB.Where("instance_id = ?", instanceID).Find(&existing).Error; err != nil {
		return err
	}
	keep := make(map[[2]string]bool, len(states))
	for _, s := range states {
		keep[[2]string{s.Channel, s.AccountID}] = true
	}
	for _, e := range existing {
		if !keep[[2]string{e.Channel, e.AccountID}] {
			if err := database.DB.Delete(&database.ChannelHealthStatus{}, e.ID).Error; err != nil {
				return err
			}
		}
	}

	for _, s := range states {
		row := database.ChannelHealthStatus{
			InstanceID:        instanceID,
			Channel:           s.Channel,
			AccountID:         s.AccountID,
			Status:            s.Status,
			Enabled:           s.Enabled,
			Running:           s.Running,
			Connected:         s.Connected,
			Mode:              s.Mode,
			LastEventAt:       s.LastEventAt,
			LastInboundAt:     s.LastInboundAt,
			LastOutboundAt:    s.LastOutboundAt,
			LastError:         s.LastError,
			ReconnectAttempts: s.ReconnectAttempts,
			CheckedAt:         s.CheckedAt,
		}
		err := database.DB.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "instance_id"}, {Name: "channel"}, {Name: "account_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"status", "enabled", "running", "connected", "mode",
				"last_event_at", "last_inbound_at", "last_outbound_at",
				"last_error", "reconnect_attempts", "checked_at", "updated_at",
			}),
		}).Create(&row).Error
		if err != nil {
			return err
		}
	}
	return nil
}

// queryChannelsStatus dials the gateway over the local tunnel port, issues
// a channels.status request (without probing — probes hit provider APIs),
// and returns the raw response payload.
func queryChannelsStatus(ctx context.Context, port int, gatewayToken string) (json.RawMessage, error) {
	conn, err := sshproxy.DialGateway(ctx, port, gatewayToken)
	if err != nil {
		return nil, err
	}
	defer conn.CloseNow()

	reqID := fmt.Sprintf("chanhealth-%d", time.Now().UnixNano())
	frame := map[string]any{
		"type":   "req",
		"id":     reqID,
		"method": "channels.status",
		"params": map[string]any{},
	}
	reqJSON, err := json.Marshal(frame)
	if err != nil {
		return nil, fmt.Errorf("marshal channels.status: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, reqJSON); err != nil {
		return nil, fmt.Errorf("send channels.status: %w", err)
	}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("read channels.status: %w", err)
		}
		var resp struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			OK      bool            `json:"ok"`
			Payload json.RawMessage `json:"payload"`
			Error   json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		if resp.Type != "res" || resp.ID != reqID {
			continue
		}
		if !resp.OK {
			return nil, fmt.Errorf("channels.status failed: %s", string(resp.Error))
		}
		return resp.Payload, nil
	}
}
