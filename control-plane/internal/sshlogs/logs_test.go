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
		// Verify no -f or -F flag for non-follow mode
		if strings.Contains(cmd, "-f") || strings.Contains(cmd, "-F") {
			ch.Stderr().Write([]byte("unexpected follow flag"))
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

		// Simulate tail -F: write some lines, then wait until the channel is closed
		ch.Write([]byte("initial line 1\n"))
		ch.Write([]byte("initial line 2\n"))

		// Wait a bit then write another line
		time.Sleep(50 * time.Millisecond)
		ch.Write([]byte("new line 3\n"))

		// Block until the channel is closed (simulating tail -F waiting for new data)
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

	// Verify the command used -F flag (follow by name for log rotation)
	mu.Lock()
	cmd := receivedCmd
	mu.Unlock()
	if !strings.Contains(cmd, "-F") {
		t.Errorf("expected -F flag in command, got %q", cmd)
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
	cmd := buildTailCommand("/var/log/test.log", 50, false, true)
	expected := "tail -n 50 '/var/log/test.log'"
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

func TestBuildTailCommandFollowByName(t *testing.T) {
	// Default follow mode uses -F for log rotation awareness
	cmd := buildTailCommand("/var/log/test.log", 100, true, true)
	expected := "tail -F -n 100 '/var/log/test.log'"
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

func TestBuildTailCommandFollowByDescriptor(t *testing.T) {
	// With followByName=false, uses -f (follow by descriptor)
	cmd := buildTailCommand("/var/log/test.log", 100, true, false)
	expected := "tail -f -n 100 '/var/log/test.log'"
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

func TestBuildTailCommandWithSpecialPath(t *testing.T) {
	cmd := buildTailCommand("/var/log/my app's log.txt", 10, false, true)
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

// --- Integration / scenario tests ---

// TestStreamLogsConcurrentStreams verifies that multiple log streams can run
// simultaneously against the same SSH client (multiplexed connection).
func TestStreamLogsConcurrentStreams(t *testing.T) {
	var mu sync.Mutex
	sessions := 0

	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		mu.Lock()
		sessions++
		mu.Unlock()

		// Each "file" produces different output
		if strings.Contains(cmd, "openclaw") {
			ch.Write([]byte("openclaw line 1\nopenclaw line 2\n"))
		} else if strings.Contains(cmd, "browser") {
			ch.Write([]byte("browser line 1\nbrowser line 2\n"))
		} else if strings.Contains(cmd, "sshd") {
			ch.Write([]byte("system line 1\nsystem line 2\n"))
		}
		sendExitStatus(ch, 0)
	})
	defer cleanup()

	ctx := context.Background()

	// Start 3 concurrent streams for different log types
	type result struct {
		lines []string
		err   error
	}
	results := make([]result, 3)
	var wg sync.WaitGroup

	paths := []string{"/var/log/openclaw.log", "/tmp/browser.log", "/var/log/sshd.log"}
	for i, path := range paths {
		wg.Add(1)
		go func(idx int, logPath string) {
			defer wg.Done()
			logCh, err := StreamLogs(ctx, client, logPath, 50, false)
			if err != nil {
				results[idx] = result{err: err}
				return
			}
			var lines []string
			for line := range logCh {
				lines = append(lines, line)
			}
			results[idx] = result{lines: lines}
		}(i, path)
	}

	wg.Wait()

	// Verify all 3 streams worked
	for i, r := range results {
		if r.err != nil {
			t.Errorf("stream %d failed: %v", i, r.err)
			continue
		}
		if len(r.lines) != 2 {
			t.Errorf("stream %d: expected 2 lines, got %d: %v", i, len(r.lines), r.lines)
		}
	}

	// Verify all 3 SSH sessions were opened
	mu.Lock()
	if sessions != 3 {
		t.Errorf("expected 3 SSH sessions, got %d", sessions)
	}
	mu.Unlock()

	// Verify content is from the correct log file
	if len(results[0].lines) > 0 && !strings.Contains(results[0].lines[0], "openclaw") {
		t.Errorf("stream 0 expected openclaw content, got %q", results[0].lines[0])
	}
	if len(results[1].lines) > 0 && !strings.Contains(results[1].lines[0], "browser") {
		t.Errorf("stream 1 expected browser content, got %q", results[1].lines[0])
	}
	if len(results[2].lines) > 0 && !strings.Contains(results[2].lines[0], "system") {
		t.Errorf("stream 2 expected system content, got %q", results[2].lines[0])
	}
}

// TestStreamLogsGoroutineCleanup verifies that goroutines are cleaned up after
// streaming ends, both for normal completion and context cancellation.
func TestStreamLogsGoroutineCleanup(t *testing.T) {
	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		ch.Write([]byte("line 1\nline 2\n"))

		if strings.Contains(cmd, "-F") || strings.Contains(cmd, "-f") {
			// Follow mode: block until channel closes
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		}
		sendExitStatus(ch, 0)
	})
	defer cleanup()

	// Test 1: Normal completion (non-follow) — goroutine should exit after drain
	ctx1 := context.Background()
	logCh1, err := StreamLogs(ctx1, client, "/var/log/test.log", 50, false)
	if err != nil {
		t.Fatalf("StreamLogs non-follow: %v", err)
	}
	for range logCh1 {
	}
	// Channel closed means goroutine exited

	// Test 2: Context cancellation (follow mode) — goroutine should exit promptly
	ctx2, cancel2 := context.WithCancel(context.Background())
	logCh2, err := StreamLogs(ctx2, client, "/var/log/test.log", 50, true)
	if err != nil {
		t.Fatalf("StreamLogs follow: %v", err)
	}

	// Read initial lines
	for i := 0; i < 2; i++ {
		select {
		case _, ok := <-logCh2:
			if !ok {
				t.Fatal("channel closed prematurely")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timeout reading lines")
		}
	}

	// Cancel context
	cancel2()

	// Channel should close within a reasonable time
	select {
	case _, ok := <-logCh2:
		if ok {
			for range logCh2 {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not clean up after context cancel")
	}
}

// TestStreamLogsLargeVolume verifies that streaming a large number of lines
// works correctly without losing data or blocking.
func TestStreamLogsLargeVolume(t *testing.T) {
	const lineCount = 5000
	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		for i := 0; i < lineCount; i++ {
			fmt.Fprintf(ch, "log line %05d\n", i)
		}
		sendExitStatus(ch, 0)
	})
	defer cleanup()

	ctx := context.Background()
	logCh, err := StreamLogs(ctx, client, "/var/log/test.log", lineCount, false)
	if err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}

	var received []string
	for line := range logCh {
		received = append(received, line)
	}

	if len(received) != lineCount {
		t.Fatalf("expected %d lines, got %d", lineCount, len(received))
	}

	// Verify first and last lines for correctness
	if received[0] != "log line 00000" {
		t.Errorf("first line: expected 'log line 00000', got %q", received[0])
	}
	if received[lineCount-1] != fmt.Sprintf("log line %05d", lineCount-1) {
		t.Errorf("last line: expected 'log line %05d', got %q", lineCount-1, received[lineCount-1])
	}
}

// TestStreamLogsSlowConsumer verifies that a slow consumer doesn't block the
// streaming goroutine indefinitely (channel has 100-capacity buffer, context
// cancellation should abort delivery).
func TestStreamLogsSlowConsumer(t *testing.T) {
	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		// Write more lines than the channel buffer can hold
		for i := 0; i < 200; i++ {
			fmt.Fprintf(ch, "line %d\n", i)
		}
		sendExitStatus(ch, 0)
	})
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	logCh, err := StreamLogs(ctx, client, "/var/log/test.log", 200, false)
	if err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}

	// Read just a few lines slowly, then cancel
	for i := 0; i < 5; i++ {
		select {
		case _, ok := <-logCh:
			if !ok {
				t.Fatal("channel closed too early")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Cancel while there are still buffered lines
	cancel()

	// Channel should close soon (goroutine should not be stuck)
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-logCh:
			if !ok {
				return // success
			}
		case <-timeout:
			t.Fatal("goroutine stuck after context cancel with buffered data")
		}
	}
}

// TestStreamLogsFollowWithDelayedLines verifies real-time streaming where lines
// arrive with delays, simulating a real tail -f scenario.
func TestStreamLogsFollowWithDelayedLines(t *testing.T) {
	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		// Simulate a log file that receives entries at intervals
		ch.Write([]byte("startup complete\n"))
		time.Sleep(50 * time.Millisecond)
		ch.Write([]byte("request received\n"))
		time.Sleep(50 * time.Millisecond)
		ch.Write([]byte("response sent\n"))

		// Block to simulate tail -f waiting
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

	expected := []string{"startup complete", "request received", "response sent"}
	for i, want := range expected {
		select {
		case got, ok := <-logCh:
			if !ok {
				t.Fatalf("channel closed after %d lines", i)
			}
			if got != want {
				t.Errorf("line %d: expected %q, got %q", i, want, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for line %d", i)
		}
	}

	cancel()
}

// --- Log rotation tests ---

// TestStreamLogsDefaultUsesFollowByName verifies that the default StreamLogs
// call uses -F (follow by name) for log rotation awareness.
func TestStreamLogsDefaultUsesFollowByName(t *testing.T) {
	var receivedCmd string

	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		receivedCmd = cmd
		ch.Write([]byte("line 1\n"))

		// Block until channel closes (simulating tail -F)
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

	// Read one line to ensure command was executed
	select {
	case <-logCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for line")
	}

	cancel()
	for range logCh {
	}

	// Default should use -F (follow by name with retry)
	if !strings.Contains(receivedCmd, "-F") {
		t.Errorf("default follow mode should use -F, got command: %q", receivedCmd)
	}
	// Should NOT contain lowercase -f
	if strings.Contains(receivedCmd, " -f ") {
		t.Errorf("default follow mode should not use -f, got command: %q", receivedCmd)
	}
}

// TestStreamLogsFollowByDescriptorOption verifies that StreamOptions can
// override the default to use tail -f instead of tail -F.
func TestStreamLogsFollowByDescriptorOption(t *testing.T) {
	var receivedCmd string

	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		receivedCmd = cmd
		ch.Write([]byte("line 1\n"))

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

	opts := StreamOptions{FollowByName: false}
	logCh, err := StreamLogs(ctx, client, "/var/log/test.log", 100, true, opts)
	if err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}

	select {
	case <-logCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for line")
	}

	cancel()
	for range logCh {
	}

	// Should use -f (follow by descriptor), not -F
	if strings.Contains(receivedCmd, "-F") {
		t.Errorf("FollowByName=false should use -f, got command: %q", receivedCmd)
	}
	if !strings.Contains(receivedCmd, "-f") {
		t.Errorf("FollowByName=false should use -f flag, got command: %q", receivedCmd)
	}
}

// TestStreamLogsLogRotation simulates a log rotation scenario where the
// server (via tail -F) continues streaming after the file is replaced.
// The test verifies that lines from both before and after the simulated
// rotation are delivered to the client.
func TestStreamLogsLogRotation(t *testing.T) {
	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		// Verify -F flag is used
		if !strings.Contains(cmd, "-F") {
			ch.Stderr().Write([]byte("expected -F flag for rotation test"))
			sendExitStatus(ch, 1)
			return
		}

		// Simulate pre-rotation lines
		ch.Write([]byte("pre-rotation line 1\n"))
		ch.Write([]byte("pre-rotation line 2\n"))

		// Simulate the brief pause during log rotation
		time.Sleep(50 * time.Millisecond)

		// Simulate tail -F reopening the new file and continuing
		ch.Write([]byte("post-rotation line 1\n"))
		ch.Write([]byte("post-rotation line 2\n"))

		// Block until channel closes
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

	// Collect all 4 lines (2 pre-rotation + 2 post-rotation)
	var received []string
	for i := 0; i < 4; i++ {
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

	cancel()
	for range logCh {
	}

	if len(received) != 4 {
		t.Fatalf("expected 4 lines, got %d: %v", len(received), received)
	}
	if received[0] != "pre-rotation line 1" {
		t.Errorf("line 0: expected 'pre-rotation line 1', got %q", received[0])
	}
	if received[2] != "post-rotation line 1" {
		t.Errorf("line 2: expected 'post-rotation line 1', got %q", received[2])
	}
}

// TestDefaultStreamOptions verifies the default options have log rotation
// awareness enabled.
func TestDefaultStreamOptions(t *testing.T) {
	opts := DefaultStreamOptions()
	if !opts.FollowByName {
		t.Error("DefaultStreamOptions should have FollowByName=true")
	}
}

// TestStreamLogsNoFollowIgnoresRotationOption verifies that in non-follow
// mode, the FollowByName option has no effect (no follow flag is added).
func TestStreamLogsNoFollowIgnoresRotationOption(t *testing.T) {
	var receivedCmd string

	client, cleanup := startSSHServer(t, func(cmd string, ch gossh.Channel) {
		receivedCmd = cmd
		ch.Write([]byte("line 1\n"))
		sendExitStatus(ch, 0)
	})
	defer cleanup()

	ctx := context.Background()
	opts := StreamOptions{FollowByName: true}
	logCh, err := StreamLogs(ctx, client, "/var/log/test.log", 50, false, opts)
	if err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}

	for range logCh {
	}

	// Non-follow mode should not have any follow flags
	if strings.Contains(receivedCmd, "-F") || strings.Contains(receivedCmd, "-f") {
		t.Errorf("non-follow mode should not have follow flags, got command: %q", receivedCmd)
	}
}
