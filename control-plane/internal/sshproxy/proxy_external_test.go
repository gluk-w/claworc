//go:build docker_integration

package sshproxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"
)

// getExternalAgentInfo reads container info from environment variables set by
// the TypeScript test harness (tests/ssh/proxy.test.ts). If any required
// variable is missing, the test is skipped so that normal docker_integration
// tests (which create their own containers) are not affected.
func getExternalAgentInfo(t *testing.T) (containerID, sshHost string, sshPort int) {
	t.Helper()
	containerID = os.Getenv("AGENT_CONTAINER_ID")
	sshHost = os.Getenv("AGENT_SSH_HOST")
	portStr := os.Getenv("AGENT_SSH_PORT")
	if containerID == "" || sshHost == "" || portStr == "" {
		t.Skip("External agent not configured (set AGENT_CONTAINER_ID, AGENT_SSH_HOST, AGENT_SSH_PORT)")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("invalid AGENT_SSH_PORT %q: %v", portStr, err)
	}
	return containerID, sshHost, port
}

// setupExternalEnv creates a test environment against an externally managed
// Docker container. The container lifecycle is handled by the TypeScript test
// harness, so cleanup only closes SSH connections and tunnels — NOT the container.
func setupExternalEnv(t *testing.T) (*dockerTestEnv, *agentInstance, *SSHManager, *TunnelManager, *dockerIntegrationOrchestrator) {
	t.Helper()

	containerID, sshHost, sshPort := getExternalAgentInfo(t)

	env := newDockerTestEnv(t)
	inst := &agentInstance{
		containerID: containerID,
		name:        "external-agent",
		sshHost:     sshHost,
		sshPort:     sshPort,
	}

	tmpDir := t.TempDir()
	signer, publicKey, err := EnsureKeyPair(tmpDir)
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}

	orch := &dockerIntegrationOrchestrator{env: env, inst: inst}
	sshMgr := NewSSHManager(signer, publicKey)
	tunnelMgr := NewTunnelManager(sshMgr)
	t.Cleanup(func() {
		tunnelMgr.StopAll()
		sshMgr.CloseAll()
	})

	return env, inst, sshMgr, tunnelMgr, orch
}

// TestExternalIntegration_TunnelProxyEndToEnd tests the full SSH tunnel proxy
// lifecycle against an externally managed Docker container:
//   - SSH connection with on-demand key upload via EnsureConnected
//   - Automatic VNC and Gateway tunnel creation via StartTunnelsForInstance
//   - HTTP data flow through VNC tunnel (reaches agent port 3000)
//   - TCP data flow through Gateway tunnel (reaches agent port 18789)
//   - Tunnel cleanup: local ports closed and state cleared
func TestExternalIntegration_TunnelProxyEndToEnd(t *testing.T) {
	env, inst, sshMgr, tunnelMgr, orch := setupExternalEnv(t)

	waitForSSHD(t, inst.sshHost, inst.sshPort)
	env.startMockServices(t, inst.containerID)

	ctx := context.Background()

	t.Run("ssh_connection_and_key_upload", func(t *testing.T) {
		client, err := sshMgr.EnsureConnected(ctx, 1, orch)
		if err != nil {
			t.Fatalf("EnsureConnected: %v", err)
		}
		if client == nil {
			t.Fatal("SSH client is nil")
		}
		if !sshMgr.IsConnected(1) {
			t.Fatal("not connected after EnsureConnected")
		}
		if orch.configureCalls != 1 {
			t.Errorf("ConfigureSSHAccess called %d times, want 1", orch.configureCalls)
		}
		t.Log("SSH connection established with on-demand key upload")
	})

	t.Run("tunnel_creation", func(t *testing.T) {
		if err := tunnelMgr.StartTunnelsForInstance(ctx, 1, orch); err != nil {
			t.Fatalf("StartTunnelsForInstance: %v", err)
		}
		tunnels := tunnelMgr.GetTunnelsForInstance(1)
		if len(tunnels) != 2 {
			t.Fatalf("expected 2 tunnels (VNC + Gateway), got %d", len(tunnels))
		}
		for _, tun := range tunnels {
			if tun.Status != "active" {
				t.Errorf("tunnel %s: status = %q, want active", tun.Label, tun.Status)
			}
		}
		t.Logf("Tunnels created: VNC:%d, Gateway:%d",
			tunnelMgr.GetVNCLocalPort(1), tunnelMgr.GetGatewayLocalPort(1))
	})

	t.Run("http_proxy_through_vnc_tunnel", func(t *testing.T) {
		vncPort := tunnelMgr.GetVNCLocalPort(1)
		if vncPort == 0 {
			t.Fatal("VNC tunnel port is 0")
		}
		resp := httpGetWithRetry(t, fmt.Sprintf("http://127.0.0.1:%d/", vncPort), serviceReadyTimeout)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("HTTP status = %d, want 200", resp.StatusCode)
		}
		if len(body) == 0 {
			t.Error("HTTP response body is empty")
		}
		t.Logf("VNC proxy: HTTP %d, %d bytes", resp.StatusCode, len(body))
	})

	t.Run("tcp_proxy_through_gateway_tunnel", func(t *testing.T) {
		gwPort := tunnelMgr.GetGatewayLocalPort(1)
		if gwPort == 0 {
			t.Fatal("Gateway tunnel port is 0")
		}
		tcpEchoTest(t, gwPort, "external-proxy-integration", serviceReadyTimeout)
		t.Log("Gateway proxy: TCP echo OK")
	})

	t.Run("tunnel_cleanup", func(t *testing.T) {
		vncPort := tunnelMgr.GetVNCLocalPort(1)
		gwPort := tunnelMgr.GetGatewayLocalPort(1)

		if err := tunnelMgr.StopTunnelsForInstance(1); err != nil {
			t.Fatalf("StopTunnelsForInstance: %v", err)
		}

		remaining := tunnelMgr.GetTunnelsForInstance(1)
		if len(remaining) != 0 {
			t.Errorf("expected 0 tunnels after cleanup, got %d", len(remaining))
		}

		if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", vncPort), time.Second); err == nil {
			conn.Close()
			t.Error("VNC port still accepting connections after cleanup")
		}
		if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", gwPort), time.Second); err == nil {
			conn.Close()
			t.Error("Gateway port still accepting connections after cleanup")
		}
		t.Log("Tunnels cleaned up: local ports closed")
	})
}

// TestExternalIntegration_TunnelReuse verifies that calling StartTunnelsForInstance
// multiple times reuses existing healthy tunnels (same local ports). This matches
// the behavior of the 60-second reconciliation loop — repeated calls should not
// disrupt active connections.
func TestExternalIntegration_TunnelReuse(t *testing.T) {
	env, inst, _, tunnelMgr, orch := setupExternalEnv(t)

	waitForSSHD(t, inst.sshHost, inst.sshPort)
	env.startMockServices(t, inst.containerID)

	ctx := context.Background()

	// First call creates tunnels
	if err := tunnelMgr.StartTunnelsForInstance(ctx, 1, orch); err != nil {
		t.Fatalf("first StartTunnelsForInstance: %v", err)
	}
	vncPort1 := tunnelMgr.GetVNCLocalPort(1)
	gwPort1 := tunnelMgr.GetGatewayLocalPort(1)
	if vncPort1 == 0 || gwPort1 == 0 {
		t.Fatalf("tunnels not created (VNC:%d, Gateway:%d)", vncPort1, gwPort1)
	}

	// Verify initial data flow
	resp := httpGetWithRetry(t, fmt.Sprintf("http://127.0.0.1:%d/", vncPort1), serviceReadyTimeout)
	resp.Body.Close()
	tcpEchoTest(t, gwPort1, "before-reuse", serviceReadyTimeout)

	// Second call should reuse existing tunnels
	if err := tunnelMgr.StartTunnelsForInstance(ctx, 1, orch); err != nil {
		t.Fatalf("second StartTunnelsForInstance: %v", err)
	}
	vncPort2 := tunnelMgr.GetVNCLocalPort(1)
	gwPort2 := tunnelMgr.GetGatewayLocalPort(1)

	if vncPort1 != vncPort2 {
		t.Errorf("VNC port changed on reuse: %d -> %d", vncPort1, vncPort2)
	}
	if gwPort1 != gwPort2 {
		t.Errorf("Gateway port changed on reuse: %d -> %d", gwPort1, gwPort2)
	}

	// Verify data flow still works after reuse
	resp = httpGetWithRetry(t, fmt.Sprintf("http://127.0.0.1:%d/", vncPort2), serviceReadyTimeout)
	resp.Body.Close()
	tcpEchoTest(t, gwPort2, "after-reuse", serviceReadyTimeout)

	t.Logf("Tunnel reuse verified: VNC:%d, Gateway:%d (ports unchanged, data flow OK)", vncPort2, gwPort2)
}

// TestExternalIntegration_ConcurrentTunnelProxy tests concurrent HTTP and TCP
// requests through SSH tunnels to verify the proxy handles parallel traffic
// without errors or data corruption.
func TestExternalIntegration_ConcurrentTunnelProxy(t *testing.T) {
	env, inst, _, tunnelMgr, orch := setupExternalEnv(t)

	waitForSSHD(t, inst.sshHost, inst.sshPort)
	env.startMockServices(t, inst.containerID)

	ctx := context.Background()
	if err := tunnelMgr.StartTunnelsForInstance(ctx, 1, orch); err != nil {
		t.Fatalf("StartTunnelsForInstance: %v", err)
	}

	vncPort := tunnelMgr.GetVNCLocalPort(1)
	gwPort := tunnelMgr.GetGatewayLocalPort(1)
	if vncPort == 0 || gwPort == 0 {
		t.Fatalf("tunnels not created (VNC:%d, Gateway:%d)", vncPort, gwPort)
	}

	// Wait for services to be reachable
	httpGetWithRetry(t, fmt.Sprintf("http://127.0.0.1:%d/", vncPort), serviceReadyTimeout).Body.Close()
	tcpEchoTest(t, gwPort, "warmup", serviceReadyTimeout)

	t.Run("concurrent_http_requests", func(t *testing.T) {
		const concurrency = 10
		errCh := make(chan error, concurrency)

		for i := 0; i < concurrency; i++ {
			go func(idx int) {
				client := &http.Client{Timeout: 10 * time.Second}
				resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", vncPort))
				if err != nil {
					errCh <- fmt.Errorf("request %d: %w", idx, err)
					return
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					errCh <- fmt.Errorf("request %d: status %d", idx, resp.StatusCode)
					return
				}
				errCh <- nil
			}(i)
		}

		for i := 0; i < concurrency; i++ {
			if err := <-errCh; err != nil {
				t.Error(err)
			}
		}
		t.Logf("%d concurrent HTTP requests through VNC tunnel: OK", concurrency)
	})

	t.Run("concurrent_tcp_echo", func(t *testing.T) {
		const concurrency = 5
		errCh := make(chan error, concurrency)

		for i := 0; i < concurrency; i++ {
			go func(idx int) {
				msg := fmt.Sprintf("concurrent-echo-%d", idx)
				errCh <- tryTCPEcho(gwPort, msg)
			}(i)
		}

		for i := 0; i < concurrency; i++ {
			if err := <-errCh; err != nil {
				t.Error(err)
			}
		}
		t.Logf("%d concurrent TCP echo requests through Gateway tunnel: OK", concurrency)
	})
}
