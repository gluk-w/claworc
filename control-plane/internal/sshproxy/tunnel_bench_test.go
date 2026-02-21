package sshproxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// connectManagerBench creates an SSHManager, connects to the test server, and
// returns the TunnelManager and the SSHManager. Benchmark-compatible.
func connectManagerBench(b *testing.B, signer ssh.Signer, ts *testServer, instanceID uint) (*TunnelManager, *SSHManager) {
	b.Helper()

	mgr := NewSSHManager(signer, "")
	host, port := parseHostPortBench(b, ts.addr)
	_, err := mgr.Connect(context.Background(), instanceID, host, port)
	if err != nil {
		b.Fatalf("Connect() error: %v", err)
	}

	tm := NewTunnelManager(mgr)
	return tm, mgr
}

// parseHostPortBench is a benchmark-compatible version of parseHostPort.
func parseHostPortBench(b *testing.B, addr string) (string, int) {
	b.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		b.Fatalf("split host port %q: %v", addr, err)
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}

// benchSignerAndServer creates a signer and test SSH server for benchmarks.
// Uses a testing.T wrapper for the SSH server (which requires *testing.T).
func benchSignerAndServer(b *testing.B) (ssh.Signer, string, func()) {
	b.Helper()

	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		b.Fatalf("generate key pair: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		b.Fatalf("parse private key: %v", err)
	}

	// Start SSH server directly without test helper
	_, hostKeyPEM, err := GenerateKeyPair()
	if err != nil {
		b.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.ParsePrivateKey(hostKeyPEM)
	if err != nil {
		b.Fatalf("parse host key: %v", err)
	}

	config := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if ssh.FingerprintSHA256(key) == ssh.FingerprintSHA256(signer.PublicKey()) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	config.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			netConn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleTestConnectionForTunnel(netConn, config)
		}
	}()

	cleanup := func() {
		listener.Close()
		<-done
	}

	return signer, listener.Addr().String(), cleanup
}

// BenchmarkGetVNCLocalPort measures the latency of looking up VNC tunnel ports.
// This is called on every proxy request, so its overhead matters.
// Performance target: <100ns per lookup (O(1) map access + RLock).
func BenchmarkGetVNCLocalPort(b *testing.B) {
	signer, addr, cleanup := benchSignerAndServer(b)
	defer cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPortBench(b, addr)
	_, err := mgr.Connect(context.Background(), 1, host, port)
	if err != nil {
		b.Fatalf("Connect: %v", err)
	}

	tm := NewTunnelManager(mgr)
	_, err = tm.CreateTunnelForVNC(context.Background(), 1)
	if err != nil {
		b.Fatalf("CreateTunnelForVNC: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		port := tm.GetVNCLocalPort(1)
		if port == 0 {
			b.Fatal("expected non-zero port")
		}
	}
}

// BenchmarkGetGatewayLocalPort measures the latency of looking up Gateway tunnel ports.
func BenchmarkGetGatewayLocalPort(b *testing.B) {
	signer, addr, cleanup := benchSignerAndServer(b)
	defer cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPortBench(b, addr)
	_, err := mgr.Connect(context.Background(), 1, host, port)
	if err != nil {
		b.Fatalf("Connect: %v", err)
	}

	tm := NewTunnelManager(mgr)
	_, err = tm.CreateTunnelForGateway(context.Background(), 1, 0)
	if err != nil {
		b.Fatalf("CreateTunnelForGateway: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		port := tm.GetGatewayLocalPort(1)
		if port == 0 {
			b.Fatal("expected non-zero port")
		}
	}
}

// BenchmarkGetVNCLocalPort_Concurrent measures concurrent port lookup throughput.
// Multiple goroutines look up the same instance's VNC port simultaneously,
// exercising the RWMutex contention path.
func BenchmarkGetVNCLocalPort_Concurrent(b *testing.B) {
	signer, addr, cleanup := benchSignerAndServer(b)
	defer cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPortBench(b, addr)
	_, err := mgr.Connect(context.Background(), 1, host, port)
	if err != nil {
		b.Fatalf("Connect: %v", err)
	}

	tm := NewTunnelManager(mgr)
	_, err = tm.CreateTunnelForVNC(context.Background(), 1)
	if err != nil {
		b.Fatalf("CreateTunnelForVNC: %v", err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			port := tm.GetVNCLocalPort(1)
			if port == 0 {
				b.Fatal("expected non-zero port")
			}
		}
	})
}

// BenchmarkTunnelDataFlow measures the round-trip latency of data flowing through an SSH tunnel.
// Path: client -> local TCP listener -> SSH channel -> test SSH server -> direct-tcpip -> echo server -> back.
// Uses a persistent connection to avoid ephemeral port exhaustion.
func BenchmarkTunnelDataFlow(b *testing.B) {
	signer, addr, cleanup := benchSignerAndServer(b)
	defer cleanup()

	// Start echo server that the tunnel will forward to
	echoListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("echo listener: %v", err)
	}
	defer echoListener.Close()
	echoPort := echoListener.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := echoListener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				io.Copy(conn, conn)
			}()
		}
	}()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPortBench(b, addr)
	_, err = mgr.Connect(context.Background(), 1, host, port)
	if err != nil {
		b.Fatalf("Connect: %v", err)
	}

	tm := NewTunnelManager(mgr)
	localPort, err := tm.CreateReverseTunnel(context.Background(), 1, "bench", echoPort, 0)
	if err != nil {
		b.Fatalf("CreateReverseTunnel: %v", err)
	}

	msg := []byte("benchmark-payload-1234567890")
	buf := make([]byte, len(msg))

	// Use a single persistent connection to measure per-message latency
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 2*time.Second)
	if err != nil {
		b.Fatalf("dial tunnel: %v", err)
	}
	defer conn.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err = conn.Write(msg)
		if err != nil {
			b.Fatalf("write: %v", err)
		}

		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = io.ReadFull(conn, buf)
		if err != nil {
			b.Fatalf("read: %v", err)
		}
	}
}

// BenchmarkTunnelDataFlow_Concurrent measures concurrent data flow through a single tunnel.
// Each parallel goroutine uses its own persistent connection, measuring throughput
// of multiple simultaneous streams over a single SSH connection (channel multiplexing).
func BenchmarkTunnelDataFlow_Concurrent(b *testing.B) {
	signer, addr, cleanup := benchSignerAndServer(b)
	defer cleanup()

	echoListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("echo listener: %v", err)
	}
	defer echoListener.Close()
	echoPort := echoListener.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := echoListener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				io.Copy(conn, conn)
			}()
		}
	}()

	sshMgr := NewSSHManager(signer, "")
	defer sshMgr.CloseAll()

	host, port := parseHostPortBench(b, addr)
	_, err = sshMgr.Connect(context.Background(), 1, host, port)
	if err != nil {
		b.Fatalf("Connect: %v", err)
	}

	tm := NewTunnelManager(sshMgr)
	localPort, err := tm.CreateReverseTunnel(context.Background(), 1, "bench", echoPort, 0)
	if err != nil {
		b.Fatalf("CreateReverseTunnel: %v", err)
	}

	msg := []byte("concurrent-payload")
	tunnelAddr := fmt.Sprintf("127.0.0.1:%d", localPort)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		// Each goroutine gets its own persistent connection
		conn, err := net.DialTimeout("tcp", tunnelAddr, 2*time.Second)
		if err != nil {
			b.Errorf("dial tunnel: %v", err)
			return
		}
		defer conn.Close()

		buf := make([]byte, len(msg))
		for pb.Next() {
			_, err = conn.Write(msg)
			if err != nil {
				b.Errorf("write: %v", err)
				return
			}

			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, err = io.ReadFull(conn, buf)
			if err != nil {
				b.Errorf("read: %v", err)
				return
			}
		}
	})
}

// BenchmarkMultiInstanceTunnelLookup measures port lookup performance across multiple instances.
// Verifies that the map-based lookup scales well with more instances.
func BenchmarkMultiInstanceTunnelLookup(b *testing.B) {
	signer, addr, cleanup := benchSignerAndServer(b)
	defer cleanup()

	sshMgr := NewSSHManager(signer, "")
	defer sshMgr.CloseAll()

	host, port := parseHostPortBench(b, addr)
	tm := NewTunnelManager(sshMgr)

	const numInstances = 10
	for i := uint(1); i <= numInstances; i++ {
		_, err := sshMgr.Connect(context.Background(), i, host, port)
		if err != nil {
			b.Fatalf("Connect(%d): %v", i, err)
		}
		_, err = tm.CreateTunnelForVNC(context.Background(), i)
		if err != nil {
			b.Fatalf("CreateTunnelForVNC(%d): %v", i, err)
		}
		_, err = tm.CreateTunnelForGateway(context.Background(), i, 0)
		if err != nil {
			b.Fatalf("CreateTunnelForGateway(%d): %v", i, err)
		}
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		id := uint(1)
		for pb.Next() {
			tm.GetVNCLocalPort(id)
			tm.GetGatewayLocalPort(id)
			id = id%numInstances + 1
		}
	})
}

// TestTunnelReuse verifies that StartTunnelsForInstance does not create
// redundant tunnels when called multiple times for the same instance.
// This is a correctness test, not a benchmark, but is critical for performance:
// without tunnel reuse, every reconciliation loop would tear down and recreate
// tunnels, causing connection drops and unnecessary overhead.
func TestTunnelReuse(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	sshMgr := NewSSHManager(signer, "ssh-pubkey")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	orch := &tunnelMockOrch{sshAddr: host, sshPort: port}

	tm := NewTunnelManager(sshMgr)

	// First call creates tunnels
	err := tm.StartTunnelsForInstance(context.Background(), 1, orch)
	if err != nil {
		t.Fatalf("first StartTunnelsForInstance: %v", err)
	}

	tunnels1 := tm.GetTunnelsForInstance(1)
	if len(tunnels1) != 2 {
		t.Fatalf("expected 2 tunnels, got %d", len(tunnels1))
	}

	vncPort1 := tm.GetVNCLocalPort(1)
	gwPort1 := tm.GetGatewayLocalPort(1)

	// Second call should be a no-op (tunnel reuse)
	err = tm.StartTunnelsForInstance(context.Background(), 1, orch)
	if err != nil {
		t.Fatalf("second StartTunnelsForInstance: %v", err)
	}

	tunnels2 := tm.GetTunnelsForInstance(1)
	if len(tunnels2) != 2 {
		t.Errorf("expected 2 tunnels after reuse, got %d", len(tunnels2))
	}

	vncPort2 := tm.GetVNCLocalPort(1)
	gwPort2 := tm.GetGatewayLocalPort(1)

	// Ports must be identical — same tunnels, not new ones
	if vncPort1 != vncPort2 {
		t.Errorf("VNC port changed: %d -> %d (tunnel was recreated instead of reused)", vncPort1, vncPort2)
	}
	if gwPort1 != gwPort2 {
		t.Errorf("Gateway port changed: %d -> %d (tunnel was recreated instead of reused)", gwPort1, gwPort2)
	}

	// Third call to be sure
	err = tm.StartTunnelsForInstance(context.Background(), 1, orch)
	if err != nil {
		t.Fatalf("third StartTunnelsForInstance: %v", err)
	}

	if tm.GetVNCLocalPort(1) != vncPort1 {
		t.Error("VNC port changed on third call")
	}
}

// TestConcurrentTunnelAccess verifies that concurrent tunnel operations
// do not race or corrupt state under high contention.
func TestConcurrentTunnelAccess(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	sshMgr := NewSSHManager(signer, "ssh-pubkey")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	orch := &tunnelMockOrch{sshAddr: host, sshPort: port}

	tm := NewTunnelManager(sshMgr)

	// Create tunnels for multiple instances
	const numInstances = 5
	for i := uint(1); i <= numInstances; i++ {
		if err := tm.StartTunnelsForInstance(context.Background(), i, orch); err != nil {
			t.Fatalf("StartTunnelsForInstance(%d): %v", i, err)
		}
	}

	// Concurrent lookups and health checks from 10 goroutines
	var wg sync.WaitGroup
	errors := int64(0)

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				id := uint(i%numInstances) + 1
				if p := tm.GetVNCLocalPort(id); p == 0 {
					atomic.AddInt64(&errors, 1)
				}
				if p := tm.GetGatewayLocalPort(id); p == 0 {
					atomic.AddInt64(&errors, 1)
				}
				tm.GetTunnelsForInstance(id)
				tm.areTunnelsHealthy(id)
			}
		}()
	}

	wg.Wait()

	if errors > 0 {
		t.Errorf("concurrent access produced %d zero-port lookups", errors)
	}
}

// TestMultiInstanceDataFlow verifies that data can flow through tunnels
// to multiple instances simultaneously without cross-contamination.
func TestMultiInstanceDataFlow(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	// Create separate echo servers for each "instance"
	const numInstances = 3
	echoListeners := make([]net.Listener, numInstances)
	echoPorts := make([]int, numInstances)

	for i := 0; i < numInstances; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("echo listener %d: %v", i, err)
		}
		defer listener.Close()
		echoListeners[i] = listener
		echoPorts[i] = listener.Addr().(*net.TCPAddr).Port

		idx := i
		go func() {
			for {
				conn, err := echoListeners[idx].Accept()
				if err != nil {
					return
				}
				go func() {
					defer conn.Close()
					// Echo with instance-specific prefix
					buf := make([]byte, 1024)
					n, err := conn.Read(buf)
					if err != nil {
						return
					}
					resp := fmt.Sprintf("inst%d:%s", idx+1, string(buf[:n]))
					conn.Write([]byte(resp))
				}()
			}
		}()
	}

	sshMgr := NewSSHManager(signer, "")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	tm := NewTunnelManager(sshMgr)

	tunnelPorts := make([]int, numInstances)
	for i := 0; i < numInstances; i++ {
		id := uint(i + 1)
		_, err := sshMgr.Connect(context.Background(), id, host, port)
		if err != nil {
			t.Fatalf("Connect(%d): %v", id, err)
		}
		lp, err := tm.CreateReverseTunnel(context.Background(), id, "test", echoPorts[i], 0)
		if err != nil {
			t.Fatalf("CreateReverseTunnel(%d): %v", id, err)
		}
		tunnelPorts[i] = lp
	}

	// Send data through all tunnels concurrently
	var wg sync.WaitGroup
	for i := 0; i < numInstances; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", tunnelPorts[idx]), 2*time.Second)
			if err != nil {
				t.Errorf("dial tunnel %d: %v", idx+1, err)
				return
			}
			defer conn.Close()

			_, err = conn.Write([]byte("hello"))
			if err != nil {
				t.Errorf("write tunnel %d: %v", idx+1, err)
				return
			}

			buf := make([]byte, 64)
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, err := conn.Read(buf)
			if err != nil {
				t.Errorf("read tunnel %d: %v", idx+1, err)
				return
			}

			expected := fmt.Sprintf("inst%d:hello", idx+1)
			if string(buf[:n]) != expected {
				t.Errorf("tunnel %d: expected %q, got %q", idx+1, expected, string(buf[:n]))
			}
		}()
	}

	wg.Wait()
}
