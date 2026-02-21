package sshtunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	gossh "golang.org/x/crypto/ssh"
)

// portMapping maps requested destination ports to actual local service ports.
// This lets us remap well-known ports (3000, 8080) to ephemeral test ports.
type portMapping map[int]int

// startTestSSHServer starts a minimal SSH server that handles direct-tcpip
// channel requests (used by ssh.Client.Dial). Port remapping is applied so
// that requests for well-known agent ports reach the actual test services.
func startTestSSHServer(t *testing.T, mapping portMapping) (addr string, cleanup func()) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}

	cfg := &gossh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ssh server listen: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveSSHConn(conn, cfg, mapping)
		}
	}()

	return listener.Addr().String(), func() { listener.Close() }
}

func serveSSHConn(netConn net.Conn, cfg *gossh.ServerConfig, mapping portMapping) {
	defer netConn.Close()

	srvConn, chans, reqs, err := gossh.NewServerConn(netConn, cfg)
	if err != nil {
		return
	}
	defer srvConn.Close()

	go gossh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "direct-tcpip" {
			newChan.Reject(gossh.UnknownChannelType, "unsupported channel type")
			continue
		}
		go serveDirectTCPIP(newChan, mapping)
	}
}

// directTCPIPData matches the SSH wire format for direct-tcpip extra data.
type directTCPIPData struct {
	DestHost   string
	DestPort   uint32
	OriginHost string
	OriginPort uint32
}

func serveDirectTCPIP(newChan gossh.NewChannel, mapping portMapping) {
	var data directTCPIPData
	if err := gossh.Unmarshal(newChan.ExtraData(), &data); err != nil {
		newChan.Reject(gossh.ConnectionFailed, "invalid payload")
		return
	}

	destPort := int(data.DestPort)
	if mapped, ok := mapping[destPort]; ok {
		destPort = mapped
	}

	dest, err := net.Dial("tcp", fmt.Sprintf("%s:%d", data.DestHost, destPort))
	if err != nil {
		newChan.Reject(gossh.ConnectionFailed, err.Error())
		return
	}
	defer dest.Close()

	ch, reqs, err := newChan.Accept()
	if err != nil {
		return
	}
	defer ch.Close()
	go gossh.DiscardRequests(reqs)

	done := make(chan struct{}, 2)
	go func() { io.Copy(ch, dest); done <- struct{}{} }()
	go func() { io.Copy(dest, ch); done <- struct{}{} }()
	<-done
}

// startEchoServer starts a TCP echo server. Returns the bound port and a cleanup function.
func startEchoServer(t *testing.T) (port int, cleanup func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo server listen: %v", err)
	}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				io.Copy(conn, conn)
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, func() { l.Close() }
}

// startHTTPTestServer starts an HTTP server returning the given body at "/".
func startHTTPTestServer(t *testing.T, body string) (port int, cleanup func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("http server listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(l)
	return l.Addr().(*net.TCPAddr).Port, func() { srv.Close() }
}

// startWSEchoServer starts a WebSocket echo server.
func startWSEchoServer(t *testing.T) (port int, cleanup func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			typ, msg, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err := conn.Write(r.Context(), typ, msg); err != nil {
				return
			}
		}
	})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ws echo server listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(l)
	return l.Addr().(*net.TCPAddr).Port, func() { srv.Close() }
}

// connectTestSSH dials the test SSH server and returns the client.
func connectTestSSH(t *testing.T, addr string) *gossh.Client {
	t.Helper()
	client, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("ssh dial %s: %v", addr, err)
	}
	return client
}

// findTunnel locates a tunnel by service label in the given slice.
func findTunnel(tunnels []*ActiveTunnel, service ServiceLabel) *ActiveTunnel {
	for _, tun := range tunnels {
		if tun.Config.Service == service && !tun.IsClosed() {
			return tun
		}
	}
	return nil
}

// --- Integration Tests ---

func TestIntegrationSSHConnectionEstablishes(t *testing.T) {
	sshAddr, sshCleanup := startTestSSHServer(t, nil)
	defer sshCleanup()

	sm := sshmanager.NewSSHManager()
	client := connectTestSSH(t, sshAddr)
	defer client.Close()

	sm.SetClient("test-instance", client)

	if !sm.HasClient("test-instance") {
		t.Fatal("expected SSH client to be stored")
	}

	got, err := sm.GetClient("test-instance")
	if err != nil {
		t.Fatalf("GetClient: %v", err)
	}
	if got != client {
		t.Error("GetClient returned a different client")
	}
}

func TestIntegrationTunnelsCreatedAutomatically(t *testing.T) {
	vncPort, vncCleanup := startHTTPTestServer(t, "VNC-OK")
	defer vncCleanup()
	gwPort, gwCleanup := startEchoServer(t)
	defer gwCleanup()

	sshAddr, sshCleanup := startTestSSHServer(t, portMapping{
		DefaultVNCPort:     vncPort,
		DefaultGatewayPort: gwPort,
	})
	defer sshCleanup()

	sm := sshmanager.NewSSHManager()
	client := connectTestSSH(t, sshAddr)
	defer client.Close()
	sm.SetClient("test-instance", client)

	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	if err := tm.StartTunnelsForInstance(t.Context(), "test-instance"); err != nil {
		t.Fatalf("StartTunnelsForInstance: %v", err)
	}

	tunnels := tm.GetTunnels("test-instance")
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels, got %d", len(tunnels))
	}

	// Verify VNC tunnel
	vnc := findTunnel(tunnels, ServiceVNC)
	if vnc == nil {
		t.Fatal("VNC tunnel not found")
	}
	if vnc.Config.RemotePort != DefaultVNCPort {
		t.Errorf("VNC remote port = %d, want %d", vnc.Config.RemotePort, DefaultVNCPort)
	}
	if vnc.LocalPort == 0 {
		t.Error("VNC tunnel should have a bound local port")
	}
	if vnc.Config.Type != TunnelReverse {
		t.Errorf("VNC tunnel type = %s, want %s", vnc.Config.Type, TunnelReverse)
	}

	// Verify Gateway tunnel
	gw := findTunnel(tunnels, ServiceGateway)
	if gw == nil {
		t.Fatal("Gateway tunnel not found")
	}
	if gw.Config.RemotePort != DefaultGatewayPort {
		t.Errorf("Gateway remote port = %d, want %d", gw.Config.RemotePort, DefaultGatewayPort)
	}
	if gw.LocalPort == 0 {
		t.Error("Gateway tunnel should have a bound local port")
	}
	if gw.Config.Type != TunnelReverse {
		t.Errorf("Gateway tunnel type = %s, want %s", gw.Config.Type, TunnelReverse)
	}
}

func TestIntegrationVNCHTTPDataFlow(t *testing.T) {
	vncPort, vncCleanup := startHTTPTestServer(t, "VNC-OK")
	defer vncCleanup()
	gwPort, gwCleanup := startEchoServer(t)
	defer gwCleanup()

	sshAddr, sshCleanup := startTestSSHServer(t, portMapping{
		DefaultVNCPort:     vncPort,
		DefaultGatewayPort: gwPort,
	})
	defer sshCleanup()

	sm := sshmanager.NewSSHManager()
	client := connectTestSSH(t, sshAddr)
	defer client.Close()
	sm.SetClient("test-instance", client)

	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	if err := tm.StartTunnelsForInstance(t.Context(), "test-instance"); err != nil {
		t.Fatalf("StartTunnelsForInstance: %v", err)
	}

	vnc := findTunnel(tm.GetTunnels("test-instance"), ServiceVNC)
	if vnc == nil {
		t.Fatal("VNC tunnel not found")
	}

	// HTTP GET through the VNC tunnel should reach the mock HTTP server
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", vnc.LocalPort))
	if err != nil {
		t.Fatalf("HTTP GET through VNC tunnel: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "VNC-OK" {
		t.Errorf("body = %q, want %q", string(body), "VNC-OK")
	}
}

func TestIntegrationGatewayWebSocketDataFlow(t *testing.T) {
	vncPort, vncCleanup := startEchoServer(t)
	defer vncCleanup()
	wsPort, wsCleanup := startWSEchoServer(t)
	defer wsCleanup()

	sshAddr, sshCleanup := startTestSSHServer(t, portMapping{
		DefaultVNCPort:     vncPort,
		DefaultGatewayPort: wsPort,
	})
	defer sshCleanup()

	sm := sshmanager.NewSSHManager()
	client := connectTestSSH(t, sshAddr)
	defer client.Close()
	sm.SetClient("test-instance", client)

	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	if err := tm.StartTunnelsForInstance(t.Context(), "test-instance"); err != nil {
		t.Fatalf("StartTunnelsForInstance: %v", err)
	}

	gw := findTunnel(tm.GetTunnels("test-instance"), ServiceGateway)
	if gw == nil {
		t.Fatal("Gateway tunnel not found")
	}

	// WebSocket connection through the gateway tunnel
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/", gw.LocalPort)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket dial through gateway tunnel: %v", err)
	}
	defer conn.CloseNow()

	// Send and receive a message
	msg := "hello-gateway"
	if err := conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
		t.Fatalf("WebSocket write: %v", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("WebSocket read: %v", err)
	}
	if string(data) != msg {
		t.Errorf("echo = %q, want %q", string(data), msg)
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestIntegrationTunnelSurvivesDisruption(t *testing.T) {
	echoPort, echoCleanup := startEchoServer(t)
	defer echoCleanup()

	sshAddr, sshCleanup := startTestSSHServer(t, portMapping{
		DefaultVNCPort:     echoPort,
		DefaultGatewayPort: echoPort,
	})
	defer sshCleanup()

	sm := sshmanager.NewSSHManager()
	client := connectTestSSH(t, sshAddr)
	defer client.Close()
	sm.SetClient("test-instance", client)

	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	if err := tm.StartTunnelsForInstance(t.Context(), "test-instance"); err != nil {
		t.Fatalf("StartTunnelsForInstance: %v", err)
	}

	tunnels := tm.GetTunnels("test-instance")
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels, got %d", len(tunnels))
	}

	// Verify data flows before disruption
	vnc := findTunnel(tunnels, ServiceVNC)
	if vnc == nil {
		t.Fatal("VNC tunnel not found")
	}
	verifyEchoThroughPort(t, vnc.LocalPort, "before-disruption")

	// Simulate network disruption by closing the VNC tunnel
	vnc.Close()
	if !vnc.IsClosed() {
		t.Fatal("VNC tunnel should be closed after disruption")
	}

	// Trigger the health check / reconnection logic directly
	tm.checkAndReconnectTunnels(t.Context(), "test-instance")

	// A new VNC tunnel should have been created
	newVNC := findTunnel(tm.GetTunnels("test-instance"), ServiceVNC)
	if newVNC == nil {
		t.Fatal("VNC tunnel was not recreated after disruption")
	}

	// Verify data flows through the new tunnel
	verifyEchoThroughPort(t, newVNC.LocalPort, "after-disruption")
}

func TestIntegrationTunnelCleanupOnStop(t *testing.T) {
	echoPort, echoCleanup := startEchoServer(t)
	defer echoCleanup()

	sshAddr, sshCleanup := startTestSSHServer(t, portMapping{
		DefaultVNCPort:     echoPort,
		DefaultGatewayPort: echoPort,
	})
	defer sshCleanup()

	sm := sshmanager.NewSSHManager()
	client := connectTestSSH(t, sshAddr)
	defer client.Close()
	sm.SetClient("test-instance", client)

	tm := NewTunnelManager(sm)
	defer tm.Shutdown()

	if err := tm.StartTunnelsForInstance(t.Context(), "test-instance"); err != nil {
		t.Fatalf("StartTunnelsForInstance: %v", err)
	}

	tunnels := tm.GetTunnels("test-instance")
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels, got %d", len(tunnels))
	}

	// Capture bound local ports
	localPorts := make([]int, len(tunnels))
	for i, tun := range tunnels {
		localPorts[i] = tun.LocalPort
	}

	// Stop the instance — all tunnels should be closed
	if err := tm.StopTunnelsForInstance("test-instance"); err != nil {
		t.Fatalf("StopTunnelsForInstance: %v", err)
	}

	// Tunnels should be marked closed
	for _, tun := range tunnels {
		if !tun.IsClosed() {
			t.Errorf("tunnel (service=%s) should be closed after stop", tun.Config.Service)
		}
	}

	// No tunnels should remain in the manager
	remaining := tm.GetTunnels("test-instance")
	if len(remaining) != 0 {
		t.Errorf("expected 0 tunnels after stop, got %d", len(remaining))
	}

	// Local ports should be freed — connections should be refused
	for _, port := range localPorts {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Errorf("port %d should be freed after tunnel close", port)
		}
	}
}

// verifyEchoThroughPort connects to the given local port, sends data, and
// verifies it is echoed back through the tunnel.
func verifyEchoThroughPort(t *testing.T, port int, label string) {
	t.Helper()

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("[%s] dial port %d: %v", label, port, err)
	}
	defer conn.Close()

	payload := fmt.Sprintf("echo-test-%s", label)
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("[%s] write: %v", label, err)
	}

	buf := make([]byte, len(payload))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("[%s] read: %v", label, err)
	}
	if string(buf) != payload {
		t.Errorf("[%s] echo = %q, want %q", label, string(buf), payload)
	}
}
