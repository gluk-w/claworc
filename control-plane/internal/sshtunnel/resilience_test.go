package sshtunnel

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
)

// =============================================================================
// Resilience scenario tests for tunnel health monitoring and recovery.
//
// These tests simulate production failure scenarios:
//   - Tunnel recreation after SSH reconnection
//   - Tunnel health check detects and recovers from dead ports
//   - Multiple tunnels fail and recover simultaneously
//   - Graceful shutdown during tunnel recovery
// =============================================================================

// --- Scenario 1: Tunnel Recreation After Closed Tunnels ---
// Expected behavior: When tunnels are closed (e.g., after SSH reconnection),
// the per-instance monitor detects the missing tunnels and the system skips
// reconnection when no SSH client is available.

func TestTunnelRecreationDetectsClosedTunnels(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	defer sm.CloseAll()
	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	// Add tunnels that are already closed (simulating post-SSH-failure state)
	closedVNC := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: DefaultVNCPort, Type: TunnelReverse, Protocol: ProtocolTCP, Service: ServiceVNC},
		LocalPort: 12345,
		StartedAt: time.Now(),
		closed:    true,
	}
	closedGW := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: DefaultGatewayPort, Type: TunnelReverse, Protocol: ProtocolTCP, Service: ServiceGateway},
		LocalPort: 12346,
		StartedAt: time.Now(),
		closed:    true,
	}
	tm.addTunnel("agent-1", closedVNC)
	tm.addTunnel("agent-1", closedGW)

	// checkAndReconnectTunnels should remove closed tunnels
	tm.checkAndReconnectTunnels(t.Context(), "agent-1")

	// Closed tunnels should be cleaned up
	tunnels := tm.GetTunnels("agent-1")
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels after cleanup, got %d", len(tunnels))
	}
}

// --- Scenario 2: Global Health Check Closes Unhealthy Tunnels ---
// Expected behavior: The global health check probes all tunnel ports.
// Tunnels pointing to dead ports are closed so per-instance monitors
// can detect and recreate them.

func TestGlobalHealthCheckClosesUnhealthyTunnels(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	defer sm.CloseAll()
	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	// Create a healthy tunnel with a real listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer listener.Close()
	healthyPort := listener.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	healthyTunnel := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceVNC, RemotePort: 3000},
		LocalPort: healthyPort,
		StartedAt: time.Now(),
	}

	// Create an unhealthy tunnel pointing to a dead port
	unhealthyTunnel := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceGateway, RemotePort: 8080},
		LocalPort: 19994, // not listening
		StartedAt: time.Now(),
	}

	tm.addTunnel("agent-1", healthyTunnel)
	tm.addTunnel("agent-1", unhealthyTunnel)

	// Run global health check
	tm.runGlobalHealthCheck()

	// Healthy tunnel should remain
	if healthyTunnel.IsClosed() {
		t.Error("healthy tunnel should not be closed")
	}

	// Unhealthy tunnel should be closed
	if !unhealthyTunnel.IsClosed() {
		t.Error("unhealthy tunnel should be closed by global health check")
	}

	// Verify health check recorded on healthy tunnel
	m := healthyTunnel.Metrics()
	if m.LastCheck.IsZero() {
		t.Error("healthy tunnel LastCheck should be updated")
	}
	if !m.Healthy {
		t.Error("healthy tunnel should still be marked healthy")
	}
}

// --- Scenario 3: Multiple Instances with Mixed Health ---
// Expected behavior: When multiple instances have tunnels in various
// states (healthy, unhealthy, closed), the global health check correctly
// handles each case independently.

func TestMultiInstanceMixedTunnelHealth(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	defer sm.CloseAll()
	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	// Instance A: all healthy
	listenerA, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen A: %v", err)
	}
	defer listenerA.Close()
	portA := listenerA.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			conn, err := listenerA.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	tunnelA := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceVNC, RemotePort: 3000},
		LocalPort: portA,
		StartedAt: time.Now(),
	}
	tm.addTunnel("instance-a", tunnelA)

	// Instance B: unhealthy (dead port)
	tunnelB := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceVNC, RemotePort: 3000},
		LocalPort: 19993, // not listening
		StartedAt: time.Now(),
	}
	tm.addTunnel("instance-b", tunnelB)

	// Instance C: already closed
	tunnelC := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceGateway, RemotePort: 8080},
		LocalPort: 19992,
		StartedAt: time.Now(),
		closed:    true,
	}
	tm.addTunnel("instance-c", tunnelC)

	// Run health check
	tm.runGlobalHealthCheck()

	// Instance A: should remain healthy
	if tunnelA.IsClosed() {
		t.Error("instance-a tunnel should remain open")
	}
	mA := tunnelA.Metrics()
	if !mA.Healthy {
		t.Error("instance-a tunnel should be healthy")
	}

	// Instance B: should be closed
	if !tunnelB.IsClosed() {
		t.Error("instance-b tunnel should be closed")
	}

	// Instance C: was already closed, should remain closed
	if !tunnelC.IsClosed() {
		t.Error("instance-c tunnel should still be closed")
	}
}

// --- Scenario 4: Tunnel Metrics Track Health Over Time ---
// Expected behavior: Tunnel metrics correctly accumulate health check
// results including successful checks, failed checks, and last error.

func TestTunnelMetricsTrackHealthOverTime(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	defer sm.CloseAll()
	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	// Create a listener that we can stop and start
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	tunnel := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceVNC, RemotePort: 3000},
		LocalPort: port,
		StartedAt: time.Now(),
	}
	tm.addTunnel("test", tunnel)

	// Health check should succeed
	err = tm.CheckTunnelHealth("test", "vnc")
	if err != nil {
		t.Fatalf("first health check should pass: %v", err)
	}

	m := tunnel.Metrics()
	if !m.Healthy {
		t.Error("tunnel should be healthy after successful check")
	}
	if m.LastSuccessfulCheck.IsZero() {
		t.Error("LastSuccessfulCheck should be set")
	}
	firstSuccess := m.LastSuccessfulCheck

	// Close the listener (simulate tunnel failure)
	listener.Close()
	<-acceptDone
	time.Sleep(50 * time.Millisecond)

	// Health check should fail
	err = tm.CheckTunnelHealth("test", "vnc")
	if err == nil {
		t.Error("health check should fail after listener closed")
	}

	m = tunnel.Metrics()
	if m.Healthy {
		t.Error("tunnel should be unhealthy after failed check")
	}
	if m.LastError == "" {
		t.Error("LastError should be set after failed check")
	}
	// LastSuccessfulCheck should not advance
	if m.LastSuccessfulCheck != firstSuccess {
		t.Error("LastSuccessfulCheck should not change on failure")
	}
}

// --- Scenario 5: Shutdown During Active Monitoring ---
// Expected behavior: Calling Shutdown while per-instance monitors are
// running stops all monitors, closes all tunnels, and returns cleanly
// without deadlocks.

func TestShutdownDuringActiveMonitoring(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	defer sm.CloseAll()
	tm := NewTunnelManager(sm)

	// Start fake monitors for multiple instances
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("instance-%d", i)
		ctx, cancel := context.WithCancel(t.Context())
		tm.monMu.Lock()
		tm.monitors[name] = cancel
		tm.monMu.Unlock()

		// Add some tunnels
		tm.addTunnel(name, &ActiveTunnel{
			Config:    TunnelConfig{Service: ServiceVNC, RemotePort: 3000},
			LocalPort: 12345 + i,
			StartedAt: time.Now(),
		})

		// Start a goroutine simulating the monitor
		go func(ctx context.Context, n string) {
			<-ctx.Done()
		}(ctx, name)
	}

	// Shutdown should complete without deadlock
	done := make(chan struct{})
	go func() {
		tm.Shutdown()
		close(done)
	}()

	select {
	case <-done:
		// Clean shutdown
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown deadlocked with active monitors")
	}

	// Verify all tunnels closed
	all := tm.GetAllTunnels()
	if len(all) != 0 {
		t.Errorf("expected 0 tunnels after Shutdown, got %d", len(all))
	}

	// Verify all monitors removed
	tm.monMu.Lock()
	monCount := len(tm.monitors)
	tm.monMu.Unlock()
	if monCount != 0 {
		t.Errorf("expected 0 monitors after Shutdown, got %d", monCount)
	}
}

// --- Scenario 6: Concurrent Tunnel Operations During Health Check ---
// Expected behavior: Concurrent reads to tunnel state, metrics, and
// configuration while health checks are running should not cause
// race conditions or deadlocks.

func TestConcurrentTunnelOperationsDuringHealthCheck(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	defer sm.CloseAll()
	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	// Create several tunnels with real listeners
	listeners := make([]net.Listener, 3)
	for i := 0; i < 3; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen %d: %v", i, err)
		}
		listeners[i] = l
		defer l.Close()

		go func(l net.Listener) {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				conn.Close()
			}
		}(l)

		port := l.Addr().(*net.TCPAddr).Port
		tunnel := &ActiveTunnel{
			Config:    TunnelConfig{Service: ServiceLabel(fmt.Sprintf("svc-%d", i)), RemotePort: 3000 + i},
			LocalPort: port,
			StartedAt: time.Now(),
		}
		tm.addTunnel("test-instance", tunnel)
	}

	// Run concurrent health checks and reads
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				tm.runGlobalHealthCheck()
				time.Sleep(10 * time.Millisecond)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				tm.GetTunnels("test-instance")
				tm.GetAllTunnels()
				tm.GetTunnelMetrics("test-instance")
				tm.GetReconnectionCount("test-instance")
				time.Sleep(10 * time.Millisecond)
			}
		}()
	}

	wg.Wait()

	// Verify tunnels are still present
	tunnels := tm.GetTunnels("test-instance")
	if len(tunnels) != 3 {
		t.Errorf("expected 3 tunnels after concurrent operations, got %d", len(tunnels))
	}
}

// --- Scenario 7: Reconnection Counter Tracks Attempts ---
// Expected behavior: The reconnection counter correctly tracks the number
// of successful tunnel reconnections per instance.

func TestReconnectionCounterAccuracy(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	defer sm.CloseAll()
	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	// Initially zero
	if c := tm.GetReconnectionCount("agent-1"); c != 0 {
		t.Errorf("expected 0 reconnections initially, got %d", c)
	}

	// Increment from multiple goroutines
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				tm.incrementReconnects("agent-1")
			}
		}()
	}
	wg.Wait()

	count := tm.GetReconnectionCount("agent-1")
	if count != 50 {
		t.Errorf("expected 50 reconnections, got %d", count)
	}

	// Different instances are tracked independently
	tm.incrementReconnects("agent-2")
	if tm.GetReconnectionCount("agent-2") != 1 {
		t.Errorf("expected 1 reconnection for agent-2, got %d", tm.GetReconnectionCount("agent-2"))
	}
	if tm.GetReconnectionCount("agent-1") != 50 {
		t.Errorf("agent-1 count should still be 50")
	}
}

// --- Scenario 8: Per-Instance Monitor Stops on Context Cancel ---
// Expected behavior: When a per-instance monitor's context is cancelled
// (e.g., via StopTunnelsForInstance), the monitor goroutine exits promptly.

func TestPerInstanceMonitorExitsOnCancel(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	defer sm.CloseAll()
	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})
	go func() {
		tm.monitorInstance(ctx, "test")
		close(done)
	}()

	// Cancel and verify the monitor exits quickly
	cancel()

	select {
	case <-done:
		// Monitor exited cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("monitor did not exit after context cancellation")
	}
}

// --- Scenario 9: Tunnel Close and Reopen Cycle ---
// Expected behavior: Tunnels can be closed and new tunnels can be added
// for the same instance, simulating the full lifecycle of tunnel recovery.

func TestTunnelCloseAndReopenCycle(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	defer sm.CloseAll()
	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	for cycle := 0; cycle < 3; cycle++ {
		// Add tunnels
		vncTunnel := &ActiveTunnel{
			Config:    TunnelConfig{Service: ServiceVNC, RemotePort: DefaultVNCPort, Type: TunnelReverse, Protocol: ProtocolTCP},
			LocalPort: 12345 + cycle*10,
			StartedAt: time.Now(),
		}
		gwTunnel := &ActiveTunnel{
			Config:    TunnelConfig{Service: ServiceGateway, RemotePort: DefaultGatewayPort, Type: TunnelReverse, Protocol: ProtocolTCP},
			LocalPort: 12346 + cycle*10,
			StartedAt: time.Now(),
		}
		tm.addTunnel("agent-1", vncTunnel)
		tm.addTunnel("agent-1", gwTunnel)

		tunnels := tm.GetTunnels("agent-1")
		if len(tunnels) != 2 {
			t.Fatalf("cycle %d: expected 2 tunnels, got %d", cycle, len(tunnels))
		}

		// Close all tunnels for the instance
		tm.CloseTunnels("agent-1")

		tunnels = tm.GetTunnels("agent-1")
		if len(tunnels) != 0 {
			t.Fatalf("cycle %d: expected 0 tunnels after close, got %d", cycle, len(tunnels))
		}

		if !vncTunnel.IsClosed() {
			t.Errorf("cycle %d: VNC tunnel should be closed", cycle)
		}
		if !gwTunnel.IsClosed() {
			t.Errorf("cycle %d: Gateway tunnel should be closed", cycle)
		}
	}
}
