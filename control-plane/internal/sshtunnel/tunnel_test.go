package sshtunnel

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
)

func TestNewTunnelManager(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)
	if tm == nil {
		t.Fatal("NewTunnelManager returned nil")
	}
	if tm.sshManager != sm {
		t.Error("sshManager not set correctly")
	}
	if tm.tunnels == nil {
		t.Error("tunnels map not initialized")
	}
}

func TestTunnelConfig(t *testing.T) {
	cfg := TunnelConfig{
		LocalPort:  8080,
		RemotePort: 3000,
		Type:       TunnelReverse,
		Protocol:   ProtocolTCP,
	}
	if cfg.LocalPort != 8080 {
		t.Errorf("expected LocalPort 8080, got %d", cfg.LocalPort)
	}
	if cfg.RemotePort != 3000 {
		t.Errorf("expected RemotePort 3000, got %d", cfg.RemotePort)
	}
	if cfg.Type != TunnelReverse {
		t.Errorf("expected Type reverse, got %s", cfg.Type)
	}
	if cfg.Protocol != ProtocolTCP {
		t.Errorf("expected Protocol tcp, got %s", cfg.Protocol)
	}
}

func TestTunnelTypes(t *testing.T) {
	if TunnelForward != "forward" {
		t.Errorf("expected TunnelForward = 'forward', got %q", TunnelForward)
	}
	if TunnelReverse != "reverse" {
		t.Errorf("expected TunnelReverse = 'reverse', got %q", TunnelReverse)
	}
	if ProtocolTCP != "tcp" {
		t.Errorf("expected ProtocolTCP = 'tcp', got %q", ProtocolTCP)
	}
	if ProtocolUnix != "unix" {
		t.Errorf("expected ProtocolUnix = 'unix', got %q", ProtocolUnix)
	}
}

func TestActiveTunnelCloseIdempotent(t *testing.T) {
	tunnel := &ActiveTunnel{
		Config: TunnelConfig{
			LocalPort:  0,
			RemotePort: 3000,
			Type:       TunnelReverse,
			Protocol:   ProtocolTCP,
		},
		StartedAt: time.Now(),
	}

	if tunnel.IsClosed() {
		t.Error("new tunnel should not be closed")
	}

	if err := tunnel.Close(); err != nil {
		t.Errorf("first Close() returned error: %v", err)
	}
	if !tunnel.IsClosed() {
		t.Error("tunnel should be closed after Close()")
	}

	// Second close should be a no-op
	if err := tunnel.Close(); err != nil {
		t.Errorf("second Close() returned error: %v", err)
	}
}

func TestActiveTunnelLastCheck(t *testing.T) {
	tunnel := &ActiveTunnel{
		StartedAt: time.Now(),
	}

	checkTime, checkErr := tunnel.LastCheck()
	if !checkTime.IsZero() {
		t.Error("expected zero time for new tunnel")
	}
	if checkErr != nil {
		t.Error("expected nil error for new tunnel")
	}
}

func TestGetTunnelsEmpty(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	tunnels := tm.GetTunnels("nonexistent")
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels, got %d", len(tunnels))
	}
}

func TestGetAllTunnelsEmpty(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	all := tm.GetAllTunnels()
	if len(all) != 0 {
		t.Errorf("expected empty map, got %d entries", len(all))
	}
}

func TestAddAndGetTunnels(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	tunnel := &ActiveTunnel{
		Config: TunnelConfig{
			LocalPort:  8080,
			RemotePort: 3000,
			Type:       TunnelReverse,
			Protocol:   ProtocolTCP,
		},
		LocalPort: 8080,
		StartedAt: time.Now(),
	}

	tm.addTunnel("test-instance", tunnel)

	tunnels := tm.GetTunnels("test-instance")
	if len(tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(tunnels))
	}
	if tunnels[0].LocalPort != 8080 {
		t.Errorf("expected LocalPort 8080, got %d", tunnels[0].LocalPort)
	}
	if tunnels[0].Config.RemotePort != 3000 {
		t.Errorf("expected RemotePort 3000, got %d", tunnels[0].Config.RemotePort)
	}
}

func TestAddMultipleTunnels(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	t1 := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: 3000, Type: TunnelReverse, Protocol: ProtocolTCP},
		LocalPort: 8080,
		StartedAt: time.Now(),
	}
	t2 := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: 8080, Type: TunnelReverse, Protocol: ProtocolTCP},
		LocalPort: 9090,
		StartedAt: time.Now(),
	}

	tm.addTunnel("instance-a", t1)
	tm.addTunnel("instance-a", t2)
	tm.addTunnel("instance-b", &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: 3000, Type: TunnelReverse, Protocol: ProtocolTCP},
		LocalPort: 7070,
		StartedAt: time.Now(),
	})

	tunnelsA := tm.GetTunnels("instance-a")
	if len(tunnelsA) != 2 {
		t.Errorf("expected 2 tunnels for instance-a, got %d", len(tunnelsA))
	}

	tunnelsB := tm.GetTunnels("instance-b")
	if len(tunnelsB) != 1 {
		t.Errorf("expected 1 tunnel for instance-b, got %d", len(tunnelsB))
	}

	all := tm.GetAllTunnels()
	if len(all) != 2 {
		t.Errorf("expected 2 instances in GetAllTunnels, got %d", len(all))
	}
}

func TestCloseTunnels(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	tunnel := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: 3000, Type: TunnelReverse, Protocol: ProtocolTCP},
		LocalPort: 8080,
		StartedAt: time.Now(),
	}
	tm.addTunnel("test-instance", tunnel)

	tm.CloseTunnels("test-instance")

	if !tunnel.IsClosed() {
		t.Error("tunnel should be closed")
	}

	tunnels := tm.GetTunnels("test-instance")
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels after CloseTunnels, got %d", len(tunnels))
	}
}

func TestCloseTunnelsNonexistent(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Should not panic
	tm.CloseTunnels("nonexistent")
}

func TestCloseAll(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	t1 := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: 3000, Type: TunnelReverse, Protocol: ProtocolTCP},
		LocalPort: 8080,
		StartedAt: time.Now(),
	}
	t2 := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: 8080, Type: TunnelReverse, Protocol: ProtocolTCP},
		LocalPort: 9090,
		StartedAt: time.Now(),
	}

	tm.addTunnel("instance-a", t1)
	tm.addTunnel("instance-b", t2)

	tm.CloseAll()

	if !t1.IsClosed() {
		t.Error("t1 should be closed")
	}
	if !t2.IsClosed() {
		t.Error("t2 should be closed")
	}

	all := tm.GetAllTunnels()
	if len(all) != 0 {
		t.Errorf("expected empty map after CloseAll, got %d entries", len(all))
	}
}

func TestRemoveClosed(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	open := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: 3000, Type: TunnelReverse, Protocol: ProtocolTCP},
		LocalPort: 8080,
		StartedAt: time.Now(),
	}
	closed := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: 8080, Type: TunnelReverse, Protocol: ProtocolTCP},
		LocalPort: 9090,
		StartedAt: time.Now(),
		closed:    true,
	}

	tm.addTunnel("test", open)
	tm.addTunnel("test", closed)

	tm.removeClosed("test")

	tunnels := tm.GetTunnels("test")
	if len(tunnels) != 1 {
		t.Fatalf("expected 1 tunnel after removeClosed, got %d", len(tunnels))
	}
	if tunnels[0].LocalPort != 8080 {
		t.Errorf("expected the open tunnel (port 8080), got %d", tunnels[0].LocalPort)
	}
}

func TestRemoveClosedAllClosed(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	closed := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: 3000, Type: TunnelReverse, Protocol: ProtocolTCP},
		LocalPort: 8080,
		StartedAt: time.Now(),
		closed:    true,
	}

	tm.addTunnel("test", closed)
	tm.removeClosed("test")

	tunnels := tm.GetTunnels("test")
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels, got %d", len(tunnels))
	}

	// Verify instance removed from map entirely
	all := tm.GetAllTunnels()
	if _, exists := all["test"]; exists {
		t.Error("instance should be removed from map when all tunnels are closed")
	}
}

func TestCreateReverseTunnelNoSSHClient(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	ctx := t.Context()
	_, err := tm.CreateReverseTunnel(ctx, "nonexistent", 3000, 0)
	if err == nil {
		t.Error("expected error when no SSH client exists")
	}
}

func TestServiceLabelConstants(t *testing.T) {
	if ServiceVNC != "vnc" {
		t.Errorf("expected ServiceVNC = 'vnc', got %q", ServiceVNC)
	}
	if ServiceGateway != "gateway" {
		t.Errorf("expected ServiceGateway = 'gateway', got %q", ServiceGateway)
	}
	if ServiceCustom != "custom" {
		t.Errorf("expected ServiceCustom = 'custom', got %q", ServiceCustom)
	}
}

func TestDefaultPorts(t *testing.T) {
	if DefaultVNCPort != 3000 {
		t.Errorf("expected DefaultVNCPort = 3000, got %d", DefaultVNCPort)
	}
	if DefaultGatewayPort != 8080 {
		t.Errorf("expected DefaultGatewayPort = 8080, got %d", DefaultGatewayPort)
	}
}

func TestCreateTunnelForVNCNoSSHClient(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	ctx := t.Context()
	_, err := tm.CreateTunnelForVNC(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error when no SSH client exists")
	}
}

func TestCreateTunnelForGatewayNoSSHClient(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	ctx := t.Context()
	_, err := tm.CreateTunnelForGateway(ctx, "nonexistent", 8080)
	if err == nil {
		t.Error("expected error when no SSH client exists")
	}
}

func TestCreateTunnelForGatewayDefaultPort(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	ctx := t.Context()
	// With zero port, should default to DefaultGatewayPort but still fail due to no SSH client
	_, err := tm.CreateTunnelForGateway(ctx, "nonexistent", 0)
	if err == nil {
		t.Error("expected error when no SSH client exists")
	}

	// With negative port, should also default
	_, err = tm.CreateTunnelForGateway(ctx, "nonexistent", -1)
	if err == nil {
		t.Error("expected error when no SSH client exists")
	}
}

func TestTunnelConfigService(t *testing.T) {
	cfg := TunnelConfig{
		LocalPort:  0,
		RemotePort: 3000,
		Type:       TunnelReverse,
		Protocol:   ProtocolTCP,
		Service:    ServiceVNC,
	}
	if cfg.Service != ServiceVNC {
		t.Errorf("expected Service vnc, got %s", cfg.Service)
	}
}

func TestGetTunnelsReturnsCopy(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	tunnel := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: 3000, Type: TunnelReverse, Protocol: ProtocolTCP},
		LocalPort: 8080,
		StartedAt: time.Now(),
	}
	tm.addTunnel("test", tunnel)

	// Modifying the returned slice should not affect internal state
	tunnels := tm.GetTunnels("test")
	tunnels = append(tunnels, &ActiveTunnel{})

	internal := tm.GetTunnels("test")
	if len(internal) != 1 {
		t.Errorf("internal state was modified, expected 1 tunnel, got %d", len(internal))
	}
}

// --- Lifecycle management tests ---

func TestStartTunnelsForInstanceNoSSHClient(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	err := tm.StartTunnelsForInstance(t.Context(), "no-client")
	if err == nil {
		t.Fatal("expected error when no SSH client exists")
	}

	// No tunnels should remain on failure
	tunnels := tm.GetTunnels("no-client")
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels after failed start, got %d", len(tunnels))
	}
}

func TestStopTunnelsForInstance(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Manually add tunnels and a monitor
	vncTunnel := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: DefaultVNCPort, Type: TunnelReverse, Protocol: ProtocolTCP, Service: ServiceVNC},
		LocalPort: 12345,
		StartedAt: time.Now(),
	}
	gwTunnel := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: DefaultGatewayPort, Type: TunnelReverse, Protocol: ProtocolTCP, Service: ServiceGateway},
		LocalPort: 12346,
		StartedAt: time.Now(),
	}
	tm.addTunnel("test-instance", vncTunnel)
	tm.addTunnel("test-instance", gwTunnel)

	// Register a mock monitor cancel function
	monCtx, monCancel := context.WithCancel(t.Context())
	tm.monMu.Lock()
	tm.monitors["test-instance"] = monCancel
	tm.monMu.Unlock()

	err := tm.StopTunnelsForInstance("test-instance")
	if err != nil {
		t.Fatalf("StopTunnelsForInstance returned error: %v", err)
	}

	// Tunnels should be closed
	if !vncTunnel.IsClosed() {
		t.Error("VNC tunnel should be closed")
	}
	if !gwTunnel.IsClosed() {
		t.Error("gateway tunnel should be closed")
	}

	// No tunnels should remain
	tunnels := tm.GetTunnels("test-instance")
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels after stop, got %d", len(tunnels))
	}

	// Monitor context should be cancelled
	select {
	case <-monCtx.Done():
		// expected
	default:
		t.Error("monitor context should be cancelled")
	}

	// Monitor should be removed from map
	tm.monMu.Lock()
	_, exists := tm.monitors["test-instance"]
	tm.monMu.Unlock()
	if exists {
		t.Error("monitor should be removed from monitors map")
	}
}

func TestStopTunnelsForInstanceNonexistent(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Should not panic or error
	err := tm.StopTunnelsForInstance("nonexistent")
	if err != nil {
		t.Errorf("StopTunnelsForInstance returned error for nonexistent instance: %v", err)
	}
}

func TestUpdateHealthCheck(t *testing.T) {
	tunnel := &ActiveTunnel{
		StartedAt: time.Now(),
	}

	// Initially zero
	checkTime, checkErr := tunnel.LastCheck()
	if !checkTime.IsZero() {
		t.Error("expected zero lastCheck for new tunnel")
	}
	if checkErr != nil {
		t.Error("expected nil lastError for new tunnel")
	}

	// Update with nil error (healthy)
	tunnel.updateHealthCheck(nil)
	checkTime, checkErr = tunnel.LastCheck()
	if checkTime.IsZero() {
		t.Error("lastCheck should be updated after updateHealthCheck")
	}
	if checkErr != nil {
		t.Error("lastError should be nil after healthy check")
	}

	// Update with an error
	testErr := fmt.Errorf("connection lost")
	tunnel.updateHealthCheck(testErr)
	checkTime2, checkErr2 := tunnel.LastCheck()
	if checkTime2.Before(checkTime) {
		t.Error("lastCheck should advance after second updateHealthCheck")
	}
	if checkErr2 == nil || checkErr2.Error() != "connection lost" {
		t.Errorf("expected 'connection lost' error, got %v", checkErr2)
	}
}

func TestCheckAndReconnectTunnelsAllPresent(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Add both VNC and Gateway tunnels manually
	vncTunnel := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: DefaultVNCPort, Type: TunnelReverse, Protocol: ProtocolTCP, Service: ServiceVNC},
		LocalPort: 12345,
		StartedAt: time.Now(),
	}
	gwTunnel := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: DefaultGatewayPort, Type: TunnelReverse, Protocol: ProtocolTCP, Service: ServiceGateway},
		LocalPort: 12346,
		StartedAt: time.Now(),
	}
	tm.addTunnel("test", vncTunnel)
	tm.addTunnel("test", gwTunnel)

	// With both tunnels present, no reconnection should be attempted (no SSH client needed)
	tm.checkAndReconnectTunnels(t.Context(), "test")

	// Tunnels should still be there and healthy
	tunnels := tm.GetTunnels("test")
	if len(tunnels) != 2 {
		t.Errorf("expected 2 tunnels, got %d", len(tunnels))
	}

	// Health checks should be updated
	checkTime, checkErr := vncTunnel.LastCheck()
	if checkTime.IsZero() {
		t.Error("VNC tunnel lastCheck should be updated")
	}
	if checkErr != nil {
		t.Errorf("VNC tunnel should have nil error, got %v", checkErr)
	}
}

func TestCheckAndReconnectTunnelsMissingNoSSH(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// No tunnels and no SSH client - should log but not panic
	tm.checkAndReconnectTunnels(t.Context(), "test")

	// Still no tunnels since reconnection can't happen without SSH client
	tunnels := tm.GetTunnels("test")
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels without SSH client, got %d", len(tunnels))
	}
}

func TestCheckAndReconnectTunnelsClosedRemoved(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Add a closed tunnel and an open one
	closedTunnel := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: DefaultVNCPort, Type: TunnelReverse, Protocol: ProtocolTCP, Service: ServiceVNC},
		LocalPort: 12345,
		StartedAt: time.Now(),
		closed:    true,
	}
	openTunnel := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: DefaultGatewayPort, Type: TunnelReverse, Protocol: ProtocolTCP, Service: ServiceGateway},
		LocalPort: 12346,
		StartedAt: time.Now(),
	}
	tm.addTunnel("test", closedTunnel)
	tm.addTunnel("test", openTunnel)

	// Without SSH client, closed tunnels get removed but reconnection is skipped
	tm.checkAndReconnectTunnels(t.Context(), "test")

	tunnels := tm.GetTunnels("test")
	if len(tunnels) != 1 {
		t.Fatalf("expected 1 tunnel after cleanup, got %d", len(tunnels))
	}
	if tunnels[0].Config.Service != ServiceGateway {
		t.Errorf("expected remaining tunnel to be gateway, got %s", tunnels[0].Config.Service)
	}
}

func TestReconnectTunnelContextCancelled(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Cancel immediately

	// Should return immediately without blocking
	tm.reconnectTunnel(ctx, "test", ServiceVNC)

	// No tunnels should be created (no SSH client and context is cancelled)
	tunnels := tm.GetTunnels("test")
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels, got %d", len(tunnels))
	}
}

func TestMonitorInstanceStopsOnContextCancel(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})
	go func() {
		tm.monitorInstance(ctx, "test")
		close(done)
	}()

	// Cancel the context and verify the monitor exits
	cancel()

	select {
	case <-done:
		// Monitor exited as expected
	case <-time.After(2 * time.Second):
		t.Fatal("monitorInstance did not exit after context cancellation")
	}
}

func TestNewTunnelManagerInitializesMonitors(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	if tm.monitors == nil {
		t.Error("monitors map not initialized")
	}
	if len(tm.monitors) != 0 {
		t.Errorf("expected empty monitors map, got %d entries", len(tm.monitors))
	}
}

func TestStopTunnelsForInstanceCancelsMonitor(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Start a fake monitor goroutine
	ctx, cancel := context.WithCancel(t.Context())
	tm.monMu.Lock()
	tm.monitors["test-instance"] = cancel
	tm.monMu.Unlock()

	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(done)
	}()

	_ = tm.StopTunnelsForInstance("test-instance")

	select {
	case <-done:
		// Monitor was cancelled as expected
	case <-time.After(2 * time.Second):
		t.Fatal("monitor context was not cancelled by StopTunnelsForInstance")
	}
}

func TestStartTunnelsForInstanceReplacesExistingMonitor(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Register an existing monitor
	oldCtx, oldCancel := context.WithCancel(t.Context())
	tm.monMu.Lock()
	tm.monitors["test-instance"] = oldCancel
	tm.monMu.Unlock()

	// StartTunnelsForInstance will fail (no SSH client) but should still
	// try to replace the monitor. Since it fails before reaching monitor setup,
	// the old monitor should remain.
	_ = tm.StartTunnelsForInstance(t.Context(), "test-instance")

	// The old monitor was not replaced because Start failed before setting monitor
	select {
	case <-oldCtx.Done():
		t.Error("old monitor context should NOT have been cancelled on early failure")
	default:
		// expected - the old context is still active
	}
}

func TestShutdownEmpty(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)
	// Should not panic on empty state
	tm.Shutdown()
}

func TestShutdownClosesAllTunnelsAndMonitors(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Add tunnels for two instances
	t1 := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: DefaultVNCPort, Type: TunnelReverse, Protocol: ProtocolTCP, Service: ServiceVNC},
		LocalPort: 12345,
		StartedAt: time.Now(),
	}
	t2 := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: DefaultGatewayPort, Type: TunnelReverse, Protocol: ProtocolTCP, Service: ServiceGateway},
		LocalPort: 12346,
		StartedAt: time.Now(),
	}
	t3 := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: DefaultVNCPort, Type: TunnelReverse, Protocol: ProtocolTCP, Service: ServiceVNC},
		LocalPort: 12347,
		StartedAt: time.Now(),
	}
	tm.addTunnel("instance-a", t1)
	tm.addTunnel("instance-a", t2)
	tm.addTunnel("instance-b", t3)

	// Add mock monitors
	monCtxA, monCancelA := context.WithCancel(t.Context())
	monCtxB, monCancelB := context.WithCancel(t.Context())
	tm.monMu.Lock()
	tm.monitors["instance-a"] = monCancelA
	tm.monitors["instance-b"] = monCancelB
	tm.monMu.Unlock()

	tm.Shutdown()

	// All tunnels should be closed
	if !t1.IsClosed() {
		t.Error("t1 should be closed")
	}
	if !t2.IsClosed() {
		t.Error("t2 should be closed")
	}
	if !t3.IsClosed() {
		t.Error("t3 should be closed")
	}

	// All tunnels should be removed
	all := tm.GetAllTunnels()
	if len(all) != 0 {
		t.Errorf("expected empty map after Shutdown, got %d entries", len(all))
	}

	// Monitor contexts should be cancelled
	select {
	case <-monCtxA.Done():
		// expected
	default:
		t.Error("monitor context A should be cancelled")
	}
	select {
	case <-monCtxB.Done():
		// expected
	default:
		t.Error("monitor context B should be cancelled")
	}

	// Monitors map should be empty
	tm.monMu.Lock()
	monLen := len(tm.monitors)
	tm.monMu.Unlock()
	if monLen != 0 {
		t.Errorf("expected 0 monitors after Shutdown, got %d", monLen)
	}
}

func TestShutdownIdempotent(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	tunnel := &ActiveTunnel{
		Config:    TunnelConfig{RemotePort: 3000, Type: TunnelReverse, Protocol: ProtocolTCP},
		LocalPort: 8080,
		StartedAt: time.Now(),
	}
	tm.addTunnel("test", tunnel)

	tm.Shutdown()
	// Second call should not panic
	tm.Shutdown()
}

func TestLifecycleConstants(t *testing.T) {
	if defaultHealthCheckInterval <= 0 {
		t.Error("defaultHealthCheckInterval should be positive")
	}
	if tunnelHealthCheckInterval <= 0 {
		t.Error("tunnelHealthCheckInterval should be positive")
	}
	if tunnelHealthProbeTimeout <= 0 {
		t.Error("tunnelHealthProbeTimeout should be positive")
	}
	if tunnelHealthCheckInterval < defaultHealthCheckInterval {
		t.Error("tunnelHealthCheckInterval should be >= defaultHealthCheckInterval")
	}
	if reconnectBaseDelay <= 0 {
		t.Error("reconnectBaseDelay should be positive")
	}
	if reconnectMaxDelay < reconnectBaseDelay {
		t.Error("reconnectMaxDelay should be >= reconnectBaseDelay")
	}
	if reconnectBackoffFactor < 2 {
		t.Error("reconnectBackoffFactor should be >= 2")
	}
}

// --- Tunnel health monitoring tests ---

func TestTunnelMetricsType(t *testing.T) {
	m := TunnelMetrics{
		Service:             ServiceVNC,
		LocalPort:           12345,
		RemotePort:          3000,
		CreatedAt:           time.Now(),
		LastCheck:           time.Now(),
		LastSuccessfulCheck: time.Now(),
		BytesTransferred:    1024,
		Healthy:             true,
	}
	if m.Service != ServiceVNC {
		t.Errorf("expected service vnc, got %s", m.Service)
	}
	if m.LocalPort != 12345 {
		t.Errorf("expected local port 12345, got %d", m.LocalPort)
	}
	if m.BytesTransferred != 1024 {
		t.Errorf("expected 1024 bytes, got %d", m.BytesTransferred)
	}
}

func TestActiveTunnelMetrics(t *testing.T) {
	now := time.Now()
	tunnel := &ActiveTunnel{
		Config: TunnelConfig{
			RemotePort: 3000,
			Type:       TunnelReverse,
			Protocol:   ProtocolTCP,
			Service:    ServiceVNC,
		},
		LocalPort: 12345,
		StartedAt: now,
	}

	// Initial metrics
	m := tunnel.Metrics()
	if m.Service != ServiceVNC {
		t.Errorf("expected service vnc, got %s", m.Service)
	}
	if m.LocalPort != 12345 {
		t.Errorf("expected local port 12345, got %d", m.LocalPort)
	}
	if m.RemotePort != 3000 {
		t.Errorf("expected remote port 3000, got %d", m.RemotePort)
	}
	if m.CreatedAt != now {
		t.Error("CreatedAt should match StartedAt")
	}
	if !m.Healthy {
		t.Error("new tunnel should be healthy")
	}
	if m.BytesTransferred != 0 {
		t.Errorf("expected 0 bytes transferred, got %d", m.BytesTransferred)
	}
	if !m.LastCheck.IsZero() {
		t.Error("expected zero LastCheck for new tunnel")
	}
	if m.LastError != "" {
		t.Errorf("expected empty LastError, got %q", m.LastError)
	}
}

func TestActiveTunnelMetricsAfterHealthCheck(t *testing.T) {
	tunnel := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceGateway, RemotePort: 8080},
		LocalPort: 9090,
		StartedAt: time.Now(),
	}

	// Successful health check
	tunnel.updateHealthCheck(nil)
	m := tunnel.Metrics()
	if m.LastCheck.IsZero() {
		t.Error("LastCheck should be set after health check")
	}
	if m.LastSuccessfulCheck.IsZero() {
		t.Error("LastSuccessfulCheck should be set after successful check")
	}
	if !m.Healthy {
		t.Error("tunnel should be healthy after successful check")
	}

	// Failed health check
	tunnel.updateHealthCheck(fmt.Errorf("connection refused"))
	m = tunnel.Metrics()
	if m.LastError != "connection refused" {
		t.Errorf("expected 'connection refused' error, got %q", m.LastError)
	}
	if m.Healthy {
		t.Error("tunnel should be unhealthy after failed check")
	}
	// LastSuccessfulCheck should still be set from the previous successful check
	if m.LastSuccessfulCheck.IsZero() {
		t.Error("LastSuccessfulCheck should retain value from previous success")
	}
}

func TestActiveTunnelMetricsClosed(t *testing.T) {
	tunnel := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceVNC, RemotePort: 3000},
		LocalPort: 12345,
		StartedAt: time.Now(),
	}
	tunnel.Close()

	m := tunnel.Metrics()
	if m.Healthy {
		t.Error("closed tunnel should not be healthy")
	}
}

func TestUpdateHealthCheckSetsLastSuccessfulCheck(t *testing.T) {
	tunnel := &ActiveTunnel{StartedAt: time.Now()}

	// Successful check should set lastSuccessfulCheck
	tunnel.updateHealthCheck(nil)
	tunnel.mu.Lock()
	if tunnel.lastSuccessfulCheck.IsZero() {
		t.Error("lastSuccessfulCheck should be set after successful check")
	}
	firstSuccess := tunnel.lastSuccessfulCheck
	tunnel.mu.Unlock()

	time.Sleep(time.Millisecond)

	// Failed check should NOT update lastSuccessfulCheck
	tunnel.updateHealthCheck(fmt.Errorf("fail"))
	tunnel.mu.Lock()
	if tunnel.lastSuccessfulCheck != firstSuccess {
		t.Error("lastSuccessfulCheck should not change on failed check")
	}
	tunnel.mu.Unlock()

	time.Sleep(time.Millisecond)

	// Another success should update lastSuccessfulCheck
	tunnel.updateHealthCheck(nil)
	tunnel.mu.Lock()
	if !tunnel.lastSuccessfulCheck.After(firstSuccess) {
		t.Error("lastSuccessfulCheck should advance after new success")
	}
	tunnel.mu.Unlock()
}

func TestAddBytesTransferred(t *testing.T) {
	tunnel := &ActiveTunnel{StartedAt: time.Now()}

	tunnel.addBytesTransferred(100)
	tunnel.addBytesTransferred(200)
	tunnel.addBytesTransferred(50)

	m := tunnel.Metrics()
	if m.BytesTransferred != 350 {
		t.Errorf("expected 350 bytes, got %d", m.BytesTransferred)
	}
}

func TestCountingConnRead(t *testing.T) {
	tunnel := &ActiveTunnel{StartedAt: time.Now()}

	// Create a pipe to simulate connection
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	counted := &countingConn{Conn: client, tunnel: tunnel}

	// Write data from the server side
	testData := []byte("hello world")
	go func() {
		server.Write(testData)
	}()

	buf := make([]byte, 64)
	n, err := counted.Read(buf)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if n != len(testData) {
		t.Errorf("expected %d bytes read, got %d", len(testData), n)
	}
	if !bytes.Equal(buf[:n], testData) {
		t.Errorf("data mismatch: got %q", buf[:n])
	}

	m := tunnel.Metrics()
	if m.BytesTransferred != int64(len(testData)) {
		t.Errorf("expected %d bytes transferred, got %d", len(testData), m.BytesTransferred)
	}
}

func TestCountingConnWrite(t *testing.T) {
	tunnel := &ActiveTunnel{StartedAt: time.Now()}

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	counted := &countingConn{Conn: client, tunnel: tunnel}

	testData := []byte("hello tunnel")
	go func() {
		buf := make([]byte, 64)
		server.Read(buf)
	}()

	n, err := counted.Write(testData)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(testData) {
		t.Errorf("expected %d bytes written, got %d", len(testData), n)
	}

	m := tunnel.Metrics()
	if m.BytesTransferred != int64(len(testData)) {
		t.Errorf("expected %d bytes transferred, got %d", len(testData), m.BytesTransferred)
	}
}

func TestCountingConnBidirectional(t *testing.T) {
	tunnel := &ActiveTunnel{StartedAt: time.Now()}

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	counted := &countingConn{Conn: client, tunnel: tunnel}

	// Write from counted side
	writeData := []byte("request")
	go func() {
		buf := make([]byte, 64)
		server.Read(buf)
		server.Write([]byte("response"))
	}()

	counted.Write(writeData)

	buf := make([]byte, 64)
	n, _ := counted.Read(buf)

	m := tunnel.Metrics()
	expected := int64(len(writeData)) + int64(n)
	if m.BytesTransferred != expected {
		t.Errorf("expected %d total bytes, got %d", expected, m.BytesTransferred)
	}
}

func TestCheckTunnelHealthWithListener(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Start a real TCP listener to simulate tunnel's local port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create test listener: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	// Accept connections in background (simulates tunnel accept loop)
	go func() {
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
	tm.addTunnel("test-instance", tunnel)

	err = tm.CheckTunnelHealth("test-instance", "vnc")
	if err != nil {
		t.Errorf("expected healthy tunnel, got error: %v", err)
	}

	// Verify health check was recorded
	m := tunnel.Metrics()
	if m.LastCheck.IsZero() {
		t.Error("LastCheck should be updated after health check")
	}
	if m.LastSuccessfulCheck.IsZero() {
		t.Error("LastSuccessfulCheck should be set after successful check")
	}
}

func TestCheckTunnelHealthNoTunnel(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	err := tm.CheckTunnelHealth("nonexistent", "vnc")
	if err == nil {
		t.Error("expected error when no tunnel exists")
	}
}

func TestCheckTunnelHealthClosedTunnel(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	tunnel := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceVNC, RemotePort: 3000},
		LocalPort: 12345,
		StartedAt: time.Now(),
		closed:    true,
	}
	tm.addTunnel("test-instance", tunnel)

	err := tm.CheckTunnelHealth("test-instance", "vnc")
	if err == nil {
		t.Error("expected error for closed tunnel")
	}
}

func TestCheckTunnelHealthWrongType(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	tunnel := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceVNC, RemotePort: 3000},
		LocalPort: 12345,
		StartedAt: time.Now(),
	}
	tm.addTunnel("test-instance", tunnel)

	err := tm.CheckTunnelHealth("test-instance", "gateway")
	if err == nil {
		t.Error("expected error when tunnel type doesn't match")
	}
}

func TestCheckTunnelHealthDeadPort(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Use a port that's not listening
	tunnel := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceVNC, RemotePort: 3000},
		LocalPort: 19999, // unlikely to be in use
		StartedAt: time.Now(),
	}
	tm.addTunnel("test-instance", tunnel)

	err := tm.CheckTunnelHealth("test-instance", "vnc")
	if err == nil {
		t.Error("expected error for dead port")
	}

	// Verify health check failure was recorded
	m := tunnel.Metrics()
	if m.LastError == "" {
		t.Error("LastError should be set after failed health check")
	}
	if m.Healthy {
		t.Error("tunnel should be unhealthy after failed check")
	}
}

func TestProbeTunnelPortHealthy(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create test listener: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	tunnel := &ActiveTunnel{
		LocalPort: port,
		StartedAt: time.Now(),
	}

	err = tm.probeTunnelPort(tunnel)
	if err != nil {
		t.Errorf("expected no error for listening port, got: %v", err)
	}
}

func TestProbeTunnelPortUnhealthy(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	tunnel := &ActiveTunnel{
		LocalPort: 19998, // not listening
		StartedAt: time.Now(),
	}

	err := tm.probeTunnelPort(tunnel)
	if err == nil {
		t.Error("expected error for non-listening port")
	}
}

func TestGetTunnelMetrics(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	t1 := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceVNC, RemotePort: 3000},
		LocalPort: 12345,
		StartedAt: time.Now(),
	}
	t2 := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceGateway, RemotePort: 8080},
		LocalPort: 12346,
		StartedAt: time.Now(),
	}
	tm.addTunnel("test", t1)
	tm.addTunnel("test", t2)

	metrics := tm.GetTunnelMetrics("test")
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}
	if metrics[0].Service != ServiceVNC {
		t.Errorf("expected first metric service vnc, got %s", metrics[0].Service)
	}
	if metrics[1].Service != ServiceGateway {
		t.Errorf("expected second metric service gateway, got %s", metrics[1].Service)
	}
}

func TestGetTunnelMetricsEmpty(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	metrics := tm.GetTunnelMetrics("nonexistent")
	if len(metrics) != 0 {
		t.Errorf("expected 0 metrics, got %d", len(metrics))
	}
}

func TestReconnectionCount(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Initial count should be zero
	count := tm.GetReconnectionCount("test")
	if count != 0 {
		t.Errorf("expected 0 reconnections, got %d", count)
	}

	// Increment
	tm.incrementReconnects("test")
	tm.incrementReconnects("test")
	tm.incrementReconnects("test")

	count = tm.GetReconnectionCount("test")
	if count != 3 {
		t.Errorf("expected 3 reconnections, got %d", count)
	}

	// Different instance
	tm.incrementReconnects("other")
	if tm.GetReconnectionCount("other") != 1 {
		t.Errorf("expected 1 reconnection for other, got %d", tm.GetReconnectionCount("other"))
	}
	if tm.GetReconnectionCount("test") != 3 {
		t.Errorf("test reconnections should still be 3")
	}
}

func TestNewTunnelManagerInitializesHealthCheck(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	if tm.healthCtx == nil {
		t.Error("healthCtx not initialized")
	}
	if tm.healthCancel == nil {
		t.Error("healthCancel not initialized")
	}
	if tm.reconnects == nil {
		t.Error("reconnects map not initialized")
	}
}

func TestShutdownStopsGlobalHealthCheck(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Save context to check it's cancelled
	ctx := tm.healthCtx

	tm.Shutdown()

	select {
	case <-ctx.Done():
		// expected - health check goroutine should be stopped
	default:
		t.Error("health check context should be cancelled after Shutdown")
	}
}

func TestRunGlobalHealthCheckHealthy(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Start a real listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	go func() {
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

	tm.runGlobalHealthCheck()

	// Tunnel should still be open and healthy
	if tunnel.IsClosed() {
		t.Error("healthy tunnel should not be closed")
	}
	m := tunnel.Metrics()
	if !m.LastSuccessfulCheck.IsZero() == false {
		t.Error("LastSuccessfulCheck should be set")
	}
	if !m.Healthy {
		t.Error("tunnel should be healthy")
	}
}

func TestRunGlobalHealthCheckUnhealthy(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Create a tunnel pointing to a dead port
	tunnel := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceVNC, RemotePort: 3000},
		LocalPort: 19997, // not listening
		StartedAt: time.Now(),
	}
	tm.addTunnel("test", tunnel)

	tm.runGlobalHealthCheck()

	// Tunnel should be closed by the health check
	if !tunnel.IsClosed() {
		t.Error("unhealthy tunnel should be closed by global health check")
	}
}

func TestRunGlobalHealthCheckSkipsClosed(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	tunnel := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceVNC, RemotePort: 3000},
		LocalPort: 19996,
		StartedAt: time.Now(),
		closed:    true,
	}
	tm.addTunnel("test", tunnel)

	// Should not panic on closed tunnels
	tm.runGlobalHealthCheck()
}

func TestRunGlobalHealthCheckEmpty(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Should not panic with no tunnels
	tm.runGlobalHealthCheck()
}

func TestRunGlobalHealthCheckMultipleInstances(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Start two listeners
	listener1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener1: %v", err)
	}
	defer listener1.Close()
	port1 := listener1.Addr().(*net.TCPAddr).Port

	listener2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener2: %v", err)
	}
	defer listener2.Close()
	port2 := listener2.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := listener1.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	go func() {
		for {
			conn, err := listener2.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	t1 := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceVNC, RemotePort: 3000},
		LocalPort: port1,
		StartedAt: time.Now(),
	}
	t2 := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceGateway, RemotePort: 8080},
		LocalPort: port2,
		StartedAt: time.Now(),
	}
	// Dead tunnel
	t3 := &ActiveTunnel{
		Config:    TunnelConfig{Service: ServiceVNC, RemotePort: 3000},
		LocalPort: 19995, // not listening
		StartedAt: time.Now(),
	}

	tm.addTunnel("instance-a", t1)
	tm.addTunnel("instance-a", t2)
	tm.addTunnel("instance-b", t3)

	tm.runGlobalHealthCheck()

	// instance-a tunnels should be healthy
	if t1.IsClosed() {
		t.Error("t1 should still be open")
	}
	if t2.IsClosed() {
		t.Error("t2 should still be open")
	}

	// instance-b tunnel should be closed (dead port)
	if !t3.IsClosed() {
		t.Error("t3 should be closed (dead port)")
	}
}

func TestGlobalHealthCheckLoopStopsOnCancel(t *testing.T) {
	sm := sshmanager.NewSSHManager(0)
	tm := NewTunnelManager(sm)

	// Shutdown should stop the health check loop
	done := make(chan struct{})
	go func() {
		tm.Shutdown()
		close(done)
	}()

	select {
	case <-done:
		// Shutdown completed, health check loop exited
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not complete (health check loop stuck)")
	}
}
