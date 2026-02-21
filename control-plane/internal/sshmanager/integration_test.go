//go:build docker_integration

package sshmanager

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"

	dockerclient "github.com/docker/docker/client"
	"github.com/gluk-w/claworc/control-plane/internal/sshkeys"
	"golang.org/x/crypto/ssh"
)

const (
	// defaultAgentImage is the Docker image used for integration tests.
	// Override with AGENT_TEST_IMAGE env var.
	defaultAgentImage = "claworc-agent:local"

	// sshReadyTimeout is how long to wait for sshd to start inside the container.
	sshReadyTimeout = 90 * time.Second

	// sshReadyPollInterval is how often to check if sshd is ready.
	sshReadyPollInterval = 1 * time.Second
)

func agentImage() string {
	if img := os.Getenv("AGENT_TEST_IMAGE"); img != "" {
		return img
	}
	return defaultAgentImage
}

// agentInstance tracks a running agent container.
type agentInstance struct {
	containerID string
	name        string
	sshHost     string // host to connect to (127.0.0.1 on Docker Desktop)
	sshPort     int    // mapped host port for SSH
}

// dockerTestEnv manages Docker resources for integration tests.
type dockerTestEnv struct {
	client    *dockerclient.Client
	instances []*agentInstance
}

func newDockerTestEnv(t *testing.T) *dockerTestEnv {
	t.Helper()
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}

	// Verify Docker is reachable
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		t.Fatalf("docker ping: %v", err)
	}

	return &dockerTestEnv{client: cli}
}

func (e *dockerTestEnv) cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, inst := range e.instances {
		timeout := 5
		e.client.ContainerStop(ctx, inst.containerID, container.StopOptions{Timeout: &timeout})
		e.client.ContainerRemove(ctx, inst.containerID, container.RemoveOptions{Force: true})
	}
}

// startAgent starts an agent container with port 22 published to a random host port.
// Returns the agentInstance with host/port for SSH access.
func (e *dockerTestEnv) startAgent(t *testing.T, name string) *agentInstance {
	t.Helper()
	ctx := context.Background()

	containerCfg := &container.Config{
		Image: agentImage(),
		Labels: map[string]string{
			"managed-by": "claworc-inttest",
			"test":       name,
		},
		ExposedPorts: nat.PortSet{
			"22/tcp": struct{}{},
		},
	}

	hostCfg := &container.HostConfig{
		Privileged: true,
		PortBindings: nat.PortMap{
			"22/tcp": []nat.PortBinding{
				{HostIP: "127.0.0.1", HostPort: "0"}, // random port
			},
		},
	}

	// Remove any stale container with the same name from a previous run
	e.client.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})

	resp, err := e.client.ContainerCreate(ctx, containerCfg, hostCfg, nil, nil, name)
	if err != nil {
		t.Fatalf("create container %s: %v", name, err)
	}

	if err := e.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		// Clean up on failure
		e.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		t.Fatalf("start container %s: %v", name, err)
	}

	// Get the mapped port
	inspect, err := e.client.ContainerInspect(ctx, resp.ID)
	if err != nil {
		t.Fatalf("inspect container %s: %v", name, err)
	}

	bindings, ok := inspect.NetworkSettings.Ports["22/tcp"]
	if !ok || len(bindings) == 0 {
		t.Fatalf("container %s: no port mapping for 22/tcp", name)
	}

	var port int
	fmt.Sscanf(bindings[0].HostPort, "%d", &port)

	inst := &agentInstance{
		containerID: resp.ID,
		name:        name,
		sshHost:     "127.0.0.1",
		sshPort:     port,
	}
	e.instances = append(e.instances, inst)
	return inst
}

// execInContainer runs a command inside a container and returns stdout.
func (e *dockerTestEnv) execInContainer(ctx context.Context, containerID string, cmd []string) (string, error) {
	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := e.client.ContainerExecCreate(ctx, containerID, execCfg)
	if err != nil {
		return "", fmt.Errorf("exec create: %w", err)
	}

	resp, err := e.client.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	output, err := io.ReadAll(resp.Reader)
	if err != nil {
		return "", fmt.Errorf("read output: %w", err)
	}

	inspectResp, err := e.client.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return string(output), fmt.Errorf("exec inspect: %w", err)
	}
	if inspectResp.ExitCode != 0 {
		return string(output), fmt.Errorf("exit code %d: %s", inspectResp.ExitCode, string(output))
	}

	return string(output), nil
}

// uploadPublicKey uploads a public key to the container's authorized_keys.
func (e *dockerTestEnv) uploadPublicKey(ctx context.Context, containerID, publicKey string) error {
	// Create .ssh directory
	_, err := e.execInContainer(ctx, containerID, []string{"sh", "-c", "mkdir -p /root/.ssh && chmod 700 /root/.ssh"})
	if err != nil {
		return fmt.Errorf("create .ssh dir: %w", err)
	}

	// Write public key using base64 encoding to safely pass content
	b64 := base64.StdEncoding.EncodeToString([]byte(publicKey))
	cmd := fmt.Sprintf("echo '%s' | base64 -d > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys", b64)
	_, err = e.execInContainer(ctx, containerID, []string{"sh", "-c", cmd})
	if err != nil {
		return fmt.Errorf("write authorized_keys: %w", err)
	}

	return nil
}

// waitForSSHD waits until sshd is accepting SSH connections and returning a
// valid protocol banner (e.g., "SSH-2.0-..."). A TCP-only check is not enough
// because Docker Desktop port forwarding accepts connections before sshd
// inside the container has finished generating host keys.
func waitForSSHD(t *testing.T, host string, port int) {
	t.Helper()

	deadline := time.Now().Add(sshReadyTimeout)
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			time.Sleep(sshReadyPollInterval)
			continue
		}
		// Read the SSH banner to confirm sshd is truly ready
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		buf := make([]byte, 256)
		n, err := conn.Read(buf)
		conn.Close()
		if err == nil && n > 0 && strings.HasPrefix(string(buf[:n]), "SSH-") {
			log.Printf("sshd ready at %s (banner: %s)", addr, strings.TrimSpace(string(buf[:n])))
			return
		}
		time.Sleep(sshReadyPollInterval)
	}
	t.Fatalf("sshd not ready at %s after %v", addr, sshReadyTimeout)
}

// --- Integration Tests ---

func TestIntegration_SSHConnectAndExecuteCommand(t *testing.T) {
	env := newDockerTestEnv(t)
	defer env.cleanup(t)

	// 1. Generate global key pair to a temp directory
	tmpDir := t.TempDir()
	signer, publicKey, err := sshkeys.EnsureKeyPair(tmpDir)
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	t.Logf("Generated key pair in %s (public key: %d bytes)", tmpDir, len(publicKey))

	// 2. Start an agent container
	inst := env.startAgent(t, "claworc-inttest-ssh-1")
	t.Logf("Agent container %s started at %s:%d", inst.name, inst.sshHost, inst.sshPort)

	// 3. Wait for sshd to be ready
	waitForSSHD(t, inst.sshHost, inst.sshPort)

	// 4. Upload the public key via exec
	ctx := context.Background()
	if err := env.uploadPublicKey(ctx, inst.containerID, publicKey); err != nil {
		t.Fatalf("upload public key: %v", err)
	}
	t.Log("Public key uploaded to container")

	// 5. Establish SSH connection with the private key
	mgr := NewSSHManager(signer, publicKey)
	defer mgr.CloseAll()

	client, err := mgr.Connect(ctx, "test-instance", inst.sshHost, inst.sshPort)
	if err != nil {
		t.Fatalf("SSH connect: %v", err)
	}
	t.Log("SSH connection established")

	// 6. Execute commands over SSH and verify output
	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	output, err := session.CombinedOutput("echo 'SSH test successful'")
	if err != nil {
		t.Fatalf("exec command: %v", err)
	}
	got := strings.TrimSpace(string(output))
	if got != "SSH test successful" {
		t.Errorf("command output = %q, want %q", got, "SSH test successful")
	}
	t.Logf("Command output: %s", got)

	// Test running whoami
	session2, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session for whoami: %v", err)
	}
	output2, err := session2.CombinedOutput("whoami")
	if err != nil {
		t.Fatalf("exec whoami: %v", err)
	}
	got2 := strings.TrimSpace(string(output2))
	if got2 != "root" {
		t.Errorf("whoami = %q, want %q", got2, "root")
	}
	t.Logf("whoami: %s", got2)

	// Test hostname command
	session3, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session for hostname: %v", err)
	}
	output3, err := session3.CombinedOutput("hostname")
	if err != nil {
		t.Fatalf("exec hostname: %v", err)
	}
	t.Logf("hostname: %s", strings.TrimSpace(string(output3)))
}

func TestIntegration_ReconnectAfterKeyReupload(t *testing.T) {
	env := newDockerTestEnv(t)
	defer env.cleanup(t)

	// Generate key pair
	tmpDir := t.TempDir()
	signer, publicKey, err := sshkeys.EnsureKeyPair(tmpDir)
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}

	// Start agent
	inst := env.startAgent(t, "claworc-inttest-ssh-rekey")
	waitForSSHD(t, inst.sshHost, inst.sshPort)

	ctx := context.Background()
	if err := env.uploadPublicKey(ctx, inst.containerID, publicKey); err != nil {
		t.Fatalf("upload public key: %v", err)
	}

	// First connection
	mgr := NewSSHManager(signer, publicKey)
	defer mgr.CloseAll()

	client1, err := mgr.Connect(ctx, "test-instance", inst.sshHost, inst.sshPort)
	if err != nil {
		t.Fatalf("first SSH connect: %v", err)
	}

	session1, err := client1.NewSession()
	if err != nil {
		t.Fatalf("first session: %v", err)
	}
	out1, err := session1.CombinedOutput("echo connection-1")
	if err != nil {
		t.Fatalf("first command: %v", err)
	}
	if !strings.Contains(string(out1), "connection-1") {
		t.Errorf("first output = %q, want to contain 'connection-1'", string(out1))
	}

	// Close connection
	if err := mgr.Close("test-instance"); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Re-upload the same key (idempotent operation)
	if err := env.uploadPublicKey(ctx, inst.containerID, publicKey); err != nil {
		t.Fatalf("re-upload public key: %v", err)
	}

	// Reconnect
	client2, err := mgr.Connect(ctx, "test-instance", inst.sshHost, inst.sshPort)
	if err != nil {
		t.Fatalf("second SSH connect: %v", err)
	}

	session2, err := client2.NewSession()
	if err != nil {
		t.Fatalf("second session: %v", err)
	}
	out2, err := session2.CombinedOutput("echo connection-2")
	if err != nil {
		t.Fatalf("second command: %v", err)
	}
	if !strings.Contains(string(out2), "connection-2") {
		t.Errorf("second output = %q, want to contain 'connection-2'", string(out2))
	}

	t.Log("Reconnection after key re-upload: OK")
}

func TestIntegration_SameKeyPairMultipleContainers(t *testing.T) {
	env := newDockerTestEnv(t)
	defer env.cleanup(t)

	// Single global key pair
	tmpDir := t.TempDir()
	signer, publicKey, err := sshkeys.EnsureKeyPair(tmpDir)
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}

	mgr := NewSSHManager(signer, publicKey)
	defer mgr.CloseAll()

	ctx := context.Background()
	names := []string{
		"claworc-inttest-multi-1",
		"claworc-inttest-multi-2",
	}
	instances := make([]*agentInstance, len(names))

	// Start multiple containers
	for i, name := range names {
		instances[i] = env.startAgent(t, name)
		t.Logf("Started %s at %s:%d", name, instances[i].sshHost, instances[i].sshPort)
	}

	// Wait for sshd in all containers
	for i, inst := range instances {
		waitForSSHD(t, inst.sshHost, inst.sshPort)
		t.Logf("sshd ready in %s", names[i])
	}

	// Upload the same key to all containers and connect
	for i, inst := range instances {
		if err := env.uploadPublicKey(ctx, inst.containerID, publicKey); err != nil {
			t.Fatalf("upload key to %s: %v", names[i], err)
		}

		instanceName := fmt.Sprintf("instance-%d", i)
		client, err := mgr.Connect(ctx, instanceName, inst.sshHost, inst.sshPort)
		if err != nil {
			t.Fatalf("connect to %s: %v", names[i], err)
		}

		// Execute a command to verify the connection works
		session, err := client.NewSession()
		if err != nil {
			t.Fatalf("session on %s: %v", names[i], err)
		}
		output, err := session.CombinedOutput(fmt.Sprintf("echo 'hello from %s'", names[i]))
		if err != nil {
			t.Fatalf("exec on %s: %v", names[i], err)
		}
		got := strings.TrimSpace(string(output))
		want := fmt.Sprintf("hello from %s", names[i])
		if got != want {
			t.Errorf("output from %s = %q, want %q", names[i], got, want)
		}
		t.Logf("Verified SSH to %s: %s", names[i], got)
	}

	// Verify all connections are active
	for i := range names {
		instanceName := fmt.Sprintf("instance-%d", i)
		if !mgr.IsConnected(instanceName) {
			t.Errorf("instance-%d should be connected", i)
		}
	}

	t.Log("Same key pair works across multiple containers: OK")
}

func TestIntegration_EnsureConnectedFlow(t *testing.T) {
	env := newDockerTestEnv(t)
	defer env.cleanup(t)

	// Generate key pair
	tmpDir := t.TempDir()
	signer, publicKey, err := sshkeys.EnsureKeyPair(tmpDir)
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}

	// Start agent
	inst := env.startAgent(t, "claworc-inttest-ensure")
	waitForSSHD(t, inst.sshHost, inst.sshPort)

	ctx := context.Background()

	// Create an orchestrator that does real Docker exec operations
	orch := &dockerIntegrationOrchestrator{
		env:  env,
		inst: inst,
	}

	mgr := NewSSHManager(signer, publicKey)
	defer mgr.CloseAll()

	// EnsureConnected should: get address → upload key → connect
	client, err := mgr.EnsureConnected(ctx, inst.name, orch)
	if err != nil {
		t.Fatalf("EnsureConnected: %v", err)
	}

	// Verify we can execute commands
	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	output, err := session.CombinedOutput("echo 'EnsureConnected works'")
	if err != nil {
		t.Fatalf("exec command: %v", err)
	}
	got := strings.TrimSpace(string(output))
	if got != "EnsureConnected works" {
		t.Errorf("output = %q, want %q", got, "EnsureConnected works")
	}

	// Second call should reuse cached connection
	client2, err := mgr.EnsureConnected(ctx, inst.name, orch)
	if err != nil {
		t.Fatalf("second EnsureConnected: %v", err)
	}
	if client != client2 {
		t.Error("second EnsureConnected returned different client, expected cache reuse")
	}

	// Verify orchestrator was called correctly
	if orch.addressCalls != 1 {
		t.Errorf("GetSSHAddress called %d times, want 1 (second call should use cache)", orch.addressCalls)
	}
	if orch.configureCalls != 1 {
		t.Errorf("ConfigureSSHAccess called %d times, want 1 (second call should use cache)", orch.configureCalls)
	}

	t.Log("EnsureConnected flow: OK")
}

func TestIntegration_KeyPairPersistence(t *testing.T) {
	// Verify that generating a key pair, saving it, and loading it back
	// produces a signer that works for SSH auth against a real container.
	env := newDockerTestEnv(t)
	defer env.cleanup(t)

	tmpDir := t.TempDir()

	// First run: generate and save
	signer1, publicKey1, err := sshkeys.EnsureKeyPair(tmpDir)
	if err != nil {
		t.Fatalf("first EnsureKeyPair: %v", err)
	}

	// Verify keys exist on disk
	if !sshkeys.KeyPairExists(tmpDir) {
		t.Fatal("key pair should exist after generation")
	}

	// Second run: load from disk (simulates restart)
	signer2, publicKey2, err := sshkeys.EnsureKeyPair(tmpDir)
	if err != nil {
		t.Fatalf("second EnsureKeyPair: %v", err)
	}

	// Keys should be identical
	if publicKey1 != publicKey2 {
		t.Error("public keys differ between runs")
	}
	if ssh.FingerprintSHA256(signer1.PublicKey()) != ssh.FingerprintSHA256(signer2.PublicKey()) {
		t.Error("private key fingerprints differ between runs")
	}

	// Use the loaded key to actually connect
	inst := env.startAgent(t, "claworc-inttest-persist")
	waitForSSHD(t, inst.sshHost, inst.sshPort)

	ctx := context.Background()
	if err := env.uploadPublicKey(ctx, inst.containerID, publicKey2); err != nil {
		t.Fatalf("upload public key: %v", err)
	}

	mgr := NewSSHManager(signer2, publicKey2)
	defer mgr.CloseAll()

	client, err := mgr.Connect(ctx, "test-instance", inst.sshHost, inst.sshPort)
	if err != nil {
		t.Fatalf("connect with persisted key: %v", err)
	}

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	output, err := session.CombinedOutput("echo persisted-key-works")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(string(output), "persisted-key-works") {
		t.Errorf("output = %q, want to contain 'persisted-key-works'", string(output))
	}

	t.Log("Key pair persistence: OK")
}

// dockerIntegrationOrchestrator implements sshmanager.Orchestrator using
// real Docker exec operations for integration testing.
type dockerIntegrationOrchestrator struct {
	env  *dockerTestEnv
	inst *agentInstance

	addressCalls   int
	configureCalls int
}

func (o *dockerIntegrationOrchestrator) GetSSHAddress(_ context.Context, _ string) (string, int, error) {
	o.addressCalls++
	return o.inst.sshHost, o.inst.sshPort, nil
}

func (o *dockerIntegrationOrchestrator) ConfigureSSHAccess(ctx context.Context, _ string, publicKey string) error {
	o.configureCalls++
	return o.env.uploadPublicKey(ctx, o.inst.containerID, publicKey)
}
