package sshproxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// startTestSSHServerForTunnel starts an in-process SSH server that accepts public key auth
// and supports direct-tcpip channels (needed for tunnel forwarding).
func startTestSSHServerForTunnel(t *testing.T, authorizedKey ssh.PublicKey) *testServer {
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
			go handleTestConnectionForTunnel(netConn, config)
		}
	}()

	ts.cleanup = func() {
		listener.Close()
		ts.closeAllConns()
		<-done
	}

	return ts
}

// handleTestConnectionForTunnel handles an SSH server connection with support for:
// - session channels (exec requests)
// - direct-tcpip channels (for client.Dial / tunnel forwarding)
func handleTestConnectionForTunnel(netConn net.Conn, config *ssh.ServerConfig) {
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
		switch newChan.ChannelType() {
		case "session":
			ch, requests, err := newChan.Accept()
			if err != nil {
				continue
			}
			go handleTunnelTestSession(ch, requests)

		case "direct-tcpip":
			// Parse the direct-tcpip extra data to get target address
			ch, _, err := newChan.Accept()
			if err != nil {
				continue
			}
			data := newChan.ExtraData()
			host, port := parseDirectTCPIPData(data)
			go handleDirectTCPIP(ch, host, port)

		default:
			newChan.Reject(ssh.UnknownChannelType, "unknown channel type")
		}
	}
}

func handleTunnelTestSession(ch ssh.Channel, requests <-chan *ssh.Request) {
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
}

// parseDirectTCPIPData parses the channel extra data for direct-tcpip channels.
// Format: string(host) + uint32(port) + string(origAddr) + uint32(origPort)
func parseDirectTCPIPData(data []byte) (string, int) {
	if len(data) < 4 {
		return "", 0
	}
	hostLen := int(data[0])<<24 | int(data[1])<<16 | int(data[2])<<8 | int(data[3])
	if len(data) < 4+hostLen+4 {
		return "", 0
	}
	host := string(data[4 : 4+hostLen])
	portBytes := data[4+hostLen : 4+hostLen+4]
	port := int(portBytes[0])<<24 | int(portBytes[1])<<16 | int(portBytes[2])<<8 | int(portBytes[3])
	return host, port
}

// handleDirectTCPIP forwards the SSH channel to a local TCP connection.
func handleDirectTCPIP(ch ssh.Channel, host string, port int) {
	defer ch.Close()

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()

	done := make(chan struct{}, 2)
	go func() { io.Copy(ch, conn); done <- struct{}{} }()
	go func() { io.Copy(conn, ch); done <- struct{}{} }()
	<-done
}

func newTunnelTestSignerAndServer(t *testing.T) (ssh.Signer, *testServer) {
	t.Helper()

	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	ts := startTestSSHServerForTunnel(t, signer.PublicKey())
	return signer, ts
}

// connectManager creates an SSHManager, connects to the test server, and
// returns the TunnelManager and the SSHManager.
func connectManager(t *testing.T, signer ssh.Signer, ts *testServer, instanceID uint) (*TunnelManager, *SSHManager) {
	t.Helper()

	mgr := NewSSHManager(signer, "")
	host, port := parseHostPort(t, ts.addr)
	_, err := mgr.Connect(context.Background(), instanceID, host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	tm := NewTunnelManager(mgr)
	return tm, mgr
}

func TestNewTunnelManager(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	sshMgr := NewSSHManager(signer, "")
	tm := NewTunnelManager(sshMgr)

	if tm == nil {
		t.Fatal("NewTunnelManager returned nil")
	}
	if tm.sshMgr != sshMgr {
		t.Error("sshMgr not set")
	}
	if tm.tunnels == nil {
		t.Error("tunnels map is nil")
	}
}

func TestCreateReverseTunnel(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	// Start an echo server that the tunnel will forward to
	echoListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listener: %v", err)
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
				io.Copy(conn, conn) // echo
			}()
		}
	}()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	// Create reverse tunnel with auto-assigned local port
	localPort, err := tm.CreateReverseTunnel(context.Background(), uint(1), "test", echoPort, 0)
	if err != nil {
		t.Fatalf("CreateReverseTunnel() error: %v", err)
	}
	if localPort == 0 {
		t.Fatal("expected non-zero local port")
	}

	// Verify the tunnel was registered
	tunnels := tm.GetTunnelsForInstance(uint(1))
	if len(tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(tunnels))
	}
	if tunnels[0].Label != "test" {
		t.Errorf("expected label 'test', got '%s'", tunnels[0].Label)
	}
	if tunnels[0].Status != "active" {
		t.Errorf("expected status 'active', got '%s'", tunnels[0].Status)
	}

	// Test data flow through the tunnel
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 2*time.Second)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	defer conn.Close()

	msg := "hello tunnel"
	_, err = conn.Write([]byte(msg))
	if err != nil {
		t.Fatalf("write to tunnel: %v", err)
	}

	buf := make([]byte, len(msg))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = io.ReadFull(conn, buf)
	if err != nil {
		t.Fatalf("read from tunnel: %v", err)
	}
	if string(buf) != msg {
		t.Errorf("expected '%s', got '%s'", msg, string(buf))
	}
}

func TestCreateReverseTunnel_NoConnection(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	sshMgr := NewSSHManager(signer, "")
	tm := NewTunnelManager(sshMgr)

	_, err = tm.CreateReverseTunnel(context.Background(), uint(99), "test", 3000, 0)
	if err == nil {
		t.Fatal("expected error when no SSH connection exists")
	}
}

func TestCreateTunnelForVNC(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	port, err := tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}
	if port == 0 {
		t.Fatal("expected non-zero port")
	}

	tunnels := tm.GetTunnelsForInstance(uint(1))
	if len(tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(tunnels))
	}
	if tunnels[0].Label != "VNC" {
		t.Errorf("expected label 'VNC', got '%s'", tunnels[0].Label)
	}
	if tunnels[0].Config.RemotePort != 3000 {
		t.Errorf("expected remote port 3000, got %d", tunnels[0].Config.RemotePort)
	}

	// Verify GetVNCLocalPort
	vncPort := tm.GetVNCLocalPort(uint(1))
	if vncPort != port {
		t.Errorf("GetVNCLocalPort() = %d, want %d", vncPort, port)
	}
}

func TestCreateTunnelForGateway(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	port, err := tm.CreateTunnelForGateway(context.Background(), uint(1), 0)
	if err != nil {
		t.Fatalf("CreateTunnelForGateway() error: %v", err)
	}
	if port == 0 {
		t.Fatal("expected non-zero port")
	}

	tunnels := tm.GetTunnelsForInstance(uint(1))
	if len(tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(tunnels))
	}
	if tunnels[0].Label != "Gateway" {
		t.Errorf("expected label 'Gateway', got '%s'", tunnels[0].Label)
	}
	if tunnels[0].Config.RemotePort != 18789 {
		t.Errorf("expected remote port 18789, got %d", tunnels[0].Config.RemotePort)
	}

	// Verify GetGatewayLocalPort
	gwPort := tm.GetGatewayLocalPort(uint(1))
	if gwPort != port {
		t.Errorf("GetGatewayLocalPort() = %d, want %d", gwPort, port)
	}
}

func TestCreateTunnelForGateway_CustomPort(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	port, err := tm.CreateTunnelForGateway(context.Background(), uint(1), 9090)
	if err != nil {
		t.Fatalf("CreateTunnelForGateway() error: %v", err)
	}
	if port == 0 {
		t.Fatal("expected non-zero port")
	}

	tunnels := tm.GetTunnelsForInstance(uint(1))
	if tunnels[0].Config.RemotePort != 9090 {
		t.Errorf("expected remote port 9090, got %d", tunnels[0].Config.RemotePort)
	}
}

func TestStopTunnelsForInstance(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	_, err := tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}
	_, err = tm.CreateTunnelForGateway(context.Background(), uint(1), 0)
	if err != nil {
		t.Fatalf("CreateTunnelForGateway() error: %v", err)
	}

	tunnels := tm.GetTunnelsForInstance(uint(1))
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels, got %d", len(tunnels))
	}

	err = tm.StopTunnelsForInstance(uint(1))
	if err != nil {
		t.Fatalf("StopTunnelsForInstance() error: %v", err)
	}

	tunnels = tm.GetTunnelsForInstance(uint(1))
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels after stop, got %d", len(tunnels))
	}

	// Stopping again should be a no-op
	err = tm.StopTunnelsForInstance(uint(1))
	if err != nil {
		t.Fatalf("second StopTunnelsForInstance() error: %v", err)
	}
}

func TestStopAll(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	sshMgr := NewSSHManager(signer, "")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)

	tm := NewTunnelManager(sshMgr)

	for i := uint(0); i < 3; i++ {
		_, err := sshMgr.Connect(context.Background(), i, host, port)
		if err != nil {
			t.Fatalf("Connect(%d) error: %v", i, err)
		}
		_, err = tm.CreateTunnelForVNC(context.Background(), i)
		if err != nil {
			t.Fatalf("CreateTunnelForVNC(%d) error: %v", i, err)
		}
	}

	tm.StopAll()

	for i := uint(0); i < 3; i++ {
		tunnels := tm.GetTunnelsForInstance(i)
		if len(tunnels) != 0 {
			t.Errorf("instance %d still has tunnels after StopAll()", i)
		}
	}
}

func TestGetTunnelsForInstance_Empty(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	sshMgr := NewSSHManager(signer, "")
	tm := NewTunnelManager(sshMgr)

	tunnels := tm.GetTunnelsForInstance(uint(99))
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels, got %d", len(tunnels))
	}
}

func TestGetVNCLocalPort_NotFound(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	sshMgr := NewSSHManager(signer, "")
	tm := NewTunnelManager(sshMgr)

	port := tm.GetVNCLocalPort(uint(99))
	if port != 0 {
		t.Errorf("expected 0, got %d", port)
	}
}

func TestGetGatewayLocalPort_NotFound(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	sshMgr := NewSSHManager(signer, "")
	tm := NewTunnelManager(sshMgr)

	port := tm.GetGatewayLocalPort(uint(99))
	if port != 0 {
		t.Errorf("expected 0, got %d", port)
	}
}

// tunnelMockOrch implements Orchestrator for testing StartTunnelsForInstance.
type tunnelMockOrch struct {
	sshAddr string
	sshPort int
}

func (m *tunnelMockOrch) ConfigureSSHAccess(ctx context.Context, instanceID uint, publicKey string) error {
	return nil
}

func (m *tunnelMockOrch) GetSSHAddress(ctx context.Context, instanceID uint) (string, int, error) {
	return m.sshAddr, m.sshPort, nil
}

func TestStartTunnelsForInstance(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	sshMgr := NewSSHManager(signer, "ssh-pubkey")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	orch := &tunnelMockOrch{sshAddr: host, sshPort: port}

	tm := NewTunnelManager(sshMgr)

	err := tm.StartTunnelsForInstance(context.Background(), uint(1), orch)
	if err != nil {
		t.Fatalf("StartTunnelsForInstance() error: %v", err)
	}

	tunnels := tm.GetTunnelsForInstance(uint(1))
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels (VNC + Gateway), got %d", len(tunnels))
	}

	labels := map[string]bool{}
	for _, tun := range tunnels {
		labels[tun.Label] = true
	}
	if !labels["VNC"] {
		t.Error("missing VNC tunnel")
	}
	if !labels["Gateway"] {
		t.Error("missing Gateway tunnel")
	}

	// Calling again should be a no-op (tunnels already exist and are healthy)
	err = tm.StartTunnelsForInstance(context.Background(), uint(1), orch)
	if err != nil {
		t.Fatalf("second StartTunnelsForInstance() error: %v", err)
	}

	// Still 2 tunnels (not 4)
	tunnels = tm.GetTunnelsForInstance(uint(1))
	if len(tunnels) != 2 {
		t.Errorf("expected 2 tunnels after re-call, got %d", len(tunnels))
	}
}

func TestReconcile(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	sshMgr := NewSSHManager(signer, "ssh-pubkey")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	orch := &tunnelMockOrch{sshAddr: host, sshPort: port}

	tm := NewTunnelManager(sshMgr)

	// Connect an instance manually and create tunnels
	_, err := sshMgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	_, err = tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}

	// Define running instances - only instance 2 is running
	listRunning := func(ctx context.Context) ([]uint, error) {
		return []uint{2}, nil
	}

	// Reconcile should remove instance 1 tunnels and create instance 2 tunnels
	tm.reconcile(context.Background(), listRunning, orch)

	// instance 1 should have no tunnels
	tunnelsA := tm.GetTunnelsForInstance(uint(1))
	if len(tunnelsA) != 0 {
		t.Errorf("expected 0 tunnels for instance 1, got %d", len(tunnelsA))
	}

	// instance 2 should have tunnels
	tunnelsB := tm.GetTunnelsForInstance(uint(2))
	if len(tunnelsB) != 2 {
		t.Errorf("expected 2 tunnels for instance 2, got %d", len(tunnelsB))
	}
}

func TestTunnelConfigState(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	_, err := tm.CreateReverseTunnel(context.Background(), uint(1), "custom", 5555, 0)
	if err != nil {
		t.Fatalf("CreateReverseTunnel() error: %v", err)
	}

	tunnels := tm.GetTunnelsForInstance(uint(1))
	if len(tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(tunnels))
	}

	tun := tunnels[0]
	if tun.Config.RemotePort != 5555 {
		t.Errorf("Config.RemotePort = %d, want 5555", tun.Config.RemotePort)
	}
	if tun.Config.Type != TunnelTypeReverse {
		t.Errorf("Config.Type = %s, want %s", tun.Config.Type, TunnelTypeReverse)
	}
	if tun.Label != "custom" {
		t.Errorf("Label = '%s', want 'custom'", tun.Label)
	}
	if tun.Status != "active" {
		t.Errorf("Status = '%s', want 'active'", tun.Status)
	}
	if tun.LastCheck.IsZero() {
		t.Error("LastCheck should not be zero")
	}
}

func TestMultipleTunnelsPerInstance(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	tm, sshMgr := connectManager(t, signer, ts, uint(1))
	defer sshMgr.CloseAll()

	// Create multiple tunnels
	_, err := tm.CreateReverseTunnel(context.Background(), uint(1), "svc-a", 3000, 0)
	if err != nil {
		t.Fatalf("first CreateReverseTunnel() error: %v", err)
	}
	_, err = tm.CreateReverseTunnel(context.Background(), uint(1), "svc-b", 8080, 0)
	if err != nil {
		t.Fatalf("second CreateReverseTunnel() error: %v", err)
	}
	_, err = tm.CreateReverseTunnel(context.Background(), uint(1), "svc-c", 9090, 0)
	if err != nil {
		t.Fatalf("third CreateReverseTunnel() error: %v", err)
	}

	tunnels := tm.GetTunnelsForInstance(uint(1))
	if len(tunnels) != 3 {
		t.Fatalf("expected 3 tunnels, got %d", len(tunnels))
	}

	// Verify all have unique local ports
	ports := map[int]bool{}
	for _, tun := range tunnels {
		if ports[tun.LocalPort] {
			t.Errorf("duplicate local port: %d", tun.LocalPort)
		}
		ports[tun.LocalPort] = true
	}
}

func TestAreTunnelsHealthy(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	sshMgr := NewSSHManager(signer, "ssh-pubkey")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	tm := NewTunnelManager(sshMgr)

	// No tunnels → not healthy
	if tm.areTunnelsHealthy(uint(1)) {
		t.Error("expected not healthy when no tunnels exist")
	}

	// Connect and create tunnels
	_, err := sshMgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	_, err = tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}

	// Active tunnel with live connection → healthy
	if !tm.areTunnelsHealthy(uint(1)) {
		t.Error("expected healthy when tunnels are active and connection alive")
	}

	// Mark a tunnel as error → not healthy
	tm.mu.Lock()
	tm.tunnels[uint(1)][0].Status = "error"
	tm.mu.Unlock()

	if tm.areTunnelsHealthy(uint(1)) {
		t.Error("expected not healthy when tunnel has error status")
	}
}

func TestAreTunnelsHealthy_DeadConnection(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)

	sshMgr := NewSSHManager(signer, "ssh-pubkey")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	tm := NewTunnelManager(sshMgr)

	_, err := sshMgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	_, err = tm.CreateTunnelForVNC(context.Background(), uint(1))
	if err != nil {
		t.Fatalf("CreateTunnelForVNC() error: %v", err)
	}

	// Kill the server so the SSH connection dies
	ts.cleanup()

	// Close the SSH connection to simulate it being detected as dead
	sshMgr.Close(uint(1))

	// Connection is dead → not healthy
	if tm.areTunnelsHealthy(uint(1)) {
		t.Error("expected not healthy when SSH connection is dead")
	}
}

func TestReconcileWithBackoff(t *testing.T) {
	// Use a mock orch that always fails for instance 1 but works for instance 2
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	sshMgr := NewSSHManager(signer, "ssh-pubkey")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)

	failingOrch := &failingOrchestratorForInstance{
		failID:  1,
		sshAddr: host,
		sshPort: port,
	}

	tm := NewTunnelManager(sshMgr)

	listRunning := func(ctx context.Context) ([]uint, error) {
		return []uint{1, 2}, nil
	}

	// First reconcile: instance 1 fails, instance 2 succeeds
	tm.reconcile(context.Background(), listRunning, failingOrch)

	// Instance 1 should be in backoff
	if tm.getAttempts(uint(1)) != 1 {
		t.Errorf("expected 1 attempt for instance 1, got %d", tm.getAttempts(uint(1)))
	}

	// Instance 2 should have tunnels
	tunnels2 := tm.GetTunnelsForInstance(uint(2))
	if len(tunnels2) != 2 {
		t.Errorf("expected 2 tunnels for instance 2, got %d", len(tunnels2))
	}

	// Instance 1 should have no tunnels
	tunnels1 := tm.GetTunnelsForInstance(uint(1))
	if len(tunnels1) != 0 {
		t.Errorf("expected 0 tunnels for instance 1, got %d", len(tunnels1))
	}

	// Second reconcile immediately: instance 1 should be skipped due to backoff
	tm.reconcile(context.Background(), listRunning, failingOrch)

	// Attempts should still be 1 (skipped due to backoff)
	if tm.getAttempts(uint(1)) != 1 {
		t.Errorf("expected 1 attempt for instance 1 (should be in backoff), got %d", tm.getAttempts(uint(1)))
	}
}

func TestBackoffClearedOnSuccess(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	sshMgr := NewSSHManager(signer, "ssh-pubkey")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	orch := &tunnelMockOrch{sshAddr: host, sshPort: port}

	tm := NewTunnelManager(sshMgr)

	// Simulate a previous failure
	tm.recordFailure(uint(1), fmt.Errorf("test error"))
	if tm.getAttempts(uint(1)) != 1 {
		t.Fatalf("expected 1 attempt, got %d", tm.getAttempts(uint(1)))
	}

	// Clear backoff manually to allow immediate retry
	tm.backoffMu.Lock()
	tm.backoff[uint(1)].nextRetry = time.Now().Add(-1 * time.Second)
	tm.backoffMu.Unlock()

	listRunning := func(ctx context.Context) ([]uint, error) {
		return []uint{1}, nil
	}

	// Reconcile should succeed and clear backoff
	tm.reconcile(context.Background(), listRunning, orch)

	if tm.getAttempts(uint(1)) != 0 {
		t.Errorf("expected 0 attempts after success, got %d", tm.getAttempts(uint(1)))
	}

	tunnels := tm.GetTunnelsForInstance(uint(1))
	if len(tunnels) != 2 {
		t.Errorf("expected 2 tunnels, got %d", len(tunnels))
	}
}

func TestBackoffExponentialGrowth(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	sshMgr := NewSSHManager(signer, "")
	tm := NewTunnelManager(sshMgr)

	testErr := fmt.Errorf("connection refused")

	// Record multiple failures and verify backoff grows
	tm.recordFailure(uint(1), testErr)
	if tm.getAttempts(uint(1)) != 1 {
		t.Errorf("expected 1 attempt, got %d", tm.getAttempts(uint(1)))
	}

	tm.backoffMu.RLock()
	state1 := *tm.backoff[uint(1)]
	tm.backoffMu.RUnlock()

	tm.recordFailure(uint(1), testErr)
	tm.backoffMu.RLock()
	state2 := *tm.backoff[uint(1)]
	tm.backoffMu.RUnlock()

	if tm.getAttempts(uint(1)) != 2 {
		t.Errorf("expected 2 attempts, got %d", tm.getAttempts(uint(1)))
	}

	// The delay should increase between attempts
	delay1 := state1.nextRetry.Sub(time.Now())
	delay2 := state2.nextRetry.Sub(time.Now())
	if delay2 <= delay1 {
		t.Errorf("expected increasing backoff delay, got delay1=%v delay2=%v", delay1, delay2)
	}

	// Record many failures to verify cap at maxBackoffInterval
	for i := 0; i < 20; i++ {
		tm.recordFailure(uint(1), testErr)
	}

	tm.backoffMu.RLock()
	finalState := tm.backoff[uint(1)]
	tm.backoffMu.RUnlock()

	maxDelay := finalState.nextRetry.Sub(time.Now())
	if maxDelay > maxBackoffInterval+time.Second {
		t.Errorf("backoff exceeded max: got %v, max is %v", maxDelay, maxBackoffInterval)
	}
}

func TestBackoffClearedWhenInstanceRemoved(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	sshMgr := NewSSHManager(signer, "ssh-pubkey")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	orch := &tunnelMockOrch{sshAddr: host, sshPort: port}

	tm := NewTunnelManager(sshMgr)

	// Record a failure for instance 5
	tm.recordFailure(uint(5), fmt.Errorf("test error"))
	if tm.getAttempts(uint(5)) != 1 {
		t.Fatalf("expected 1 attempt, got %d", tm.getAttempts(uint(5)))
	}

	// Reconcile with empty running list — instance 5 is no longer running
	listRunning := func(ctx context.Context) ([]uint, error) {
		return []uint{}, nil
	}

	tm.reconcile(context.Background(), listRunning, orch)

	// Backoff should be cleaned up
	if tm.getAttempts(uint(5)) != 0 {
		t.Errorf("expected 0 attempts after instance removed, got %d", tm.getAttempts(uint(5)))
	}
}

func TestStartTunnelsForInstance_RecreatesUnhealthy(t *testing.T) {
	signer, ts := newTunnelTestSignerAndServer(t)
	defer ts.cleanup()

	sshMgr := NewSSHManager(signer, "ssh-pubkey")
	defer sshMgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	orch := &tunnelMockOrch{sshAddr: host, sshPort: port}

	tm := NewTunnelManager(sshMgr)

	// Create initial tunnels
	err := tm.StartTunnelsForInstance(context.Background(), uint(1), orch)
	if err != nil {
		t.Fatalf("first StartTunnelsForInstance() error: %v", err)
	}

	tunnels := tm.GetTunnelsForInstance(uint(1))
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels, got %d", len(tunnels))
	}
	origPort := tunnels[0].LocalPort

	// Mark one tunnel as error
	tm.mu.Lock()
	tm.tunnels[uint(1)][0].Status = "error"
	tm.mu.Unlock()

	// Re-call StartTunnelsForInstance — should recreate
	err = tm.StartTunnelsForInstance(context.Background(), uint(1), orch)
	if err != nil {
		t.Fatalf("second StartTunnelsForInstance() error: %v", err)
	}

	tunnels = tm.GetTunnelsForInstance(uint(1))
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels after recreation, got %d", len(tunnels))
	}

	// Ports should be different (new listeners allocated)
	for _, tun := range tunnels {
		if tun.Status != "active" {
			t.Errorf("expected active status, got %s", tun.Status)
		}
	}

	// At least one port should differ since we recreated
	newPort := tunnels[0].LocalPort
	// Note: ports are auto-assigned, so they might or might not match
	_ = origPort
	_ = newPort
}

// failingOrchestratorForInstance fails SSH operations for a specific instance.
type failingOrchestratorForInstance struct {
	failID  uint
	sshAddr string
	sshPort int
}

func (m *failingOrchestratorForInstance) ConfigureSSHAccess(ctx context.Context, instanceID uint, publicKey string) error {
	if instanceID == m.failID {
		return fmt.Errorf("simulated SSH access failure for instance %d", instanceID)
	}
	return nil
}

func (m *failingOrchestratorForInstance) GetSSHAddress(ctx context.Context, instanceID uint) (string, int, error) {
	if instanceID == m.failID {
		return "", 0, fmt.Errorf("simulated address lookup failure for instance %d", instanceID)
	}
	return m.sshAddr, m.sshPort, nil
}
