package sshproxy

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// testServer tracks a test SSH server's state.
type testServer struct {
	addr    string
	cleanup func()

	mu       sync.Mutex
	netConns []net.Conn
}

// closeAllConns forcefully closes all accepted TCP connections.
func (ts *testServer) closeAllConns() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, c := range ts.netConns {
		c.Close()
	}
	ts.netConns = nil
}

// testSSHServer starts an in-process SSH server that accepts public key auth.
func testSSHServer(t *testing.T, authorizedKey ssh.PublicKey) *testServer {
	t.Helper()

	_, hostKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.ParsePrivateKey(hostKeyPEM)
	if err != nil {
		t.Fatalf("parse host key: %v", err)
	}

	config := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if ssh.FingerprintSHA256(key) == ssh.FingerprintSHA256(authorizedKey) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	config.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ts := &testServer{
		addr: listener.Addr().String(),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			netConn, err := listener.Accept()
			if err != nil {
				return
			}
			ts.mu.Lock()
			ts.netConns = append(ts.netConns, netConn)
			ts.mu.Unlock()
			go handleTestConnection(netConn, config)
		}
	}()

	ts.cleanup = func() {
		listener.Close()
		ts.closeAllConns()
		<-done
	}

	return ts
}

func handleTestConnection(netConn net.Conn, config *ssh.ServerConfig) {
	sshConn, chans, reqs, err := ssh.NewServerConn(netConn, config)
	if err != nil {
		netConn.Close()
		return
	}
	defer sshConn.Close()

	go func() {
		for req := range reqs {
			if req.WantReply {
				req.Reply(true, nil)
			}
		}
	}()

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range requests {
				if req.Type == "exec" {
					ch.Write([]byte("ok\n"))
					ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
					if req.WantReply {
						req.Reply(true, nil)
					}
					return
				}
				if req.WantReply {
					req.Reply(true, nil)
				}
			}
		}()
	}
}

// newTestSignerAndServer creates a key pair, starts a test SSH server, and
// returns the signer and the test server.
func newTestSignerAndServer(t *testing.T) (ssh.Signer, *testServer) {
	t.Helper()

	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	ts := testSSHServer(t, signer.PublicKey())
	return signer, ts
}

func parseHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}

func TestNewSSHManager(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, "")
	if mgr == nil {
		t.Fatal("NewSSHManager returned nil")
	}
	if mgr.signer == nil {
		t.Fatal("signer is nil")
	}
	if mgr.conns == nil {
		t.Fatal("conns map is nil")
	}
}

func TestConnect_ValidKey(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	client, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	if client == nil {
		t.Fatal("Connect() returned nil client")
	}

	// Verify the connection is usable by opening a session
	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession() error: %v", err)
	}
	session.Close()
}

func TestConnect_InvalidKey(t *testing.T) {
	_, serverPrivPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	serverSigner, err := ParsePrivateKey(serverPrivPEM)
	if err != nil {
		t.Fatalf("parse server key: %v", err)
	}

	ts := testSSHServer(t, serverSigner.PublicKey())
	defer ts.cleanup()

	// Use a different key that the server won't accept
	_, wrongPrivPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate wrong key: %v", err)
	}
	wrongSigner, err := ParsePrivateKey(wrongPrivPEM)
	if err != nil {
		t.Fatalf("parse wrong key: %v", err)
	}

	mgr := NewSSHManager(wrongSigner, "")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	_, err = mgr.Connect(context.Background(), uint(1), host, port)
	if err == nil {
		t.Fatal("Connect() expected error with wrong key")
	}
}

func TestConnect_InvalidHost(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, "")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = mgr.Connect(ctx, uint(1), "127.0.0.1", 1)
	if err == nil {
		t.Fatal("Connect() expected error for invalid host")
	}
}

func TestGetConnection(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	// Before connecting
	_, ok := mgr.GetConnection(uint(1))
	if ok {
		t.Error("GetConnection() found connection before Connect()")
	}

	host, port := parseHostPort(t, ts.addr)
	client, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	// After connecting
	got, ok := mgr.GetConnection(uint(1))
	if !ok {
		t.Fatal("GetConnection() did not find connection after Connect()")
	}
	if got != client {
		t.Error("GetConnection() returned different client")
	}

	// Non-existent instance
	_, ok = mgr.GetConnection(uint(2))
	if ok {
		t.Error("GetConnection() found connection for non-existent instance")
	}
}

func TestClose(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")

	host, port := parseHostPort(t, ts.addr)
	_, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	_, ok := mgr.GetConnection(uint(1))
	if !ok {
		t.Fatal("connection should exist before Close()")
	}

	if err := mgr.Close(uint(1)); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	_, ok = mgr.GetConnection(uint(1))
	if ok {
		t.Error("connection still exists after Close()")
	}

	// Closing again should be a no-op
	if err := mgr.Close(uint(1)); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}
}

func TestCloseAll(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")

	host, port := parseHostPort(t, ts.addr)

	for i := uint(0); i < 3; i++ {
		_, err := mgr.Connect(context.Background(), i, host, port)
		if err != nil {
			t.Fatalf("Connect(%d) error: %v", i, err)
		}
	}

	for i := uint(0); i < 3; i++ {
		if _, ok := mgr.GetConnection(i); !ok {
			t.Errorf("instance %d not connected", i)
		}
	}

	if err := mgr.CloseAll(); err != nil {
		t.Fatalf("CloseAll() error: %v", err)
	}

	for i := uint(0); i < 3; i++ {
		if _, ok := mgr.GetConnection(i); ok {
			t.Errorf("instance %d still connected after CloseAll()", i)
		}
	}
}

func TestConnect_ReplacesExisting(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)

	client1, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("first Connect() error: %v", err)
	}

	client2, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("second Connect() error: %v", err)
	}

	if client1 == client2 {
		t.Error("second Connect() returned same client, expected a new one")
	}

	got, ok := mgr.GetConnection(uint(1))
	if !ok {
		t.Fatal("GetConnection() did not find connection")
	}
	if got != client2 {
		t.Error("GetConnection() returned old client instead of new one")
	}
}

func TestIsConnected(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	if mgr.IsConnected(uint(1)) {
		t.Error("IsConnected() true before connecting")
	}

	host, port := parseHostPort(t, ts.addr)
	_, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	if !mgr.IsConnected(uint(1)) {
		t.Error("IsConnected() false after connecting")
	}
}

func TestConcurrentAccess(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)

	_, err := mgr.Connect(context.Background(), uint(99), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 50)

	// Concurrent connects
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := uint(100 + i)
			_, err := mgr.Connect(context.Background(), id, host, port)
			if err != nil {
				errs <- fmt.Errorf("Connect(%d): %w", id, err)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr.GetConnection(uint(99))
		}()
	}

	// Concurrent IsConnected
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr.IsConnected(uint(99))
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent error: %v", err)
	}
}

func TestGetPublicKeyFingerprint(t *testing.T) {
	pubKeyBytes, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, string(pubKeyBytes))

	fp := mgr.GetPublicKeyFingerprint()
	if fp == "" {
		t.Fatal("GetPublicKeyFingerprint() returned empty string")
	}
	// SHA256 fingerprints start with "SHA256:"
	if len(fp) < 7 || fp[:7] != "SHA256:" {
		t.Errorf("GetPublicKeyFingerprint() = %q, want SHA256:... prefix", fp)
	}

	// GetPublicKey should return the same public key string
	pk := mgr.GetPublicKey()
	if pk != string(pubKeyBytes) {
		t.Errorf("GetPublicKey() mismatch: got %d bytes, want %d bytes", len(pk), len(pubKeyBytes))
	}
}

func TestKeepalive_RemovesDeadConnection(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)

	mgr := NewSSHManager(signer, "")

	host, port := parseHostPort(t, ts.addr)
	_, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	if !mgr.IsConnected(uint(1)) {
		t.Fatal("should be connected")
	}

	// Kill the server and forcefully close all TCP connections
	ts.cleanup()

	// Give the TCP stack a moment to propagate the RST
	time.Sleep(200 * time.Millisecond)

	// IsConnected should now return false because the keepalive check fails
	if mgr.IsConnected(uint(1)) {
		t.Error("IsConnected() should be false after server shutdown")
	}

	mgr.CloseAll()
}
