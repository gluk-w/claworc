package sshproxy

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// unresponsiveSSHServer completes the SSH handshake and then silently swallows
// every global request, never replying. This is what a half-open connection
// looks like to us: the TCP session is up, the peer is gone.
func unresponsiveSSHServer(t *testing.T, authorizedKey ssh.PublicKey) (addr string, cleanup func()) {
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

	var mu sync.Mutex
	var conns []net.Conn
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			netConn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, netConn)
			mu.Unlock()

			go func(nc net.Conn) {
				sshConn, chans, reqs, err := ssh.NewServerConn(nc, config)
				if err != nil {
					nc.Close()
					return
				}
				// Drain without ever replying — the client's wantReply=true
				// request will wait forever.
				go func() {
					for range reqs {
					}
				}()
				go func() {
					for newChan := range chans {
						newChan.Reject(ssh.Prohibited, "unresponsive test server")
					}
				}()
				_ = sshConn
			}(netConn)
		}
	}()

	return listener.Addr().String(), func() {
		listener.Close()
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
		<-done
	}
}

func dialUnresponsive(t *testing.T) *ssh.Client {
	t.Helper()

	_, privPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	signer, err := ssh.ParsePrivateKey(privPEM)
	if err != nil {
		t.Fatalf("parse client key: %v", err)
	}

	addr, cleanup := unresponsiveSSHServer(t, signer.PublicKey())
	t.Cleanup(cleanup)

	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "test",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

// TestProbeAlive_TimesOutOnHalfOpenConnection is the regression test for the
// keepalive deadlock: without a bound, SendRequest never returns here.
func TestProbeAlive_TimesOutOnHalfOpenConnection(t *testing.T) {
	client := dialUnresponsive(t)

	start := time.Now()
	err := probeAlive(client, 300*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from an unresponsive peer")
	}
	if elapsed > 3*time.Second {
		t.Errorf("probe took %s — it did not honor the timeout", elapsed)
	}
}

// TestProbeAlive_SecondProbeIsNotWedged is the part a bare timeout would fail.
// The abandoned request still holds the mux's globalSentMu, so unless the first
// probe also closes the client, this second probe blocks on that mutex forever.
func TestProbeAlive_SecondProbeIsNotWedged(t *testing.T) {
	client := dialUnresponsive(t)

	if err := probeAlive(client, 300*time.Millisecond); err == nil {
		t.Fatal("expected the first probe to fail")
	}

	done := make(chan error, 1)
	go func() { done <- probeAlive(client, 300*time.Millisecond) }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected the second probe to fail too")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second probe wedged — the first one left globalSentMu held")
	}
}

// TestIsConnected_DoesNotHangOnHalfOpenConnection covers the caller that made
// this a production problem: IsConnected fronts EnsureConnectedWithIPCheck,
// which every SSH-backed handler goes through.
func TestIsConnected_DoesNotHangOnHalfOpenConnection(t *testing.T) {
	client := dialUnresponsive(t)

	mgr := &SSHManager{
		conns:        map[uint]*managedConn{1: {client: client}},
		stateTracker: newStateTracker(),
	}

	done := make(chan bool, 1)
	go func() { done <- mgr.IsConnected(1) }()

	select {
	case connected := <-done:
		if connected {
			t.Error("IsConnected reported a half-open connection as healthy")
		}
	case <-time.After(keepaliveTimeout + 10*time.Second):
		t.Fatal("IsConnected hung on a half-open connection")
	}
}

// TestProbeAlive_HealthyConnection guards against the bound rejecting a
// connection that is simply working.
func TestProbeAlive_HealthyConnection(t *testing.T) {
	_, privPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	signer, err := ssh.ParsePrivateKey(privPEM)
	if err != nil {
		t.Fatalf("parse client key: %v", err)
	}

	ts := testSSHServer(t, signer.PublicKey())
	t.Cleanup(ts.cleanup)

	client, err := ssh.Dial("tcp", ts.addr, &ssh.ClientConfig{
		User:            "test",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	if err := probeAlive(client, keepaliveTimeout); err != nil {
		t.Errorf("probeAlive failed on a healthy connection: %v", err)
	}
}

// blockingListener stands in for an ssh.tcpListener whose Close blocks forever,
// which is what x/crypto does on a half-open connection: Close sends a
// cancel-tcpip-forward request with wantReply=true and waits for a reply that
// never comes, holding the mux mutex the whole time.
type blockingListener struct {
	release chan struct{}
	closed  atomic.Bool
}

func (l *blockingListener) Accept() (net.Conn, error) { <-l.release; return nil, net.ErrClosed }
func (l *blockingListener) Addr() net.Addr            { return &net.TCPAddr{} }
func (l *blockingListener) Close() error {
	<-l.release // never returns until the test releases it
	l.closed.Store(true)
	return nil
}

// TestStopTunnelsForInstance_DoesNotHangOnDeadConnection is the regression test
// for the instance-delete hang: StopTunnelsForInstance closed listeners
// serially, so the first dead one wedged DeleteInstance — and with it the HTTP
// handler — indefinitely.
func TestStopTunnelsForInstance_DoesNotHangOnDeadConnection(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	tm := &TunnelManager{tunnels: map[uint][]*ActiveTunnel{}}
	var tunnels []*ActiveTunnel
	for i := 0; i < 3; i++ {
		_, cancel := context.WithCancel(context.Background())
		tunnels = append(tunnels, &ActiveTunnel{
			cancel:   cancel,
			listener: &blockingListener{release: release},
		})
	}
	tm.tunnels[1] = tunnels

	done := make(chan error, 1)
	go func() { done <- tm.StopTunnelsForInstance(1) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("StopTunnelsForInstance: %v", err)
		}
	case <-time.After(closeListenersTimeout + 15*time.Second):
		t.Fatal("StopTunnelsForInstance hung on unresponsive listeners")
	}
}

// TestCloseTunnelListeners_ReportsStuckCount checks the bookkeeping the log
// line depends on, and that a healthy listener still closes.
func TestCloseTunnelListeners_ReportsStuckCount(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	good, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	tunnels := []*ActiveTunnel{
		{listener: good},
		{listener: &blockingListener{release: release}},
		{listener: nil}, // agent-listener tunnels can have no local listener
	}

	stuck := closeTunnelListeners(tunnels, 300*time.Millisecond)
	if stuck != 1 {
		t.Errorf("stuck = %d, want 1", stuck)
	}
}

// TestCloseTunnelListeners_AllHealthy returns promptly and reports none stuck.
func TestCloseTunnelListeners_AllHealthy(t *testing.T) {
	var tunnels []*ActiveTunnel
	for i := 0; i < 3; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		tunnels = append(tunnels, &ActiveTunnel{listener: l})
	}

	start := time.Now()
	if stuck := closeTunnelListeners(tunnels, 5*time.Second); stuck != 0 {
		t.Errorf("stuck = %d, want 0", stuck)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("healthy closes took %s — it waited on the timer", elapsed)
	}
}
