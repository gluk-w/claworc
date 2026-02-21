// health.go implements SSH connection health monitoring for the sshproxy package.
//
// It extends SSHManager with periodic health checks that execute a lightweight
// command ("echo ping") over each active SSH connection to verify end-to-end
// functionality. This complements the protocol-level keepalive in manager.go:
// keepalive detects dead TCP connections, while health checks verify that the
// SSH server is responsive and can execute commands.
//
// A background goroutine (StartHealthChecker) runs checks at a configurable
// interval and removes connections that fail, triggering reconnection via the
// tunnel reconciliation loop.

package sshproxy

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	// healthCheckTimeout is the maximum time to wait for a health check command.
	healthCheckTimeout = 5 * time.Second

	// healthCheckInterval is how often the background goroutine checks connections.
	healthCheckInterval = 30 * time.Second

	// healthCheckCommand is the lightweight command executed to verify SSH functionality.
	healthCheckCommand = "echo ping"
)

// ConnectionMetrics tracks health metrics for an SSH connection.
type ConnectionMetrics struct {
	mu               sync.Mutex
	ConnectedAt      time.Time `json:"connected_at"`
	LastHealthCheck  time.Time `json:"last_health_check"`
	SuccessfulChecks int64     `json:"successful_checks"`
	FailedChecks     int64     `json:"failed_checks"`
}

// Uptime returns the duration since the connection was established.
func (cm *ConnectionMetrics) Uptime() time.Duration {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.ConnectedAt.IsZero() {
		return 0
	}
	return time.Since(cm.ConnectedAt)
}

// Snapshot returns a copy of the metrics safe for concurrent use.
func (cm *ConnectionMetrics) Snapshot() ConnectionMetrics {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return ConnectionMetrics{
		ConnectedAt:      cm.ConnectedAt,
		LastHealthCheck:  cm.LastHealthCheck,
		SuccessfulChecks: cm.SuccessfulChecks,
		FailedChecks:     cm.FailedChecks,
	}
}

// HealthCheck executes a lightweight command ("echo ping") on the SSH connection
// for the given instance ID and returns an error if the command fails or times out.
// It updates the connection's health metrics regardless of outcome.
func (m *SSHManager) HealthCheck(instanceID uint) error {
	m.mu.RLock()
	mc, ok := m.conns[instanceID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no connection for instance %d", instanceID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), healthCheckTimeout)
	defer cancel()

	session, err := mc.client.NewSession()
	if err != nil {
		mc.metrics.recordFailure()
		return fmt.Errorf("create session for instance %d: %w", instanceID, err)
	}
	defer session.Close()

	done := make(chan error, 1)
	go func() {
		_, err := session.Output(healthCheckCommand)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			mc.metrics.recordFailure()
			return fmt.Errorf("health check command failed for instance %d: %w", instanceID, err)
		}
		mc.metrics.recordSuccess()
		return nil
	case <-ctx.Done():
		mc.metrics.recordFailure()
		return fmt.Errorf("health check timed out for instance %d", instanceID)
	}
}

// StartHealthChecker starts a background goroutine that periodically health-checks
// all active SSH connections. Unhealthy connections are closed and removed, which
// triggers reconnection via the tunnel reconciliation loop.
func (m *SSHManager) StartHealthChecker(ctx context.Context) {
	hcCtx, hcCancel := context.WithCancel(ctx)
	m.healthCancel = hcCancel

	go func() {
		ticker := time.NewTicker(healthCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-hcCtx.Done():
				return
			case <-ticker.C:
				m.checkAllConnections()
			}
		}
	}()

	log.Printf("SSH health checker started (interval: %s)", healthCheckInterval)
}

// StopHealthChecker stops the background health check goroutine.
func (m *SSHManager) StopHealthChecker() {
	if m.healthCancel != nil {
		m.healthCancel()
		m.healthCancel = nil
	}
}

// checkAllConnections runs a health check against every active connection.
// Connections that fail the health check are closed and removed from the map.
func (m *SSHManager) checkAllConnections() {
	m.mu.RLock()
	instanceIDs := make([]uint, 0, len(m.conns))
	for id := range m.conns {
		instanceIDs = append(instanceIDs, id)
	}
	m.mu.RUnlock()

	for _, id := range instanceIDs {
		if err := m.HealthCheck(id); err != nil {
			log.Printf("SSH health check failed for instance %d: %v", id, err)
			reason := fmt.Sprintf("health check failed: %v", err)
			m.stateTracker.setState(id, StateDisconnected, reason)
			m.Close(id)
			m.emitEvent(ConnectionEvent{
				InstanceID: id,
				Type:       EventDisconnected,
				Timestamp:  time.Now(),
				Details:    reason,
			})
			m.triggerReconnect(id, reason)
		}
	}
}

// GetMetrics returns a snapshot of the connection health metrics for the given
// instance ID, or nil if no connection exists.
func (m *SSHManager) GetMetrics(instanceID uint) *ConnectionMetrics {
	m.mu.RLock()
	mc, ok := m.conns[instanceID]
	m.mu.RUnlock()

	if !ok {
		return nil
	}

	snapshot := mc.metrics.Snapshot()
	return &snapshot
}

// GetAllMetrics returns a snapshot of health metrics for all active connections.
func (m *SSHManager) GetAllMetrics() map[uint]ConnectionMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[uint]ConnectionMetrics, len(m.conns))
	for id, mc := range m.conns {
		result[id] = mc.metrics.Snapshot()
	}
	return result
}

// recordSuccess updates metrics after a successful health check.
func (cm *ConnectionMetrics) recordSuccess() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.LastHealthCheck = time.Now()
	cm.SuccessfulChecks++
}

// recordFailure updates metrics after a failed health check.
func (cm *ConnectionMetrics) recordFailure() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.LastHealthCheck = time.Now()
	cm.FailedChecks++
}
