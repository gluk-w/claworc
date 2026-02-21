package sshproxy

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestConnectionState_String(t *testing.T) {
	tests := []struct {
		state ConnectionState
		want  string
	}{
		{StateDisconnected, "disconnected"},
		{StateConnecting, "connecting"},
		{StateConnected, "connected"},
		{StateReconnecting, "reconnecting"},
		{StateFailed, "failed"},
		{ConnectionState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("ConnectionState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestStateTracker_SetAndGet(t *testing.T) {
	st := newStateTracker()

	// Default state for unknown instance
	if got := st.getState(1); got != StateDisconnected {
		t.Errorf("getState(1) = %v, want StateDisconnected", got)
	}

	// Set and get
	st.setState(1, StateConnecting, "test")
	if got := st.getState(1); got != StateConnecting {
		t.Errorf("getState(1) = %v, want StateConnecting", got)
	}

	st.setState(1, StateConnected, "connected")
	if got := st.getState(1); got != StateConnected {
		t.Errorf("getState(1) = %v, want StateConnected", got)
	}
}

func TestStateTracker_NoOpOnSameState(t *testing.T) {
	st := newStateTracker()

	st.setState(1, StateConnected, "first")
	st.setState(1, StateConnected, "duplicate") // should be no-op

	transitions := st.getTransitions(1)
	if len(transitions) != 1 {
		t.Errorf("expected 1 transition, got %d", len(transitions))
	}
}

func TestStateTracker_TransitionHistory(t *testing.T) {
	st := newStateTracker()

	st.setState(1, StateConnecting, "connecting")
	st.setState(1, StateConnected, "connected")
	st.setState(1, StateDisconnected, "keepalive failed")
	st.setState(1, StateReconnecting, "auto-reconnect")
	st.setState(1, StateConnected, "reconnected")

	transitions := st.getTransitions(1)
	if len(transitions) != 5 {
		t.Fatalf("expected 5 transitions, got %d", len(transitions))
	}

	// Verify chronological order
	expected := []struct {
		from, to ConnectionState
	}{
		{StateDisconnected, StateConnecting},
		{StateConnecting, StateConnected},
		{StateConnected, StateDisconnected},
		{StateDisconnected, StateReconnecting},
		{StateReconnecting, StateConnected},
	}
	for i, e := range expected {
		if transitions[i].From != e.from || transitions[i].To != e.to {
			t.Errorf("transition[%d] = %v→%v, want %v→%v",
				i, transitions[i].From, transitions[i].To, e.from, e.to)
		}
		if transitions[i].Timestamp.IsZero() {
			t.Errorf("transition[%d].Timestamp is zero", i)
		}
	}

	// Verify timestamps are in order
	for i := 1; i < len(transitions); i++ {
		if transitions[i].Timestamp.Before(transitions[i-1].Timestamp) {
			t.Errorf("transition[%d] timestamp before transition[%d]", i, i-1)
		}
	}
}

func TestStateTracker_RingBuffer_Wraps(t *testing.T) {
	st := newStateTracker()

	// Write more than buffer size transitions (buffer = 50)
	// Toggle between two states to ensure each set is a state change
	for i := 0; i < 60; i++ {
		if i%2 == 0 {
			st.setState(1, StateConnected, fmt.Sprintf("transition-%d", i))
		} else {
			st.setState(1, StateDisconnected, fmt.Sprintf("transition-%d", i))
		}
	}

	transitions := st.getTransitions(1)
	if len(transitions) != stateTransitionBufferSize {
		t.Fatalf("expected %d transitions, got %d", stateTransitionBufferSize, len(transitions))
	}

	// The oldest retained transition should be transition-10 (60-50=10)
	if transitions[0].Reason != "transition-10" {
		t.Errorf("oldest transition = %q, want %q", transitions[0].Reason, "transition-10")
	}

	// The newest transition should be transition-59
	if transitions[len(transitions)-1].Reason != "transition-59" {
		t.Errorf("newest transition = %q, want %q", transitions[len(transitions)-1].Reason, "transition-59")
	}

	// Verify chronological order
	for i := 1; i < len(transitions); i++ {
		if transitions[i].Timestamp.Before(transitions[i-1].Timestamp) {
			t.Errorf("transition[%d] timestamp before transition[%d]", i, i-1)
		}
	}
}

func TestStateTracker_Callbacks(t *testing.T) {
	st := newStateTracker()

	var mu sync.Mutex
	var calls []struct {
		id   uint
		from ConnectionState
		to   ConnectionState
	}

	st.onStateChange(func(instanceID uint, from, to ConnectionState) {
		mu.Lock()
		calls = append(calls, struct {
			id   uint
			from ConnectionState
			to   ConnectionState
		}{instanceID, from, to})
		mu.Unlock()
	})

	st.setState(1, StateConnecting, "test")
	st.setState(1, StateConnected, "test")
	st.setState(2, StateConnecting, "test2")

	mu.Lock()
	defer mu.Unlock()

	if len(calls) != 3 {
		t.Fatalf("expected 3 callback invocations, got %d", len(calls))
	}

	if calls[0].id != 1 || calls[0].from != StateDisconnected || calls[0].to != StateConnecting {
		t.Errorf("call[0] = %+v, want {1, Disconnected, Connecting}", calls[0])
	}
	if calls[1].id != 1 || calls[1].from != StateConnecting || calls[1].to != StateConnected {
		t.Errorf("call[1] = %+v, want {1, Connecting, Connected}", calls[1])
	}
	if calls[2].id != 2 || calls[2].from != StateDisconnected || calls[2].to != StateConnecting {
		t.Errorf("call[2] = %+v, want {2, Disconnected, Connecting}", calls[2])
	}
}

func TestStateTracker_CallbackNotFiredOnSameState(t *testing.T) {
	st := newStateTracker()

	callCount := 0
	st.onStateChange(func(_ uint, _, _ ConnectionState) {
		callCount++
	})

	st.setState(1, StateConnected, "first")
	st.setState(1, StateConnected, "same") // no-op

	if callCount != 1 {
		t.Errorf("callback count = %d, want 1 (no-op for same state)", callCount)
	}
}

func TestStateTracker_MultipleCallbacks(t *testing.T) {
	st := newStateTracker()

	count1 := 0
	count2 := 0

	st.onStateChange(func(_ uint, _, _ ConnectionState) { count1++ })
	st.onStateChange(func(_ uint, _, _ ConnectionState) { count2++ })

	st.setState(1, StateConnected, "test")

	if count1 != 1 || count2 != 1 {
		t.Errorf("callback counts = (%d, %d), want (1, 1)", count1, count2)
	}
}

func TestStateTracker_Remove(t *testing.T) {
	st := newStateTracker()

	st.setState(1, StateConnected, "test")
	st.remove(1)

	if got := st.getState(1); got != StateDisconnected {
		t.Errorf("getState after remove = %v, want StateDisconnected", got)
	}
	if transitions := st.getTransitions(1); len(transitions) != 0 {
		t.Errorf("transitions after remove = %d, want 0", len(transitions))
	}
}

func TestStateTracker_ConcurrentAccess(t *testing.T) {
	st := newStateTracker()

	var wg sync.WaitGroup
	const goroutines = 20
	const iterations = 100

	// Register a callback to exercise the callback path under concurrency
	var mu sync.Mutex
	callCount := 0
	st.onStateChange(func(_ uint, _, _ ConnectionState) {
		mu.Lock()
		callCount++
		mu.Unlock()
	})

	// Concurrent writers (different instances)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id uint) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if i%2 == 0 {
					st.setState(id, StateConnected, "connected")
				} else {
					st.setState(id, StateDisconnected, "disconnected")
				}
			}
		}(uint(g))
	}

	// Concurrent readers
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id uint) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = st.getState(id)
				_ = st.getTransitions(id)
			}
		}(uint(g))
	}

	wg.Wait()

	// Verify no panics occurred and all goroutines completed.
	// Every instance should end in a known state.
	for g := 0; g < goroutines; g++ {
		state := st.getState(uint(g))
		if state != StateConnected && state != StateDisconnected {
			t.Errorf("instance %d ended in unexpected state: %v", g, state)
		}
	}
}

func TestStateTracker_TransitionsForUnknownInstance(t *testing.T) {
	st := newStateTracker()

	transitions := st.getTransitions(999)
	if transitions != nil {
		t.Errorf("expected nil transitions for unknown instance, got %v", transitions)
	}
}

func TestStateTracker_TransitionReasonAndTimestamp(t *testing.T) {
	st := newStateTracker()

	before := time.Now()
	st.setState(1, StateConnecting, "my-reason")
	after := time.Now()

	transitions := st.getTransitions(1)
	if len(transitions) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(transitions))
	}

	tr := transitions[0]
	if tr.Reason != "my-reason" {
		t.Errorf("reason = %q, want %q", tr.Reason, "my-reason")
	}
	if tr.Timestamp.Before(before) || tr.Timestamp.After(after) {
		t.Errorf("timestamp %v not in range [%v, %v]", tr.Timestamp, before, after)
	}
}

func TestStateTracker_MultipleInstances(t *testing.T) {
	st := newStateTracker()

	st.setState(1, StateConnected, "inst1")
	st.setState(2, StateReconnecting, "inst2")
	st.setState(3, StateFailed, "inst3")

	if got := st.getState(1); got != StateConnected {
		t.Errorf("instance 1 state = %v, want Connected", got)
	}
	if got := st.getState(2); got != StateReconnecting {
		t.Errorf("instance 2 state = %v, want Reconnecting", got)
	}
	if got := st.getState(3); got != StateFailed {
		t.Errorf("instance 3 state = %v, want Failed", got)
	}
}

// Tests for SSHManager integration

func TestSSHManager_GetConnectionState_Default(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	if got := mgr.GetConnectionState(1); got != StateDisconnected {
		t.Errorf("GetConnectionState = %v, want StateDisconnected", got)
	}
}

func TestSSHManager_SetConnectionState(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	mgr.SetConnectionState(1, StateConnected, "test")
	if got := mgr.GetConnectionState(1); got != StateConnected {
		t.Errorf("GetConnectionState = %v, want StateConnected", got)
	}
}

func TestSSHManager_OnStateChange(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	var called bool
	mgr.OnStateChange(func(id uint, from, to ConnectionState) {
		called = true
		if id != 1 {
			t.Errorf("callback id = %d, want 1", id)
		}
		if from != StateDisconnected {
			t.Errorf("callback from = %v, want Disconnected", from)
		}
		if to != StateConnected {
			t.Errorf("callback to = %v, want Connected", to)
		}
	})

	mgr.SetConnectionState(1, StateConnected, "test")
	if !called {
		t.Error("state change callback was not invoked")
	}
}

func TestSSHManager_GetStateTransitions(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	mgr.SetConnectionState(1, StateConnecting, "connecting")
	mgr.SetConnectionState(1, StateConnected, "connected")
	mgr.SetConnectionState(1, StateDisconnected, "closed")

	transitions := mgr.GetStateTransitions(1)
	if len(transitions) != 3 {
		t.Fatalf("expected 3 transitions, got %d", len(transitions))
	}
}

func TestSSHManager_Connect_SetsState(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)

	_, err := mgr.Connect(t.Context(), 1, host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	if got := mgr.GetConnectionState(1); got != StateConnected {
		t.Errorf("state after Connect = %v, want StateConnected", got)
	}

	// Verify transition history includes Connecting → Connected
	transitions := mgr.GetStateTransitions(1)
	if len(transitions) < 2 {
		t.Fatalf("expected at least 2 transitions, got %d", len(transitions))
	}

	// First transition: Disconnected → Connecting
	if transitions[0].From != StateDisconnected || transitions[0].To != StateConnecting {
		t.Errorf("transition[0] = %v→%v, want Disconnected→Connecting", transitions[0].From, transitions[0].To)
	}

	// Last transition should end at Connected
	last := transitions[len(transitions)-1]
	if last.To != StateConnected {
		t.Errorf("last transition.To = %v, want Connected", last.To)
	}
}

func TestSSHManager_Close_SetsState(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)

	_, err := mgr.Connect(t.Context(), 1, host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	mgr.Close(1)

	if got := mgr.GetConnectionState(1); got != StateDisconnected {
		t.Errorf("state after Close = %v, want StateDisconnected", got)
	}
}

func TestSSHManager_Reconnect_SetsState(t *testing.T) {
	saved := reconnectInitialBackoff
	reconnectInitialBackoff = 10 * time.Millisecond
	defer func() { reconnectInitialBackoff = saved }()

	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	host, port := parseHostPort(t, ts.addr)
	orch := &mockOrch{host: host, port: port}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	err := mgr.reconnectWithBackoff(t.Context(), 1, 3, orch, "test")
	if err != nil {
		t.Fatalf("reconnectWithBackoff() error: %v", err)
	}

	// After successful reconnection, state should be Connected
	if got := mgr.GetConnectionState(1); got != StateConnected {
		t.Errorf("state after reconnect = %v, want StateConnected", got)
	}

	// Verify Reconnecting state was in the transition history
	transitions := mgr.GetStateTransitions(1)
	foundReconnecting := false
	for _, tr := range transitions {
		if tr.To == StateReconnecting {
			foundReconnecting = true
			break
		}
	}
	if !foundReconnecting {
		t.Error("expected StateReconnecting in transition history")
	}
}

func TestSSHManager_ReconnectFailed_SetsStateFailed(t *testing.T) {
	saved := reconnectInitialBackoff
	reconnectInitialBackoff = 10 * time.Millisecond
	defer func() { reconnectInitialBackoff = saved }()

	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	orch := &mockOrch{configureErr: fmt.Errorf("always fails")}
	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	_ = mgr.reconnectWithBackoff(t.Context(), 1, 2, orch, "test-fail")

	// After all retries exhausted, state should be Failed
	if got := mgr.GetConnectionState(1); got != StateFailed {
		t.Errorf("state after failed reconnect = %v, want StateFailed", got)
	}
}
