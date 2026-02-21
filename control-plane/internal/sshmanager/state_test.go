package sshmanager

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- ConnectionState type tests ---

func TestConnectionStateString(t *testing.T) {
	tests := []struct {
		state    ConnectionState
		expected string
	}{
		{StateDisconnected, "disconnected"},
		{StateConnecting, "connecting"},
		{StateConnected, "connected"},
		{StateReconnecting, "reconnecting"},
		{StateFailed, "failed"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.expected {
			t.Errorf("ConnectionState(%q).String() = %q, want %q", tt.state, got, tt.expected)
		}
	}
}

func TestConnectionStateIsValid(t *testing.T) {
	validStates := []ConnectionState{
		StateDisconnected, StateConnecting, StateConnected, StateReconnecting, StateFailed,
	}
	for _, s := range validStates {
		if !s.IsValid() {
			t.Errorf("state %q should be valid", s)
		}
	}

	invalidStates := []ConnectionState{"", "unknown", "pending", "CONNECTED"}
	for _, s := range invalidStates {
		if s.IsValid() {
			t.Errorf("state %q should be invalid", s)
		}
	}
}

// --- ConnectionStateTracker unit tests ---

func TestNewConnectionStateTracker(t *testing.T) {
	tracker := NewConnectionStateTracker()
	if tracker == nil {
		t.Fatal("NewConnectionStateTracker returned nil")
	}
	if tracker.states == nil {
		t.Error("states map not initialized")
	}
	if tracker.transitions == nil {
		t.Error("transitions map not initialized")
	}
}

func TestGetStateDefaultDisconnected(t *testing.T) {
	tracker := NewConnectionStateTracker()
	state := tracker.GetState("unknown-instance")
	if state != StateDisconnected {
		t.Errorf("expected StateDisconnected for unknown instance, got %q", state)
	}
}

func TestSetAndGetState(t *testing.T) {
	tracker := NewConnectionStateTracker()

	old := tracker.SetState("instance-a", StateConnecting)
	if old != StateDisconnected {
		t.Errorf("expected previous state Disconnected, got %q", old)
	}

	got := tracker.GetState("instance-a")
	if got != StateConnecting {
		t.Errorf("expected StateConnecting, got %q", got)
	}

	old = tracker.SetState("instance-a", StateConnected)
	if old != StateConnecting {
		t.Errorf("expected previous state Connecting, got %q", old)
	}

	got = tracker.GetState("instance-a")
	if got != StateConnected {
		t.Errorf("expected StateConnected, got %q", got)
	}
}

func TestSetStateSameStateNoOp(t *testing.T) {
	tracker := NewConnectionStateTracker()

	tracker.SetState("instance-a", StateConnected)
	// Setting same state should be a no-op (no new transition)
	old := tracker.SetState("instance-a", StateConnected)
	if old != StateConnected {
		t.Errorf("expected StateConnected returned, got %q", old)
	}

	transitions := tracker.GetTransitions("instance-a")
	// Should only have one transition: Disconnected -> Connected
	if len(transitions) != 1 {
		t.Errorf("expected 1 transition (no-op on same state), got %d", len(transitions))
	}
}

func TestRemoveState(t *testing.T) {
	tracker := NewConnectionStateTracker()

	tracker.SetState("instance-a", StateConnected)
	tracker.RemoveState("instance-a")

	// After removal, should return default
	got := tracker.GetState("instance-a")
	if got != StateDisconnected {
		t.Errorf("expected StateDisconnected after removal, got %q", got)
	}

	// Transitions should still be there for debugging
	transitions := tracker.GetTransitions("instance-a")
	if len(transitions) == 0 {
		t.Error("transitions should be preserved after RemoveState")
	}
}

func TestClearInstance(t *testing.T) {
	tracker := NewConnectionStateTracker()

	tracker.SetState("instance-a", StateConnected)
	tracker.ClearInstance("instance-a")

	got := tracker.GetState("instance-a")
	if got != StateDisconnected {
		t.Errorf("expected StateDisconnected after clear, got %q", got)
	}

	transitions := tracker.GetTransitions("instance-a")
	if len(transitions) != 0 {
		t.Errorf("expected 0 transitions after ClearInstance, got %d", len(transitions))
	}
}

func TestClearAll(t *testing.T) {
	tracker := NewConnectionStateTracker()

	tracker.SetState("instance-a", StateConnected)
	tracker.SetState("instance-b", StateReconnecting)
	tracker.ClearAll()

	states := tracker.GetAllStates()
	if len(states) != 0 {
		t.Errorf("expected 0 states after ClearAll, got %d", len(states))
	}

	transA := tracker.GetTransitions("instance-a")
	transB := tracker.GetTransitions("instance-b")
	if len(transA) != 0 || len(transB) != 0 {
		t.Error("expected 0 transitions after ClearAll")
	}
}

func TestGetAllStates(t *testing.T) {
	tracker := NewConnectionStateTracker()

	tracker.SetState("a", StateConnected)
	tracker.SetState("b", StateReconnecting)
	tracker.SetState("c", StateFailed)

	states := tracker.GetAllStates()
	if len(states) != 3 {
		t.Fatalf("expected 3 states, got %d", len(states))
	}
	if states["a"] != StateConnected {
		t.Errorf("expected a=Connected, got %q", states["a"])
	}
	if states["b"] != StateReconnecting {
		t.Errorf("expected b=Reconnecting, got %q", states["b"])
	}
	if states["c"] != StateFailed {
		t.Errorf("expected c=Failed, got %q", states["c"])
	}

	// Verify it's a copy
	states["a"] = StateFailed
	if tracker.GetState("a") != StateConnected {
		t.Error("GetAllStates should return a copy")
	}
}

// --- Transition history tests ---

func TestTransitionsRecorded(t *testing.T) {
	tracker := NewConnectionStateTracker()

	tracker.SetState("instance-a", StateConnecting)
	tracker.SetState("instance-a", StateConnected)

	transitions := tracker.GetTransitions("instance-a")
	if len(transitions) != 2 {
		t.Fatalf("expected 2 transitions, got %d", len(transitions))
	}

	if transitions[0].From != StateDisconnected || transitions[0].To != StateConnecting {
		t.Errorf("transition 0: expected Disconnected->Connecting, got %s->%s", transitions[0].From, transitions[0].To)
	}
	if transitions[1].From != StateConnecting || transitions[1].To != StateConnected {
		t.Errorf("transition 1: expected Connecting->Connected, got %s->%s", transitions[1].From, transitions[1].To)
	}

	// Timestamps should be set
	if transitions[0].Timestamp.IsZero() {
		t.Error("transition timestamp should be set")
	}
}

func TestTransitionsRingBuffer(t *testing.T) {
	tracker := NewConnectionStateTracker()

	// Alternate states to generate more transitions than the buffer holds
	states := []ConnectionState{StateConnected, StateDisconnected}
	for i := 0; i < maxTransitionsPerInstance+20; i++ {
		tracker.SetState("instance-a", states[i%2])
	}

	transitions := tracker.GetTransitions("instance-a")
	if len(transitions) != maxTransitionsPerInstance {
		t.Errorf("expected %d transitions (ring buffer), got %d", maxTransitionsPerInstance, len(transitions))
	}
}

func TestGetRecentTransitions(t *testing.T) {
	tracker := NewConnectionStateTracker()

	tracker.SetState("instance-a", StateConnecting)
	tracker.SetState("instance-a", StateConnected)
	tracker.SetState("instance-a", StateDisconnected)
	tracker.SetState("instance-a", StateReconnecting)
	tracker.SetState("instance-a", StateConnected)

	recent := tracker.GetRecentTransitions("instance-a", 2)
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent transitions, got %d", len(recent))
	}

	// Should be the last 2 transitions
	if recent[0].To != StateReconnecting {
		t.Errorf("expected transition to Reconnecting, got to %q", recent[0].To)
	}
	if recent[1].To != StateConnected {
		t.Errorf("expected transition to Connected, got to %q", recent[1].To)
	}
}

func TestGetRecentTransitionsFewerThanN(t *testing.T) {
	tracker := NewConnectionStateTracker()

	tracker.SetState("instance-a", StateConnected)

	recent := tracker.GetRecentTransitions("instance-a", 10)
	if len(recent) != 1 {
		t.Errorf("expected 1 transition, got %d", len(recent))
	}
}

func TestGetTransitionsReturnsACopy(t *testing.T) {
	tracker := NewConnectionStateTracker()

	tracker.SetState("instance-a", StateConnected)

	trans1 := tracker.GetTransitions("instance-a")
	trans1[0].To = StateFailed // mutate

	trans2 := tracker.GetTransitions("instance-a")
	if trans2[0].To == StateFailed {
		t.Error("GetTransitions should return a copy")
	}
}

func TestGetTransitionsEmptyForUnknown(t *testing.T) {
	tracker := NewConnectionStateTracker()
	transitions := tracker.GetTransitions("nonexistent")
	if len(transitions) != 0 {
		t.Errorf("expected 0 transitions for unknown instance, got %d", len(transitions))
	}
}

// --- Callback tests ---

func TestCallbackFiredOnStateChange(t *testing.T) {
	tracker := NewConnectionStateTracker()

	var called atomic.Int32
	var lastInstance string
	var lastFrom, lastTo ConnectionState
	var mu sync.Mutex

	tracker.OnStateChange(func(instanceName string, from, to ConnectionState) {
		called.Add(1)
		mu.Lock()
		lastInstance = instanceName
		lastFrom = from
		lastTo = to
		mu.Unlock()
	})

	tracker.SetState("instance-a", StateConnected)

	if called.Load() != 1 {
		t.Errorf("expected callback called once, got %d", called.Load())
	}
	mu.Lock()
	if lastInstance != "instance-a" {
		t.Errorf("expected instance name instance-a, got %q", lastInstance)
	}
	if lastFrom != StateDisconnected || lastTo != StateConnected {
		t.Errorf("expected Disconnected->Connected, got %s->%s", lastFrom, lastTo)
	}
	mu.Unlock()
}

func TestCallbackNotFiredOnSameState(t *testing.T) {
	tracker := NewConnectionStateTracker()

	var called atomic.Int32
	tracker.OnStateChange(func(instanceName string, from, to ConnectionState) {
		called.Add(1)
	})

	tracker.SetState("instance-a", StateConnected)
	tracker.SetState("instance-a", StateConnected) // Same state, no-op

	if called.Load() != 1 {
		t.Errorf("callback should not fire on same state, called %d times", called.Load())
	}
}

func TestMultipleCallbacks(t *testing.T) {
	tracker := NewConnectionStateTracker()

	var count1, count2 atomic.Int32
	tracker.OnStateChange(func(instanceName string, from, to ConnectionState) {
		count1.Add(1)
	})
	tracker.OnStateChange(func(instanceName string, from, to ConnectionState) {
		count2.Add(1)
	})

	tracker.SetState("instance-a", StateConnected)

	if count1.Load() != 1 || count2.Load() != 1 {
		t.Errorf("expected both callbacks called once, got %d and %d", count1.Load(), count2.Load())
	}
}

// --- Concurrent access tests ---

func TestConcurrentStateAccess(t *testing.T) {
	tracker := NewConnectionStateTracker()

	var wg sync.WaitGroup
	states := []ConnectionState{StateDisconnected, StateConnecting, StateConnected, StateReconnecting, StateFailed}

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("instance-%d", i%5)
			state := states[i%len(states)]
			tracker.SetState(name, state)
			tracker.GetState(name)
			tracker.GetAllStates()
			tracker.GetTransitions(name)
		}(i)
	}
	wg.Wait()
}

func TestConcurrentCallbackRegistration(t *testing.T) {
	tracker := NewConnectionStateTracker()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tracker.OnStateChange(func(instanceName string, from, to ConnectionState) {})
		}()
	}
	wg.Wait()

	// Set a state to exercise all registered callbacks
	tracker.SetState("test", StateConnected)
}

// --- SSHManager integration tests ---

func TestSSHManagerStateOnConnect(t *testing.T) {
	addr, cleanup := startTestSSHServerWithExec(t)
	defer cleanup()

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	// Before connect, state should be disconnected
	if state := m.GetConnectionState("test-instance"); state != StateDisconnected {
		t.Errorf("expected Disconnected before connect, got %q", state)
	}

	_, err := m.Connect(context.Background(), "test-instance", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// After connect, state should be connected
	if state := m.GetConnectionState("test-instance"); state != StateConnected {
		t.Errorf("expected Connected after connect, got %q", state)
	}

	// Should have transitions: Disconnected->Connecting, Connecting->Connected
	transitions := m.GetStateTransitions("test-instance")
	if len(transitions) != 2 {
		t.Fatalf("expected 2 transitions, got %d", len(transitions))
	}
	if transitions[0].From != StateDisconnected || transitions[0].To != StateConnecting {
		t.Errorf("transition 0: expected Disconnected->Connecting, got %s->%s", transitions[0].From, transitions[0].To)
	}
	if transitions[1].From != StateConnecting || transitions[1].To != StateConnected {
		t.Errorf("transition 1: expected Connecting->Connected, got %s->%s", transitions[1].From, transitions[1].To)
	}
}

func TestSSHManagerStateOnClose(t *testing.T) {
	addr, cleanup := startTestSSHServerWithExec(t)
	defer cleanup()

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	_, err := m.Connect(context.Background(), "test-instance", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	m.Close("test-instance")

	if state := m.GetConnectionState("test-instance"); state != StateDisconnected {
		t.Errorf("expected Disconnected after Close, got %q", state)
	}
}

func TestSSHManagerStateOnCloseAll(t *testing.T) {
	m := NewSSHManager(0)

	m.SetClient("a", nil)
	m.stateTracker.SetState("a", StateConnected)
	m.SetClient("b", nil)
	m.stateTracker.SetState("b", StateConnected)

	m.CloseAll()

	states := m.GetAllConnectionStates()
	if len(states) != 0 {
		t.Errorf("expected 0 states after CloseAll, got %d", len(states))
	}
}

func TestSSHManagerStateOnRemoveClient(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	m.SetClient("test-instance", nil)
	m.stateTracker.SetState("test-instance", StateConnected)

	m.RemoveClient("test-instance")

	if state := m.GetConnectionState("test-instance"); state != StateDisconnected {
		t.Errorf("expected Disconnected after RemoveClient, got %q", state)
	}

	// Transitions should also be cleared by ClearInstance
	transitions := m.GetStateTransitions("test-instance")
	if len(transitions) != 0 {
		t.Errorf("expected 0 transitions after RemoveClient, got %d", len(transitions))
	}
}

func TestSSHManagerStateConnectFailure(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	tmpDir := t.TempDir()
	keyPath := writeTestKey(t, tmpDir, "test")

	// Connect to an invalid address - should transition to Connecting then back to Disconnected
	_, err := m.Connect(context.Background(), "test-instance", "127.0.0.1", 1, keyPath)
	if err == nil {
		t.Fatal("expected error connecting to invalid address")
	}

	state := m.GetConnectionState("test-instance")
	if state != StateDisconnected {
		t.Errorf("expected Disconnected after failed connect, got %q", state)
	}

	// Should have transitions showing the failure
	transitions := m.GetStateTransitions("test-instance")
	if len(transitions) < 2 {
		t.Fatalf("expected at least 2 transitions, got %d", len(transitions))
	}
	// First: Disconnected -> Connecting
	if transitions[0].To != StateConnecting {
		t.Errorf("expected first transition to Connecting, got %q", transitions[0].To)
	}
	// Second: Connecting -> Disconnected (failure)
	if transitions[1].To != StateDisconnected {
		t.Errorf("expected second transition to Disconnected, got %q", transitions[1].To)
	}
}

func TestSSHManagerReconnectStateFailed(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	tmpDir := t.TempDir()
	keyPath := writeTestKey(t, tmpDir, "test")

	params := &ConnectionParams{Host: "127.0.0.1", Port: 1, PrivateKeyPath: keyPath}
	err := m.reconnectWithBackoff(context.Background(), "test-instance", params, 2)
	if err == nil {
		t.Fatal("expected reconnection to fail")
	}

	state := m.GetConnectionState("test-instance")
	if state != StateFailed {
		t.Errorf("expected Failed after exhausting retries, got %q", state)
	}
}

func TestSSHManagerStateCallbackIntegration(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	var transitions []struct {
		from, to ConnectionState
	}
	var mu sync.Mutex

	m.OnConnectionStateChange(func(instanceName string, from, to ConnectionState) {
		mu.Lock()
		transitions = append(transitions, struct{ from, to ConnectionState }{from, to})
		mu.Unlock()
	})

	m.SetConnectionState("test-instance", StateConnecting)
	m.SetConnectionState("test-instance", StateConnected)
	m.SetConnectionState("test-instance", StateDisconnected)

	mu.Lock()
	defer mu.Unlock()
	if len(transitions) != 3 {
		t.Fatalf("expected 3 state change callbacks, got %d", len(transitions))
	}
	if transitions[0].from != StateDisconnected || transitions[0].to != StateConnecting {
		t.Errorf("callback 0: expected Disconnected->Connecting")
	}
	if transitions[1].from != StateConnecting || transitions[1].to != StateConnected {
		t.Errorf("callback 1: expected Connecting->Connected")
	}
	if transitions[2].from != StateConnected || transitions[2].to != StateDisconnected {
		t.Errorf("callback 2: expected Connected->Disconnected")
	}
}

func TestSSHManagerCheckConnectionsUpdatesState(t *testing.T) {
	addr, tracker, cleanup := startTestSSHServerWithExecAndConns(t)

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	_, err := m.Connect(context.Background(), "test-instance", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if state := m.GetConnectionState("test-instance"); state != StateConnected {
		t.Fatalf("expected Connected, got %q", state)
	}

	// Kill the server
	cleanup()
	tracker.CloseAll()
	time.Sleep(100 * time.Millisecond)

	// Trigger health check which should detect dead connection
	m.checkConnections()

	// Give time for reconnect goroutine to start and set state
	time.Sleep(200 * time.Millisecond)

	state := m.GetConnectionState("test-instance")
	// State should be either Disconnected, Reconnecting, or Failed depending on timing
	if state != StateDisconnected && state != StateReconnecting && state != StateFailed {
		t.Errorf("expected Disconnected, Reconnecting, or Failed after dead connection, got %q", state)
	}
}

func TestSSHManagerReconnectSuccessRestoresConnectedState(t *testing.T) {
	addr, cleanup := startTestSSHServerWithExec(t)
	defer cleanup()

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	params := &ConnectionParams{Host: host, Port: port, PrivateKeyPath: keyPath}

	// Set initial state as if we were reconnecting
	m.stateTracker.SetState("test-instance", StateReconnecting)

	err := m.reconnectWithBackoff(context.Background(), "test-instance", params, 3)
	if err != nil {
		t.Fatalf("reconnectWithBackoff should succeed: %v", err)
	}

	// After successful reconnect, state should be Connected
	// (Connect method sets Connecting then Connected)
	state := m.GetConnectionState("test-instance")
	if state != StateConnected {
		t.Errorf("expected Connected after successful reconnect, got %q", state)
	}
}

func TestSSHManagerContextCancelledState(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	tmpDir := t.TempDir()
	keyPath := writeTestKey(t, tmpDir, "test")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.Connect(ctx, "test-instance", "127.0.0.1", 22, keyPath)
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}

	state := m.GetConnectionState("test-instance")
	if state != StateDisconnected {
		t.Errorf("expected Disconnected after cancelled context, got %q", state)
	}
}
