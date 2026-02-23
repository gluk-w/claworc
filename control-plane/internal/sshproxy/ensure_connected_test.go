package sshproxy

import (
	"context"
	"fmt"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)

// mockOrchestrator implements the Orchestrator interface for testing.
type mockOrchestrator struct {
	sshHost string
	sshPort int

	configureErr error
	addressErr   error

	configureCalls int
	addressCalls   int
}

func (m *mockOrchestrator) ConfigureSSHAccess(_ context.Context, _ uint, _ string) error {
	m.configureCalls++
	return m.configureErr
}

func (m *mockOrchestrator) GetSSHAddress(_ context.Context, _ uint) (string, int, error) {
	m.addressCalls++
	if m.addressErr != nil {
		return "", 0, m.addressErr
	}
	return m.sshHost, m.sshPort, nil
}

func newTestManagerWithPublicKey(t *testing.T) (*SSHManager, ssh.Signer, *testServer) {
	t.Helper()

	pubKeyBytes, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	ts := testSSHServer(t, signer.PublicKey())
	mgr := NewSSHManager(signer, string(pubKeyBytes))

	return mgr, signer, ts
}

func TestEnsureConnected_FreshConnection(t *testing.T) {
	mgr, _, ts := newTestManagerWithPublicKey(t)
	defer ts.cleanup()
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	orch := &mockOrchestrator{sshHost: host, sshPort: port}

	client, err := mgr.EnsureConnected(context.Background(), uint(1), orch)
	if err != nil {
		t.Fatalf("EnsureConnected() error: %v", err)
	}
	if client == nil {
		t.Fatal("EnsureConnected() returned nil client")
	}

	// Verify orchestrator was called
	if orch.addressCalls != 1 {
		t.Errorf("GetSSHAddress called %d times, want 1", orch.addressCalls)
	}
	if orch.configureCalls != 1 {
		t.Errorf("ConfigureSSHAccess called %d times, want 1", orch.configureCalls)
	}

	// Verify connection is usable
	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession() error: %v", err)
	}
	session.Close()
}

func TestEnsureConnected_CachedConnectionReuse(t *testing.T) {
	mgr, _, ts := newTestManagerWithPublicKey(t)
	defer ts.cleanup()
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	orch := &mockOrchestrator{sshHost: host, sshPort: port}

	// First call establishes a connection
	client1, err := mgr.EnsureConnected(context.Background(), uint(1), orch)
	if err != nil {
		t.Fatalf("first EnsureConnected() error: %v", err)
	}

	// Second call should reuse the cached connection
	client2, err := mgr.EnsureConnected(context.Background(), uint(1), orch)
	if err != nil {
		t.Fatalf("second EnsureConnected() error: %v", err)
	}

	if client1 != client2 {
		t.Error("second call returned a different client, expected cached reuse")
	}

	// Orchestrator should only be called once (for the first connection)
	if orch.addressCalls != 1 {
		t.Errorf("GetSSHAddress called %d times, want 1", orch.addressCalls)
	}
	if orch.configureCalls != 1 {
		t.Errorf("ConfigureSSHAccess called %d times, want 1", orch.configureCalls)
	}
}

func TestEnsureConnected_UploadFailure(t *testing.T) {
	mgr, _, ts := newTestManagerWithPublicKey(t)
	defer ts.cleanup()
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	orch := &mockOrchestrator{
		sshHost:      host,
		sshPort:      port,
		configureErr: fmt.Errorf("container not running"),
	}

	_, err := mgr.EnsureConnected(context.Background(), uint(1), orch)
	if err == nil {
		t.Fatal("EnsureConnected() expected error when upload fails")
	}

	// Should not have a cached connection
	if _, ok := mgr.GetConnection(uint(1)); ok {
		t.Error("connection should not be cached after upload failure")
	}
}

func TestEnsureConnected_AddressFailure(t *testing.T) {
	mgr, _, ts := newTestManagerWithPublicKey(t)
	defer ts.cleanup()
	defer mgr.CloseAll()

	orch := &mockOrchestrator{
		addressErr: fmt.Errorf("instance not found"),
	}

	_, err := mgr.EnsureConnected(context.Background(), uint(1), orch)
	if err == nil {
		t.Fatal("EnsureConnected() expected error when address lookup fails")
	}

	// ConfigureSSHAccess should not have been called
	if orch.configureCalls != 0 {
		t.Errorf("ConfigureSSHAccess called %d times, want 0", orch.configureCalls)
	}
}

func TestEnsureConnected_ConnectFailureAfterUpload(t *testing.T) {
	mgr, _, ts := newTestManagerWithPublicKey(t)
	defer ts.cleanup()
	defer mgr.CloseAll()

	// Use a port that's not listening (find a free port and don't start a server)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	badAddr := listener.Addr().String()
	listener.Close() // Close immediately so the port is unreachable

	badHost, badPort := parseHostPort(t, badAddr)
	orch := &mockOrchestrator{sshHost: badHost, sshPort: badPort}

	_, err = mgr.EnsureConnected(context.Background(), uint(1), orch)
	if err == nil {
		t.Fatal("EnsureConnected() expected error when SSH connect fails after upload")
	}

	// Both address lookup and upload should have been called
	if orch.addressCalls != 1 {
		t.Errorf("GetSSHAddress called %d times, want 1", orch.addressCalls)
	}
	if orch.configureCalls != 1 {
		t.Errorf("ConfigureSSHAccess called %d times, want 1", orch.configureCalls)
	}
}

func TestEnsureConnected_ReconnectsAfterDeadConnection(t *testing.T) {
	mgr, _, ts := newTestManagerWithPublicKey(t)
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	orch := &mockOrchestrator{sshHost: host, sshPort: port}

	// Establish initial connection
	_, err := mgr.EnsureConnected(context.Background(), uint(1), orch)
	if err != nil {
		t.Fatalf("first EnsureConnected() error: %v", err)
	}

	// Kill the server to make the connection dead
	ts.cleanup()

	// Start a new server on a different port
	ts2 := testSSHServer(t, mgr.signer.PublicKey())
	defer ts2.cleanup()

	host2, port2 := parseHostPort(t, ts2.addr)
	orch.sshHost = host2
	orch.sshPort = port2

	// EnsureConnected should detect the dead connection and reconnect
	client, err := mgr.EnsureConnected(context.Background(), uint(1), orch)
	if err != nil {
		t.Fatalf("second EnsureConnected() error: %v", err)
	}
	if client == nil {
		t.Fatal("second EnsureConnected() returned nil client")
	}

	// Should have called the orchestrator again for the reconnection
	if orch.addressCalls != 2 {
		t.Errorf("GetSSHAddress called %d times, want 2", orch.addressCalls)
	}
	if orch.configureCalls != 2 {
		t.Errorf("ConfigureSSHAccess called %d times, want 2", orch.configureCalls)
	}
}
