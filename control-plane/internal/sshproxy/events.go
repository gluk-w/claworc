// events.go implements connection event logging for the sshproxy package.
//
// It stores ConnectionEvents emitted by the SSHManager lifecycle (connect,
// disconnect, health check failure, reconnection, key upload) in a per-instance
// ring buffer (100 entries) for later retrieval. This complements the
// state transition history in state.go, which tracks state changes, while
// events.go tracks individual actions and their outcomes.
//
// Events are automatically logged whenever emitEvent is called, so all
// existing event emission points (reconnect.go, health.go, manager.go)
// are covered without modification.

package sshproxy

import (
	"sync"
	"time"
)

const (
	// eventBufferSize is the maximum number of events stored per instance.
	eventBufferSize = 100
)

// EventHealthCheckFailed is emitted when a health check fails for an instance.
const EventHealthCheckFailed ConnectionEventType = "health_check_failed"

// eventBuffer is a fixed-size ring buffer of ConnectionEvents for one instance.
type eventBuffer struct {
	events [eventBufferSize]ConnectionEvent
	head   int // next write position
	count  int // total entries written (capped at buffer size for reads)
}

// record adds an event to the ring buffer.
func (b *eventBuffer) record(event ConnectionEvent) {
	b.events[b.head] = event
	b.head = (b.head + 1) % eventBufferSize
	if b.count < eventBufferSize {
		b.count++
	}
}

// history returns events in chronological order (oldest first).
func (b *eventBuffer) history() []ConnectionEvent {
	if b.count == 0 {
		return nil
	}

	result := make([]ConnectionEvent, b.count)
	if b.count < eventBufferSize {
		copy(result, b.events[:b.count])
	} else {
		// Buffer is full — head is the oldest entry.
		n := copy(result, b.events[b.head:])
		copy(result[n:], b.events[:b.head])
	}
	return result
}

// eventLog manages per-instance event ring buffers.
type eventLog struct {
	mu      sync.RWMutex
	buffers map[uint]*eventBuffer
}

// newEventLog creates an initialized eventLog.
func newEventLog() *eventLog {
	return &eventLog{
		buffers: make(map[uint]*eventBuffer),
	}
}

// logEvent records a connection event for the given instance.
func (el *eventLog) logEvent(instanceID uint, eventType ConnectionEventType, details string) {
	el.mu.Lock()
	defer el.mu.Unlock()

	buf, ok := el.buffers[instanceID]
	if !ok {
		buf = &eventBuffer{}
		el.buffers[instanceID] = buf
	}

	buf.record(ConnectionEvent{
		InstanceID: instanceID,
		Type:       eventType,
		Timestamp:  time.Now(),
		Details:    details,
	})
}

// recordEvent stores a pre-built ConnectionEvent in the ring buffer.
func (el *eventLog) recordEvent(event ConnectionEvent) {
	el.mu.Lock()
	defer el.mu.Unlock()

	buf, ok := el.buffers[event.InstanceID]
	if !ok {
		buf = &eventBuffer{}
		el.buffers[event.InstanceID] = buf
	}

	buf.record(event)
}

// getEvents returns the event history for an instance in chronological order.
// Returns nil if no events exist for the instance.
func (el *eventLog) getEvents(instanceID uint) []ConnectionEvent {
	el.mu.RLock()
	defer el.mu.RUnlock()

	buf, ok := el.buffers[instanceID]
	if !ok {
		return nil
	}
	return buf.history()
}

// getAllEvents returns event histories for all tracked instances.
func (el *eventLog) getAllEvents() map[uint][]ConnectionEvent {
	el.mu.RLock()
	defer el.mu.RUnlock()

	result := make(map[uint][]ConnectionEvent, len(el.buffers))
	for id, buf := range el.buffers {
		if events := buf.history(); events != nil {
			result[id] = events
		}
	}
	return result
}

// remove deletes all event history for an instance.
func (el *eventLog) remove(instanceID uint) {
	el.mu.Lock()
	defer el.mu.Unlock()
	delete(el.buffers, instanceID)
}

// LogEvent records a connection event for the given instance.
// This is the public API for manually logging events.
func (m *SSHManager) LogEvent(instanceID uint, eventType ConnectionEventType, details string) {
	m.eventLog.logEvent(instanceID, eventType, details)
}

// GetEventHistory returns the connection event history for an instance,
// in chronological order (oldest first). Up to 100 events are retained.
func (m *SSHManager) GetEventHistory(instanceID uint) []ConnectionEvent {
	return m.eventLog.getEvents(instanceID)
}

// GetAllEventHistory returns connection event histories for all tracked instances.
func (m *SSHManager) GetAllEventHistory() map[uint][]ConnectionEvent {
	return m.eventLog.getAllEvents()
}
