//go:build docker_integration

package sshproxy

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// getExternalAgentInfo reads container info from environment variables set by
// the TypeScript test harness.
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

// uploadPublicKeyViaDocker installs a public key on the container using docker exec.
func uploadPublicKeyViaDocker(t *testing.T, containerID, publicKey string) {
	t.Helper()
	cmd := exec.Command("docker", "exec", containerID, "sh", "-c", "mkdir -p /root/.ssh && chmod 700 /root/.ssh")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create .ssh dir: %v\n%s", err, out)
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(publicKey))
	writeCmd := fmt.Sprintf("echo '%s' | base64 -d > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys", b64)
	cmd = exec.Command("docker", "exec", containerID, "sh", "-c", writeCmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write authorized_keys: %v\n%s", err, out)
	}
}

// connectSSH establishes an SSH connection to the container.
func connectSSH(t *testing.T, host string, port int, signer ssh.Signer) *ssh.Client {
	t.Helper()
	cfg := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatalf("ssh dial %s: %v", addr, err)
	}
	return client
}

// setupExternalSSH sets up an SSH connection to an externally managed container.
func setupExternalSSH(t *testing.T) (*ssh.Client, string) {
	t.Helper()
	containerID, sshHost, sshPort := getExternalAgentInfo(t)
	waitForSSHD(t, sshHost, sshPort)

	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	pubKeyStr := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	uploadPublicKeyViaDocker(t, containerID, pubKeyStr)

	client := connectSSH(t, sshHost, sshPort, signer)
	t.Cleanup(func() { client.Close() })

	return client, containerID
}

// writeLogFileViaDocker writes content to a file inside the container.
func writeLogFileViaDocker(t *testing.T, containerID, path, content string) {
	t.Helper()
	// Ensure parent directory exists
	dir := path[:strings.LastIndex(path, "/")]
	cmd := exec.Command("docker", "exec", containerID, "sh", "-c", fmt.Sprintf("mkdir -p '%s'", dir))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mkdir -p %s: %v\n%s", dir, err, out)
	}

	b64 := base64.StdEncoding.EncodeToString([]byte(content))
	writeCmd := fmt.Sprintf("echo '%s' | base64 -d > '%s'", b64, path)
	cmd = exec.Command("docker", "exec", containerID, "sh", "-c", writeCmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write file %s: %v\n%s", path, err, out)
	}
}

// appendLogLineViaDocker appends a line to a log file inside the container.
func appendLogLineViaDocker(t *testing.T, containerID, path, line string) {
	t.Helper()
	b64 := base64.StdEncoding.EncodeToString([]byte(line + "\n"))
	appendCmd := fmt.Sprintf("echo '%s' | base64 -d >> '%s'", b64, path)
	cmd := exec.Command("docker", "exec", containerID, "sh", "-c", appendCmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("append to %s: %v\n%s", path, err, out)
	}
}

// TestExternalIntegration_StreamLogsNonFollow tests streaming log lines without
// follow mode against a real agent container.
func TestExternalIntegration_StreamLogsNonFollow(t *testing.T) {
	client, containerID := setupExternalSSH(t)

	logPath := "/tmp/test-stream-nonfollow.log"
	content := "line-one\nline-two\nline-three\nline-four\nline-five\n"
	writeLogFileViaDocker(t, containerID, logPath, content)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ch, err := StreamLogs(ctx, client, logPath, StreamOptions{Tail: 100})
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	var lines []string
	for line := range ch {
		lines = append(lines, line)
	}

	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "line-one" {
		t.Errorf("expected first line 'line-one', got %q", lines[0])
	}
	if lines[4] != "line-five" {
		t.Errorf("expected last line 'line-five', got %q", lines[4])
	}
	t.Logf("Non-follow stream OK: %d lines", len(lines))
}

// TestExternalIntegration_StreamLogsTailParameter tests that the tail parameter
// limits the number of initial lines returned.
func TestExternalIntegration_StreamLogsTailParameter(t *testing.T) {
	client, containerID := setupExternalSSH(t)

	logPath := "/tmp/test-stream-tail.log"
	content := "alpha\nbeta\ngamma\ndelta\nepsilon\n"
	writeLogFileViaDocker(t, containerID, logPath, content)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ch, err := StreamLogs(ctx, client, logPath, StreamOptions{Tail: 2})
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	var lines []string
	for line := range ch {
		lines = append(lines, line)
	}

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines with tail=2, got %d: %v", len(lines), lines)
	}
	if lines[0] != "delta" {
		t.Errorf("expected 'delta', got %q", lines[0])
	}
	if lines[1] != "epsilon" {
		t.Errorf("expected 'epsilon', got %q", lines[1])
	}
	t.Logf("Tail parameter OK: got last %d lines", len(lines))
}

// TestExternalIntegration_StreamLogsFollowRealTime tests follow mode by appending
// new lines to a log file and verifying they appear in the stream in real time.
func TestExternalIntegration_StreamLogsFollowRealTime(t *testing.T) {
	client, containerID := setupExternalSSH(t)

	logPath := "/tmp/test-stream-follow.log"
	content := "initial-line\n"
	writeLogFileViaDocker(t, containerID, logPath, content)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ch, err := StreamLogs(ctx, client, logPath, StreamOptions{Tail: 100, Follow: true})
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	// Read the initial line
	select {
	case line, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before initial line")
		}
		if line != "initial-line" {
			t.Errorf("expected 'initial-line', got %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for initial line")
	}

	// Append new lines and verify they appear in real time
	for i := 1; i <= 3; i++ {
		expected := fmt.Sprintf("realtime-line-%d", i)
		appendLogLineViaDocker(t, containerID, logPath, expected)

		select {
		case line, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed before receiving realtime-line-%d", i)
			}
			if line != expected {
				t.Errorf("expected %q, got %q", expected, line)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for %s", expected)
		}
	}

	cancel()
	t.Log("Follow mode real-time streaming OK")
}

// TestExternalIntegration_StreamLogsClientDisconnect tests that cancelling the
// context cleans up the SSH session and closes the channel.
func TestExternalIntegration_StreamLogsClientDisconnect(t *testing.T) {
	client, containerID := setupExternalSSH(t)

	logPath := "/tmp/test-stream-disconnect.log"
	content := "disconnect-line-1\ndisconnect-line-2\n"
	writeLogFileViaDocker(t, containerID, logPath, content)

	ctx, cancel := context.WithCancel(context.Background())

	ch, err := StreamLogs(ctx, client, logPath, StreamOptions{Tail: 100, Follow: true})
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	// Read initial lines
	for i := 0; i < 2; i++ {
		select {
		case _, ok := <-ch:
			if !ok {
				t.Fatal("channel closed early")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for initial lines")
		}
	}

	// Cancel context to simulate client disconnect
	cancel()

	// Verify channel closes within a reasonable time
	closed := false
	deadline := time.After(5 * time.Second)
	for !closed {
		select {
		case _, ok := <-ch:
			if !ok {
				closed = true
			}
		case <-deadline:
			t.Fatal("channel did not close after context cancellation within 5s")
		}
	}

	// Verify the SSH client is still usable (the stream should have cleaned up
	// only its own session, not the entire connection)
	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("SSH client unusable after stream cleanup: %v", err)
	}
	output, err := session.CombinedOutput("echo still-alive")
	if err != nil {
		t.Fatalf("command failed after stream cleanup: %v", err)
	}
	if !strings.Contains(string(output), "still-alive") {
		t.Errorf("unexpected output: %q", string(output))
	}

	t.Log("Client disconnect cleanup OK — SSH connection still usable")
}

// TestExternalIntegration_StreamLogsMultipleSimultaneous tests streaming from
// multiple log files simultaneously over the same SSH connection.
func TestExternalIntegration_StreamLogsMultipleSimultaneous(t *testing.T) {
	client, containerID := setupExternalSSH(t)

	// Create multiple log files
	files := map[string]string{
		"/tmp/test-multi-stream-a.log": "a-line-1\na-line-2\na-line-3\n",
		"/tmp/test-multi-stream-b.log": "b-line-1\nb-line-2\n",
		"/tmp/test-multi-stream-c.log": "c-line-1\nc-line-2\nc-line-3\nc-line-4\n",
	}
	for path, content := range files {
		writeLogFileViaDocker(t, containerID, path, content)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	type streamResult struct {
		path  string
		lines []string
		err   error
	}

	var wg sync.WaitGroup
	results := make(chan streamResult, len(files))

	for path := range files {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			ch, err := StreamLogs(ctx, client, p, StreamOptions{Tail: 100})
			if err != nil {
				results <- streamResult{path: p, err: err}
				return
			}
			var lines []string
			for line := range ch {
				lines = append(lines, line)
			}
			results <- streamResult{path: p, lines: lines}
		}(path)
	}

	wg.Wait()
	close(results)

	for r := range results {
		if r.err != nil {
			t.Errorf("stream %s error: %v", r.path, r.err)
			continue
		}
		expected := strings.Split(strings.TrimRight(files[r.path], "\n"), "\n")
		if len(r.lines) != len(expected) {
			t.Errorf("stream %s: expected %d lines, got %d: %v", r.path, len(expected), len(r.lines), r.lines)
			continue
		}
		t.Logf("stream %s: %d lines OK", r.path, len(r.lines))
	}

	t.Log("Multiple simultaneous streams OK")
}

// TestExternalIntegration_StreamLogsNonExistentFile tests behavior when the
// target log file doesn't exist on the agent.
func TestExternalIntegration_StreamLogsNonExistentFile(t *testing.T) {
	client, _ := setupExternalSSH(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ch, err := StreamLogs(ctx, client, "/nonexistent/path/to/log.file", StreamOptions{Tail: 10})
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	// Channel should close with no lines (tail writes error to stderr)
	var lines []string
	for line := range ch {
		lines = append(lines, line)
	}

	if len(lines) != 0 {
		t.Errorf("expected 0 lines for non-existent file, got %d: %v", len(lines), lines)
	}
	t.Log("Non-existent file handled correctly: 0 lines, channel closed")
}

// TestExternalIntegration_StreamLogsMemoryStability tests that starting and
// stopping multiple follow-mode streams does not leak goroutines.
func TestExternalIntegration_StreamLogsMemoryStability(t *testing.T) {
	client, containerID := setupExternalSSH(t)

	logPath := "/tmp/test-stream-stability.log"
	writeLogFileViaDocker(t, containerID, logPath, "stability-check\n")

	// Record baseline goroutine count
	runtime.GC()
	baseline := runtime.NumGoroutine()
	t.Logf("Baseline goroutines: %d", baseline)

	// Start and stop several follow-mode streams
	iterations := 10
	for i := 0; i < iterations; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := StreamLogs(ctx, client, logPath, StreamOptions{Tail: 1, Follow: true})
		if err != nil {
			t.Fatalf("iteration %d: StreamLogs error: %v", i, err)
		}

		// Read at least one line
		select {
		case _, ok := <-ch:
			if !ok {
				t.Fatalf("iteration %d: channel closed early", i)
			}
		case <-time.After(10 * time.Second):
			cancel()
			t.Fatalf("iteration %d: timed out", i)
		}

		cancel()

		// Drain channel
		for range ch {
		}
	}

	// Allow goroutines to settle
	time.Sleep(2 * time.Second)
	runtime.GC()
	final := runtime.NumGoroutine()
	t.Logf("Final goroutines: %d (baseline: %d, iterations: %d)", final, baseline, iterations)

	// Allow a reasonable margin: some goroutines may belong to the SSH mux or
	// runtime internals. The key check is that we don't accumulate proportional
	// to iterations.
	maxAllowed := baseline + 10
	if final > maxAllowed {
		t.Errorf("goroutine count %d exceeds allowed %d — possible leak after %d iterations", final, maxAllowed, iterations)
	}

	t.Log("Memory stability OK — no goroutine leak detected")
}

// TestExternalIntegration_GetAvailableLogFiles tests discovering available log
// files on a real agent container.
func TestExternalIntegration_GetAvailableLogFiles(t *testing.T) {
	client, _ := setupExternalSSH(t)

	files, err := GetAvailableLogFiles(client)
	if err != nil {
		t.Fatalf("GetAvailableLogFiles error: %v", err)
	}

	t.Logf("Available log files: %v", files)

	// The agent container should have at least syslog or auth.log
	// (the exact set depends on the agent image configuration)
	if len(files) == 0 {
		t.Log("Warning: no standard log files found — agent may not have started log services yet")
	}

	// All returned paths should be absolute
	for _, f := range files {
		if !strings.HasPrefix(f, "/") {
			t.Errorf("expected absolute path, got %q", f)
		}
	}

	t.Logf("GetAvailableLogFiles OK: found %d files", len(files))
}

// TestExternalIntegration_StreamLogsFollowWithCancel tests that follow mode
// properly handles multiple rapid cancel/restart cycles.
func TestExternalIntegration_StreamLogsFollowWithCancel(t *testing.T) {
	client, containerID := setupExternalSSH(t)

	logPath := "/tmp/test-stream-rapid-cancel.log"
	writeLogFileViaDocker(t, containerID, logPath, "rapid-cancel-init\n")

	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := StreamLogs(ctx, client, logPath, StreamOptions{Tail: 1, Follow: true})
		if err != nil {
			t.Fatalf("cycle %d: StreamLogs error: %v", i, err)
		}

		// Read one line
		select {
		case line, ok := <-ch:
			if !ok {
				t.Fatalf("cycle %d: channel closed early", i)
			}
			if line != "rapid-cancel-init" {
				t.Logf("cycle %d: got line %q", i, line)
			}
		case <-time.After(10 * time.Second):
			cancel()
			t.Fatalf("cycle %d: timed out", i)
		}

		// Cancel immediately
		cancel()

		// Drain and verify closure
		for range ch {
		}
	}

	t.Log("Rapid cancel/restart cycles OK")
}

// rotateLogFileViaDocker simulates logrotate by renaming the current file
// and creating a fresh empty file at the original path.
func rotateLogFileViaDocker(t *testing.T, containerID, path string) {
	t.Helper()
	rotateCmd := fmt.Sprintf("mv '%s' '%s.1' && touch '%s'", path, path, path)
	cmd := exec.Command("docker", "exec", containerID, "sh", "-c", rotateCmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rotate log file %s: %v\n%s", path, err, out)
	}
}

// TestExternalIntegration_StreamLogsLogRotation tests that follow-by-name mode
// (tail -F) continues streaming after log rotation. This simulates what
// logrotate does: rename the current file and create a fresh one.
func TestExternalIntegration_StreamLogsLogRotation(t *testing.T) {
	client, containerID := setupExternalSSH(t)

	logPath := "/tmp/test-stream-rotation.log"
	content := "pre-rotation-line-1\npre-rotation-line-2\n"
	writeLogFileViaDocker(t, containerID, logPath, content)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Start streaming with follow-by-name (default, rotation-aware)
	ch, err := StreamLogs(ctx, client, logPath, StreamOptions{Tail: 100, Follow: true})
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	// Read the initial lines
	for i := 0; i < 2; i++ {
		select {
		case line, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before initial lines")
			}
			expected := fmt.Sprintf("pre-rotation-line-%d", i+1)
			if line != expected {
				t.Errorf("expected %q, got %q", expected, line)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for initial lines")
		}
	}
	t.Log("Initial pre-rotation lines received")

	// Simulate log rotation: rename current file and create fresh one
	rotateLogFileViaDocker(t, containerID, logPath)
	t.Log("Log file rotated")

	// Wait for tail -F to detect the rotation. tail -F checks periodically
	// (typically every ~1s) and prints a diagnostic to stderr when it detects
	// that the file was replaced.
	time.Sleep(2 * time.Second)

	// Write new lines to the fresh (post-rotation) file
	for i := 1; i <= 3; i++ {
		line := fmt.Sprintf("post-rotation-line-%d", i)
		appendLogLineViaDocker(t, containerID, logPath, line)
	}

	// Verify the post-rotation lines appear in the stream
	for i := 1; i <= 3; i++ {
		expected := fmt.Sprintf("post-rotation-line-%d", i)
		select {
		case line, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed before receiving %s", expected)
			}
			if line != expected {
				t.Errorf("expected %q, got %q", expected, line)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("timed out waiting for %s after rotation", expected)
		}
	}

	cancel()
	t.Log("Log rotation streaming OK — tail -F continued after file replacement")
}
