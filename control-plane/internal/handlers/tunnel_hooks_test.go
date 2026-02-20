package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/tunnel"
)

// cleanupReconnectCtxs removes all entries from the global sync.Map to avoid
// test pollution between cases.
func cleanupReconnectCtxs(t *testing.T) {
	t.Helper()
	reconnectCtxs.Range(func(key, value any) bool {
		reconnectCtxs.Delete(key)
		return true
	})
}

func TestStopTunnelForInstance_CancelsContextAndDisconnects(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()
	tunnel.Manager = tunnel.NewTunnelManager()
	cleanupReconnectCtxs(t)
	defer cleanupReconnectCtxs(t)

	ctx, cancel := context.WithCancel(context.Background())
	reconnectCtxs.Store(uint(42), cancel)

	// Store a tunnel client
	cli, _ := newYamuxPair(t)
	tc := tunnel.NewTunnelClient(42, "test")
	tc.SetSession(cli)
	tunnel.Manager.Set(42, tc)

	stopTunnelForInstance(42)

	select {
	case <-ctx.Done():
		// OK — context was cancelled
	default:
		t.Error("expected context to be cancelled")
	}

	if tunnel.Manager.Get(42) != nil {
		t.Error("expected tunnel client to be removed")
	}

	// Verify reconnect context was removed from map
	if _, ok := reconnectCtxs.Load(uint(42)); ok {
		t.Error("expected reconnect context to be removed from map")
	}
}

func TestStopTunnelForInstance_NoExistingContext(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()
	tunnel.Manager = tunnel.NewTunnelManager()
	cleanupReconnectCtxs(t)
	defer cleanupReconnectCtxs(t)

	// Should not panic when there's no existing context or tunnel
	stopTunnelForInstance(999)
}

func TestStopTunnelForInstance_WithContextButNoTunnel(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()
	tunnel.Manager = tunnel.NewTunnelManager()
	cleanupReconnectCtxs(t)
	defer cleanupReconnectCtxs(t)

	ctx, cancel := context.WithCancel(context.Background())
	reconnectCtxs.Store(uint(50), cancel)

	stopTunnelForInstance(50)

	select {
	case <-ctx.Done():
	default:
		t.Error("expected context to be cancelled")
	}
}

func TestStopTunnelForInstance_WithTunnelButNoContext(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()
	tunnel.Manager = tunnel.NewTunnelManager()
	cleanupReconnectCtxs(t)
	defer cleanupReconnectCtxs(t)

	cli, _ := newYamuxPair(t)
	tc := tunnel.NewTunnelClient(60, "test")
	tc.SetSession(cli)
	tunnel.Manager.Set(60, tc)

	// Should disconnect tunnel even without a stored context
	stopTunnelForInstance(60)

	if tunnel.Manager.Get(60) != nil {
		t.Error("expected tunnel client to be removed")
	}
}

func TestStopTunnelForInstance_Idempotent(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()
	tunnel.Manager = tunnel.NewTunnelManager()
	cleanupReconnectCtxs(t)
	defer cleanupReconnectCtxs(t)

	ctx, cancel := context.WithCancel(context.Background())
	reconnectCtxs.Store(uint(70), cancel)

	cli, _ := newYamuxPair(t)
	tc := tunnel.NewTunnelClient(70, "test")
	tc.SetSession(cli)
	tunnel.Manager.Set(70, tc)

	// Call twice — should not panic
	stopTunnelForInstance(70)
	stopTunnelForInstance(70)

	select {
	case <-ctx.Done():
	default:
		t.Error("expected context to be cancelled")
	}
}

func TestReconnectCtxsLifecycle(t *testing.T) {
	cleanupReconnectCtxs(t)
	defer cleanupReconnectCtxs(t)

	// Store a context
	ctx1, cancel1 := context.WithCancel(context.Background())
	reconnectCtxs.Store(uint(1), cancel1)

	// Store another for a different instance
	ctx2, cancel2 := context.WithCancel(context.Background())
	reconnectCtxs.Store(uint(2), cancel2)

	// Verify both are stored
	if _, ok := reconnectCtxs.Load(uint(1)); !ok {
		t.Error("expected context 1 to be stored")
	}
	if _, ok := reconnectCtxs.Load(uint(2)); !ok {
		t.Error("expected context 2 to be stored")
	}

	// Delete one and cancel it (simulates stopTunnelForInstance pattern)
	if val, ok := reconnectCtxs.LoadAndDelete(uint(1)); ok {
		val.(context.CancelFunc)()
	}

	select {
	case <-ctx1.Done():
	default:
		t.Error("expected ctx1 to be cancelled")
	}

	// ctx2 should still be alive
	select {
	case <-ctx2.Done():
		t.Error("expected ctx2 to still be alive")
	default:
	}

	// Replace context for instance 2 (simulates startTunnelForInstance overwrite)
	if val, ok := reconnectCtxs.LoadAndDelete(uint(2)); ok {
		val.(context.CancelFunc)()
	}
	ctx3, cancel3 := context.WithCancel(context.Background())
	_ = ctx3
	reconnectCtxs.Store(uint(2), cancel3)

	select {
	case <-ctx2.Done():
	default:
		t.Error("expected old ctx2 to be cancelled after replacement")
	}
}

func TestStartTunnelForInstance_NoOrchestrator(t *testing.T) {
	// startTunnelForInstance returns early if no orchestrator is available.
	// orchestrator.Get() returns nil in test environments.
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()
	tunnel.Manager = tunnel.NewTunnelManager()
	cleanupReconnectCtxs(t)
	defer cleanupReconnectCtxs(t)

	inst := &database.Instance{Name: "bot-test"}
	inst.ID = 100

	// Should not panic or block
	startTunnelForInstance(inst)

	// No reconnect context should be stored (orchestrator was nil)
	if _, ok := reconnectCtxs.Load(uint(100)); ok {
		t.Error("expected no reconnect context when orchestrator is nil")
	}
}

func TestStopStartCycle(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()
	tunnel.Manager = tunnel.NewTunnelManager()
	cleanupReconnectCtxs(t)
	defer cleanupReconnectCtxs(t)

	// Simulate a start → stop → start cycle for the same instance
	ctx1, cancel1 := context.WithCancel(context.Background())
	reconnectCtxs.Store(uint(80), cancel1)

	cli, _ := newYamuxPair(t)
	tc := tunnel.NewTunnelClient(80, "test")
	tc.SetSession(cli)
	tunnel.Manager.Set(80, tc)

	// Stop
	stopTunnelForInstance(80)

	select {
	case <-ctx1.Done():
	default:
		t.Error("expected ctx1 to be cancelled after stop")
	}

	// Simulate start (new context)
	ctx2, cancel2 := context.WithCancel(context.Background())
	reconnectCtxs.Store(uint(80), cancel2)

	// Verify new context is stored and alive
	select {
	case <-ctx2.Done():
		t.Error("expected new context to be alive")
	default:
	}

	// Stop again
	stopTunnelForInstance(80)

	select {
	case <-ctx2.Done():
	default:
		t.Error("expected ctx2 to be cancelled after second stop")
	}
}

// TestConcurrentStopCalls verifies that concurrent stops on the same instance
// do not race or panic.
func TestConcurrentStopCalls(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()
	tunnel.Manager = tunnel.NewTunnelManager()
	cleanupReconnectCtxs(t)
	defer cleanupReconnectCtxs(t)

	_, cancel := context.WithCancel(context.Background())
	reconnectCtxs.Store(uint(90), cancel)

	cli, _ := newYamuxPair(t)
	tc := tunnel.NewTunnelClient(90, "test")
	tc.SetSession(cli)
	tunnel.Manager.Set(90, tc)

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			stopTunnelForInstance(90)
			done <- struct{}{}
		}()
	}

	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for concurrent stops")
		}
	}
}
