package sshproxy

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestCheckTunnelHealth_Success(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	_, err := tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}

	err = tm.CheckTunnelHealth(uint(1), "VNC")
	if err != nil {
		t.Fatalf("CheckTunnelHealth() error: %v", err)
	}

	// Verify metrics were updated
	tm.mu.RLock()
	tunnel := tm.tunnels[uint(1)][0]
	tm.mu.RUnlock()

	snap := tunnel.metrics.Snapshot()
	if snap.SuccessfulChecks != 1 {
		t.Errorf("expected 1 successful check, got %d", snap.SuccessfulChecks)
	}
	if snap.FailedChecks != 0 {
		t.Errorf("expected 0 failed checks, got %d", snap.FailedChecks)
	}
	if snap.LastHealthCheck.IsZero() {
		t.Error("LastHealthCheck should not be zero after successful check")
	}
}

func TestCheckTunnelHealth_NotFound(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	sshMgr := NewSSHManager(signer, "")
	tm := NewTunnelManager(sshMgr)

	err = tm.CheckTunnelHealth(uint(99), "VNC")
	if err == nil {
		t.Fatal("expected error for non-existent tunnel")
	}
}

func TestCheckTunnelHealth_ErrorStatus(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	_, err := tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}

	// Mark the tunnel as error
	tm.mu.Lock()
	tm.tunnels[uint(1)][0].Status = "error"
	tm.mu.Unlock()

	err = tm.CheckTunnelHealth(uint(1), "VNC")
	if err == nil {
		t.Fatal("expected error for tunnel with error status")
	}

	// Verify failure was recorded
	tm.mu.RLock()
	tunnel := tm.tunnels[uint(1)][0]
	tm.mu.RUnlock()

	snap := tunnel.metrics.Snapshot()
	if snap.FailedChecks != 1 {
		t.Errorf("expected 1 failed check, got %d", snap.FailedChecks)
	}
}

func TestCheckTunnelHealth_ClosedListener(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	_, err := tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}

	// Close the tunnel's listener to make it unhealthy
	tm.mu.Lock()
	tun := tm.tunnels[uint(1)][0]
	tun.cancel()
	tun.listener.Close()
	tm.mu.Unlock()

	// Wait for accept loop to exit
	time.Sleep(200 * time.Millisecond)

	// Override timeout for faster test
	origTimeout := tunnelHealthCheckTimeout
	tunnelHealthCheckTimeout = 500 * time.Millisecond
	defer func() { tunnelHealthCheckTimeout = origTimeout }()

	err = tm.CheckTunnelHealth(uint(1), "VNC")
	if err == nil {
		t.Fatal("expected error for tunnel with closed listener")
	}
}

func TestCheckTunnelHealth_MultipleLabels(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	_, err := tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}
	_, err = tm.CreateTunnelForGateway(context.Background(), uint(1), 0)
	if err != nil {
		t.Fatalf("CreateTunnelForGateway() error: %v", err)
	}

	// Both should be healthy
	if err := tm.CheckTunnelHealth(uint(1), "VNC"); err != nil {
		t.Errorf("VNC health check failed: %v", err)
	}
	if err := tm.CheckTunnelHealth(uint(1), "Gateway"); err != nil {
		t.Errorf("Gateway health check failed: %v", err)
	}
}

func TestTunnelMetrics_CreatedAt(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	before := time.Now()
	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	_, err := tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}
	after := time.Now()

	tm.mu.RLock()
	tunnel := tm.tunnels[uint(1)][0]
	tm.mu.RUnlock()

	snap := tunnel.metrics.Snapshot()
	if snap.CreatedAt.Before(before) || snap.CreatedAt.After(after) {
		t.Errorf("CreatedAt %v not between %v and %v", snap.CreatedAt, before, after)
	}
}

func TestTunnelMetrics_Uptime(t *testing.T) {
	m := &TunnelMetrics{CreatedAt: time.Now().Add(-5 * time.Second)}

	uptime := m.Uptime()
	if uptime < 4*time.Second || uptime > 6*time.Second {
		t.Errorf("expected ~5s uptime, got %v", uptime)
	}
}

func TestTunnelMetrics_Uptime_Zero(t *testing.T) {
	m := &TunnelMetrics{}

	uptime := m.Uptime()
	if uptime != 0 {
		t.Errorf("expected 0 uptime for zero CreatedAt, got %v", uptime)
	}
}

func TestTunnelMetrics_Snapshot(t *testing.T) {
	now := time.Now()
	m := &TunnelMetrics{
		CreatedAt:        now,
		LastHealthCheck:  now.Add(1 * time.Second),
		SuccessfulChecks: 5,
		FailedChecks:     2,
	}

	snap := m.Snapshot()

	if snap.CreatedAt != now {
		t.Errorf("CreatedAt mismatch: %v != %v", snap.CreatedAt, now)
	}
	if snap.LastHealthCheck != now.Add(1*time.Second) {
		t.Errorf("LastHealthCheck mismatch")
	}
	if snap.SuccessfulChecks != 5 {
		t.Errorf("SuccessfulChecks = %d, want 5", snap.SuccessfulChecks)
	}
	if snap.FailedChecks != 2 {
		t.Errorf("FailedChecks = %d, want 2", snap.FailedChecks)
	}

	// Verify snapshot is independent from original
	m.recordSuccess()
	if snap.SuccessfulChecks != 5 {
		t.Error("snapshot should be independent from original")
	}
}

func TestTunnelMetrics_RecordSuccessFailure(t *testing.T) {
	m := &TunnelMetrics{CreatedAt: time.Now()}

	m.recordSuccess()
	m.recordSuccess()
	m.recordFailure()

	snap := m.Snapshot()
	if snap.SuccessfulChecks != 2 {
		t.Errorf("SuccessfulChecks = %d, want 2", snap.SuccessfulChecks)
	}
	if snap.FailedChecks != 1 {
		t.Errorf("FailedChecks = %d, want 1", snap.FailedChecks)
	}
	if snap.LastHealthCheck.IsZero() {
		t.Error("LastHealthCheck should not be zero")
	}
}

func TestCheckAllTunnelHealth(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	sshMgr := NewSSHManager(signer, "")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	tm := NewTunnelManager(sshMgr)

	// Create tunnels for two instances
	for _, id := range []uint{1, 2} {
		_, err := sshMgr.Connect(context.Background(), id, host, port)
		if err != nil {
			t.Fatalf("Connect(%d) error: %v", id, err)
		}
		_, err = tm.CreateTunnelForVNC(context.Background(), id)
		if err != nil {
			t.Fatalf("CreateTunnelForVNC(%d) error: %v", id, err)
		}
	}

	// checkAllTunnelHealth should succeed for all
	tm.checkAllTunnelHealth()

	// Verify metrics updated for both
	for _, id := range []uint{1, 2} {
		tm.mu.RLock()
		tunnel := tm.tunnels[id][0]
		tm.mu.RUnlock()

		snap := tunnel.metrics.Snapshot()
		if snap.SuccessfulChecks != 1 {
			t.Errorf("instance %d: expected 1 successful check, got %d", id, snap.SuccessfulChecks)
		}
	}
}

func TestCheckAllTunnelHealth_MixedStatus(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	sshMgr := NewSSHManager(signer, "")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	tm := NewTunnelManager(sshMgr)

	_, err := sshMgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	_, err = tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}
	_, err = tm.CreateTunnelForGateway(context.Background(), uint(1), 0)
	if err != nil {
		t.Fatalf("CreateTunnelForGateway() error: %v", err)
	}

	// Mark Gateway as error — checkAllTunnelHealth should skip it
	tm.mu.Lock()
	for _, tun := range tm.tunnels[uint(1)] {
		if tun.Label == "Gateway" {
			tun.Status = "error"
		}
	}
	tm.mu.Unlock()

	tm.checkAllTunnelHealth()

	// VNC should have 1 successful check
	tm.mu.RLock()
	for _, tun := range tm.tunnels[uint(1)] {
		snap := tun.metrics.Snapshot()
		if tun.Label == "VNC" {
			if snap.SuccessfulChecks != 1 {
				t.Errorf("VNC: expected 1 successful check, got %d", snap.SuccessfulChecks)
			}
		}
	}
	tm.mu.RUnlock()
}

func TestTunnelHealthChecker_Background(t *testing.T) {
	origInterval := tunnelHealthCheckInterval
	tunnelHealthCheckInterval = 50 * time.Millisecond
	defer func() { tunnelHealthCheckInterval = origInterval }()

	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	_, err := tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.StartTunnelHealthChecker(ctx)
	defer tm.StopTunnelHealthChecker()

	// Wait for a few health check cycles
	time.Sleep(300 * time.Millisecond)

	// Verify metrics show successful checks
	tm.mu.RLock()
	tunnel := tm.tunnels[uint(1)][0]
	tm.mu.RUnlock()

	snap := tunnel.metrics.Snapshot()
	if snap.SuccessfulChecks == 0 {
		t.Error("expected >0 successful checks after background health checker ran")
	}
}

func TestStopTunnelHealthChecker(t *testing.T) {
	origInterval := tunnelHealthCheckInterval
	tunnelHealthCheckInterval = 50 * time.Millisecond
	defer func() { tunnelHealthCheckInterval = origInterval }()

	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	_, err := tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.StartTunnelHealthChecker(ctx)

	// Wait for at least one cycle
	time.Sleep(100 * time.Millisecond)

	tm.StopTunnelHealthChecker()

	// Wait for any in-flight check to complete
	time.Sleep(100 * time.Millisecond)

	// Record current check count after stop has taken effect
	tm.mu.RLock()
	tunnel := tm.tunnels[uint(1)][0]
	tm.mu.RUnlock()
	countAfterStop := tunnel.metrics.Snapshot().SuccessfulChecks

	// Wait and verify count doesn't increase
	time.Sleep(200 * time.Millisecond)
	countAfterWait := tunnel.metrics.Snapshot().SuccessfulChecks

	if countAfterWait != countAfterStop {
		t.Errorf("checks continued after stop: before=%d, after=%d", countAfterStop, countAfterWait)
	}

	// Stopping again should be safe (no panic)
	tm.StopTunnelHealthChecker()
}

func TestGetTunnelMetrics(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	_, err := tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}
	_, err = tm.CreateTunnelForGateway(context.Background(), uint(1), 0)
	if err != nil {
		t.Fatalf("CreateTunnelForGateway() error: %v", err)
	}

	// Run a health check to generate metrics
	tm.CheckTunnelHealth(uint(1), "VNC")

	metrics := tm.GetTunnelMetrics(uint(1))
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metric snapshots, got %d", len(metrics))
	}

	labels := map[string]bool{}
	for _, m := range metrics {
		labels[m.Label] = true
		if m.CreatedAt.IsZero() {
			t.Errorf("CreatedAt should not be zero for tunnel %q", m.Label)
		}
		if m.Status != "active" {
			t.Errorf("expected status 'active' for tunnel %q, got '%s'", m.Label, m.Status)
		}
		if m.Uptime <= 0 {
			t.Errorf("expected positive uptime for tunnel %q", m.Label)
		}
		if m.ReconnectionCount != 0 {
			t.Errorf("expected 0 reconnections for tunnel %q, got %d", m.Label, m.ReconnectionCount)
		}
	}

	if !labels["VNC"] {
		t.Error("missing VNC in metrics")
	}
	if !labels["Gateway"] {
		t.Error("missing Gateway in metrics")
	}
}

func TestGetTunnelMetrics_Empty(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	sshMgr := NewSSHManager(signer, "")
	tm := NewTunnelManager(sshMgr)

	metrics := tm.GetTunnelMetrics(uint(99))
	if metrics != nil {
		t.Errorf("expected nil metrics for non-existent instance, got %v", metrics)
	}
}

func TestGetAllTunnelMetrics(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	sshMgr := NewSSHManager(signer, "")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	tm := NewTunnelManager(sshMgr)

	for _, id := range []uint{1, 2, 3} {
		_, err := sshMgr.Connect(context.Background(), id, host, port)
		if err != nil {
			t.Fatalf("Connect(%d) error: %v", id, err)
		}
		_, err = tm.CreateTunnelForVNC(context.Background(), id)
		if err != nil {
			t.Fatalf("CreateTunnelForVNC(%d) error: %v", id, err)
		}
	}

	allMetrics := tm.GetAllTunnelMetrics()
	if len(allMetrics) != 3 {
		t.Fatalf("expected 3 instances in metrics, got %d", len(allMetrics))
	}

	for id, metrics := range allMetrics {
		if len(metrics) != 1 {
			t.Errorf("instance %d: expected 1 tunnel metric, got %d", id, len(metrics))
		}
		if metrics[0].Label != "VNC" {
			t.Errorf("instance %d: expected label 'VNC', got '%s'", id, metrics[0].Label)
		}
	}
}

func TestReconnectionCount(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	sshMgr := NewSSHManager(signer, "ssh-pubkey")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	orch := &tunnelMockOrch{sshAddr: host, sshPort: port}
	tm := NewTunnelManager(sshMgr)

	// Create initial tunnels
	err := tm.StartTunnelsForInstance(context.Background(), uint(1), orch)
	if err != nil {
		t.Fatalf("first StartTunnelsForInstance() error: %v", err)
	}

	// Verify count is 0
	if count := tm.getReconnectCount(uint(1)); count != 0 {
		t.Errorf("expected 0 reconnections, got %d", count)
	}

	// Mark a tunnel as error to force recreation
	tm.mu.Lock()
	tm.tunnels[uint(1)][0].Status = "error"
	tm.mu.Unlock()

	// Recreate tunnels
	err = tm.StartTunnelsForInstance(context.Background(), uint(1), orch)
	if err != nil {
		t.Fatalf("second StartTunnelsForInstance() error: %v", err)
	}

	// Verify count incremented
	if count := tm.getReconnectCount(uint(1)); count != 1 {
		t.Errorf("expected 1 reconnection, got %d", count)
	}

	// Verify metrics include reconnection count
	metrics := tm.GetTunnelMetrics(uint(1))
	for _, m := range metrics {
		if m.ReconnectionCount != 1 {
			t.Errorf("tunnel %q: expected ReconnectionCount=1, got %d", m.Label, m.ReconnectionCount)
		}
	}
}

func TestConcurrentTunnelHealthCheck(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	sshMgr := NewSSHManager(signer, "")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	tm := NewTunnelManager(sshMgr)

	// Create tunnels for multiple instances
	for _, id := range []uint{1, 2, 3} {
		_, err := sshMgr.Connect(context.Background(), id, host, port)
		if err != nil {
			t.Fatalf("Connect(%d) error: %v", id, err)
		}
		_, err = tm.CreateTunnelForVNC(context.Background(), id)
		if err != nil {
			t.Fatalf("CreateTunnelForVNC(%d) error: %v", id, err)
		}
	}

	// Run concurrent health checks
	var wg sync.WaitGroup
	errors := make(chan error, 30)

	for i := 0; i < 10; i++ {
		for _, id := range []uint{1, 2, 3} {
			wg.Add(1)
			go func(instanceID uint) {
				defer wg.Done()
				if err := tm.CheckTunnelHealth(instanceID, "VNC"); err != nil {
					errors <- fmt.Errorf("instance %d: %w", instanceID, err)
				}
			}(id)
		}
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent health check failed: %v", err)
	}

	// All metrics should show successful checks
	for _, id := range []uint{1, 2, 3} {
		tm.mu.RLock()
		tunnel := tm.tunnels[id][0]
		tm.mu.RUnlock()

		snap := tunnel.metrics.Snapshot()
		if snap.SuccessfulChecks != 10 {
			t.Errorf("instance %d: expected 10 successful checks, got %d", id, snap.SuccessfulChecks)
		}
	}
}

func TestTunnelMetrics_ConcurrentAccess(t *testing.T) {
	m := &TunnelMetrics{CreatedAt: time.Now()}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			m.recordSuccess()
		}()
		go func() {
			defer wg.Done()
			m.recordFailure()
		}()
		go func() {
			defer wg.Done()
			_ = m.Snapshot()
			_ = m.Uptime()
		}()
	}

	wg.Wait()

	snap := m.Snapshot()
	if snap.SuccessfulChecks != 20 {
		t.Errorf("expected 20 successful checks, got %d", snap.SuccessfulChecks)
	}
	if snap.FailedChecks != 20 {
		t.Errorf("expected 20 failed checks, got %d", snap.FailedChecks)
	}
}

func TestStopAllStopsHealthChecker(t *testing.T) {
	origInterval := tunnelHealthCheckInterval
	tunnelHealthCheckInterval = 50 * time.Millisecond
	defer func() { tunnelHealthCheckInterval = origInterval }()

	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	_, err := tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}

	ctx := context.Background()
	tm.StartTunnelHealthChecker(ctx)

	// Wait for at least one cycle
	time.Sleep(100 * time.Millisecond)

	// StopAll should also stop health checker
	tm.StopAll()

	if tm.healthCancel != nil {
		t.Error("healthCancel should be nil after StopAll")
	}
}

func TestGetTunnelMetrics_IncludesPortInfo(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	vncPort, err := tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}

	metrics := tm.GetTunnelMetrics(uint(1))
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}

	m := metrics[0]
	if m.LocalPort != vncPort {
		t.Errorf("LocalPort = %d, want %d", m.LocalPort, vncPort)
	}
	if m.RemotePort != 3000 {
		t.Errorf("RemotePort = %d, want 3000", m.RemotePort)
	}
	if m.Label != "VNC" {
		t.Errorf("Label = %q, want %q", m.Label, "VNC")
	}
}
