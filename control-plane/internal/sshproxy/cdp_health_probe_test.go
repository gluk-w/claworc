package sshproxy

import (
	"context"
	"testing"
	"time"
)

// newCDPTunnel inserts a fake CDP agent-listener tunnel into the manager
// without requiring a real SSH server. CheckTunnelHealth only inspects
// in-memory state for agent-listener tunnels, so this is sufficient.
func newCDPTunnel(tm *TunnelManager, instanceID uint, status string) *ActiveTunnel {
	t := &ActiveTunnel{
		Config:    TunnelConfig{LocalPort: 9222, Type: TunnelTypeAgentListener},
		Label:     "CDP",
		LocalPort: 9222,
		Status:    status,
		LastCheck: time.Now(),
		cancel:    func() {},
		metrics:   &TunnelMetrics{CreatedAt: time.Now()},
	}
	tm.addTunnel(instanceID, t)
	return t
}

func TestCheckTunnelHealth_CDP_ProbeFalse_FlipsToIdle(t *testing.T) {
	tm := NewTunnelManager(NewSSHManager(nil, ""))
	tunnel := newCDPTunnel(tm, 1, "active")
	tm.SetCDPHealthProbe(func(ctx context.Context, instanceID uint) bool { return false })

	if err := tm.CheckTunnelHealth(1, "CDP"); err != nil {
		t.Fatalf("CheckTunnelHealth() unexpected error: %v", err)
	}
	if tunnel.Status != "idle" {
		t.Errorf("status = %q, want %q", tunnel.Status, "idle")
	}
	snap := tunnel.metrics.Snapshot()
	if snap.FailedChecks != 0 {
		t.Errorf("idle must not count as failure; FailedChecks=%d", snap.FailedChecks)
	}
	if snap.SuccessfulChecks != 0 {
		t.Errorf("idle must not count as success; SuccessfulChecks=%d", snap.SuccessfulChecks)
	}
}

func TestCheckTunnelHealth_CDP_ProbeTrue_StaysActive(t *testing.T) {
	tm := NewTunnelManager(NewSSHManager(nil, ""))
	tunnel := newCDPTunnel(tm, 1, "active")
	tm.SetCDPHealthProbe(func(ctx context.Context, instanceID uint) bool { return true })

	if err := tm.CheckTunnelHealth(1, "CDP"); err != nil {
		t.Fatalf("CheckTunnelHealth() unexpected error: %v", err)
	}
	if tunnel.Status != "active" {
		t.Errorf("status = %q, want %q", tunnel.Status, "active")
	}
	if got := tunnel.metrics.Snapshot().SuccessfulChecks; got != 1 {
		t.Errorf("SuccessfulChecks = %d, want 1", got)
	}
}

func TestCheckTunnelHealth_CDP_ProbeRecoversIdleToActive(t *testing.T) {
	tm := NewTunnelManager(NewSSHManager(nil, ""))
	tunnel := newCDPTunnel(tm, 1, "idle")
	tm.SetCDPHealthProbe(func(ctx context.Context, instanceID uint) bool { return true })

	if err := tm.CheckTunnelHealth(1, "CDP"); err != nil {
		t.Fatalf("CheckTunnelHealth() unexpected error: %v", err)
	}
	if tunnel.Status != "active" {
		t.Errorf("status = %q, want %q (probe should resurrect idle)", tunnel.Status, "active")
	}
}

func TestCheckTunnelHealth_CDP_NoProbe_StaysActive(t *testing.T) {
	tm := NewTunnelManager(NewSSHManager(nil, ""))
	tunnel := newCDPTunnel(tm, 1, "active")
	// no probe registered — preserve legacy "always healthy" behaviour.

	if err := tm.CheckTunnelHealth(1, "CDP"); err != nil {
		t.Fatalf("CheckTunnelHealth() unexpected error: %v", err)
	}
	if tunnel.Status != "active" {
		t.Errorf("status = %q, want %q", tunnel.Status, "active")
	}
}
