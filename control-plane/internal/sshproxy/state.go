// state.go implements connection state tracking for the sshproxy package.
//
// Each SSH connection has a ConnectionState (Disconnected, Connecting, Connected,
// Reconnecting, Failed) that is updated automatically by the SSHManager lifecycle
// methods and can also be set manually. State transitions are recorded in a
// per-instance ring buffer (50 entries) for debugging, and registered callbacks
// are invoked on every state change for UI updates or alerting.

package sshproxy

import (
	"sync"
	"time"
)

// ConnectionState represents the current state of an SSH connection.
type ConnectionState int

const (
	StateDisconnected ConnectionState = iota
	StateConnecting
	StateConnected
	StateReconnecting
	StateFailed
)

// String returns the human-readable name of the connection state.
func (s ConnectionState) String() string {
	switch s {
	case StateDisconnected:
		return "disconnected"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateReconnecting:
		return "reconnecting"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// stateTransitionBufferSize is the maximum number of state transitions stored
// per instance for debugging.
const stateTransitionBufferSize = 50

// StateTransition records a single state change for debugging.
type StateTransition struct {
	From      ConnectionState `json:"from"`
	To        ConnectionState `json:"to"`
	Timestamp time.Time       `json:"timestamp"`
	Reason    string          `json:"reason"`
}

// StateChangeCallback is called when a connection state changes.
// Callbacks are invoked synchronously — long-running handlers should spawn goroutines.
type StateChangeCallback func(instanceID uint, from, to ConnectionState)

// stateEntry tracks the current state and transition history for one instance.
type stateEntry struct {
	current     ConnectionState
	transitions [stateTransitionBufferSize]StateTransition // fixed-size ring buffer
	head        int                                        // next write position
	count       int                                        // total entries written (capped at buffer size for reads)
}

// record adds a state transition to the ring buffer.
func (e *stateEntry) record(from, to ConnectionState, reason string) {
	e.transitions[e.head] = StateTransition{
		From:      from,
		To:        to,
		Timestamp: time.Now(),
		Reason:    reason,
	}
	e.head = (e.head + 1) % stateTransitionBufferSize
	if e.count < stateTransitionBufferSize {
		e.count++
	}
}

// history returns the state transitions in chronological order.
func (e *stateEntry) history() []StateTransition {
	if e.count == 0 {
		return nil
	}

	result := make([]StateTransition, e.count)
	if e.count < stateTransitionBufferSize {
		// Buffer not yet full — entries start at index 0.
		copy(result, e.transitions[:e.count])
	} else {
		// Buffer is full — head is the oldest entry.
		n := copy(result, e.transitions[e.head:])
		copy(result[n:], e.transitions[:e.head])
	}
	return result
}

// stateTracker manages per-instance connection state, transition history,
// and state change callbacks. It is embedded in SSHManager.
type stateTracker struct {
	mu        sync.RWMutex
	states    map[uint]*stateEntry
	callbacks []StateChangeCallback
}

// newStateTracker creates an initialized stateTracker.
func newStateTracker() *stateTracker {
	return &stateTracker{
		states: make(map[uint]*stateEntry),
	}
}

// getOrCreate returns the state entry for an instance, creating it if needed.
// Caller must hold st.mu (write lock).
func (st *stateTracker) getOrCreate(instanceID uint) *stateEntry {
	entry, ok := st.states[instanceID]
	if !ok {
		entry = &stateEntry{current: StateDisconnected}
		st.states[instanceID] = entry
	}
	return entry
}

// setState updates the connection state for an instance, records the transition,
// and invokes callbacks. If the state is unchanged, this is a no-op.
func (st *stateTracker) setState(instanceID uint, state ConnectionState, reason string) {
	st.mu.Lock()
	entry := st.getOrCreate(instanceID)
	from := entry.current
	if from == state {
		st.mu.Unlock()
		return
	}
	entry.current = state
	entry.record(from, state, reason)

	// Copy callbacks under lock, invoke outside lock
	cbs := make([]StateChangeCallback, len(st.callbacks))
	copy(cbs, st.callbacks)
	st.mu.Unlock()

	for _, cb := range cbs {
		cb(instanceID, from, state)
	}
}

// getState returns the current connection state for an instance.
// Returns StateDisconnected if the instance has no tracked state.
func (st *stateTracker) getState(instanceID uint) ConnectionState {
	st.mu.RLock()
	defer st.mu.RUnlock()
	entry, ok := st.states[instanceID]
	if !ok {
		return StateDisconnected
	}
	return entry.current
}

// getTransitions returns the state transition history for an instance
// in chronological order (oldest first).
func (st *stateTracker) getTransitions(instanceID uint) []StateTransition {
	st.mu.RLock()
	defer st.mu.RUnlock()
	entry, ok := st.states[instanceID]
	if !ok {
		return nil
	}
	return entry.history()
}

// onStateChange registers a callback for state changes.
func (st *stateTracker) onStateChange(cb StateChangeCallback) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.callbacks = append(st.callbacks, cb)
}

// remove deletes all state tracking for an instance.
func (st *stateTracker) remove(instanceID uint) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.states, instanceID)
}

// GetConnectionState returns the current connection state for an instance.
// Returns StateDisconnected if the instance has no tracked state.
func (m *SSHManager) GetConnectionState(instanceID uint) ConnectionState {
	return m.stateTracker.getState(instanceID)
}

// SetConnectionState updates the connection state for an instance.
// Triggers registered state change callbacks and records the transition.
func (m *SSHManager) SetConnectionState(instanceID uint, state ConnectionState, reason string) {
	m.stateTracker.setState(instanceID, state, reason)
}

// GetStateTransitions returns the recent state transition history for an instance,
// in chronological order (oldest first). Up to 50 transitions are retained.
func (m *SSHManager) GetStateTransitions(instanceID uint) []StateTransition {
	return m.stateTracker.getTransitions(instanceID)
}

// OnStateChange registers a callback that is invoked on every connection state
// change. Callbacks are called synchronously — long-running handlers should
// spawn goroutines.
func (m *SSHManager) OnStateChange(cb StateChangeCallback) {
	m.stateTracker.onStateChange(cb)
}
