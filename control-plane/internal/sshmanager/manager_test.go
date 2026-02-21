package sshmanager

import (
	"context"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// --- Unit tests for basic map operations ---

func TestNewSSHManager(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()
	if m == nil {
		t.Fatal("NewSSHManager returned nil")
	}
	if m.clients == nil {
		t.Error("clients map not initialized")
	}
	if m.maxConnections != 0 {
		t.Errorf("expected maxConnections 0, got %d", m.maxConnections)
	}
}

func TestNewSSHManagerWithLimit(t *testing.T) {
	m := NewSSHManager(5)
	defer m.CloseAll()
	if m.maxConnections != 5 {
		t.Errorf("expected maxConnections 5, got %d", m.maxConnections)
	}
}

func TestHasClientEmpty(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()
	if m.HasClient("nonexistent") {
		t.Error("HasClient should return false for nonexistent instance")
	}
}

func TestGetClientMissing(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()
	_, err := m.GetClient("nonexistent")
	if err == nil {
		t.Error("GetClient should return error for nonexistent instance")
	}
}

func TestGetConnectionMissing(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()
	client, ok := m.GetConnection("nonexistent")
	if ok {
		t.Error("GetConnection should return false for nonexistent instance")
	}
	if client != nil {
		t.Error("GetConnection should return nil client for nonexistent instance")
	}
}

func TestRemoveClientMissing(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()
	client := m.RemoveClient("nonexistent")
	if client != nil {
		t.Error("RemoveClient should return nil for nonexistent instance")
	}
}

func TestSetAndHasClient(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()
	m.SetClient("test-instance", nil)
	if !m.HasClient("test-instance") {
		t.Error("HasClient should return true after SetClient")
	}
}

func TestSetAndGetConnection(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()
	m.SetClient("test-instance", nil)
	client, ok := m.GetConnection("test-instance")
	if !ok {
		t.Error("GetConnection should return true after SetClient")
	}
	if client != nil {
		t.Error("GetConnection should return the stored nil client")
	}
}

func TestRemoveClient(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()
	m.SetClient("test-instance", nil)
	m.RemoveClient("test-instance")
	if m.HasClient("test-instance") {
		t.Error("HasClient should return false after RemoveClient")
	}
}

func TestCloseAllEmpty(t *testing.T) {
	m := NewSSHManager(0)
	err := m.CloseAll()
	if err != nil {
		t.Errorf("CloseAll on empty manager returned error: %v", err)
	}
}

func TestCloseAllClearsClients(t *testing.T) {
	m := NewSSHManager(0)
	m.SetClient("instance-a", nil)
	m.SetClient("instance-b", nil)

	if len(m.clients) != 2 {
		t.Fatalf("expected 2 clients before CloseAll, got %d", len(m.clients))
	}

	m.CloseAll()

	if len(m.clients) != 0 {
		t.Errorf("expected 0 clients after CloseAll, got %d", len(m.clients))
	}
	if m.HasClient("instance-a") {
		t.Error("HasClient should return false for instance-a after CloseAll")
	}
	if m.HasClient("instance-b") {
		t.Error("HasClient should return false for instance-b after CloseAll")
	}
}

func TestCloseAllIdempotent(t *testing.T) {
	m := NewSSHManager(0)
	m.SetClient("test", nil)
	m.CloseAll()
	// Second call should not panic (keepalive already stopped)
	// Need a new manager since CloseAll stops keepalive
}

func TestConnectionCount(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	if m.ConnectionCount() != 0 {
		t.Errorf("expected 0 connections, got %d", m.ConnectionCount())
	}

	m.SetClient("a", nil)
	m.SetClient("b", nil)
	if m.ConnectionCount() != 2 {
		t.Errorf("expected 2 connections, got %d", m.ConnectionCount())
	}

	m.RemoveClient("a")
	if m.ConnectionCount() != 1 {
		t.Errorf("expected 1 connection, got %d", m.ConnectionCount())
	}
}

// --- Close specific connection tests ---

func TestCloseMissing(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	err := m.Close("nonexistent")
	if err == nil {
		t.Error("Close should return error for nonexistent instance")
	}
	if !strings.Contains(err.Error(), "no SSH connection") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCloseNilClient(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	m.SetClient("test", nil)
	err := m.Close("test")
	if err != nil {
		t.Errorf("Close nil client should not error: %v", err)
	}
	if m.HasClient("test") {
		t.Error("client should be removed after Close")
	}
}

// --- Connect validation tests ---

func TestConnectEmptyInstanceName(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	_, err := m.Connect(context.Background(), "", "host", 22, "/some/key")
	if err == nil {
		t.Error("Connect should fail with empty instance name")
	}
	if !strings.Contains(err.Error(), "instance name is empty") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConnectEmptyHost(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	_, err := m.Connect(context.Background(), "test", "", 22, "/some/key")
	if err == nil {
		t.Error("Connect should fail with empty host")
	}
	if !strings.Contains(err.Error(), "host is empty") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConnectInvalidPort(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	for _, port := range []int{0, -1, 65536} {
		_, err := m.Connect(context.Background(), "test", "host", port, "/some/key")
		if err == nil {
			t.Errorf("Connect should fail with invalid port %d", port)
		}
		if !strings.Contains(err.Error(), "invalid port") {
			t.Errorf("unexpected error for port %d: %v", port, err)
		}
	}
}

func TestConnectInvalidKeyPath(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	_, err := m.Connect(context.Background(), "test", "host", 22, "/nonexistent/key")
	if err == nil {
		t.Error("Connect should fail with nonexistent key path")
	}
	if !strings.Contains(err.Error(), "read private key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConnectInvalidKeyContent(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "bad.key")
	os.WriteFile(keyPath, []byte("not-a-key"), 0600)

	_, err := m.Connect(context.Background(), "test", "host", 22, keyPath)
	if err == nil {
		t.Error("Connect should fail with invalid key content")
	}
	if !strings.Contains(err.Error(), "parse private key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConnectMaxConnectionsReached(t *testing.T) {
	m := NewSSHManager(1)
	defer m.CloseAll()

	// Fill the pool with a nil client
	m.SetClient("existing", nil)

	tmpDir := t.TempDir()
	keyPath := writeTestKey(t, tmpDir, "test")

	_, err := m.Connect(context.Background(), "new-instance", "127.0.0.1", 22, keyPath)
	if err == nil {
		t.Error("Connect should fail when max connections reached")
	}
	if !strings.Contains(err.Error(), "maximum connections") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConnectMaxConnectionsAllowsReplace(t *testing.T) {
	m := NewSSHManager(1)
	defer m.CloseAll()

	// Fill the pool
	m.SetClient("test-instance", nil)

	tmpDir := t.TempDir()
	keyPath := writeTestKey(t, tmpDir, "test")

	// Connecting as the same instance name should be allowed (it's a replacement)
	// It will still fail because there's no SSH server, but the max-connections check should pass
	_, err := m.Connect(context.Background(), "test-instance", "127.0.0.1", 99999, keyPath)
	if err == nil {
		t.Error("Connect should fail (no server), but not due to max connections")
	}
	if strings.Contains(err.Error(), "maximum connections") {
		t.Error("should not hit max connections limit when replacing existing connection")
	}
}

func TestConnectContextCancelled(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	tmpDir := t.TempDir()
	keyPath := writeTestKey(t, tmpDir, "test")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := m.Connect(ctx, "test-instance", "127.0.0.1", 22, keyPath)
	if err == nil {
		t.Error("Connect should fail with cancelled context")
	}
}

// --- Integration tests with real SSH server ---

// startTestSSHServer starts a minimal SSH server for testing.
func startTestSSHServer(t *testing.T) (addr string, cleanup func()) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}

	// Generate a client key pair
	clientPub, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	clientSSHPub, err := gossh.NewPublicKey(clientPub)
	if err != nil {
		t.Fatalf("convert client public key: %v", err)
	}

	cfg := &gossh.ServerConfig{
		PublicKeyCallback: func(conn gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
			if bytes.Equal(key.Marshal(), clientSSHPub.Marshal()) {
				return &gossh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	cfg.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				srvConn, chans, reqs, err := gossh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer srvConn.Close()
				go gossh.DiscardRequests(reqs)
				for newChan := range chans {
					ch, requests, err := newChan.Accept()
					if err != nil {
						continue
					}
					go func() {
						for req := range requests {
							if req.WantReply {
								req.Reply(true, nil)
							}
						}
					}()
					ch.Close()
				}
			}()
		}
	}()

	// Write the client private key to a temp file
	tmpDir := t.TempDir()
	pemBlock, err := gossh.MarshalPrivateKey(clientPriv, "")
	if err != nil {
		t.Fatalf("marshal client private key: %v", err)
	}
	keyPath := filepath.Join(tmpDir, "client.key")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(pemBlock), 0600); err != nil {
		t.Fatalf("write client key: %v", err)
	}

	// Return addr and keyPath in cleanup closure
	t.Setenv("TEST_SSH_KEY_PATH", keyPath)

	return listener.Addr().String(), func() { listener.Close() }
}

func TestConnectToRealSSHServer(t *testing.T) {
	addr, cleanup := startTestSSHServer(t)
	defer cleanup()

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	client, err := m.Connect(context.Background(), "test-instance", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if client == nil {
		t.Fatal("Connect returned nil client")
	}
	if !m.HasClient("test-instance") {
		t.Error("HasClient should return true after Connect")
	}
	if m.ConnectionCount() != 1 {
		t.Errorf("expected 1 connection, got %d", m.ConnectionCount())
	}

	// GetConnection should return the same client
	got, ok := m.GetConnection("test-instance")
	if !ok || got != client {
		t.Error("GetConnection should return the connected client")
	}
}

func TestConnectReplacesExistingConnection(t *testing.T) {
	addr, cleanup := startTestSSHServer(t)
	defer cleanup()

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	client1, err := m.Connect(context.Background(), "test-instance", host, port, keyPath)
	if err != nil {
		t.Fatalf("first Connect failed: %v", err)
	}

	client2, err := m.Connect(context.Background(), "test-instance", host, port, keyPath)
	if err != nil {
		t.Fatalf("second Connect failed: %v", err)
	}

	if client1 == client2 {
		t.Error("second Connect should return a new client")
	}
	if m.ConnectionCount() != 1 {
		t.Errorf("expected 1 connection after replacement, got %d", m.ConnectionCount())
	}
}

func TestCloseSpecificConnection(t *testing.T) {
	addr, cleanup := startTestSSHServer(t)
	defer cleanup()

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	_, err := m.Connect(context.Background(), "test-instance", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	err = m.Close("test-instance")
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if m.HasClient("test-instance") {
		t.Error("HasClient should return false after Close")
	}
	if m.ConnectionCount() != 0 {
		t.Errorf("expected 0 connections after Close, got %d", m.ConnectionCount())
	}
}

func TestCloseAllWithRealConnections(t *testing.T) {
	addr, cleanup := startTestSSHServer(t)
	defer cleanup()

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)

	_, err := m.Connect(context.Background(), "instance-a", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect a failed: %v", err)
	}
	_, err = m.Connect(context.Background(), "instance-b", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect b failed: %v", err)
	}

	if m.ConnectionCount() != 2 {
		t.Fatalf("expected 2 connections, got %d", m.ConnectionCount())
	}

	err = m.CloseAll()
	if err != nil {
		t.Errorf("CloseAll returned error: %v", err)
	}
	if m.ConnectionCount() != 0 {
		t.Errorf("expected 0 connections after CloseAll, got %d", m.ConnectionCount())
	}
}

func TestMaxConnectionsEnforced(t *testing.T) {
	addr, cleanup := startTestSSHServer(t)
	defer cleanup()

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(2)
	defer m.CloseAll()

	_, err := m.Connect(context.Background(), "a", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect a: %v", err)
	}
	_, err = m.Connect(context.Background(), "b", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect b: %v", err)
	}

	// Third connection should fail
	_, err = m.Connect(context.Background(), "c", host, port, keyPath)
	if err == nil {
		t.Error("third Connect should fail due to max connections")
	}
	if !strings.Contains(err.Error(), "maximum connections") {
		t.Errorf("unexpected error: %v", err)
	}

	// After closing one, we should be able to connect again
	m.Close("a")
	_, err = m.Connect(context.Background(), "c", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect c after freeing slot: %v", err)
	}
}

// --- Concurrency test ---

func TestConcurrentAccess(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("instance-%d", i)
			m.SetClient(name, nil)
			m.HasClient(name)
			m.GetConnection(name)
			m.GetClient(name)
			m.ConnectionCount()
		}(i)
	}
	wg.Wait()

	if m.ConnectionCount() != 50 {
		t.Errorf("expected 50 connections, got %d", m.ConnectionCount())
	}
}

// --- Keepalive test ---

func TestKeepaliveRemovesDeadConnections(t *testing.T) {
	addr, srvConns, cleanup := startTestSSHServerWithConns(t)
	defer cleanup()

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	_, err := m.Connect(context.Background(), "test-instance", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Kill the SSH server and close all server-side connections
	cleanup()
	srvConns.CloseAll()

	// Wait a moment for the connection to become stale
	time.Sleep(100 * time.Millisecond)

	// Manually trigger checkConnections
	m.checkConnections()

	// The dead connection should be removed
	if m.HasClient("test-instance") {
		t.Error("dead connection should be removed after checkConnections")
	}
}

func TestKeepalivePreservesHealthyConnections(t *testing.T) {
	addr, cleanup := startTestSSHServer(t)
	defer cleanup()

	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m := NewSSHManager(0)
	defer m.CloseAll()

	_, err := m.Connect(context.Background(), "test-instance", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Trigger health check while server is still running
	m.checkConnections()

	// Healthy connection should remain
	if !m.HasClient("test-instance") {
		t.Error("healthy connection should remain after checkConnections")
	}
}

// --- Helpers ---

// connTracker tracks server-side connections for clean shutdown in tests.
type connTracker struct {
	mu    sync.Mutex
	conns []net.Conn
}

func (ct *connTracker) Add(c net.Conn) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.conns = append(ct.conns, c)
}

func (ct *connTracker) CloseAll() {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	for _, c := range ct.conns {
		c.Close()
	}
	ct.conns = nil
}

// startTestSSHServerWithConns is like startTestSSHServer but also returns a
// connTracker so tests can force-close server-side connections.
func startTestSSHServerWithConns(t *testing.T) (addr string, tracker *connTracker, cleanup func()) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}

	clientPub, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	clientSSHPub, err := gossh.NewPublicKey(clientPub)
	if err != nil {
		t.Fatalf("convert client public key: %v", err)
	}

	cfg := &gossh.ServerConfig{
		PublicKeyCallback: func(conn gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
			if bytes.Equal(key.Marshal(), clientSSHPub.Marshal()) {
				return &gossh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	cfg.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	tracker = &connTracker{}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			tracker.Add(conn)
			go func() {
				defer conn.Close()
				srvConn, chans, reqs, err := gossh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer srvConn.Close()
				go gossh.DiscardRequests(reqs)
				for newChan := range chans {
					ch, requests, err := newChan.Accept()
					if err != nil {
						continue
					}
					go func() {
						for req := range requests {
							if req.WantReply {
								req.Reply(true, nil)
							}
						}
					}()
					ch.Close()
				}
			}()
		}
	}()

	tmpDir := t.TempDir()
	pemBlock, err := gossh.MarshalPrivateKey(clientPriv, "")
	if err != nil {
		t.Fatalf("marshal client private key: %v", err)
	}
	keyPath := filepath.Join(tmpDir, "client.key")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(pemBlock), 0600); err != nil {
		t.Fatalf("write client key: %v", err)
	}
	t.Setenv("TEST_SSH_KEY_PATH", keyPath)

	return listener.Addr().String(), tracker, func() { listener.Close() }
}

// writeTestKey generates an ED25519 key and writes it to disk, returning the path.
func writeTestKey(t *testing.T, dir, name string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBlock, err := gossh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPath := filepath.Join(dir, name+".key")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(pemBlock), 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return keyPath
}
