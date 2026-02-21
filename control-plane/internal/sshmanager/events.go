package sshmanager

import (
	"log"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/logutil"
)

// EventType identifies the type of connection event.
type EventType string

const (
	EventConnected        EventType = "connected"
	EventDisconnected     EventType = "disconnected"
	EventHealthCheckFailed EventType = "health_check_failed"
	EventReconnecting     EventType = "reconnecting"
	EventReconnectSuccess EventType = "reconnect_success"
	EventReconnectFailed  EventType = "reconnect_failed"
)

// ConnectionEvent represents a state change event for an SSH connection.
type ConnectionEvent struct {
	InstanceName string    `json:"instance_name"`
	Type         EventType `json:"type"`
	Details      string    `json:"details"`
	Timestamp    time.Time `json:"timestamp"`
}

// maxEventsPerInstance limits the number of stored events per instance.
const maxEventsPerInstance = 100

// LogEvent records a connection event for the given instance. This is the
// public API for event logging. Events are stored in a ring buffer (last 100
// per instance) and also written to the standard logger for observability.
func (m *SSHManager) LogEvent(instanceName string, eventType EventType, details string) {
	m.emitEvent(instanceName, eventType, details)
}

// emitEvent records a connection event and logs it.
func (m *SSHManager) emitEvent(instanceName string, eventType EventType, details string) {
	event := ConnectionEvent{
		InstanceName: instanceName,
		Type:         eventType,
		Details:      details,
		Timestamp:    time.Now(),
	}

	m.eventsMu.Lock()
	events := m.events[instanceName]
	events = append(events, event)
	if len(events) > maxEventsPerInstance {
		events = events[len(events)-maxEventsPerInstance:]
	}
	m.events[instanceName] = events
	m.eventsMu.Unlock()

	log.Printf("[ssh] event %s/%s: %s", logutil.SanitizeForLog(instanceName), eventType, details)
}

// GetEvents returns all stored connection events for the given instance.
func (m *SSHManager) GetEvents(instanceName string) []ConnectionEvent {
	m.eventsMu.RLock()
	defer m.eventsMu.RUnlock()
	events := m.events[instanceName]
	result := make([]ConnectionEvent, len(events))
	copy(result, events)
	return result
}

// GetRecentEvents returns the most recent n events for the given instance.
func (m *SSHManager) GetRecentEvents(instanceName string, n int) []ConnectionEvent {
	m.eventsMu.RLock()
	defer m.eventsMu.RUnlock()
	events := m.events[instanceName]
	if len(events) <= n {
		result := make([]ConnectionEvent, len(events))
		copy(result, events)
		return result
	}
	result := make([]ConnectionEvent, n)
	copy(result, events[len(events)-n:])
	return result
}

// ClearEvents removes all stored events for the given instance.
func (m *SSHManager) ClearEvents(instanceName string) {
	m.eventsMu.Lock()
	defer m.eventsMu.Unlock()
	delete(m.events, instanceName)
}

// GetEventCountsByType returns a count of events matching the given type for
// each instance. Only instances that have at least one matching event are
// included in the result.
func (m *SSHManager) GetEventCountsByType(eventType EventType) map[string]int {
	m.eventsMu.RLock()
	defer m.eventsMu.RUnlock()
	result := make(map[string]int)
	for name, events := range m.events {
		count := 0
		for _, e := range events {
			if e.Type == eventType {
				count++
			}
		}
		if count > 0 {
			result[name] = count
		}
	}
	return result
}
