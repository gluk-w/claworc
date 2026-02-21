package sshtunnel

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
)

func TestNewTunnelManager(t *testing.T) {
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
	tm := NewTunnelManager(sm)

	tunnels := tm.GetTunnels("nonexistent")
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels, got %d", len(tunnels))
	}
}

func TestGetAllTunnelsEmpty(t *testing.T) {
	sm := sshmanager.NewSSHManager()
	tm := NewTunnelManager(sm)

	all := tm.GetAllTunnels()
	if len(all) != 0 {
		t.Errorf("expected empty map, got %d entries", len(all))
	}
}

func TestAddAndGetTunnels(t *testing.T) {
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
	tm := NewTunnelManager(sm)

	// Should not panic
	tm.CloseTunnels("nonexistent")
}

func TestCloseAll(t *testing.T) {
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
	tm := NewTunnelManager(sm)

	ctx := t.Context()
	_, err := tm.CreateTunnelForVNC(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error when no SSH client exists")
	}
}

func TestCreateTunnelForGatewayNoSSHClient(t *testing.T) {
	sm := sshmanager.NewSSHManager()
	tm := NewTunnelManager(sm)

	ctx := t.Context()
	_, err := tm.CreateTunnelForGateway(ctx, "nonexistent", 8080)
	if err == nil {
		t.Error("expected error when no SSH client exists")
	}
}

func TestCreateTunnelForGatewayDefaultPort(t *testing.T) {
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
	tm := NewTunnelManager(sm)

	if tm.monitors == nil {
		t.Error("monitors map not initialized")
	}
	if len(tm.monitors) != 0 {
		t.Errorf("expected empty monitors map, got %d entries", len(tm.monitors))
	}
}

func TestStopTunnelsForInstanceCancelsMonitor(t *testing.T) {
	sm := sshmanager.NewSSHManager()
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
	sm := sshmanager.NewSSHManager()
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

func TestLifecycleConstants(t *testing.T) {
	if defaultHealthCheckInterval <= 0 {
		t.Error("defaultHealthCheckInterval should be positive")
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
