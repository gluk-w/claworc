//go:build docker_integration

package sshproxy

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

const (
	// serviceReadyTimeout is how long to wait for mock services and tunnels
	// to become functional before failing.
	serviceReadyTimeout = 15 * time.Second
)

// startMockServices starts an HTTP server on port 3000 and a TCP echo server
// on port 18789 inside the container to simulate VNC and Gateway services.
func (e *dockerTestEnv) startMockServices(t *testing.T, containerID string) {
	t.Helper()
	ctx := context.Background()

	// HTTP server on port 3000 (simulates VNC/Selkies web interface)
	_, err := e.execInContainer(ctx, containerID, []string{"sh", "-c",
		"nohup python3 -m http.server 3000 --bind 0.0.0.0 >/dev/null 2>&1 &"})
	if err != nil {
		t.Fatalf("start HTTP server on 3000: %v", err)
	}

	// TCP echo server on port 18789 (simulates Gateway service).
	// Write the script to a file first, then run it in the background.
	echoScript := `import socket
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", 18789))
s.listen(5)
while True:
    c, a = s.accept()
    try:
        d = c.recv(4096)
        if d:
            c.sendall(d)
    finally:
        c.close()
`
	b64Script := base64.StdEncoding.EncodeToString([]byte(echoScript))
	writeCmd := fmt.Sprintf("echo '%s' | base64 -d > /tmp/echo_server.py", b64Script)
	_, err = e.execInContainer(ctx, containerID, []string{"sh", "-c", writeCmd})
	if err != nil {
		t.Fatalf("write echo server script: %v", err)
	}

	_, err = e.execInContainer(ctx, containerID, []string{"sh", "-c",
		"nohup python3 /tmp/echo_server.py >/dev/null 2>&1 &"})
	if err != nil {
		t.Fatalf("start TCP echo server on 18789: %v", err)
	}

	// Allow services time to bind their ports
	time.Sleep(2 * time.Second)
	t.Log("Mock services started (HTTP:3000, TCP echo:18789)")
}

// removeAuthorizedKeys deletes the SSH authorized_keys file, simulating
// an agent restart where the uploaded key is lost.
func (e *dockerTestEnv) removeAuthorizedKeys(ctx context.Context, containerID string) error {
	_, err := e.execInContainer(ctx, containerID, []string{"rm", "-f", "/root/.ssh/authorized_keys"})
	return err
}

// httpGetWithRetry retries HTTP GET requests until success or timeout.
// This accounts for the tunnel and remote service needing a moment to
// become fully functional.
func httpGetWithRetry(t *testing.T, url string, timeout time.Duration) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			return resp
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("HTTP GET %s failed after %v: %v", url, timeout, lastErr)
	return nil
}

// tcpEchoTest sends a message through a TCP tunnel and verifies it echoes back.
// Retries until success or timeout.
func tcpEchoTest(t *testing.T, port int, msg string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := tryTCPEcho(port, msg); err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		return
	}
	t.Fatalf("TCP echo on port %d failed after %v: %v", port, timeout, lastErr)
}

func tryTCPEcho(port int, msg string) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte(msg)); err != nil {
		return err
	}

	buf := make([]byte, len(msg))
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return err
	}
	if string(buf) != msg {
		return fmt.Errorf("echo mismatch: got %q, want %q", string(buf), msg)
	}
	return nil
}

// TestIntegration_TunnelEstablishmentDataFlowAndCleanup tests the full tunnel lifecycle:
//   - SSH connection establishment with on-demand key upload
//   - Automatic VNC and Gateway tunnel creation
//   - HTTP data flow through VNC tunnel (reaches agent port 3000)
//   - TCP data flow through Gateway tunnel (reaches agent port 18789)
//   - Tunnel cleanup when instance is stopped
func TestIntegration_TunnelEstablishmentDataFlowAndCleanup(t *testing.T) {
	env := newDockerTestEnv(t)
	defer env.cleanup(t)

	tmpDir := t.TempDir()
	signer, publicKey, err := EnsureKeyPair(tmpDir)
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}

	inst := env.startAgent(t, "claworc-inttest-tunnel-flow")
	t.Logf("Agent started at %s:%d", inst.sshHost, inst.sshPort)
	waitForSSHD(t, inst.sshHost, inst.sshPort)
	env.startMockServices(t, inst.containerID)

	orch := &dockerIntegrationOrchestrator{env: env, inst: inst}
	sshMgr := NewSSHManager(signer, publicKey)
	defer sshMgr.CloseAll()
	tunnelMgr := NewTunnelManager(sshMgr)

	ctx := context.Background()

	// --- Tunnel Establishment ---
	// StartTunnelsForInstance uses EnsureConnected to upload the public key
	// on-demand and establish an SSH connection before creating tunnels.
	if err := tunnelMgr.StartTunnelsForInstance(ctx, uint(1), orch); err != nil {
		t.Fatalf("StartTunnelsForInstance: %v", err)
	}

	// Verify SSH connection was established with on-demand key upload
	if !sshMgr.IsConnected(uint(1)) {
		t.Fatal("SSH connection not established after StartTunnelsForInstance")
	}
	if orch.configureCalls != 1 {
		t.Errorf("ConfigureSSHAccess called %d times, want 1 (on-demand key upload)", orch.configureCalls)
	}
	t.Log("SSH connection established, public key uploaded on-demand")

	// Verify VNC reverse tunnel created automatically
	tunnels := tunnelMgr.GetTunnelsForInstance(uint(1))
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels (VNC + Gateway), got %d", len(tunnels))
	}
	vncPort := tunnelMgr.GetVNCLocalPort(uint(1))
	if vncPort == 0 {
		t.Fatal("VNC tunnel not created")
	}
	t.Logf("VNC tunnel: localhost:%d -> agent:3000", vncPort)

	// Verify Gateway reverse tunnel created automatically
	gwPort := tunnelMgr.GetGatewayLocalPort(uint(1))
	if gwPort == 0 {
		t.Fatal("Gateway tunnel not created")
	}
	t.Logf("Gateway tunnel: localhost:%d -> agent:18789", gwPort)

	for _, tun := range tunnels {
		if tun.Status != "active" {
			t.Errorf("tunnel %s: status = %q, want 'active'", tun.Label, tun.Status)
		}
	}

	// --- Data Flow: HTTP GET through VNC tunnel ---
	// Sends an HTTP GET to localhost:{vnc_tunnel_port} which reaches the
	// agent's HTTP server on port 3000 via the SSH reverse tunnel.
	resp := httpGetWithRetry(t, fmt.Sprintf("http://127.0.0.1:%d/", vncPort), serviceReadyTimeout)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("VNC tunnel: HTTP status = %d, want 200", resp.StatusCode)
	}
	if len(body) == 0 {
		t.Error("VNC tunnel: HTTP response body is empty")
	}
	t.Logf("VNC tunnel data flow: HTTP %d, %d bytes", resp.StatusCode, len(body))

	// --- Data Flow: TCP echo through Gateway tunnel ---
	// WebSocket connections use TCP as transport, so validating TCP data
	// flow proves the tunnel can carry WebSocket traffic to the agent's
	// gateway service on port 18789.
	tcpEchoTest(t, gwPort, "hello-gateway-tunnel", serviceReadyTimeout)
	t.Log("Gateway tunnel data flow: TCP echo OK")

	// --- Cleanup ---
	if err := tunnelMgr.StopTunnelsForInstance(uint(1)); err != nil {
		t.Fatalf("StopTunnelsForInstance: %v", err)
	}
	tunnels = tunnelMgr.GetTunnelsForInstance(uint(1))
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels after cleanup, got %d", len(tunnels))
	}

	// Verify local tunnel ports are no longer accepting connections
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", vncPort), time.Second); err == nil {
		conn.Close()
		t.Error("VNC tunnel port still accepting connections after cleanup")
	}
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", gwPort), time.Second); err == nil {
		conn.Close()
		t.Error("Gateway tunnel port still accepting connections after cleanup")
	}
	t.Log("Tunnel cleanup: all tunnels stopped, local ports closed")
}

// TestIntegration_TunnelRecoveryAfterDisruption tests that tunnels are properly
// detected as unhealthy after an SSH connection drop and are recreated when
// StartTunnelsForInstance is called again (as the reconcile loop would do).
func TestIntegration_TunnelRecoveryAfterDisruption(t *testing.T) {
	env := newDockerTestEnv(t)
	defer env.cleanup(t)

	tmpDir := t.TempDir()
	signer, publicKey, err := EnsureKeyPair(tmpDir)
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}

	inst := env.startAgent(t, "claworc-inttest-tunnel-disrupt")
	waitForSSHD(t, inst.sshHost, inst.sshPort)
	env.startMockServices(t, inst.containerID)

	orch := &dockerIntegrationOrchestrator{env: env, inst: inst}
	sshMgr := NewSSHManager(signer, publicKey)
	defer sshMgr.CloseAll()
	tunnelMgr := NewTunnelManager(sshMgr)

	ctx := context.Background()

	// Establish initial tunnels and verify data flow
	if err := tunnelMgr.StartTunnelsForInstance(ctx, uint(1), orch); err != nil {
		t.Fatalf("initial StartTunnelsForInstance: %v", err)
	}
	vncPort := tunnelMgr.GetVNCLocalPort(uint(1))
	gwPort := tunnelMgr.GetGatewayLocalPort(uint(1))
	httpGetWithRetry(t, fmt.Sprintf("http://127.0.0.1:%d/", vncPort), serviceReadyTimeout).Body.Close()
	tcpEchoTest(t, gwPort, "pre-disruption", serviceReadyTimeout)
	t.Log("Initial tunnels verified with data flow")

	initialConfigCalls := orch.configureCalls

	// Simulate network disruption: close the SSH connection
	sshMgr.Close(uint(1))
	t.Log("SSH connection closed (simulating network disruption)")

	// Tunnels should be detected as unhealthy (SSH connection gone)
	if tunnelMgr.areTunnelsHealthy(uint(1)) {
		t.Error("expected unhealthy tunnels after SSH disruption")
	}

	// Recovery: StartTunnelsForInstance should detect unhealthy state,
	// reconnect via EnsureConnected, and recreate tunnels
	if err := tunnelMgr.StartTunnelsForInstance(ctx, uint(1), orch); err != nil {
		t.Fatalf("recovery StartTunnelsForInstance: %v", err)
	}

	// Verify key was re-uploaded during recovery
	if orch.configureCalls <= initialConfigCalls {
		t.Error("expected ConfigureSSHAccess to be called during recovery (key re-upload)")
	}

	// Verify new tunnels with data flow
	newVNCPort := tunnelMgr.GetVNCLocalPort(uint(1))
	newGWPort := tunnelMgr.GetGatewayLocalPort(uint(1))
	if newVNCPort == 0 || newGWPort == 0 {
		t.Fatalf("tunnels not recreated (VNC port: %d, GW port: %d)", newVNCPort, newGWPort)
	}
	resp := httpGetWithRetry(t, fmt.Sprintf("http://127.0.0.1:%d/", newVNCPort), serviceReadyTimeout)
	resp.Body.Close()
	tcpEchoTest(t, newGWPort, "post-disruption", serviceReadyTimeout)
	t.Log("Tunnels recovered after disruption: data flow verified")
}

// TestIntegration_TunnelReestablishAfterKeyLoss tests that when the agent loses
// its SSH authorized_keys (simulating an agent restart or pod replacement), the
// EnsureConnected flow re-uploads the public key and tunnels are re-established.
func TestIntegration_TunnelReestablishAfterKeyLoss(t *testing.T) {
	env := newDockerTestEnv(t)
	defer env.cleanup(t)

	tmpDir := t.TempDir()
	signer, publicKey, err := EnsureKeyPair(tmpDir)
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}

	inst := env.startAgent(t, "claworc-inttest-tunnel-keyloss")
	waitForSSHD(t, inst.sshHost, inst.sshPort)
	env.startMockServices(t, inst.containerID)

	orch := &dockerIntegrationOrchestrator{env: env, inst: inst}
	sshMgr := NewSSHManager(signer, publicKey)
	defer sshMgr.CloseAll()
	tunnelMgr := NewTunnelManager(sshMgr)

	ctx := context.Background()

	// Establish initial tunnels and verify
	if err := tunnelMgr.StartTunnelsForInstance(ctx, uint(1), orch); err != nil {
		t.Fatalf("initial StartTunnelsForInstance: %v", err)
	}
	vncPort := tunnelMgr.GetVNCLocalPort(uint(1))
	httpGetWithRetry(t, fmt.Sprintf("http://127.0.0.1:%d/", vncPort), serviceReadyTimeout).Body.Close()
	t.Log("Initial tunnels verified")

	initialConfigCalls := orch.configureCalls

	// Simulate agent restart: stop tunnels, close SSH, remove authorized_keys.
	// This mirrors what happens when an agent pod is replaced in Kubernetes —
	// the SSH connection drops and the public key is no longer on disk.
	tunnelMgr.StopTunnelsForInstance(uint(1))
	sshMgr.Close(uint(1))
	if err := env.removeAuthorizedKeys(ctx, inst.containerID); err != nil {
		t.Fatalf("remove authorized_keys: %v", err)
	}
	t.Log("Simulated agent restart: SSH closed, authorized_keys removed")

	// Reestablish: StartTunnelsForInstance calls EnsureConnected which
	// detects no connection, re-uploads the public key, reconnects, and
	// then creates new tunnels.
	if err := tunnelMgr.StartTunnelsForInstance(ctx, uint(1), orch); err != nil {
		t.Fatalf("reestablish StartTunnelsForInstance: %v", err)
	}

	// Verify the public key was re-uploaded
	if orch.configureCalls <= initialConfigCalls {
		t.Errorf("ConfigureSSHAccess called %d times (was %d), expected key re-upload",
			orch.configureCalls, initialConfigCalls)
	}
	t.Logf("Public key re-uploaded (configureCalls: %d -> %d)", initialConfigCalls, orch.configureCalls)

	// Verify new tunnels work with data flow
	newVNCPort := tunnelMgr.GetVNCLocalPort(uint(1))
	newGWPort := tunnelMgr.GetGatewayLocalPort(uint(1))
	if newVNCPort == 0 || newGWPort == 0 {
		t.Fatalf("tunnels not recreated (VNC port: %d, GW port: %d)", newVNCPort, newGWPort)
	}

	resp := httpGetWithRetry(t, fmt.Sprintf("http://127.0.0.1:%d/", newVNCPort), serviceReadyTimeout)
	resp.Body.Close()
	tcpEchoTest(t, newGWPort, "after-key-reupload", serviceReadyTimeout)
	t.Log("Tunnels reestablished after key loss: data flow verified")
}
