//go:build docker_integration

package sshmanager

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/sshkeys"
)

// These tests require Docker and the claworc-agent-ssh-test:latest image.
// Build it with: docker build -t claworc-agent-ssh-test:latest agent/
// Run with: go test -tags docker_integration -run TestDocker -v -timeout 180s

const (
	testImage     = "claworc-agent-ssh-test:latest"
	testContainer = "claworc-ssh-integration-test"
)

// dockerRun starts the agent container with SSH port mapped to a random host port.
// Returns the host and mapped port for SSH access.
func dockerRun(t *testing.T) (host string, port int, cleanup func()) {
	t.Helper()

	// Remove any leftover container from a previous failed run
	exec.Command("docker", "rm", "-f", testContainer).Run()

	// Start the container with SSH port published to a random host port
	cmd := exec.Command("docker", "run", "-d",
		"--name", testContainer,
		"--hostname", "agent-test",
		"-p", "0:22", // random host port -> container port 22
		testImage,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker run failed: %v\n%s", err, out)
	}
	containerID := strings.TrimSpace(string(out))
	t.Logf("started container %s", containerID[:12])

	cleanup = func() {
		exec.Command("docker", "rm", "-f", testContainer).Run()
	}

	// Get the mapped host port for container port 22
	portCmd := exec.Command("docker", "port", testContainer, "22")
	portOut, err := portCmd.CombinedOutput()
	if err != nil {
		cleanup()
		t.Fatalf("docker port failed: %v\n%s", err, portOut)
	}

	// Output is like "0.0.0.0:55123" or "[::]:55123"
	mappedAddr := strings.TrimSpace(string(portOut))
	// Take the first line if multiple bindings
	if lines := strings.Split(mappedAddr, "\n"); len(lines) > 0 {
		mappedAddr = strings.TrimSpace(lines[0])
	}

	_, portStr, err := net.SplitHostPort(mappedAddr)
	if err != nil {
		cleanup()
		t.Fatalf("parse mapped port %q: %v", mappedAddr, err)
	}
	fmt.Sscanf(portStr, "%d", &port)

	host = "127.0.0.1"
	t.Logf("SSH accessible at %s:%d", host, port)

	return host, port, cleanup
}

// waitForSSHPort polls the container's SSH port until sshd is fully ready
// to accept SSH handshakes (not just TCP connections).
func waitForSSHPort(t *testing.T, host string, port int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	// First wait for TCP port to open
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(1 * time.Second)
	}

	// Then verify sshd sends its banner (e.g. "SSH-2.0-OpenSSH_...")
	// This confirms sshd is fully ready, not just the TCP listener.
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 256)
		n, err := conn.Read(buf)
		conn.Close()
		if err == nil && n > 0 && strings.HasPrefix(string(buf[:n]), "SSH-") {
			t.Logf("SSH port %s is ready (banner: %s)", addr, strings.TrimSpace(string(buf[:n])))
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("SSH port %s not ready after %v", addr, timeout)
}

// provisionSSHKey generates an SSH key pair, writes the authorized_keys into
// the container, and returns the path to the private key on the host.
func provisionSSHKey(t *testing.T, containerName string) (privateKeyPath string) {
	t.Helper()

	pubKey, privKey, err := sshkeys.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	t.Log("generated ED25519 SSH key pair")

	// Save private key to temp dir
	tmpDir := t.TempDir()
	keyPath, err := sshkeys.SavePrivateKeyToDir(tmpDir, "integration-test", privKey)
	if err != nil {
		t.Fatalf("SavePrivateKeyToDir: %v", err)
	}
	t.Logf("saved private key to %s", keyPath)

	// Format the public key
	authorizedKey, err := sshkeys.FormatPublicKeyForAuthorizedKeys(pubKey)
	if err != nil {
		t.Fatalf("FormatPublicKeyForAuthorizedKeys: %v", err)
	}

	// Create .ssh directory in the container
	mkdirCmd := exec.Command("docker", "exec", containerName,
		"sh", "-c", "mkdir -p /root/.ssh && chmod 700 /root/.ssh")
	if out, err := mkdirCmd.CombinedOutput(); err != nil {
		t.Fatalf("mkdir .ssh failed: %v\n%s", err, out)
	}

	// Write authorized_keys using base64 to avoid shell escaping issues
	b64Key := base64.StdEncoding.EncodeToString([]byte(authorizedKey))
	writeCmd := exec.Command("docker", "exec", containerName,
		"sh", "-c", fmt.Sprintf("echo '%s' | base64 -d > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys", b64Key))
	if out, err := writeCmd.CombinedOutput(); err != nil {
		t.Fatalf("write authorized_keys failed: %v\n%s", err, out)
	}
	t.Log("provisioned public key into container's authorized_keys")

	// Verify the key was written correctly
	verifyCmd := exec.Command("docker", "exec", containerName,
		"sh", "-c", "cat /root/.ssh/authorized_keys")
	verifyOut, err := verifyCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("verify authorized_keys failed: %v\n%s", err, verifyOut)
	}
	if !strings.Contains(string(verifyOut), "ssh-ed25519") {
		t.Fatalf("authorized_keys does not contain expected key: %s", string(verifyOut))
	}
	t.Log("verified authorized_keys file in container")

	return keyPath
}

func TestDockerSSHKeyGenAndStorage(t *testing.T) {
	host, port, cleanup := dockerRun(t)
	defer cleanup()

	waitForSSHPort(t, host, port, 60*time.Second)

	keyPath := provisionSSHKey(t, testContainer)

	// Verify private key was stored correctly and can be loaded back
	loadedKey, err := sshkeys.LoadPrivateKey(keyPath)
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	if len(loadedKey) == 0 {
		t.Fatal("loaded private key is empty")
	}

	// Verify the key file has correct permissions
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected key file permissions 0600, got %o", info.Mode().Perm())
	}

	t.Log("PASS: SSH keys are generated and stored correctly")
}

func TestDockerSSHPublicKeyUpload(t *testing.T) {
	host, port, cleanup := dockerRun(t)
	defer cleanup()

	waitForSSHPort(t, host, port, 60*time.Second)

	provisionSSHKey(t, testContainer)

	// Verify authorized_keys file in container
	cmd := exec.Command("docker", "exec", testContainer,
		"sh", "-c", "cat /root/.ssh/authorized_keys")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cat authorized_keys: %v\n%s", err, out)
	}

	content := string(out)
	if !strings.HasPrefix(content, "ssh-ed25519 ") {
		t.Errorf("authorized_keys does not start with ssh-ed25519: %s", content)
	}

	// Verify .ssh directory permissions
	permCmd := exec.Command("docker", "exec", testContainer,
		"sh", "-c", "stat -c '%a' /root/.ssh")
	permOut, err := permCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("stat .ssh: %v\n%s", err, permOut)
	}
	if !strings.Contains(string(permOut), "700") {
		t.Errorf("expected .ssh permissions 700, got %s", strings.TrimSpace(string(permOut)))
	}

	// Verify authorized_keys permissions
	akPermCmd := exec.Command("docker", "exec", testContainer,
		"sh", "-c", "stat -c '%a' /root/.ssh/authorized_keys")
	akPermOut, err := akPermCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("stat authorized_keys: %v\n%s", err, akPermOut)
	}
	if !strings.Contains(string(akPermOut), "600") {
		t.Errorf("expected authorized_keys permissions 600, got %s", strings.TrimSpace(string(akPermOut)))
	}

	t.Log("PASS: Public key uploaded to agent's authorized_keys with correct permissions")
}

func TestDockerSSHConnectionAndCommandExecution(t *testing.T) {
	host, port, cleanup := dockerRun(t)
	defer cleanup()

	waitForSSHPort(t, host, port, 60*time.Second)

	keyPath := provisionSSHKey(t, testContainer)

	// Create SSH manager and connect
	mgr := NewSSHManager(0)
	defer mgr.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mgr.Connect(ctx, "integration-test", host, port, keyPath)
	if err != nil {
		t.Fatalf("SSH Connect failed: %v", err)
	}
	if client == nil {
		t.Fatal("Connect returned nil client")
	}
	t.Log("SSH connection established successfully")

	// Verify the connection is tracked
	if !mgr.HasClient("integration-test") {
		t.Error("HasClient should return true after Connect")
	}
	if mgr.ConnectionCount() != 1 {
		t.Errorf("expected 1 connection, got %d", mgr.ConnectionCount())
	}

	// Execute a command via SSH (simulates the ssh-test endpoint)
	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput("echo 'SSH test successful'")
	if err != nil {
		t.Fatalf("command execution failed: %v", err)
	}

	result := strings.TrimSpace(string(output))
	if result != "SSH test successful" {
		t.Errorf("expected 'SSH test successful', got %q", result)
	}
	t.Logf("command output: %s", result)

	t.Log("PASS: SSH connection test and command execution succeeded")
}

func TestDockerSSHMultipleCommands(t *testing.T) {
	host, port, cleanup := dockerRun(t)
	defer cleanup()

	waitForSSHPort(t, host, port, 60*time.Second)

	keyPath := provisionSSHKey(t, testContainer)

	mgr := NewSSHManager(0)
	defer mgr.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mgr.Connect(ctx, "integration-test", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Test multiple commands in separate sessions
	commands := []struct {
		cmd      string
		expected string
	}{
		{"echo hello", "hello"},
		{"hostname", "agent-test"},
		{"whoami", "root"},
		{"cat /etc/hostname", "agent-test"},
	}

	for _, tc := range commands {
		session, err := client.NewSession()
		if err != nil {
			t.Fatalf("NewSession for %q: %v", tc.cmd, err)
		}
		output, err := session.CombinedOutput(tc.cmd)
		session.Close()
		if err != nil {
			t.Errorf("command %q failed: %v", tc.cmd, err)
			continue
		}
		got := strings.TrimSpace(string(output))
		if got != tc.expected {
			t.Errorf("command %q: expected %q, got %q", tc.cmd, tc.expected, got)
		} else {
			t.Logf("command %q -> %q", tc.cmd, got)
		}
	}

	t.Log("PASS: Multiple command executions via SSH all succeeded")
}

func TestDockerSSHKeepalive(t *testing.T) {
	host, port, cleanup := dockerRun(t)
	defer cleanup()

	waitForSSHPort(t, host, port, 60*time.Second)

	keyPath := provisionSSHKey(t, testContainer)

	mgr := NewSSHManager(0)
	defer mgr.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mgr.Connect(ctx, "integration-test", host, port, keyPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Send a keepalive and verify the connection is healthy
	_, _, err = client.SendRequest("keepalive@openssh.com", true, nil)
	if err != nil {
		t.Fatalf("keepalive request failed: %v", err)
	}
	t.Log("keepalive request succeeded")

	// Run checkConnections and verify connection survives
	mgr.checkConnections()
	if !mgr.HasClient("integration-test") {
		t.Error("healthy connection should survive checkConnections")
	}

	t.Log("PASS: SSH keepalive mechanism works correctly")
}

func TestDockerSSHReconnect(t *testing.T) {
	host, port, cleanup := dockerRun(t)
	defer cleanup()

	waitForSSHPort(t, host, port, 60*time.Second)

	keyPath := provisionSSHKey(t, testContainer)

	mgr := NewSSHManager(0)
	defer mgr.CloseAll()

	ctx := context.Background()

	// First connection
	client1, err := mgr.Connect(ctx, "integration-test", host, port, keyPath)
	if err != nil {
		t.Fatalf("first Connect: %v", err)
	}

	// Verify first connection works
	sess1, _ := client1.NewSession()
	out1, err := sess1.CombinedOutput("echo first")
	sess1.Close()
	if err != nil {
		t.Fatalf("first command: %v", err)
	}
	if strings.TrimSpace(string(out1)) != "first" {
		t.Fatalf("first command unexpected output: %s", out1)
	}

	// Reconnect (replaces existing connection)
	client2, err := mgr.Connect(ctx, "integration-test", host, port, keyPath)
	if err != nil {
		t.Fatalf("second Connect: %v", err)
	}
	if client1 == client2 {
		t.Error("reconnect should produce a new client")
	}

	// Verify new connection works
	sess2, _ := client2.NewSession()
	out2, err := sess2.CombinedOutput("echo second")
	sess2.Close()
	if err != nil {
		t.Fatalf("second command: %v", err)
	}
	if strings.TrimSpace(string(out2)) != "second" {
		t.Fatalf("second command unexpected output: %s", out2)
	}

	if mgr.ConnectionCount() != 1 {
		t.Errorf("expected 1 connection after reconnect, got %d", mgr.ConnectionCount())
	}

	t.Log("PASS: SSH reconnection works correctly")
}
