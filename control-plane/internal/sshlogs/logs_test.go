package sshlogs

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

// testLogServer is an in-memory filesystem for the test SSH server that
// supports both instant commands and streaming tail.
type testLogServer struct {
	mu    sync.Mutex
	files map[string]string // path → content
}

func newTestLogServer() *testLogServer {
	return &testLogServer{
		files: map[string]string{
			"/var/log/syslog":   "line1\nline2\nline3\nline4\nline5\n",
			"/var/log/auth.log": "auth-line1\nauth-line2\n",
		},
	}
}

// startTestSSHServer starts an in-process SSH server that handles:
// - "tail -n N path" commands (returns last N lines of file)
// - "tail -n N -F path" commands (returns last N lines then blocks until session close)
// - Compound [ -f ... ] commands for GetAvailableLogFiles
func startTestSSHServer(t *testing.T, srv *testLogServer) (*ssh.Client, func()) {
	t.Helper()

	_, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	_, hostKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.ParsePrivateKey(hostKeyPEM)
	if err != nil {
		t.Fatalf("parse host key: %v", err)
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
		t.Fatalf("listen: %v", err)
	}

	var conns []net.Conn
	var connsMu sync.Mutex

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			netConn, err := listener.Accept()
			if err != nil {
				return
			}
			connsMu.Lock()
			conns = append(conns, netConn)
			connsMu.Unlock()
			go handleServerConn(netConn, config, srv)
		}
	}()

	cleanup := func() {
		listener.Close()
		connsMu.Lock()
		for _, c := range conns {
			c.Close()
		}
		connsMu.Unlock()
		<-done
	}

	// Connect a client
	cfg := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", listener.Addr().String(), cfg)
	if err != nil {
		cleanup()
		t.Fatalf("dial test server: %v", err)
	}

	return client, func() {
		client.Close()
		cleanup()
	}
}

func handleServerConn(netConn net.Conn, config *ssh.ServerConfig, srv *testLogServer) {
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
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go handleSession(ch, requests, srv)
	}
}

func handleSession(ch ssh.Channel, requests <-chan *ssh.Request, srv *testLogServer) {
	defer ch.Close()
	for req := range requests {
		if req.Type != "exec" {
			if req.WantReply {
				req.Reply(true, nil)
			}
			continue
		}

		// Parse exec payload
		cmdLen := uint32(req.Payload[0])<<24 | uint32(req.Payload[1])<<16 | uint32(req.Payload[2])<<8 | uint32(req.Payload[3])
		cmd := string(req.Payload[4 : 4+cmdLen])

		if req.WantReply {
			req.Reply(true, nil)
		}

		srv.mu.Lock()

		if strings.HasPrefix(cmd, "tail ") {
			handleTailCmd(ch, cmd, srv)
			srv.mu.Unlock()
			return
		}

		// Handle compound file-check commands for GetAvailableLogFiles
		if strings.Contains(cmd, "[ -f ") {
			handleFileCheck(ch, cmd, srv)
			srv.mu.Unlock()
			return
		}

		srv.mu.Unlock()

		// Unknown command
		ch.Stderr().Write([]byte(fmt.Sprintf("unknown command: %s", cmd)))
		exitPayload := []byte{0, 0, 0, 127}
		ch.SendRequest("exit-status", false, exitPayload)
		return
	}
}

func handleTailCmd(ch ssh.Channel, cmd string, srv *testLogServer) {
	// Parse: tail -n N [-F] '/path'
	follow := strings.Contains(cmd, " -F ")

	// Extract tail count
	tailN := 100
	if idx := strings.Index(cmd, "-n "); idx >= 0 {
		rest := cmd[idx+3:]
		fmt.Sscanf(rest, "%d", &tailN)
	}

	// Extract path (last single-quoted arg)
	path := extractQuotedPath(cmd)

	content, ok := srv.files[path]
	if !ok {
		ch.Stderr().Write([]byte(fmt.Sprintf("tail: cannot open '%s' for reading: No such file or directory\n", path)))
		exitPayload := []byte{0, 0, 0, 1}
		ch.SendRequest("exit-status", false, exitPayload)
		return
	}

	// Get last N lines
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if tailN < len(lines) {
		lines = lines[len(lines)-tailN:]
	}

	for _, line := range lines {
		ch.Write([]byte(line + "\n"))
	}

	if !follow {
		// Non-follow mode: exit immediately
		exitPayload := []byte{0, 0, 0, 0}
		ch.SendRequest("exit-status", false, exitPayload)
		return
	}

	// Follow mode: block until the channel is closed (simulating tail -F)
	// Read from channel to detect when client closes
	buf := make([]byte, 1)
	for {
		_, err := ch.Read(buf)
		if err != nil {
			break
		}
	}
}

func handleFileCheck(ch ssh.Channel, cmd string, srv *testLogServer) {
	// The command looks like: [ -f '/path1' ] && echo '/path1'; [ -f '/path2' ] && echo '/path2'; ...
	parts := strings.Split(cmd, "; ")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "[ -f ") {
			continue
		}
		// Extract the path from [ -f '/path' ] && echo '/path'
		path := extractQuotedPath(part)
		if _, ok := srv.files[path]; ok {
			ch.Write([]byte(path + "\n"))
		}
	}
	exitPayload := []byte{0, 0, 0, 0}
	ch.SendRequest("exit-status", false, exitPayload)
}

// extractQuotedPath finds the first single-quoted string in s.
func extractQuotedPath(s string) string {
	start := strings.Index(s, "'")
	if start < 0 {
		return ""
	}
	end := strings.Index(s[start+1:], "'")
	if end < 0 {
		return s[start+1:]
	}
	return s[start+1 : start+1+end]
}

// --- StreamLogs tests ---

func TestStreamLogs_NonFollow(t *testing.T) {
	srv := newTestLogServer()
	client, cleanup := startTestSSHServer(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := StreamLogs(ctx, client, "/var/log/syslog", 3, false)
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	var lines []string
	for line := range ch {
		lines = append(lines, line)
	}

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "line3" {
		t.Errorf("expected first line 'line3', got %q", lines[0])
	}
	if lines[1] != "line4" {
		t.Errorf("expected second line 'line4', got %q", lines[1])
	}
	if lines[2] != "line5" {
		t.Errorf("expected third line 'line5', got %q", lines[2])
	}
}

func TestStreamLogs_NonFollow_AllLines(t *testing.T) {
	srv := newTestLogServer()
	client, cleanup := startTestSSHServer(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := StreamLogs(ctx, client, "/var/log/syslog", 100, false)
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
}

func TestStreamLogs_Follow_ContextCancellation(t *testing.T) {
	srv := newTestLogServer()
	client, cleanup := startTestSSHServer(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := StreamLogs(ctx, client, "/var/log/syslog", 2, true)
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	// Collect the initial lines
	var lines []string
	timeout := time.After(3 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case line, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before receiving all initial lines")
			}
			lines = append(lines, line)
		case <-timeout:
			t.Fatal("timed out waiting for initial lines")
		}
	}

	if len(lines) != 2 {
		t.Fatalf("expected 2 initial lines, got %d", len(lines))
	}
	if lines[0] != "line4" {
		t.Errorf("expected 'line4', got %q", lines[0])
	}
	if lines[1] != "line5" {
		t.Errorf("expected 'line5', got %q", lines[1])
	}

	// Cancel context — this should close the channel
	cancel()

	// Wait for channel to close
	select {
	case _, ok := <-ch:
		if ok {
			// May receive buffered data, drain
			for range ch {
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("channel not closed after context cancellation")
	}
}

func TestStreamLogs_NonExistentFile(t *testing.T) {
	srv := newTestLogServer()
	client, cleanup := startTestSSHServer(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := StreamLogs(ctx, client, "/nonexistent/file.log", 10, false)
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	// Channel should close with no output (stderr goes to stderr, not stdout)
	var lines []string
	for line := range ch {
		lines = append(lines, line)
	}

	if len(lines) != 0 {
		t.Errorf("expected 0 lines for non-existent file, got %d: %v", len(lines), lines)
	}
}

func TestStreamLogs_ClosedClient(t *testing.T) {
	srv := newTestLogServer()
	client, cleanup := startTestSSHServer(t, srv)

	// Close client before calling StreamLogs
	client.Close()
	cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := StreamLogs(ctx, client, "/var/log/syslog", 10, false)
	if err == nil {
		t.Fatal("expected error with closed client")
	}
}

func TestStreamLogs_GoroutineCleanup(t *testing.T) {
	srv := newTestLogServer()
	client, cleanup := startTestSSHServer(t, srv)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())

	ch, err := StreamLogs(ctx, client, "/var/log/syslog", 2, true)
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
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for lines")
		}
	}

	// Cancel and verify channel closes
	cancel()

	closed := false
	deadline := time.After(3 * time.Second)
	for !closed {
		select {
		case _, ok := <-ch:
			if !ok {
				closed = true
			}
		case <-deadline:
			t.Fatal("goroutine did not clean up: channel still open after 3s")
		}
	}
}

func TestStreamLogs_DifferentFiles(t *testing.T) {
	srv := newTestLogServer()
	client, cleanup := startTestSSHServer(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := StreamLogs(ctx, client, "/var/log/auth.log", 100, false)
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	var lines []string
	for line := range ch {
		lines = append(lines, line)
	}

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "auth-line1" {
		t.Errorf("expected 'auth-line1', got %q", lines[0])
	}
}

// --- GetAvailableLogFiles tests ---

func TestGetAvailableLogFiles_SomeExist(t *testing.T) {
	srv := newTestLogServer()
	client, cleanup := startTestSSHServer(t, srv)
	defer cleanup()

	files, err := GetAvailableLogFiles(client)
	if err != nil {
		t.Fatalf("GetAvailableLogFiles error: %v", err)
	}

	// Server has /var/log/syslog and /var/log/auth.log
	expected := map[string]bool{
		"/var/log/syslog":   true,
		"/var/log/auth.log": true,
	}

	for _, f := range files {
		if !expected[f] {
			t.Errorf("unexpected file in result: %s", f)
		}
		delete(expected, f)
	}
	for f := range expected {
		t.Errorf("expected file not found: %s", f)
	}
}

func TestGetAvailableLogFiles_NoneExist(t *testing.T) {
	srv := &testLogServer{
		files: map[string]string{
			// Only files not in the candidate list
			"/tmp/custom.log": "data",
		},
	}
	client, cleanup := startTestSSHServer(t, srv)
	defer cleanup()

	files, err := GetAvailableLogFiles(client)
	if err != nil {
		t.Fatalf("GetAvailableLogFiles error: %v", err)
	}

	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d: %v", len(files), files)
	}
}

func TestGetAvailableLogFiles_ClosedClient(t *testing.T) {
	srv := newTestLogServer()
	client, cleanup := startTestSSHServer(t, srv)
	client.Close()
	cleanup()

	_, err := GetAvailableLogFiles(client)
	if err == nil {
		t.Fatal("expected error with closed client")
	}
}

// --- shellQuote tests ---

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/var/log/syslog", "'/var/log/syslog'"},
		{"simple", "'simple'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
	}
	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.expected {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
