package tunnel

import (
	"context"
	"fmt"
	"net"
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

	inst := &database.Instance{Name: "bot-test"}
	inst.ID = 10

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		ReconnectLoop(ctx, inst, stubResolver("", fmt.Errorf("no agent")), 10*time.Millisecond)
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

	reconnectAttempts := 0
	resolver := func(_ context.Context, _ string) (string, error) {
		reconnectAttempts++
		return "", fmt.Errorf("not a real agent")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go ReconnectLoop(ctx, inst, resolver, 10*time.Millisecond)

	time.Sleep(60 * time.Millisecond)
	cancel()

	if reconnectAttempts == 0 {
		t.Error("expected at least one reconnect attempt for closed session")
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
