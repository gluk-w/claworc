package sshlogs

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// --- Test SSH server infrastructure ---

// sessionHandler receives the parsed command and the SSH channel, giving full
// control over stdout/stderr writes and timing (needed for streaming tests).
type sessionHandler func(cmd string, ch gossh.Channel)

// startSSHServer starts a test SSH server that invokes handler for each exec
// request. The handler is responsible for writing output and sending exit-status.
func startSSHServer(t *testing.T, handler sessionHandler) (client *gossh.Client, cleanup func()) {
	t.Helper()

	// Generate server host key
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := gossh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("create host signer: %v", err)
	}

	// Generate client key pair
	clientPub, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	clientSSHPub, err := gossh.NewPublicKey(clientPub)
	if err != nil {
		t.Fatalf("convert client pub key: %v", err)
	}

	serverCfg := &gossh.ServerConfig{
		PublicKeyCallback: func(conn gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
			if bytes.Equal(key.Marshal(), clientSSHPub.Marshal()) {
				return &gossh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	serverCfg.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleSSHConn(conn, serverCfg, handler)
		}
	}()

	// Build client config
	clientSigner, err := gossh.NewSignerFromKey(clientPriv)
	if err != nil {
		t.Fatalf("create client signer: %v", err)
	}

	clientCfg := &gossh.ClientConfig{
		User:            "root",
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(clientSigner)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}

	sshClient, err := gossh.Dial("tcp", listener.Addr().String(), clientCfg)
	if err != nil {
		listener.Close()
		t.Fatalf("dial SSH: %v", err)
	}

	return sshClient, func() {
		sshClient.Close()
		listener.Close()
	}
}

func handleSSHConn(netConn net.Conn, config *gossh.ServerConfig, handler sessionHandler) {
	defer netConn.Close()
	srvConn, chans, reqs, err := gossh.NewServerConn(netConn, config)
	if err != nil {
		return
	}
	defer srvConn.Close()
	go gossh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(gossh.UnknownChannelType, "unsupported channel type")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go handleExecSession(ch, requests, handler)
	}
}

func handleExecSession(ch gossh.Channel, reqs <-chan *gossh.Request, handler sessionHandler) {
	defer ch.Close()

	for req := range reqs {
		switch req.Type {
		case "exec":
			if len(req.Payload) < 4 {
				req.Reply(false, nil)
				continue
			}
			cmdLen := int(req.Payload[0])<<24 | int(req.Payload[1])<<16 | int(req.Payload[2])<<8 | int(req.Payload[3])
			if len(req.Payload) < 4+cmdLen {
				req.Reply(false, nil)
				continue
			}
			cmd := string(req.Payload[4 : 4+cmdLen])
			req.Reply(true, nil)

			handler(cmd, ch)
			return

		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// sendExitStatus sends an exit-status request on the SSH channel.
func sendExitStatus(ch gossh.Channel, exitCode int) {
	payload := gossh.Marshal(struct{ Status uint32 }{uint32(exitCode)})
	ch.SendRequest("exit-status", false, payload)
}

// --- StreamLogs tests ---

func TestStreamLogsNoFollow(t *testing.T) {
	logLines := "line 1\nline 2\nline 3\n"

	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		if !strings.Contains(cmd, "tail") {
			ch.Stderr().Write([]byte("unexpected command"))
			sendExitStatus(ch, 1)
			return
		}
		// Verify no -f flag for non-follow mode
		if strings.Contains(cmd, "-f") {
			ch.Stderr().Write([]byte("unexpected -f flag"))
			sendExitStatus(ch, 1)
			return
		}
		ch.Write([]byte(logLines))
		sendExitStatus(ch, 0)
	})
	defer cleanup()

	ctx := context.Background()
	logCh, err := StreamLogs(ctx, client, "/var/log/test.log", 50, false)
	if err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}

	var received []string
	for line := range logCh {
		received = append(received, line)
	}

	if len(received) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(received), received)
	}
	if received[0] != "line 1" {
		t.Errorf("expected 'line 1', got %q", received[0])
	}
	if received[1] != "line 2" {
		t.Errorf("expected 'line 2', got %q", received[1])
	}
	if received[2] != "line 3" {
		t.Errorf("expected 'line 3', got %q", received[2])
	}
}

func TestStreamLogsFollow(t *testing.T) {
	var mu sync.Mutex
	var receivedCmd string

	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		mu.Lock()
		receivedCmd = cmd
		mu.Unlock()

		// Simulate tail -f: write some lines, then wait until the channel is closed
		ch.Write([]byte("initial line 1\n"))
		ch.Write([]byte("initial line 2\n"))

		// Wait a bit then write another line
		time.Sleep(50 * time.Millisecond)
		ch.Write([]byte("new line 3\n"))

		// Block until the channel is closed (simulating tail -f waiting for new data)
		buf := make([]byte, 1)
		for {
			_, err := ch.Read(buf)
			if err != nil {
				break
			}
		}
		sendExitStatus(ch, 0)
	})
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logCh, err := StreamLogs(ctx, client, "/var/log/test.log", 100, true)
	if err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}

	// Read the 3 lines
	var received []string
	for i := 0; i < 3; i++ {
		select {
		case line, ok := <-logCh:
			if !ok {
				t.Fatalf("channel closed after %d lines", i)
			}
			received = append(received, line)
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for line %d", i)
		}
	}

	if len(received) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(received))
	}
	if received[0] != "initial line 1" {
		t.Errorf("expected 'initial line 1', got %q", received[0])
	}
	if received[2] != "new line 3" {
		t.Errorf("expected 'new line 3', got %q", received[2])
	}

	// Verify the command used -f flag
	mu.Lock()
	cmd := receivedCmd
	mu.Unlock()
	if !strings.Contains(cmd, "-f") {
		t.Errorf("expected -f flag in command, got %q", cmd)
	}

	// Cancel context to stop streaming
	cancel()

	// Channel should close after context cancellation
	select {
	case _, ok := <-logCh:
		if ok {
			// Drain any remaining lines
			for range logCh {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for channel to close after cancel")
	}
}

func TestStreamLogsContextCancellation(t *testing.T) {
	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		// Write one line, then block indefinitely
		ch.Write([]byte("first line\n"))

		// Block until channel is closed
		buf := make([]byte, 1)
		for {
			_, err := ch.Read(buf)
			if err != nil {
				break
			}
		}
		sendExitStatus(ch, 0)
	})
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())

	logCh, err := StreamLogs(ctx, client, "/var/log/test.log", 100, true)
	if err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}

	// Read the first line
	select {
	case line := <-logCh:
		if line != "first line" {
			t.Errorf("expected 'first line', got %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first line")
	}

	// Cancel context
	cancel()

	// Channel should close promptly
	select {
	case _, ok := <-logCh:
		if ok {
			for range logCh {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for channel to close after cancel")
	}
}

func TestStreamLogsEmptyOutput(t *testing.T) {
	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		// Empty file, no output
		sendExitStatus(ch, 0)
	})
	defer cleanup()

	ctx := context.Background()
	logCh, err := StreamLogs(ctx, client, "/var/log/empty.log", 50, false)
	if err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}

	var received []string
	for line := range logCh {
		received = append(received, line)
	}

	if len(received) != 0 {
		t.Errorf("expected 0 lines for empty file, got %d: %v", len(received), received)
	}
}

func TestStreamLogsFileNotFound(t *testing.T) {
	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		ch.Stderr().Write([]byte("tail: cannot open '/nonexistent.log' for reading: No such file or directory\n"))
		sendExitStatus(ch, 1)
	})
	defer cleanup()

	ctx := context.Background()
	logCh, err := StreamLogs(ctx, client, "/nonexistent.log", 50, false)
	if err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}

	// The channel should close with no lines (tail wrote to stderr, not stdout)
	var received []string
	for line := range logCh {
		received = append(received, line)
	}

	if len(received) != 0 {
		t.Errorf("expected 0 lines for missing file, got %d", len(received))
	}
}

func TestStreamLogsTailParameter(t *testing.T) {
	var receivedCmd string
	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		receivedCmd = cmd
		ch.Write([]byte("line\n"))
		sendExitStatus(ch, 0)
	})
	defer cleanup()

	ctx := context.Background()
	logCh, err := StreamLogs(ctx, client, "/var/log/test.log", 25, false)
	if err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}

	// Drain channel
	for range logCh {
	}

	if !strings.Contains(receivedCmd, "-n 25") {
		t.Errorf("expected '-n 25' in command, got %q", receivedCmd)
	}
}

func TestStreamLogsPathWithSpaces(t *testing.T) {
	var receivedCmd string
	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		receivedCmd = cmd
		ch.Write([]byte("data\n"))
		sendExitStatus(ch, 0)
	})
	defer cleanup()

	ctx := context.Background()
	logCh, err := StreamLogs(ctx, client, "/var/log/my app/test.log", 10, false)
	if err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}

	for range logCh {
	}

	// The path should be properly shell-quoted
	if !strings.Contains(receivedCmd, "'/var/log/my app/test.log'") {
		t.Errorf("expected shell-quoted path, got %q", receivedCmd)
	}
}

// --- GetAvailableLogFiles tests ---

func TestGetAvailableLogFilesAllExist(t *testing.T) {
	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		// All test -f succeed, echo all paths
		for _, p := range DefaultLogPaths {
			ch.Write([]byte(p + "\n"))
		}
		sendExitStatus(ch, 0)
	})
	defer cleanup()

	files, err := GetAvailableLogFiles(client)
	if err != nil {
		t.Fatalf("GetAvailableLogFiles: %v", err)
	}

	if len(files) != len(DefaultLogPaths) {
		t.Fatalf("expected %d files, got %d: %v", len(DefaultLogPaths), len(files), files)
	}
	for i, f := range files {
		if f != DefaultLogPaths[i] {
			t.Errorf("expected %q, got %q", DefaultLogPaths[i], f)
		}
	}
}

func TestGetAvailableLogFilesNoneExist(t *testing.T) {
	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		// No files exist, no output
		sendExitStatus(ch, 0)
	})
	defer cleanup()

	files, err := GetAvailableLogFiles(client)
	if err != nil {
		t.Fatalf("GetAvailableLogFiles: %v", err)
	}

	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d: %v", len(files), files)
	}
}

func TestGetAvailableLogFilesSomeExist(t *testing.T) {
	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		// Only the first and last paths exist
		ch.Write([]byte(DefaultLogPaths[0] + "\n"))
		ch.Write([]byte(DefaultLogPaths[len(DefaultLogPaths)-1] + "\n"))
		sendExitStatus(ch, 0)
	})
	defer cleanup()

	files, err := GetAvailableLogFiles(client)
	if err != nil {
		t.Fatalf("GetAvailableLogFiles: %v", err)
	}

	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}
	if files[0] != DefaultLogPaths[0] {
		t.Errorf("expected %q, got %q", DefaultLogPaths[0], files[0])
	}
	if files[1] != DefaultLogPaths[len(DefaultLogPaths)-1] {
		t.Errorf("expected %q, got %q", DefaultLogPaths[len(DefaultLogPaths)-1], files[1])
	}
}

// --- buildTailCommand tests ---

func TestBuildTailCommandNoFollow(t *testing.T) {
	cmd := buildTailCommand("/var/log/test.log", 50, false)
	expected := "tail -n 50 '/var/log/test.log'"
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

func TestBuildTailCommandWithFollow(t *testing.T) {
	cmd := buildTailCommand("/var/log/test.log", 100, true)
	expected := "tail -f -n 100 '/var/log/test.log'"
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

func TestBuildTailCommandWithSpecialPath(t *testing.T) {
	cmd := buildTailCommand("/var/log/my app's log.txt", 10, false)
	expected := "tail -n 10 '/var/log/my app'\\''s log.txt'"
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

// --- LogType and path resolution tests ---

func TestAllLogTypes(t *testing.T) {
	types := AllLogTypes()
	if len(types) != 3 {
		t.Fatalf("expected 3 log types, got %d", len(types))
	}
	expected := map[LogType]bool{LogTypeOpenClaw: true, LogTypeBrowser: true, LogTypeSystem: true}
	for _, lt := range types {
		if !expected[lt] {
			t.Errorf("unexpected log type %q", lt)
		}
	}
}

func TestDefaultPathForType(t *testing.T) {
	tests := []struct {
		logType  LogType
		wantPath string
		wantOK   bool
	}{
		{LogTypeOpenClaw, "/var/log/openclaw.log", true},
		{LogTypeBrowser, "/tmp/browser.log", true},
		{LogTypeSystem, "/var/log/sshd.log", true},
		{LogType("nonexistent"), "", false},
	}

	for _, tt := range tests {
		path, ok := DefaultPathForType(tt.logType)
		if ok != tt.wantOK {
			t.Errorf("DefaultPathForType(%q): ok = %v, want %v", tt.logType, ok, tt.wantOK)
		}
		if path != tt.wantPath {
			t.Errorf("DefaultPathForType(%q) = %q, want %q", tt.logType, path, tt.wantPath)
		}
	}
}

func TestResolveLogPathDefaults(t *testing.T) {
	// With nil custom paths, should return defaults
	path, ok := ResolveLogPath(LogTypeOpenClaw, nil)
	if !ok || path != "/var/log/openclaw.log" {
		t.Errorf("expected default openclaw path, got %q (ok=%v)", path, ok)
	}

	path, ok = ResolveLogPath(LogTypeBrowser, nil)
	if !ok || path != "/tmp/browser.log" {
		t.Errorf("expected default browser path, got %q (ok=%v)", path, ok)
	}
}

func TestResolveLogPathCustomOverride(t *testing.T) {
	custom := map[string]string{
		"openclaw": "/custom/openclaw.log",
	}

	// Custom path should override default
	path, ok := ResolveLogPath(LogTypeOpenClaw, custom)
	if !ok || path != "/custom/openclaw.log" {
		t.Errorf("expected custom openclaw path, got %q (ok=%v)", path, ok)
	}

	// Non-overridden type should fall back to default
	path, ok = ResolveLogPath(LogTypeBrowser, custom)
	if !ok || path != "/tmp/browser.log" {
		t.Errorf("expected default browser path, got %q (ok=%v)", path, ok)
	}
}

func TestResolveLogPathEmptyCustomValue(t *testing.T) {
	custom := map[string]string{
		"openclaw": "", // empty should fall back to default
	}

	path, ok := ResolveLogPath(LogTypeOpenClaw, custom)
	if !ok || path != "/var/log/openclaw.log" {
		t.Errorf("expected default path for empty custom, got %q (ok=%v)", path, ok)
	}
}

func TestResolveLogPathUnknownType(t *testing.T) {
	_, ok := ResolveLogPath(LogType("unknown"), nil)
	if ok {
		t.Error("expected ok=false for unknown log type")
	}
}

// --- shellQuote tests ---

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/var/log/test.log", "'/var/log/test.log'"},
		{"/path/with spaces/file.log", "'/path/with spaces/file.log'"},
		{"file'name.log", "'file'\\''name.log'"},
		{"", "''"},
		{"/normal/path", "'/normal/path'"},
	}

	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.expected {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
