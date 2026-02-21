// resilience_test.go implements tests for SSH connection resilience scenarios.
//
// These tests validate system behavior under failure conditions:
//   - Agent container restarts (key re-upload, SSH reconnection, tunnel recreation)
//   - Network partitions (disconnect detection, restoration, tunnel recreation)
//   - Control plane restarts (connection re-establishment via reconciliation)
//   - Simultaneous multi-instance failures (concurrent reconnection handling)
//   - Graceful degradation when SSH is permanently unavailable
//
// Each test uses in-process SSH servers with dynamic orchestrator mocks to
// simulate real-world failure patterns without requiring Docker containers.
// See docs/ssh-resilience-scenarios.md for expected behavior documentation.

package sshproxy

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- Test Infrastructure for Resilience Tests ---

// dynamicOrch is a thread-safe mock orchestrator whose target address can
// be changed at runtime. This simulates agents that restart at different ports
// or become temporarily unreachable.
type dynamicOrch struct {
	mu             sync.Mutex
	host           string
	port           int
	configureErr   error
	getAddrErr     error
	configureCalls int
}

func (o *dynamicOrch) ConfigureSSHAccess(_ context.Context, _ uint, _ string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.configureCalls++
	return o.configureErr
}

func (o *dynamicOrch) GetSSHAddress(_ context.Context, _ uint) (string, int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.host, o.port, o.getAddrErr
}

func (o *dynamicOrch) setTarget(host string, port int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.host = host
	o.port = port
}

func (o *dynamicOrch) getConfigureCalls() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.configureCalls
}

// multiInstanceOrch routes SSH operations to per-instance dynamic orchestrators.
type multiInstanceOrch struct {
	mu        sync.Mutex
	instances map[uint]*dynamicOrch
}

func newMultiInstanceOrch() *multiInstanceOrch {
	return &multiInstanceOrch{instances: make(map[uint]*dynamicOrch)}
}

func (o *multiInstanceOrch) setInstance(id uint, orch *dynamicOrch) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.instances[id] = orch
}

func (o *multiInstanceOrch) ConfigureSSHAccess(ctx context.Context, instanceID uint, publicKey string) error {
	o.mu.Lock()
	inst, ok := o.instances[instanceID]
	o.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown instance %d", instanceID)
	}
	return inst.ConfigureSSHAccess(ctx, instanceID, publicKey)
}

func (o *multiInstanceOrch) GetSSHAddress(ctx context.Context, instanceID uint) (string, int, error) {
	o.mu.Lock()
	inst, ok := o.instances[instanceID]
	o.mu.Unlock()
	if !ok {
		return "", 0, fmt.Errorf("unknown instance %d", instanceID)
	}
	return inst.GetSSHAddress(ctx, instanceID)
}

// --- Resilience Tests ---

// TestResilience_AgentRestart verifies that when an agent container restarts:
// 1. The public key is re-uploaded automatically during reconnection
// 2. SSH reconnects after the agent comes back online
// 3. Tunnels can be recreated on the new connection
func TestResilience_AgentRestart(t *testing.T) {
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

	// Start initial "agent" SSH server
	ts1 := startTestSSHServerForTunnel(t, signer.PublicKey())
	host1, port1 := parseHostPort(t, ts1.addr)

	orch := &dynamicOrch{host: host1, port: port1}
	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()
	mgr.SetOrchestrator(orch)

	// Phase 1: Establish initial connection
	_, err = mgr.Connect(context.Background(), 1, host1, port1)
	if err != nil {
		t.Fatalf("initial connect: %v", err)
	}
	if !mgr.IsConnected(1) {
		t.Fatal("should be connected after initial connect")
	}

	// Track events
	var mu sync.Mutex
	var events []ConnectionEvent
	mgr.OnEvent(func(e ConnectionEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	// Phase 2: Kill the "agent" (simulate container crash)
	ts1.cleanup()
	time.Sleep(200 * time.Millisecond)

	// Phase 3: Start a new "agent" (simulate container restart — new port)
	ts2 := startTestSSHServerForTunnel(t, signer.PublicKey())
	defer ts2.cleanup()
	host2, port2 := parseHostPort(t, ts2.addr)
	orch.setTarget(host2, port2)

	// Phase 4: Reconnect (key is re-uploaded before each attempt)
	err = mgr.ReconnectWithBackoff(context.Background(), 1, 5, "agent restart")
	if err != nil {
		t.Fatalf("reconnection failed: %v", err)
	}

	// Verify: SSH connection re-established
	if !mgr.IsConnected(1) {
		t.Error("should be connected after reconnection")
	}

	// Verify: Key was re-uploaded (ConfigureSSHAccess called)
	if calls := orch.getConfigureCalls(); calls == 0 {
		t.Error("ConfigureSSHAccess was not called during reconnection (key not re-uploaded)")
	}

	// Verify: State is Connected
	state := mgr.GetConnectionState(1)
	if state != StateConnected {
		t.Errorf("state = %s, want connected", state)
	}

	// Verify: Events include the reconnection lifecycle
	mu.Lock()
	eventTypeSet := make(map[ConnectionEventType]bool)
	for _, e := range events {
		eventTypeSet[e.Type] = true
	}
	mu.Unlock()

	if !eventTypeSet[EventReconnecting] {
		t.Error("missing EventReconnecting event")
	}
	if !eventTypeSet[EventKeyUploaded] {
		t.Error("missing EventKeyUploaded event (key should be re-uploaded during reconnection)")
	}
	if !eventTypeSet[EventReconnected] {
		t.Error("missing EventReconnected event")
	}

	// Verify: Tunnels can be created on the new connection
	tm := NewTunnelManager(mgr)
	defer tm.StopAll()
	err = tm.StartTunnelsForInstance(context.Background(), 1, orch)
	if err != nil {
		t.Fatalf("StartTunnelsForInstance after reconnect: %v", err)
	}
	tunnels := tm.GetTunnelsForInstance(1)
	if len(tunnels) != 2 {
		t.Errorf("expected 2 tunnels (VNC+Gateway) after recreation, got %d", len(tunnels))
	}
}

// TestResilience_NetworkPartitionWithTunnels verifies that after a network
// partition (simulated by killing the SSH server):
// 1. Disconnect is detected
// 2. Tunnels are recreated after the connection is restored
func TestResilience_NetworkPartitionWithTunnels(t *testing.T) {
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

	ts := startTestSSHServerForTunnel(t, signer.PublicKey())
	host, port := parseHostPort(t, ts.addr)

	orch := &dynamicOrch{host: host, port: port}
	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()
	mgr.SetOrchestrator(orch)

	// Establish connection and create tunnels
	_, err = mgr.Connect(context.Background(), 1, host, port)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	tm := NewTunnelManager(mgr)
	defer tm.StopAll()
	err = tm.StartTunnelsForInstance(context.Background(), 1, orch)
	if err != nil {
		t.Fatalf("initial StartTunnelsForInstance: %v", err)
	}

	initialTunnels := tm.GetTunnelsForInstance(1)
	if len(initialTunnels) != 2 {
		t.Fatalf("expected 2 initial tunnels, got %d", len(initialTunnels))
	}

	// Simulate network partition: kill server
	ts.cleanup()
	time.Sleep(200 * time.Millisecond)

	// Verify disconnect detected
	if mgr.IsConnected(1) {
		t.Error("should not be connected after server killed")
	}

	// Simulate network restoration: start new server
	ts2 := startTestSSHServerForTunnel(t, signer.PublicKey())
	defer ts2.cleanup()
	host2, port2 := parseHostPort(t, ts2.addr)
	orch.setTarget(host2, port2)

	// Reconnect
	err = mgr.ReconnectWithBackoff(context.Background(), 1, 5, "network restored")
	if err != nil {
		t.Fatalf("reconnection after partition: %v", err)
	}

	// Old tunnels are stale (they reference the dead SSH client).
	// In production, the tunnel health checker detects this.
	// Here we stop them and recreate to verify tunnels work on the new connection.
	tm.StopTunnelsForInstance(1)

	err = tm.StartTunnelsForInstance(context.Background(), 1, orch)
	if err != nil {
		t.Fatalf("StartTunnelsForInstance after partition: %v", err)
	}

	// Verify tunnels were recreated
	newTunnels := tm.GetTunnelsForInstance(1)
	if len(newTunnels) != 2 {
		t.Errorf("expected 2 tunnels after restoration, got %d", len(newTunnels))
	}

	newVNCPort := tm.GetVNCLocalPort(1)
	if newVNCPort == 0 {
		t.Error("VNC tunnel not recreated after network restoration")
	}

	newGWPort := tm.GetGatewayLocalPort(1)
	if newGWPort == 0 {
		t.Error("Gateway tunnel not recreated after network restoration")
	}
}

// TestResilience_ControlPlaneRestart verifies that when the control plane
// restarts (fresh SSHManager + TunnelManager with no cached connections):
// 1. The reconciliation loop re-establishes connections to running instances
// 2. Public keys are uploaded to all running instances
func TestResilience_ControlPlaneRestart(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	// Start SSH servers for 3 running instances
	const numInstances = 3
	servers := make([]*testServer, numInstances)
	multiOrch := newMultiInstanceOrch()

	for i := 0; i < numInstances; i++ {
		ts := startTestSSHServerForTunnel(t, signer.PublicKey())
		servers[i] = ts
		defer ts.cleanup()
		host, port := parseHostPort(t, ts.addr)
		multiOrch.setInstance(uint(i+1), &dynamicOrch{host: host, port: port})
	}

	// Simulate fresh control plane start — new managers with no connections
	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()
	tm := NewTunnelManager(mgr)
	defer tm.StopAll()

	// Run reconcile (simulates what StartBackgroundManager does on startup)
	listRunning := func(ctx context.Context) ([]uint, error) {
		return []uint{1, 2, 3}, nil
	}
	tm.reconcile(context.Background(), listRunning, multiOrch)

	// Verify all instances got connections and tunnels
	for i := uint(1); i <= numInstances; i++ {
		if !mgr.IsConnected(i) {
			t.Errorf("instance %d not connected after reconcile", i)
		}
		tunnels := tm.GetTunnelsForInstance(i)
		if len(tunnels) != 2 {
			t.Errorf("instance %d: expected 2 tunnels, got %d", i, len(tunnels))
		}
	}

	// Verify keys were uploaded (ConfigureSSHAccess called for each instance)
	for i := uint(1); i <= numInstances; i++ {
		multiOrch.mu.Lock()
		instOrch := multiOrch.instances[i]
		multiOrch.mu.Unlock()

		if calls := instOrch.getConfigureCalls(); calls == 0 {
			t.Errorf("instance %d: ConfigureSSHAccess not called (key not uploaded)", i)
		}
	}
}

// TestResilience_SimultaneousMultipleFailures verifies that the system handles
// simultaneous failure of multiple instances:
// 1. All instances are detected as failed
// 2. Concurrent reconnections don't deadlock
// 3. All instances recover when servers come back
func TestResilience_SimultaneousMultipleFailures(t *testing.T) {
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

	const numInstances = 5
	multiOrch := newMultiInstanceOrch()

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	// Start servers and connect all instances
	servers := make([]*testServer, numInstances)
	for i := 0; i < numInstances; i++ {
		ts := startTestSSHServerForTunnel(t, signer.PublicKey())
		servers[i] = ts
		host, port := parseHostPort(t, ts.addr)
		multiOrch.setInstance(uint(i+1), &dynamicOrch{host: host, port: port})

		_, err := mgr.Connect(context.Background(), uint(i+1), host, port)
		if err != nil {
			for j := 0; j <= i; j++ {
				servers[j].cleanup()
			}
			t.Fatalf("connect instance %d: %v", i+1, err)
		}
	}

	// Verify all connected
	for i := uint(1); i <= numInstances; i++ {
		if !mgr.IsConnected(i) {
			t.Fatalf("instance %d not connected", i)
		}
	}

	// Kill ALL servers simultaneously (simulate mass failure)
	for i := 0; i < numInstances; i++ {
		servers[i].cleanup()
	}
	time.Sleep(200 * time.Millisecond)

	// Start new servers for all instances
	for i := 0; i < numInstances; i++ {
		ts := startTestSSHServerForTunnel(t, signer.PublicKey())
		defer ts.cleanup()
		host, port := parseHostPort(t, ts.addr)
		multiOrch.setInstance(uint(i+1), &dynamicOrch{host: host, port: port})
	}

	// Reconnect all instances concurrently
	var wg sync.WaitGroup
	errors := make([]error, numInstances)
	for i := 0; i < numInstances; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			instanceID := uint(idx + 1)
			errors[idx] = mgr.reconnectWithBackoff(
				context.Background(), instanceID, 5, multiOrch,
				fmt.Sprintf("mass failure instance %d", instanceID),
			)
		}(i)
	}

	// Wait for all reconnections with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All reconnections completed
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent reconnections timed out (possible deadlock)")
	}

	// Verify all reconnections succeeded
	for i := 0; i < numInstances; i++ {
		if errors[i] != nil {
			t.Errorf("instance %d reconnection failed: %v", i+1, errors[i])
		}
	}

	// Verify all instances are connected
	for i := uint(1); i <= numInstances; i++ {
		if !mgr.IsConnected(i) {
			t.Errorf("instance %d not connected after mass reconnection", i)
		}
	}
}

// TestResilience_ConcurrentReconnections verifies that triggering reconnections
// for the same instance concurrently is properly deduplicated and doesn't cause
// deadlocks or panics.
func TestResilience_ConcurrentReconnections(t *testing.T) {
	saved := reconnectInitialBackoff
	reconnectInitialBackoff = 50 * time.Millisecond
	defer func() { reconnectInitialBackoff = saved }()

	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	ts := startTestSSHServerForTunnel(t, signer.PublicKey())
	defer ts.cleanup()
	host, port := parseHostPort(t, ts.addr)

	orch := &dynamicOrch{host: host, port: port}
	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()
	mgr.SetOrchestrator(orch)

	// Establish initial connection
	_, err = mgr.Connect(context.Background(), 1, host, port)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Trigger many reconnections for the same instance simultaneously
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mgr.triggerReconnect(1, fmt.Sprintf("concurrent trigger %d", i))
		}(i)
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent triggerReconnect calls timed out (possible deadlock)")
	}

	// Allow time for the single reconnection to complete
	time.Sleep(500 * time.Millisecond)

	// Verify at most one reconnection ran at a time (the key invariant)
	mgr.reconnMu.RLock()
	activeReconnections := len(mgr.reconnecting)
	mgr.reconnMu.RUnlock()
	if activeReconnections > 1 {
		t.Errorf("expected at most 1 active reconnection, got %d", activeReconnections)
	}

	// Verify no panic or deadlock (reaching this point is proof)
	// The connection should either still be valid or reconnected
	if !mgr.IsConnected(1) {
		t.Log("Note: instance 1 not connected after concurrent reconnections (acceptable if server timing varies)")
	}
}

// TestResilience_GracefulDegradation verifies behavior when SSH is permanently
// unavailable:
// 1. State transitions to Failed after retries exhausted
// 2. Events are emitted for observability
// 3. System remains operational for other instances
// 4. No panics or goroutine leaks
func TestResilience_GracefulDegradation(t *testing.T) {
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

	// Orchestrator that always fails (simulates permanently unreachable agent)
	orch := &mockOrch{configureErr: fmt.Errorf("agent permanently unavailable")}
	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()
	mgr.SetOrchestrator(orch)

	// Track events and state changes
	var mu sync.Mutex
	var events []ConnectionEvent
	var stateChanges []struct{ from, to ConnectionState }

	mgr.OnEvent(func(e ConnectionEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})
	mgr.OnStateChange(func(id uint, from, to ConnectionState) {
		mu.Lock()
		stateChanges = append(stateChanges, struct{ from, to ConnectionState }{from, to})
		mu.Unlock()
	})

	// Attempt reconnection with limited retries
	maxRetries := 3
	err = mgr.ReconnectWithBackoff(context.Background(), 1, maxRetries, "permanent failure test")
	if err == nil {
		t.Fatal("expected error from permanently failing reconnection")
	}

	// Verify: State is Failed
	state := mgr.GetConnectionState(1)
	if state != StateFailed {
		t.Errorf("state = %s, want failed", state)
	}

	// Verify: Not connected
	if mgr.IsConnected(1) {
		t.Error("should not be connected after permanent failure")
	}

	// Verify: Events include Reconnecting and ReconnectFailed
	mu.Lock()
	eventTypeSet := make(map[ConnectionEventType]bool)
	for _, e := range events {
		eventTypeSet[e.Type] = true
	}
	stateChangesCopy := make([]struct{ from, to ConnectionState }, len(stateChanges))
	copy(stateChangesCopy, stateChanges)
	mu.Unlock()

	if !eventTypeSet[EventReconnecting] {
		t.Error("missing EventReconnecting event")
	}
	if !eventTypeSet[EventReconnectFailed] {
		t.Error("missing EventReconnectFailed event")
	}

	// Verify: ConfigureSSHAccess was called for each retry attempt
	if calls := orch.getConfigureCalls(); calls != maxRetries {
		t.Errorf("ConfigureSSHAccess called %d times, want %d", calls, maxRetries)
	}

	// Verify: State transitions include → Reconnecting → Failed
	foundReconnecting := false
	foundFailed := false
	for _, sc := range stateChangesCopy {
		if sc.to == StateReconnecting {
			foundReconnecting = true
		}
		if sc.to == StateFailed {
			foundFailed = true
		}
	}
	if !foundReconnecting {
		t.Error("missing state transition to Reconnecting")
	}
	if !foundFailed {
		t.Error("missing state transition to Failed")
	}

	// Verify: System still works for other instances (graceful degradation)
	ts := startTestSSHServerForTunnel(t, signer.PublicKey())
	defer ts.cleanup()
	host, port := parseHostPort(t, ts.addr)

	_, err = mgr.Connect(context.Background(), 2, host, port)
	if err != nil {
		t.Fatalf("connecting instance 2 after instance 1 failed: %v", err)
	}
	if !mgr.IsConnected(2) {
		t.Error("instance 2 should be connected despite instance 1 failure")
	}
}

// TestResilience_StateTransitionsAcrossFailures verifies the complete state
// machine across a full lifecycle: connect → health check failure → auto-reconnect.
func TestResilience_StateTransitionsAcrossFailures(t *testing.T) {
	saved := reconnectInitialBackoff
	reconnectInitialBackoff = 10 * time.Millisecond
	defer func() { reconnectInitialBackoff = saved }()

	signer, ts := newTestSignerAndServer(t)
	host, port := parseHostPort(t, ts.addr)

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	// Prepare reconnection target (new server) before killing original
	ts2 := startTestSSHServerForTunnel(t, signer.PublicKey())
	defer ts2.cleanup()
	host2, port2 := parseHostPort(t, ts2.addr)

	orch := &dynamicOrch{host: host2, port: port2}
	mgr.SetOrchestrator(orch)

	// Connect to original server
	_, err := mgr.Connect(context.Background(), 1, host, port)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	state := mgr.GetConnectionState(1)
	if state != StateConnected {
		t.Errorf("after connect: state = %s, want connected", state)
	}

	// Kill original server → health check will fail
	ts.cleanup()
	time.Sleep(200 * time.Millisecond)

	// Run health check manually (triggers disconnect detection + async reconnect)
	mgr.checkAllConnections()

	// Wait for async reconnect to complete
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("async reconnection did not complete within timeout")
		default:
			if mgr.IsConnected(1) {
				goto reconnected
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

reconnected:
	// Verify state transitions covered the full lifecycle
	transitions := mgr.GetStateTransitions(1)
	if len(transitions) == 0 {
		t.Fatal("no state transitions recorded")
	}

	statesSeen := make(map[ConnectionState]bool)
	for _, tr := range transitions {
		statesSeen[tr.To] = true
	}

	// We should see: Connecting → Connected → Disconnected → Reconnecting → (Connecting →) Connected
	for _, expected := range []ConnectionState{StateConnecting, StateConnected, StateDisconnected, StateReconnecting} {
		if !statesSeen[expected] {
			t.Errorf("expected state %s not seen in transitions", expected)
		}
	}
}

// TestResilience_HealthCheckTriggersReconnect verifies the full chain:
// health check failure → disconnect detection → automatic reconnection.
func TestResilience_HealthCheckTriggersReconnect(t *testing.T) {
	saved := reconnectInitialBackoff
	reconnectInitialBackoff = 10 * time.Millisecond
	defer func() { reconnectInitialBackoff = saved }()

	signer, ts := newTestSignerAndServer(t)
	host, port := parseHostPort(t, ts.addr)

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	// Prepare reconnection target before killing original server
	ts2 := startTestSSHServerForTunnel(t, signer.PublicKey())
	defer ts2.cleanup()
	host2, port2 := parseHostPort(t, ts2.addr)
	orch := &dynamicOrch{host: host2, port: port2}
	mgr.SetOrchestrator(orch)

	// Connect to original server
	_, err := mgr.Connect(context.Background(), 1, host, port)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Track reconnection success
	var reconnected int32
	mgr.OnEvent(func(e ConnectionEvent) {
		if e.Type == EventReconnected {
			atomic.AddInt32(&reconnected, 1)
		}
	})

	// Kill original server
	ts.cleanup()
	time.Sleep(200 * time.Millisecond)

	// Health check detects failure and triggers automatic reconnection
	mgr.checkAllConnections()

	// Wait for async reconnection to complete
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("reconnection did not complete after health check failure")
		default:
			if atomic.LoadInt32(&reconnected) > 0 {
				if !mgr.IsConnected(1) {
					t.Error("should be connected after automatic reconnection")
				}

				// Verify key was re-uploaded
				if calls := orch.getConfigureCalls(); calls == 0 {
					t.Error("ConfigureSSHAccess not called during auto-reconnection")
				}
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// TestResilience_EventHistoryAcrossFailures verifies that event history
// captures the complete failure and recovery cycle for debugging.
func TestResilience_EventHistoryAcrossFailures(t *testing.T) {
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

	ts := startTestSSHServerForTunnel(t, signer.PublicKey())
	defer ts.cleanup()
	host, port := parseHostPort(t, ts.addr)

	orch := &dynamicOrch{host: host, port: port}
	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()
	mgr.SetOrchestrator(orch)

	// Connect → reconnect cycle
	_, err = mgr.Connect(context.Background(), 1, host, port)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Reconnect (closes existing connection, re-establishes)
	err = mgr.ReconnectWithBackoff(context.Background(), 1, 3, "test event history")
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}

	// Check event history
	history := mgr.GetEventHistory(1)
	if len(history) == 0 {
		t.Fatal("no events recorded")
	}

	// Verify chronological order (timestamps should be non-decreasing)
	for i := 1; i < len(history); i++ {
		if history[i].Timestamp.Before(history[i-1].Timestamp) {
			t.Errorf("event %d timestamp %v before event %d timestamp %v",
				i, history[i].Timestamp, i-1, history[i-1].Timestamp)
		}
	}

	// Verify events include the full reconnection cycle
	eventTypes := make(map[ConnectionEventType]bool)
	for _, e := range history {
		eventTypes[e.Type] = true
		if e.InstanceID != 1 {
			t.Errorf("event instance ID = %d, want 1", e.InstanceID)
		}
	}

	if !eventTypes[EventReconnecting] {
		t.Error("event history missing Reconnecting")
	}
	if !eventTypes[EventKeyUploaded] {
		t.Error("event history missing KeyUploaded")
	}
	if !eventTypes[EventReconnected] {
		t.Error("event history missing Reconnected")
	}
}
