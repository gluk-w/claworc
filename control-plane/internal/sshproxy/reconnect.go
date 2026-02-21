// reconnect.go implements automatic SSH reconnection with exponential backoff
// and connection state change events for the sshproxy package.
//
// When a health check or keepalive detects a dead connection, triggerReconnect
// launches an asynchronous reconnection goroutine. The reconnection re-uploads
// the global public key before each attempt (the agent container may have
// restarted, losing /root/.ssh/authorized_keys) and retries with exponential
// backoff (1s → 2s → 4s → 8s → 16s cap).
//
// Connection state change events (connected, disconnected, reconnecting, etc.)
// are emitted to registered EventListeners for observability and UI updates.

package sshproxy

import (
	"context"
	"fmt"
	"log"
	"time"
)

// Reconnection backoff configuration. Package-level vars so tests can override.
var (
	reconnectInitialBackoff = 1 * time.Second
	reconnectMaxBackoff     = 16 * time.Second
	reconnectDefaultRetries = 10
)

// ConnectionEventType defines the type of connection state change event.
type ConnectionEventType string

const (
	EventConnected       ConnectionEventType = "connected"
	EventDisconnected    ConnectionEventType = "disconnected"
	EventReconnecting    ConnectionEventType = "reconnecting"
	EventReconnected     ConnectionEventType = "reconnected"
	EventReconnectFailed ConnectionEventType = "reconnect_failed"
	EventKeyUploaded     ConnectionEventType = "key_uploaded"
)

// ConnectionEvent represents a connection state change event.
type ConnectionEvent struct {
	InstanceID uint                `json:"instance_id"`
	Type       ConnectionEventType `json:"type"`
	Timestamp  time.Time           `json:"timestamp"`
	Details    string              `json:"details"`
}

// EventListener is a callback for connection state change events.
// Listeners are called synchronously — long-running handlers should spawn goroutines.
type EventListener func(event ConnectionEvent)

// SetOrchestrator configures the orchestrator used for automatic reconnection.
// Must be called before StartHealthChecker for reconnection to work.
func (m *SSHManager) SetOrchestrator(orch Orchestrator) {
	m.reconnMu.Lock()
	defer m.reconnMu.Unlock()
	m.orch = orch
}

// OnEvent registers a listener for connection state change events.
func (m *SSHManager) OnEvent(listener EventListener) {
	m.reconnMu.Lock()
	defer m.reconnMu.Unlock()
	m.eventListeners = append(m.eventListeners, listener)
}

// emitEvent sends a connection event to all registered listeners and
// records it in the event log for later retrieval.
func (m *SSHManager) emitEvent(event ConnectionEvent) {
	// Record in persistent event log
	m.eventLog.recordEvent(event)

	// Notify listeners
	m.reconnMu.RLock()
	listeners := make([]EventListener, len(m.eventListeners))
	copy(listeners, m.eventListeners)
	m.reconnMu.RUnlock()

	for _, l := range listeners {
		l(event)
	}
}

// ReconnectWithBackoff attempts to reconnect to an instance with exponential
// backoff. It re-uploads the global public key via ConfigureSSHAccess before
// each attempt (the agent may have restarted, losing authorized_keys).
//
// The method blocks until reconnection succeeds, maxRetries is exhausted, or
// the context is cancelled. It is safe to call concurrently for different
// instances; for the same instance, use triggerReconnect which deduplicates.
func (m *SSHManager) ReconnectWithBackoff(ctx context.Context, instanceID uint, maxRetries int, reason string) error {
	m.reconnMu.RLock()
	orch := m.orch
	m.reconnMu.RUnlock()

	if orch == nil {
		return fmt.Errorf("no orchestrator configured for reconnection")
	}

	return m.reconnectWithBackoff(ctx, instanceID, maxRetries, orch, reason)
}

// reconnectWithBackoff is the internal reconnection implementation.
func (m *SSHManager) reconnectWithBackoff(ctx context.Context, instanceID uint, maxRetries int, orch Orchestrator, reason string) error {
	log.Printf("SSH reconnecting to instance %d (reason: %s)", instanceID, reason)

	m.stateTracker.setState(instanceID, StateReconnecting, reason)
	m.emitEvent(ConnectionEvent{
		InstanceID: instanceID,
		Type:       EventReconnecting,
		Timestamp:  time.Now(),
		Details:    reason,
	})

	// Close stale connection before attempting reconnect
	m.Close(instanceID)

	backoff := reconnectInitialBackoff
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		log.Printf("SSH reconnect attempt %d/%d for instance %d", attempt, maxRetries, instanceID)

		// Re-upload the global public key before each attempt
		// (agent may have restarted, losing authorized_keys)
		if err := orch.ConfigureSSHAccess(ctx, instanceID, m.publicKey); err != nil {
			lastErr = fmt.Errorf("configure ssh access (attempt %d): %w", attempt, err)
			log.Printf("SSH key upload failed for instance %d (attempt %d): %v", instanceID, attempt, err)
		} else {
			m.emitEvent(ConnectionEvent{
				InstanceID: instanceID,
				Type:       EventKeyUploaded,
				Timestamp:  time.Now(),
				Details:    fmt.Sprintf("attempt %d", attempt),
			})

			// Get SSH address from orchestrator
			host, port, err := orch.GetSSHAddress(ctx, instanceID)
			if err != nil {
				lastErr = fmt.Errorf("get ssh address (attempt %d): %w", attempt, err)
				log.Printf("SSH address lookup failed for instance %d (attempt %d): %v", instanceID, attempt, err)
			} else {
				// Attempt SSH connection
				_, err = m.Connect(ctx, instanceID, host, port)
				if err != nil {
					lastErr = fmt.Errorf("connect (attempt %d): %w", attempt, err)
					log.Printf("SSH connect failed for instance %d (attempt %d): %v", instanceID, attempt, err)
				} else {
					log.Printf("SSH reconnected to instance %d after %d attempt(s)", instanceID, attempt)
					m.emitEvent(ConnectionEvent{
						InstanceID: instanceID,
						Type:       EventReconnected,
						Timestamp:  time.Now(),
						Details:    fmt.Sprintf("reconnected after %d attempt(s) (reason: %s)", attempt, reason),
					})
					return nil
				}
			}
		}

		// Wait with exponential backoff before next attempt
		if attempt < maxRetries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > reconnectMaxBackoff {
				backoff = reconnectMaxBackoff
			}
		}
	}

	// All retries exhausted — callers can register an EventListener for
	// EventReconnectFailed to mark the instance offline in the database.
	log.Printf("SSH reconnection to instance %d failed after %d attempts: %v", instanceID, maxRetries, lastErr)
	m.stateTracker.setState(instanceID, StateFailed, fmt.Sprintf("gave up after %d attempts: %v", maxRetries, lastErr))
	m.emitEvent(ConnectionEvent{
		InstanceID: instanceID,
		Type:       EventReconnectFailed,
		Timestamp:  time.Now(),
		Details:    fmt.Sprintf("gave up after %d attempts: %v", maxRetries, lastErr),
	})

	return fmt.Errorf("reconnect to instance %d failed after %d attempts: %w", instanceID, maxRetries, lastErr)
}

// triggerReconnect starts an asynchronous reconnection for an instance.
// It returns immediately. Only one reconnection runs per instance at a time;
// duplicate calls for the same instance are silently dropped.
func (m *SSHManager) triggerReconnect(instanceID uint, reason string) {
	m.reconnMu.Lock()
	if m.orch == nil {
		m.reconnMu.Unlock()
		return
	}
	if _, inProgress := m.reconnecting[instanceID]; inProgress {
		m.reconnMu.Unlock()
		log.Printf("SSH reconnection already in progress for instance %d, skipping", instanceID)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.reconnecting[instanceID] = cancel
	orch := m.orch
	m.reconnMu.Unlock()

	go func() {
		defer func() {
			m.reconnMu.Lock()
			delete(m.reconnecting, instanceID)
			m.reconnMu.Unlock()
		}()

		if err := m.reconnectWithBackoff(ctx, instanceID, reconnectDefaultRetries, orch, reason); err != nil {
			log.Printf("SSH async reconnection failed for instance %d: %v", instanceID, err)
		}
	}()
}

// cancelAllReconnections cancels all in-progress reconnection goroutines.
func (m *SSHManager) cancelAllReconnections() {
	m.reconnMu.Lock()
	defer m.reconnMu.Unlock()

	for id, cancel := range m.reconnecting {
		cancel()
		delete(m.reconnecting, id)
	}
}
