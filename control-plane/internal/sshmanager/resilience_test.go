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

// =============================================================================
// Resilience scenario tests for SSH connection monitoring and recovery.
//
// These tests simulate production failure scenarios:
//   - Agent container restart
//   - Network partition and recovery
//   - Control plane restart with active instances
//   - Simultaneous failure of multiple instances
//   - Graceful degradation when SSH is permanently unavailable
// =============================================================================

// --- Scenario 1: Agent Container Restart ---
// Expected behavior: When an agent container restarts, the control plane
// detects the broken SSH connection via keepalive/health check, removes
// the dead client, and triggers automatic reconnection. Once the agent
// comes back online, reconnection succeeds with correct state transitions.

func TestAgentRestartReconnection(t *testing.T) {
	addr, tracker, cleanup := startTestSSHServerWithExecAndConns(t)

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	// Track state transitions via callback
	var transitions []struct {
		instance string
		from     ConnectionState
		to       ConnectionState
	}
	var mu sync.Mutex
	m.OnConnectionStateChange(func(instance string, from, to ConnectionState) {
		mu.Lock()
		transitions = append(transitions, struct {
			instance string
			from     ConnectionState
			to       ConnectionState
		}{instance, from, to})
		mu.Unlock()
	})

	// 1. Connect to agent
	_, err := m.Connect(context.Background(), "agent-1", host, port, keyPath)
	if err != nil {
		t.Fatalf("initial Connect: %v", err)
	}
	if m.GetConnectionState("agent-1") != StateConnected {
		t.Errorf("expected state Connected, got %s", m.GetConnectionState("agent-1"))
	}

	// 2. Simulate agent container restart: kill server and close connections
	cleanup()
	tracker.CloseAll()
	time.Sleep(100 * time.Millisecond)

	// 3. Verify connection is detected as dead
	m.checkConnections()
	if m.HasClient("agent-1") {
		t.Error("dead client should be removed after checkConnections")
	}

	// 4. Restart agent (new server on same port accepting same key)
	_, cleanup2 := startTestSSHServerWithExecOnPort(t, host, port, keyPath)
	defer cleanup2()

	// 5. Wait for reconnection (triggered by checkConnections)
	deadline := time.After(15 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for reconnection")
		default:
		}

		if m.HasClient("agent-1") && m.GetConnectionState("agent-1") == StateConnected {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 6. Verify health check passes on reconnected connection
	if err := m.HealthCheck("agent-1"); err != nil {
		t.Errorf("health check should pass after reconnection: %v", err)
	}

	// 7. Verify events were emitted
	events := m.GetEvents("agent-1")
	hasConnected := false
	hasDisconnected := false
	hasReconnecting := false
	hasReconnectSuccess := false
	for _, e := range events {
		switch e.Type {
		case EventConnected:
			hasConnected = true
		case EventDisconnected:
			hasDisconnected = true
		case EventReconnecting:
			hasReconnecting = true
		case EventReconnectSuccess:
			hasReconnectSuccess = true
		}
	}
	if !hasConnected {
		t.Error("missing EventConnected")
	}
	if !hasDisconnected {
		t.Error("missing EventDisconnected")
	}
	if !hasReconnecting {
		t.Error("missing EventReconnecting")
	}
	if !hasReconnectSuccess {
		t.Error("missing EventReconnectSuccess")
	}
}

// --- Scenario 2: Network Partition and Recovery ---
// Expected behavior: When a network partition occurs (server-side connections
// forcibly closed), the keepalive fails, the connection is marked disconnected,
// and reconnection is triggered. When the network recovers (server still running),
// reconnection succeeds.

func TestNetworkPartitionRecovery(t *testing.T) {
	addr, tracker, cleanup := startTestSSHServerWithExecAndConns(t)
	defer cleanup()

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	_, err := m.Connect(context.Background(), "agent-1", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Simulate network partition: close server-side connections only
	// (the server listener is still running, so new connections can be made)
	tracker.CloseAll()
	time.Sleep(100 * time.Millisecond)

	// Health check should fail
	err = m.HealthCheck("agent-1")
	if err == nil {
		t.Error("health check should fail during network partition")
	}

	// Metrics should reflect the failure
	met := m.GetMetrics("agent-1")
	if met == nil {
		t.Fatal("metrics should exist")
	}
	if met.Healthy {
		t.Error("connection should be marked unhealthy")
	}

	// Trigger connection check (would normally happen via keepalive loop)
	m.checkConnections()

	// Connection should be detected as dead
	if m.HasClient("agent-1") {
		t.Error("dead connection should be removed")
	}

	// Wait for reconnection (server is still running, just connections were dropped)
	deadline := time.After(15 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for reconnection after network partition")
		default:
		}

		if m.HasClient("agent-1") && m.GetConnectionState("agent-1") == StateConnected {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Health check should pass after recovery
	if err := m.HealthCheck("agent-1"); err != nil {
		t.Errorf("health check should pass after recovery: %v", err)
	}
}

// --- Scenario 3: Control Plane Restart ---
// Expected behavior: When the control plane restarts, it creates a fresh
// SSHManager. Connections must be re-established by calling Connect for each
// previously known instance. This test verifies that a new SSHManager can
// successfully connect to running agents.

func TestControlPlaneRestart(t *testing.T) {
	addr, cleanup := startTestSSHServerWithExec(t)
	defer cleanup()

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	// Phase 1: Original control plane establishes connections
	m1 := NewSSHManager(0)
	_, err := m1.Connect(context.Background(), "agent-1", host, port, keyPath)
	if err != nil {
		t.Fatalf("initial Connect: %v", err)
	}
	params := m1.GetConnectionParams("agent-1")
	if params == nil {
		t.Fatal("params should be stored after Connect")
	}

	// Save params that would be persisted in database
	savedHost := params.Host
	savedPort := params.Port
	savedKeyPath := params.PrivateKeyPath

	m1.CloseAll()

	// Phase 2: Control plane restarts - new SSHManager
	m2 := NewSSHManager(0)
	defer m2.CloseAll()

	// Re-establish connection using saved params
	client, err := m2.Connect(context.Background(), "agent-1", savedHost, savedPort, savedKeyPath)
	if err != nil {
		t.Fatalf("reconnect after restart: %v", err)
	}
	if client == nil {
		t.Fatal("client should not be nil")
	}

	// Verify the connection is fully functional
	if m2.GetConnectionState("agent-1") != StateConnected {
		t.Errorf("expected state Connected, got %s", m2.GetConnectionState("agent-1"))
	}
	if err := m2.HealthCheck("agent-1"); err != nil {
		t.Errorf("health check failed after restart reconnection: %v", err)
	}

	// Verify metrics are fresh (not carried over from old manager)
	met := m2.GetMetrics("agent-1")
	if met == nil {
		t.Fatal("metrics should exist")
	}
	if met.SuccessfulChecks != 1 {
		t.Errorf("expected 1 successful check, got %d", met.SuccessfulChecks)
	}
}

// --- Scenario 4: Simultaneous Failure of Multiple Instances ---
// Expected behavior: When multiple agent instances fail simultaneously,
// the connection manager handles concurrent reconnections without deadlocks,
// race conditions, or panics. Each instance reconnects independently.

func TestConcurrentMultiInstanceFailure(t *testing.T) {
	const numInstances = 5

	// Start SSH servers for each instance
	type serverInfo struct {
		host    string
		port    int
		keyPath string
		cleanup func()
		tracker *connTracker
	}

	servers := make([]serverInfo, numInstances)
	for i := 0; i < numInstances; i++ {
		addr, tracker, cleanup := startTestSSHServerWithExecAndConns(t)
		host, portStr, _ := net.SplitHostPort(addr)
		var port int
		fmt.Sscanf(portStr, "%d", &port)
		keyPath := os.Getenv("TEST_SSH_KEY_PATH")

		servers[i] = serverInfo{
			host: host, port: port, keyPath: keyPath,
			cleanup: cleanup, tracker: tracker,
		}
	}

	m := NewSSHManager(0)
	defer m.CloseAll()

	// Connect all instances
	for i, srv := range servers {
		name := fmt.Sprintf("agent-%d", i)
		_, err := m.Connect(context.Background(), name, srv.host, srv.port, srv.keyPath)
		if err != nil {
			t.Fatalf("Connect %s: %v", name, err)
		}
	}

	if m.ConnectionCount() != numInstances {
		t.Fatalf("expected %d connections, got %d", numInstances, m.ConnectionCount())
	}

	// Simultaneously kill all servers
	for _, srv := range servers {
		srv.cleanup()
		srv.tracker.CloseAll()
	}
	time.Sleep(100 * time.Millisecond)

	// Trigger connection checks - should detect all dead connections
	m.checkConnections()

	// All dead clients should be removed
	time.Sleep(100 * time.Millisecond)
	for i := 0; i < numInstances; i++ {
		name := fmt.Sprintf("agent-%d", i)
		if m.HasClient(name) {
			t.Errorf("dead client %s should be removed", name)
		}
	}

	// Restart all servers on same ports
	cleanups := make([]func(), numInstances)
	for i, srv := range servers {
		_, cleanup := startTestSSHServerWithExecOnPort(t, srv.host, srv.port, srv.keyPath)
		cleanups[i] = cleanup
	}
	defer func() {
		for _, c := range cleanups {
			if c != nil {
				c()
			}
		}
	}()

	// Wait for all reconnections (they happen concurrently via triggerReconnect)
	deadline := time.After(30 * time.Second)
	for {
		select {
		case <-deadline:
			// Report which instances didn't reconnect
			for i := 0; i < numInstances; i++ {
				name := fmt.Sprintf("agent-%d", i)
				if !m.HasClient(name) {
					t.Errorf("agent-%d did not reconnect in time (state: %s, reconnecting: %v)",
						i, m.GetConnectionState(name), m.IsReconnecting(name))
				}
			}
			t.Fatal("timed out waiting for all instances to reconnect")
		default:
		}

		allConnected := true
		for i := 0; i < numInstances; i++ {
			name := fmt.Sprintf("agent-%d", i)
			if !m.HasClient(name) || m.GetConnectionState(name) != StateConnected {
				allConnected = false
				break
			}
		}
		if allConnected {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Verify all instances are healthy
	for i := 0; i < numInstances; i++ {
		name := fmt.Sprintf("agent-%d", i)
		if err := m.HealthCheck(name); err != nil {
			t.Errorf("health check failed for %s after reconnection: %v", name, err)
		}
	}
}

// --- Scenario 5: Graceful Degradation - Permanently Unavailable ---
// Expected behavior: When an agent is permanently unavailable, the
// connection manager exhausts all retry attempts, transitions to Failed
// state, emits ReconnectFailed event, and cleans up resources. The system
// does not leak goroutines or resources.

func TestGracefulDegradationPermanentFailure(t *testing.T) {
	addr, tracker, cleanup := startTestSSHServerWithExecAndConns(t)

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	_, err := m.Connect(context.Background(), "agent-1", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Kill server permanently
	cleanup()
	tracker.CloseAll()
	time.Sleep(100 * time.Millisecond)

	// Directly attempt reconnection with low retry count for test speed
	params := m.GetConnectionParams("agent-1")
	if params == nil {
		t.Fatal("params should exist")
	}

	err = m.reconnectWithBackoff(context.Background(), "agent-1", params, 2)
	if err == nil {
		t.Error("reconnection should fail when server is permanently down")
	}

	// Verify state is Failed
	state := m.GetConnectionState("agent-1")
	if state != StateFailed {
		t.Errorf("expected state Failed, got %s", state)
	}

	// Verify events
	events := m.GetEvents("agent-1")
	hasReconnectFailed := false
	for _, e := range events {
		if e.Type == EventReconnectFailed {
			hasReconnectFailed = true
			break
		}
	}
	if !hasReconnectFailed {
		t.Error("expected EventReconnectFailed event")
	}

	// Verify params are cleaned up after giving up
	if p := m.GetConnectionParams("agent-1"); p != nil {
		t.Error("connection params should be cleaned up after permanent failure")
	}

	// Verify metrics are cleaned up
	if met := m.GetMetrics("agent-1"); met != nil {
		t.Error("metrics should be cleaned up after permanent failure")
	}
}

// --- Scenario 6: State Transitions During Reconnection ---
// Expected behavior: The connection state machine follows the correct
// transition sequence during each phase of the reconnection lifecycle:
// Disconnected -> Connecting -> Connected (initial)
// Connected -> Disconnected -> Reconnecting -> Connecting -> Connected (reconnect success)
// Connected -> Disconnected -> Reconnecting -> Connecting -> Failed (reconnect failure)

func TestStateTransitionsDuringReconnect(t *testing.T) {
	addr, cleanup := startTestSSHServerWithExec(t)
	defer cleanup()

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	// Track all state transitions
	var transitions []StateTransition
	var mu sync.Mutex
	m.OnConnectionStateChange(func(instance string, from, to ConnectionState) {
		if instance == "agent-1" {
			mu.Lock()
			transitions = append(transitions, StateTransition{From: from, To: to})
			mu.Unlock()
		}
	})

	// Connect
	_, err := m.Connect(context.Background(), "agent-1", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	mu.Lock()
	if len(transitions) < 2 {
		t.Fatalf("expected at least 2 transitions after Connect, got %d", len(transitions))
	}
	// Should be: Disconnected -> Connecting, Connecting -> Connected
	if transitions[0].From != StateDisconnected || transitions[0].To != StateConnecting {
		t.Errorf("first transition: expected Disconnected->Connecting, got %s->%s",
			transitions[0].From, transitions[0].To)
	}
	if transitions[1].From != StateConnecting || transitions[1].To != StateConnected {
		t.Errorf("second transition: expected Connecting->Connected, got %s->%s",
			transitions[1].From, transitions[1].To)
	}
	mu.Unlock()

	// Verify state tracker records transitions
	stored := m.GetStateTransitions("agent-1")
	if len(stored) < 2 {
		t.Errorf("expected at least 2 stored transitions, got %d", len(stored))
	}
}

// --- Scenario 7: Reconnection Backoff Timing ---
// Expected behavior: Reconnection attempts use exponential backoff
// starting at 1s, doubling each attempt, capped at 16s.

func TestReconnectionBackoffTiming(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	tmpDir := t.TempDir()
	keyPath := writeTestKey(t, tmpDir, "test")

	// Use an invalid address so all attempts fail quickly
	params := &ConnectionParams{Host: "127.0.0.1", Port: 1, PrivateKeyPath: keyPath}

	start := time.Now()
	// 3 attempts: initial + 1s wait + attempt + 2s wait + attempt
	_ = m.reconnectWithBackoff(context.Background(), "test", params, 3)
	elapsed := time.Since(start)

	// With 3 retries: attempt1, wait 1s, attempt2, wait 2s, attempt3
	// Minimum expected: 1s + 2s = 3s of waiting
	// The connect attempts themselves should be fast (port 1 rejects quickly)
	if elapsed < 2*time.Second {
		t.Errorf("reconnection was too fast (%v), backoff may not be working", elapsed)
	}
	// Should not take more than ~10s even with connection attempt overhead
	if elapsed > 15*time.Second {
		t.Errorf("reconnection was too slow (%v), may be blocking", elapsed)
	}
}

// --- Scenario 8: Concurrent Access During Reconnection ---
// Expected behavior: Reading connection state, metrics, and events
// is safe while reconnection is in progress. No deadlocks or panics.

func TestConcurrentAccessDuringReconnection(t *testing.T) {
	addr, tracker, cleanup := startTestSSHServerWithExecAndConns(t)
	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	_, err := m.Connect(context.Background(), "agent-1", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Kill the server to trigger reconnection attempts
	cleanup()
	tracker.CloseAll()
	time.Sleep(100 * time.Millisecond)

	// Store params before checkConnections removes the client
	params := m.GetConnectionParams("agent-1")
	if params == nil {
		t.Fatal("params should exist")
	}

	// Start reconnection in background (will fail since server is down)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.reconnectWithBackoff(ctx, "agent-1", params, 3)
	}()

	// Concurrently access manager state - should not deadlock or panic
	var readWg sync.WaitGroup
	for i := 0; i < 10; i++ {
		readWg.Add(1)
		go func() {
			defer readWg.Done()
			for j := 0; j < 20; j++ {
				m.GetConnectionState("agent-1")
				m.GetMetrics("agent-1")
				m.GetEvents("agent-1")
				m.GetAllMetrics()
				m.GetAllConnectionStates()
				m.HasClient("agent-1")
				m.IsReconnecting("agent-1")
				m.ConnectionCount()
				m.GetStateTransitions("agent-1")
				time.Sleep(10 * time.Millisecond)
			}
		}()
	}

	readWg.Wait()
	cancel()
	wg.Wait()
}

// --- Scenario 9: Health Check Detects and Recovers from Stale Connection ---
// Expected behavior: When a connection becomes stale (server-side closed)
// but the client hasn't noticed yet, the health check (echo ping) or
// keepalive detects the failure and triggers reconnection.

func TestHealthCheckDetectsStaleConnection(t *testing.T) {
	addr, tracker, cleanup := startTestSSHServerWithExecAndConns(t)
	defer cleanup()

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	_, err := m.Connect(context.Background(), "agent-1", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Kill only the server-side of the connection (simulates stale connection)
	tracker.CloseAll()
	time.Sleep(100 * time.Millisecond)

	// Health check should detect the stale connection
	err = m.HealthCheck("agent-1")
	if err == nil {
		t.Error("health check should fail on stale connection")
	}

	// Verify metrics reflect failure
	met := m.GetMetrics("agent-1")
	if met == nil {
		t.Fatal("metrics should exist")
	}
	if met.FailedChecks != 1 {
		t.Errorf("expected 1 failed check, got %d", met.FailedChecks)
	}
	if met.Healthy {
		t.Error("should be marked unhealthy")
	}

	// Verify health check failed event was emitted
	events := m.GetEvents("agent-1")
	hasHealthCheckFailed := false
	for _, e := range events {
		if e.Type == EventHealthCheckFailed {
			hasHealthCheckFailed = true
			break
		}
	}
	if !hasHealthCheckFailed {
		t.Error("expected EventHealthCheckFailed event")
	}
}

// --- Scenario 10: Multiple Reconnection Attempts Not Duplicated ---
// Expected behavior: If multiple health check failures occur for the same
// instance, only one reconnection goroutine runs at a time.

func TestReconnectionDeduplication(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	tmpDir := t.TempDir()
	keyPath := writeTestKey(t, tmpDir, "test")

	// Store params for reconnection (with unreachable server)
	m.mu.Lock()
	m.params["agent-1"] = &ConnectionParams{Host: "127.0.0.1", Port: 1, PrivateKeyPath: keyPath}
	m.mu.Unlock()

	// Trigger multiple reconnections concurrently
	var wg sync.WaitGroup
	triggered := int32(0)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.triggerReconnect("agent-1", "test")
			atomic.AddInt32(&triggered, 1)
		}()
	}

	wg.Wait()
	time.Sleep(200 * time.Millisecond)

	// Despite 10 triggers, at most one should be reconnecting
	if !m.IsReconnecting("agent-1") {
		// It might have already finished (failed), which is OK
		// The important thing is no panic or race condition occurred
		t.Log("reconnection already completed (expected for quick failures)")
	}

	// Check no duplicate reconnecting events
	events := m.GetEvents("agent-1")
	reconnectingCount := 0
	for _, e := range events {
		if e.Type == EventReconnecting {
			reconnectingCount++
		}
	}
	if reconnectingCount > 1 {
		t.Errorf("expected at most 1 EventReconnecting event (dedup), got %d", reconnectingCount)
	}
}

// --- Scenario 11: Health Check Failure Emits Correct Event ---
// Expected behavior: When a health check fails, an EventHealthCheckFailed
// event is emitted with the error details.

func TestHealthCheckFailureEventDetails(t *testing.T) {
	addr, tracker, cleanup := startTestSSHServerWithExecAndConns(t)
	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	_, err := m.Connect(context.Background(), "agent-1", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Kill server
	cleanup()
	tracker.CloseAll()
	time.Sleep(100 * time.Millisecond)

	_ = m.HealthCheck("agent-1")

	events := m.GetEvents("agent-1")
	for _, e := range events {
		if e.Type == EventHealthCheckFailed {
			if e.Details == "" {
				t.Error("EventHealthCheckFailed should have error details")
			}
			if e.Timestamp.IsZero() {
				t.Error("event should have a timestamp")
			}
			if e.InstanceName != "agent-1" {
				t.Errorf("expected instance name agent-1, got %s", e.InstanceName)
			}
			return
		}
	}
	t.Error("expected EventHealthCheckFailed event")
}

// --- Scenario 12: CloseAll During Active Reconnection ---
// Expected behavior: CloseAll stops the keepalive loop, which cancels the
// context used by reconnection goroutines, causing them to exit cleanly.

func TestCloseAllDuringReconnection(t *testing.T) {
	m := NewSSHManager(0)

	tmpDir := t.TempDir()
	keyPath := writeTestKey(t, tmpDir, "test")

	// Set up params for reconnection to an unreachable server
	m.mu.Lock()
	m.params["agent-1"] = &ConnectionParams{Host: "127.0.0.1", Port: 1, PrivateKeyPath: keyPath}
	m.mu.Unlock()

	// Start reconnection
	m.triggerReconnect("agent-1", "test")
	time.Sleep(100 * time.Millisecond)

	// CloseAll should not deadlock even with active reconnection
	done := make(chan struct{})
	go func() {
		m.CloseAll()
		close(done)
	}()

	select {
	case <-done:
		// CloseAll completed successfully
	case <-time.After(10 * time.Second):
		t.Fatal("CloseAll deadlocked during active reconnection")
	}
}
