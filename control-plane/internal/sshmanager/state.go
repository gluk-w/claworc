package sshmanager

import (
	"sync"
	"time"
)

// ConnectionState represents the current state of an SSH connection.
type ConnectionState string

const (
	StateDisconnected ConnectionState = "disconnected"
	StateConnecting   ConnectionState = "connecting"
	StateConnected    ConnectionState = "connected"
	StateReconnecting ConnectionState = "reconnecting"
	StateFailed       ConnectionState = "failed"
)

// String returns the string representation of a ConnectionState.
func (s ConnectionState) String() string {
	return string(s)
}

// IsValid returns true if the state is one of the defined constants.
func (s ConnectionState) IsValid() bool {
	switch s {
	case StateDisconnected, StateConnecting, StateConnected, StateReconnecting, StateFailed:
		return true
	default:
		return false
	}
}

// StateTransition records a state change for debugging.
type StateTransition struct {
	From      ConnectionState `json:"from"`
	To        ConnectionState `json:"to"`
	Timestamp time.Time       `json:"timestamp"`
}

// StateCallback is called when a connection's state changes.
// The callback receives the instance name, old state, and new state.
type StateCallback func(instanceName string, from, to ConnectionState)

// maxTransitionsPerInstance limits the number of stored state transitions per instance.
const maxTransitionsPerInstance = 50

// ConnectionStateTracker manages connection states, transition history, and callbacks.
type ConnectionStateTracker struct {
	mu          sync.RWMutex
	states      map[string]ConnectionState
	transitions map[string][]StateTransition
	callbacks   []StateCallback
}

// NewConnectionStateTracker creates a new state tracker.
func NewConnectionStateTracker() *ConnectionStateTracker {
	return &ConnectionStateTracker{
		states:      make(map[string]ConnectionState),
		transitions: make(map[string][]StateTransition),
	}
}

// GetState returns the current connection state for the instance.
// Returns StateDisconnected if no state has been set.
func (t *ConnectionStateTracker) GetState(instanceName string) ConnectionState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	state, ok := t.states[instanceName]
	if !ok {
		return StateDisconnected
	}
	return state
}

// SetState updates the connection state for the instance. If the state
// actually changed, it records the transition and fires registered callbacks.
// Returns the previous state.
func (t *ConnectionStateTracker) SetState(instanceName string, newState ConnectionState) ConnectionState {
	t.mu.Lock()
	oldState, ok := t.states[instanceName]
	if !ok {
		oldState = StateDisconnected
	}

	if oldState == newState {
		t.mu.Unlock()
		return oldState
	}

	t.states[instanceName] = newState

	// Record the transition
	trans := StateTransition{
		From:      oldState,
		To:        newState,
		Timestamp: time.Now(),
	}
	transitions := t.transitions[instanceName]
	transitions = append(transitions, trans)
	if len(transitions) > maxTransitionsPerInstance {
		transitions = transitions[len(transitions)-maxTransitionsPerInstance:]
	}
	t.transitions[instanceName] = transitions

	// Copy callbacks under lock to fire outside lock
	cbs := make([]StateCallback, len(t.callbacks))
	copy(cbs, t.callbacks)
	t.mu.Unlock()

	// Fire callbacks outside the lock to avoid deadlocks
	for _, cb := range cbs {
		cb(instanceName, oldState, newState)
	}

	return oldState
}

// RemoveState removes the state for the given instance.
// Does not clear transition history so it remains available for debugging.
func (t *ConnectionStateTracker) RemoveState(instanceName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.states, instanceName)
}

// ClearInstance removes both state and transition history for an instance.
func (t *ConnectionStateTracker) ClearInstance(instanceName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.states, instanceName)
	delete(t.transitions, instanceName)
}

// ClearAll removes all states and transition history.
func (t *ConnectionStateTracker) ClearAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.states = make(map[string]ConnectionState)
	t.transitions = make(map[string][]StateTransition)
}

// GetTransitions returns a copy of the state transition history for the instance.
func (t *ConnectionStateTracker) GetTransitions(instanceName string) []StateTransition {
	t.mu.RLock()
	defer t.mu.RUnlock()
	transitions := t.transitions[instanceName]
	result := make([]StateTransition, len(transitions))
	copy(result, transitions)
	return result
}

// GetRecentTransitions returns the most recent n transitions for the instance.
func (t *ConnectionStateTracker) GetRecentTransitions(instanceName string, n int) []StateTransition {
	t.mu.RLock()
	defer t.mu.RUnlock()
	transitions := t.transitions[instanceName]
	if len(transitions) <= n {
		result := make([]StateTransition, len(transitions))
		copy(result, transitions)
		return result
	}
	result := make([]StateTransition, n)
	copy(result, transitions[len(transitions)-n:])
	return result
}

// GetAllStates returns a copy of all current connection states.
func (t *ConnectionStateTracker) GetAllStates() map[string]ConnectionState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make(map[string]ConnectionState, len(t.states))
	for k, v := range t.states {
		result[k] = v
	}
	return result
}

// OnStateChange registers a callback that fires when any connection's state changes.
func (t *ConnectionStateTracker) OnStateChange(cb StateCallback) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callbacks = append(t.callbacks, cb)
}
