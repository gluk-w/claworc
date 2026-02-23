// tunnel_health.go implements TCP-level health monitoring for SSH tunnels.
//
// It extends TunnelManager with periodic TCP probes against each tunnel's local
// port to verify the listener is alive and accepting connections. This complements
// the SSH connection-level health checks in health.go: SSH health checks verify
// the underlying connection, while tunnel health checks verify the tunnel's local
// listener infrastructure.
//
// A background goroutine (StartTunnelHealthChecker) probes all active tunnels
// at a configurable interval. Failed tunnels are marked as "error", which causes
// the reconciliation loop to recreate them on its next cycle.

package sshproxy

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// Tunnel health check configuration. Package-level vars so tests can override.
var (
	tunnelHealthCheckInterval = 60 * time.Second
	tunnelHealthCheckTimeout  = 5 * time.Second
)

// TunnelMetrics tracks health metrics for an individual SSH tunnel.
type TunnelMetrics struct {
	mu               sync.Mutex
	CreatedAt        time.Time `json:"created_at"`
	LastHealthCheck  time.Time `json:"last_health_check"`
	SuccessfulChecks int64     `json:"successful_checks"`
	FailedChecks     int64     `json:"failed_checks"`
}

// Snapshot returns an immutable copy of the metrics.
func (m *TunnelMetrics) Snapshot() TunnelMetrics {
	m.mu.Lock()
	defer m.mu.Unlock()
	return TunnelMetrics{
		CreatedAt:        m.CreatedAt,
		LastHealthCheck:  m.LastHealthCheck,
		SuccessfulChecks: m.SuccessfulChecks,
		FailedChecks:     m.FailedChecks,
	}
}

// Uptime returns the duration since the tunnel was created.
func (m *TunnelMetrics) Uptime() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.CreatedAt.IsZero() {
		return 0
	}
	return time.Since(m.CreatedAt)
}

func (m *TunnelMetrics) recordSuccess() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LastHealthCheck = time.Now()
	m.SuccessfulChecks++
}

func (m *TunnelMetrics) recordFailure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LastHealthCheck = time.Now()
	m.FailedChecks++
}

// TunnelMetricsSnapshot is an immutable snapshot of tunnel metrics for external consumption.
type TunnelMetricsSnapshot struct {
	Label             string        `json:"label"`
	LocalPort         int           `json:"local_port"`
	RemotePort        int           `json:"remote_port"`
	Status            string        `json:"status"`
	CreatedAt         time.Time     `json:"created_at"`
	LastHealthCheck   time.Time     `json:"last_health_check"`
	SuccessfulChecks  int64         `json:"successful_checks"`
	FailedChecks      int64         `json:"failed_checks"`
	Uptime            time.Duration `json:"uptime"`
	ReconnectionCount int64         `json:"reconnection_count"`
}

// CheckTunnelHealth verifies that a specific tunnel is functional by attempting
// a TCP connection to its local port. Returns an error if the tunnel is not
// found, not active, or the TCP connection fails.
func (tm *TunnelManager) CheckTunnelHealth(instanceID uint, label string) error {
	tm.mu.RLock()
	tunnels := tm.tunnels[instanceID]
	var target *ActiveTunnel
	for _, t := range tunnels {
		if t.Label == label {
			target = t
			break
		}
	}
	tm.mu.RUnlock()

	if target == nil {
		return fmt.Errorf("no tunnel with label %q for instance %d", label, instanceID)
	}

	if target.Status != "active" {
		if target.metrics != nil {
			target.metrics.recordFailure()
		}
		return fmt.Errorf("tunnel %q for instance %d has status %q", label, instanceID, target.Status)
	}

	// Attempt TCP connection to verify the local listener is alive
	addr := fmt.Sprintf("127.0.0.1:%d", target.LocalPort)
	conn, err := net.DialTimeout("tcp", addr, tunnelHealthCheckTimeout)
	if err != nil {
		if target.metrics != nil {
			target.metrics.recordFailure()
		}
		tm.setTunnelStatus(instanceID, target, "error", fmt.Sprintf("health check failed: %v", err))
		return fmt.Errorf("tcp probe %s for tunnel %q instance %d: %w", addr, label, instanceID, err)
	}
	conn.Close()

	if target.metrics != nil {
		target.metrics.recordSuccess()
	}
	return nil
}

// checkAllTunnelHealth runs TCP health checks against all active tunnels.
func (tm *TunnelManager) checkAllTunnelHealth() {
	tm.mu.RLock()
	type tunnelRef struct {
		instanceID uint
		label      string
	}
	var refs []tunnelRef
	for id, tunnels := range tm.tunnels {
		for _, t := range tunnels {
			if t.Status == "active" {
				refs = append(refs, tunnelRef{instanceID: id, label: t.Label})
			}
		}
	}
	tm.mu.RUnlock()

	if len(refs) == 0 {
		return
	}

	healthy, unhealthy := 0, 0
	for _, ref := range refs {
		if err := tm.CheckTunnelHealth(ref.instanceID, ref.label); err != nil {
			unhealthy++
			log.Printf("Tunnel health check failed: instance %d %q: %v", ref.instanceID, ref.label, err)
		} else {
			healthy++
		}
	}

	log.Printf("Tunnel health check complete: %d healthy, %d unhealthy", healthy, unhealthy)
}

// StartTunnelHealthChecker starts a background goroutine that periodically
// verifies the health of all active tunnels by probing their local TCP ports.
// Failed tunnels are marked as "error", which causes the reconciliation loop
// to recreate them on its next cycle.
func (tm *TunnelManager) StartTunnelHealthChecker(ctx context.Context) {
	hcCtx, hcCancel := context.WithCancel(ctx)
	tm.healthCancel = hcCancel

	go func() {
		ticker := time.NewTicker(tunnelHealthCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-hcCtx.Done():
				return
			case <-ticker.C:
				tm.checkAllTunnelHealth()
			}
		}
	}()

	log.Printf("Tunnel health checker started (interval: %s)", tunnelHealthCheckInterval)
}

// StopTunnelHealthChecker stops the background tunnel health check goroutine.
func (tm *TunnelManager) StopTunnelHealthChecker() {
	if tm.healthCancel != nil {
		tm.healthCancel()
		tm.healthCancel = nil
	}
}

// GetTunnelMetrics returns metrics snapshots for all tunnels of the given instance.
func (tm *TunnelManager) GetTunnelMetrics(instanceID uint) []TunnelMetricsSnapshot {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	tunnels := tm.tunnels[instanceID]
	if len(tunnels) == 0 {
		return nil
	}

	reconnCount := tm.getReconnectCount(instanceID)
	result := make([]TunnelMetricsSnapshot, len(tunnels))
	for i, t := range tunnels {
		var snap TunnelMetrics
		if t.metrics != nil {
			snap = t.metrics.Snapshot()
		}
		var uptime time.Duration
		if !snap.CreatedAt.IsZero() {
			uptime = time.Since(snap.CreatedAt)
		}
		result[i] = TunnelMetricsSnapshot{
			Label:             t.Label,
			LocalPort:         t.LocalPort,
			RemotePort:        t.Config.RemotePort,
			Status:            t.Status,
			CreatedAt:         snap.CreatedAt,
			LastHealthCheck:   snap.LastHealthCheck,
			SuccessfulChecks:  snap.SuccessfulChecks,
			FailedChecks:      snap.FailedChecks,
			Uptime:            uptime,
			ReconnectionCount: reconnCount,
		}
	}
	return result
}

// GetAllTunnelMetrics returns metrics snapshots for all tunnels across all instances.
func (tm *TunnelManager) GetAllTunnelMetrics() map[uint][]TunnelMetricsSnapshot {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	result := make(map[uint][]TunnelMetricsSnapshot, len(tm.tunnels))
	for id, tunnels := range tm.tunnels {
		reconnCount := tm.getReconnectCount(id)
		snapshots := make([]TunnelMetricsSnapshot, len(tunnels))
		for i, t := range tunnels {
			var snap TunnelMetrics
			if t.metrics != nil {
				snap = t.metrics.Snapshot()
			}
			var uptime time.Duration
			if !snap.CreatedAt.IsZero() {
				uptime = time.Since(snap.CreatedAt)
			}
			snapshots[i] = TunnelMetricsSnapshot{
				Label:             t.Label,
				LocalPort:         t.LocalPort,
				RemotePort:        t.Config.RemotePort,
				Status:            t.Status,
				CreatedAt:         snap.CreatedAt,
				LastHealthCheck:   snap.LastHealthCheck,
				SuccessfulChecks:  snap.SuccessfulChecks,
				FailedChecks:      snap.FailedChecks,
				Uptime:            uptime,
				ReconnectionCount: reconnCount,
			}
		}
		result[id] = snapshots
	}
	return result
}

// getReconnectCount returns the total reconnection count for an instance's tunnels.
func (tm *TunnelManager) getReconnectCount(instanceID uint) int64 {
	tm.reconnectCountMu.RLock()
	defer tm.reconnectCountMu.RUnlock()
	return tm.reconnectCounts[instanceID]
}

// incrementReconnectCount increments the reconnection count for an instance's tunnels.
func (tm *TunnelManager) incrementReconnectCount(instanceID uint) {
	tm.reconnectCountMu.Lock()
	defer tm.reconnectCountMu.Unlock()
	tm.reconnectCounts[instanceID]++
}
