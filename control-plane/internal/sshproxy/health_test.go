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

func TestHealthCheck_Success(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	_, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	if err := mgr.HealthCheck(uint(1)); err != nil {
		t.Fatalf("HealthCheck() error: %v", err)
	}

	// Verify metrics were updated
	metrics := mgr.GetMetrics(uint(1))
	if metrics == nil {
		t.Fatal("GetMetrics() returned nil")
	}
	if metrics.SuccessfulChecks != 1 {
		t.Errorf("SuccessfulChecks = %d, want 1", metrics.SuccessfulChecks)
	}
	if metrics.FailedChecks != 0 {
		t.Errorf("FailedChecks = %d, want 0", metrics.FailedChecks)
	}
	if metrics.LastHealthCheck.IsZero() {
		t.Error("LastHealthCheck should be set after successful check")
	}
}

func TestHealthCheck_NoConnection(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, "")

	err = mgr.HealthCheck(uint(99))
	if err == nil {
		t.Fatal("HealthCheck() expected error for non-existent connection")
	}
}

func TestHealthCheck_DeadConnection(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	_, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	// Kill the server
	ts.cleanup()
	time.Sleep(200 * time.Millisecond)

	err = mgr.HealthCheck(uint(1))
	if err == nil {
		t.Fatal("HealthCheck() expected error for dead connection")
	}

	// Verify failure was recorded in metrics
	metrics := mgr.GetMetrics(uint(1))
	if metrics == nil {
		// Connection may have been cleaned up by keepalive
		return
	}
	if metrics.FailedChecks < 1 {
		t.Errorf("FailedChecks = %d, want >= 1", metrics.FailedChecks)
	}
}

func TestHealthCheck_MultipleChecks(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	_, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := mgr.HealthCheck(uint(1)); err != nil {
			t.Fatalf("HealthCheck() iteration %d error: %v", i, err)
		}
	}

	metrics := mgr.GetMetrics(uint(1))
	if metrics == nil {
		t.Fatal("GetMetrics() returned nil")
	}
	if metrics.SuccessfulChecks != 5 {
		t.Errorf("SuccessfulChecks = %d, want 5", metrics.SuccessfulChecks)
	}
}

func TestGetMetrics_NoConnection(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, "")
	metrics := mgr.GetMetrics(uint(1))
	if metrics != nil {
		t.Error("GetMetrics() should return nil for non-existent connection")
	}
}

func TestGetMetrics_ConnectedAt(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	before := time.Now()
	host, port := parseHostPort(t, ts.addr)
	_, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	after := time.Now()

	metrics := mgr.GetMetrics(uint(1))
	if metrics == nil {
		t.Fatal("GetMetrics() returned nil")
	}
	if metrics.ConnectedAt.Before(before) || metrics.ConnectedAt.After(after) {
		t.Errorf("ConnectedAt = %v, want between %v and %v", metrics.ConnectedAt, before, after)
	}
}

func TestGetMetrics_Uptime(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	_, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	// Read metrics directly to test Uptime
	mgr.mu.RLock()
	mc := mgr.conns[uint(1)]
	mgr.mu.RUnlock()

	uptime := mc.metrics.Uptime()
	if uptime <= 0 {
		t.Errorf("Uptime() = %v, want > 0", uptime)
	}
}

func TestGetAllMetrics(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	for i := uint(1); i <= 3; i++ {
		_, err := mgr.Connect(context.Background(), i, host, port)
		if err != nil {
			t.Fatalf("Connect(%d) error: %v", i, err)
		}
	}

	all := mgr.GetAllMetrics()
	if len(all) != 3 {
		t.Errorf("GetAllMetrics() returned %d entries, want 3", len(all))
	}
	for i := uint(1); i <= 3; i++ {
		if _, ok := all[i]; !ok {
			t.Errorf("GetAllMetrics() missing instance %d", i)
		}
	}
}

func TestConnectionMetrics_Snapshot(t *testing.T) {
	cm := &ConnectionMetrics{
		ConnectedAt:      time.Now().Add(-time.Hour),
		LastHealthCheck:  time.Now(),
		SuccessfulChecks: 42,
		FailedChecks:     3,
	}

	snap := cm.Snapshot()

	// Verify snapshot is a copy
	if snap.ConnectedAt != cm.ConnectedAt {
		t.Error("Snapshot ConnectedAt mismatch")
	}
	if snap.SuccessfulChecks != 42 {
		t.Errorf("Snapshot SuccessfulChecks = %d, want 42", snap.SuccessfulChecks)
	}
	if snap.FailedChecks != 3 {
		t.Errorf("Snapshot FailedChecks = %d, want 3", snap.FailedChecks)
	}

	// Modify original, snapshot should not change
	cm.mu.Lock()
	cm.SuccessfulChecks = 100
	cm.mu.Unlock()

	if snap.SuccessfulChecks != 42 {
		t.Error("Snapshot was modified when original changed")
	}
}

func TestStartHealthChecker_RunsChecks(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	_, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	// Run a manual health check cycle instead of waiting for the ticker
	mgr.checkAllConnections()

	metrics := mgr.GetMetrics(uint(1))
	if metrics == nil {
		t.Fatal("GetMetrics() returned nil after health check")
	}
	if metrics.SuccessfulChecks != 1 {
		t.Errorf("SuccessfulChecks = %d, want 1", metrics.SuccessfulChecks)
	}
}

func TestStartHealthChecker_RemovesDeadConnection(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	_, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	// Kill the server
	ts.cleanup()
	time.Sleep(200 * time.Millisecond)

	// Run health check cycle
	mgr.checkAllConnections()

	// Connection should be removed
	if _, ok := mgr.GetConnection(uint(1)); ok {
		t.Error("connection should be removed after failed health check")
	}
}

func TestStopHealthChecker(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, "")

	ctx := context.Background()
	mgr.StartHealthChecker(ctx)

	// Stop should not panic
	mgr.StopHealthChecker()

	// Double stop should be safe
	mgr.StopHealthChecker()
}

func TestHealthCheck_ConcurrentAccess(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	_, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	// Run concurrent health checks
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := mgr.HealthCheck(uint(1)); err != nil {
				errs <- err
			}
		}()
	}

	// Run concurrent metrics reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr.GetMetrics(uint(1))
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent health check error: %v", err)
	}

	metrics := mgr.GetMetrics(uint(1))
	if metrics == nil {
		t.Fatal("GetMetrics() returned nil")
	}
	if metrics.SuccessfulChecks != 10 {
		t.Errorf("SuccessfulChecks = %d, want 10", metrics.SuccessfulChecks)
	}
}

// testSSHServerWithExecHandler starts a test SSH server with a custom exec handler.
// If execHandler is nil, exec requests are rejected.
func testSSHServerWithExecHandler(t *testing.T, authorizedKey ssh.PublicKey, execHandler func(ch ssh.Channel, payload []byte)) *testServer {
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
			go handleTestConnectionWithExec(netConn, config, execHandler)
		}
	}()

	ts.cleanup = func() {
		listener.Close()
		ts.closeAllConns()
		<-done
	}

	return ts
}

func handleTestConnectionWithExec(netConn net.Conn, config *ssh.ServerConfig, execHandler func(ssh.Channel, []byte)) {
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
					if execHandler != nil {
						execHandler(ch, req.Payload)
					} else {
						// Reject exec by sending non-zero exit status
						ch.SendRequest("exit-status", false, []byte{0, 0, 0, 1})
					}
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

func TestHealthCheck_CommandFailure(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	// Start server that rejects exec commands with non-zero exit
	ts := testSSHServerWithExecHandler(t, signer.PublicKey(), func(ch ssh.Channel, _ []byte) {
		ch.Write([]byte("error\n"))
		ch.SendRequest("exit-status", false, []byte{0, 0, 0, 1}) // exit code 1
	})
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	_, err = mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	err = mgr.HealthCheck(uint(1))
	if err == nil {
		t.Fatal("HealthCheck() expected error for failing command")
	}

	metrics := mgr.GetMetrics(uint(1))
	if metrics == nil {
		t.Fatal("GetMetrics() returned nil")
	}
	if metrics.FailedChecks != 1 {
		t.Errorf("FailedChecks = %d, want 1", metrics.FailedChecks)
	}
	if metrics.SuccessfulChecks != 0 {
		t.Errorf("SuccessfulChecks = %d, want 0", metrics.SuccessfulChecks)
	}
}

func TestCheckAllConnections_MixedHealth(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	// Healthy server
	tsHealthy := testSSHServer(t, signer.PublicKey())
	defer tsHealthy.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	// Connect to healthy server
	host, port := parseHostPort(t, tsHealthy.addr)
	_, err = mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect(1) error: %v", err)
	}

	// Also connect instance 2 to healthy server, then kill it
	ts2 := testSSHServer(t, signer.PublicKey())
	host2, port2 := parseHostPort(t, ts2.addr)
	_, err = mgr.Connect(context.Background(), uint(2), host2, port2)
	if err != nil {
		t.Fatalf("Connect(2) error: %v", err)
	}
	ts2.cleanup()
	time.Sleep(200 * time.Millisecond)

	// Run health check cycle
	mgr.checkAllConnections()

	// Instance 1 should still be connected
	if _, ok := mgr.GetConnection(uint(1)); !ok {
		t.Error("instance 1 should still be connected (healthy)")
	}

	// Instance 2 should be removed (dead server)
	if _, ok := mgr.GetConnection(uint(2)); ok {
		t.Error("instance 2 should be removed (dead server)")
	}
}
