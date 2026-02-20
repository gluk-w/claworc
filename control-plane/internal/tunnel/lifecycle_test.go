package tunnel

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/hashicorp/yamux"
)

// stubResolver returns a fixed address or error.
func stubResolver(addr string, err error) AddrResolver {
	return func(_ context.Context, _ string) (string, error) {
		return addr, err
	}
}

func setupTestManager(t *testing.T) {
	t.Helper()
	old := Manager
	Manager = NewTunnelManager()
	t.Cleanup(func() { Manager = old })
}

// setFastBackoff overrides backoff durations for fast tests and restores on cleanup.
func setFastBackoff(t *testing.T) {
	t.Helper()
	oldMin, oldMax := backoffMin, backoffMax
	backoffMin = 5 * time.Millisecond
	backoffMax = 40 * time.Millisecond
	t.Cleanup(func() {
		backoffMin = oldMin
		backoffMax = oldMax
	})
}

func TestConnectInstance_AlreadyConnected(t *testing.T) {
	setupTestManager(t)

	cli, _ := newYamuxPair(t)
	existing := &TunnelClient{instanceID: 1, session: cli}
	Manager.Set(1, existing)

	inst := &database.Instance{Name: "bot-test"}
	inst.ID = 1
	inst.AgentCert = "dummy"

	// Should return nil without calling the resolver.
	called := false
	resolver := func(_ context.Context, _ string) (string, error) {
		called = true
		return "", nil
	}
	err := ConnectInstance(context.Background(), inst, resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("resolver should not be called when already connected")
	}
}

func TestConnectInstance_NoCert(t *testing.T) {
	setupTestManager(t)

	inst := &database.Instance{Name: "bot-test"}
	inst.ID = 2

	err := ConnectInstance(context.Background(), inst, stubResolver("", nil))
	if err != nil {
		t.Fatalf("expected nil for missing cert, got: %v", err)
	}
	if Manager.Get(2) != nil {
		t.Error("should not store client when cert is missing")
	}
}

func TestConnectInstance_ResolverError(t *testing.T) {
	setupTestManager(t)

	inst := &database.Instance{Name: "bot-test", AgentCert: "-----BEGIN CERTIFICATE-----\nfoo\n-----END CERTIFICATE-----"}
	inst.ID = 3

	err := ConnectInstance(context.Background(), inst, stubResolver("", fmt.Errorf("resolve failed")))
	if err == nil {
		t.Fatal("expected error from resolver")
	}
}

func TestDisconnectInstance(t *testing.T) {
	setupTestManager(t)

	cli, _ := newYamuxPair(t)
	client := &TunnelClient{instanceID: 5, session: cli}
	Manager.Set(5, client)

	DisconnectInstance(5)

	if Manager.Get(5) != nil {
		t.Error("client should be removed after disconnect")
	}
}

func TestDisconnectInstance_Nonexistent(t *testing.T) {
	setupTestManager(t)
	// Should not panic.
	DisconnectInstance(999)
}

func TestReconnectLoop_StopsOnCancel(t *testing.T) {
	setupTestManager(t)
	setFastBackoff(t)

	inst := &database.Instance{Name: "bot-test"}
	inst.ID = 10

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		ReconnectLoop(ctx, inst, stubResolver("", fmt.Errorf("no agent")))
		close(done)
	}()

	// Let a few ticks pass.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// OK — loop exited.
	case <-time.After(2 * time.Second):
		t.Fatal("ReconnectLoop did not stop after context cancel")
	}
}

func TestReconnectLoop_ReconnectsOnClosedSession(t *testing.T) {
	setupTestManager(t)
	setFastBackoff(t)

	// Create a closed session so IsClosed() returns true.
	a, b := net.Pipe()
	cli, err := yamux.Client(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Immediately close so IsClosed() returns true.
	b.Close()
	cli.Close()

	client := &TunnelClient{instanceID: 11, session: cli}
	Manager.Set(11, client)

	inst := &database.Instance{Name: "bot-test"}
	inst.ID = 11
	inst.AgentCert = "-----BEGIN CERTIFICATE-----\nfoo\n-----END CERTIFICATE-----"

	var reconnectAttempts int32
	resolver := func(_ context.Context, _ string) (string, error) {
		atomic.AddInt32(&reconnectAttempts, 1)
		return "", fmt.Errorf("not a real agent")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		ReconnectLoop(ctx, inst, resolver)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ReconnectLoop did not stop after context cancel")
	}

	if atomic.LoadInt32(&reconnectAttempts) == 0 {
		t.Error("expected at least one reconnect attempt for closed session")
	}
}

func TestReconnectLoop_ExponentialBackoff(t *testing.T) {
	setupTestManager(t)
	setFastBackoff(t)

	inst := &database.Instance{Name: "bot-backoff"}
	inst.ID = 20
	inst.AgentCert = "-----BEGIN CERTIFICATE-----\nfoo\n-----END CERTIFICATE-----"

	var timestamps []time.Time
	resolver := func(_ context.Context, _ string) (string, error) {
		timestamps = append(timestamps, time.Now())
		return "", fmt.Errorf("always fail")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		ReconnectLoop(ctx, inst, resolver)
		close(done)
	}()

	// With min=5ms, the sequence is 5ms, 10ms, 20ms, 40ms (capped).
	// Total to get 4 attempts ≈ 5+10+20+40 = 75ms. Wait plenty.
	time.Sleep(250 * time.Millisecond)
	cancel()
	<-done

	if len(timestamps) < 3 {
		t.Fatalf("expected at least 3 reconnect attempts, got %d", len(timestamps))
	}

	// Verify intervals are increasing (exponential).
	for i := 2; i < len(timestamps); i++ {
		prev := timestamps[i-1].Sub(timestamps[i-2])
		curr := timestamps[i].Sub(timestamps[i-1])
		// Allow some jitter (curr should be roughly >= prev)
		if curr < prev/2 {
			t.Errorf("interval %d (%v) shorter than half of interval %d (%v) — not exponential", i, curr, i-1, prev)
		}
	}
}

func TestIsClosed_NilSession(t *testing.T) {
	tc := &TunnelClient{}
	if !tc.IsClosed() {
		t.Error("IsClosed should return true for nil session")
	}
}

func TestIsClosed_ActiveSession(t *testing.T) {
	cli, _ := newYamuxPair(t)
	tc := &TunnelClient{session: cli}
	if tc.IsClosed() {
		t.Error("IsClosed should return false for active session")
	}
}

func TestIsClosed_ClosedSession(t *testing.T) {
	a, b := net.Pipe()
	cli, err := yamux.Client(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	cli.Close()

	tc := &TunnelClient{session: cli}
	if !tc.IsClosed() {
		t.Error("IsClosed should return true for closed session")
	}
}
