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
// supports both instant commands and streaming tail. It also records
// executed commands for assertion purposes.
type testLogServer struct {
	mu       sync.Mutex
	files    map[string]string // path → content
	commands []string          // recorded commands in execution order
}

func newTestLogServer() *testLogServer {
	return &testLogServer{
		files: map[string]string{
			"/var/log/syslog":               "line1\nline2\nline3\nline4\nline5\n",
			"/var/log/auth.log":             "auth-line1\nauth-line2\n",
			"/var/log/claworc/openclaw.log": "oc-line1\noc-line2\noc-line3\n",
			"/var/log/claworc/sshd.log":     "sshd-line1\n",
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
		srv.commands = append(srv.commands, cmd)

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
	// Parse: tail -n N [-F|-f] '/path'
	// -F = follow by name (rotation-aware), -f = follow by descriptor
	follow := strings.Contains(cmd, " -F ") || strings.Contains(cmd, " -f ")

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

	ch, err := StreamLogs(ctx, client, "/var/log/syslog", StreamOptions{Tail: 3})
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

	ch, err := StreamLogs(ctx, client, "/var/log/syslog", StreamOptions{Tail: 100})
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

	ch, err := StreamLogs(ctx, client, "/var/log/syslog", StreamOptions{Tail: 2, Follow: true})
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

	ch, err := StreamLogs(ctx, client, "/nonexistent/file.log", StreamOptions{Tail: 10})
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

	_, err := StreamLogs(ctx, client, "/var/log/syslog", StreamOptions{Tail: 10})
	if err == nil {
		t.Fatal("expected error with closed client")
	}
}

func TestStreamLogs_GoroutineCleanup(t *testing.T) {
	srv := newTestLogServer()
	client, cleanup := startTestSSHServer(t, srv)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())

	ch, err := StreamLogs(ctx, client, "/var/log/syslog", StreamOptions{Tail: 2, Follow: true})
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

	ch, err := StreamLogs(ctx, client, "/var/log/auth.log", StreamOptions{Tail: 100})
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

	// Server has syslog, auth.log, and claworc service logs
	expected := map[string]bool{
		"/var/log/syslog":               true,
		"/var/log/auth.log":             true,
		"/var/log/claworc/openclaw.log": true,
		"/var/log/claworc/sshd.log":     true,
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

// --- ResolveLogPath tests ---

func TestResolveLogPath_Defaults(t *testing.T) {
	tests := []struct {
		logType  LogType
		expected string
	}{
		{LogTypeOpenClaw, LogPathOpenClaw},
		{LogTypeSSHD, LogPathSSHD},
		{LogTypeSystem, LogPathSyslog},
		{LogTypeAuth, LogPathAuth},
	}
	for _, tt := range tests {
		got := ResolveLogPath(tt.logType, nil)
		if got != tt.expected {
			t.Errorf("ResolveLogPath(%q, nil) = %q, want %q", tt.logType, got, tt.expected)
		}
	}
}

func TestResolveLogPath_CustomOverride(t *testing.T) {
	custom := map[LogType]string{
		LogTypeOpenClaw: "/custom/openclaw.log",
	}

	// Custom path should be used
	got := ResolveLogPath(LogTypeOpenClaw, custom)
	if got != "/custom/openclaw.log" {
		t.Errorf("expected custom path, got %q", got)
	}

	// Non-overridden type should fall back to default
	got = ResolveLogPath(LogTypeSystem, custom)
	if got != LogPathSyslog {
		t.Errorf("expected default path %q, got %q", LogPathSyslog, got)
	}
}

func TestResolveLogPath_UnknownType(t *testing.T) {
	got := ResolveLogPath("nonexistent", nil)
	if got != "" {
		t.Errorf("expected empty string for unknown type, got %q", got)
	}
}

func TestAllLogTypes(t *testing.T) {
	types := AllLogTypes()
	if len(types) != 4 {
		t.Fatalf("expected 4 log types, got %d", len(types))
	}
	// Verify all types have default paths
	for _, lt := range types {
		if _, ok := DefaultLogPaths[lt]; !ok {
			t.Errorf("log type %q missing from DefaultLogPaths", lt)
		}
	}
}

func TestLogPathConstants(t *testing.T) {
	// Verify the path constants match what's in DefaultLogPaths
	if DefaultLogPaths[LogTypeOpenClaw] != "/var/log/claworc/openclaw.log" {
		t.Errorf("unexpected openclaw path: %s", DefaultLogPaths[LogTypeOpenClaw])
	}
	if DefaultLogPaths[LogTypeSSHD] != "/var/log/claworc/sshd.log" {
		t.Errorf("unexpected sshd path: %s", DefaultLogPaths[LogTypeSSHD])
	}
	if DefaultLogPaths[LogTypeSystem] != "/var/log/syslog" {
		t.Errorf("unexpected syslog path: %s", DefaultLogPaths[LogTypeSystem])
	}
	if DefaultLogPaths[LogTypeAuth] != "/var/log/auth.log" {
		t.Errorf("unexpected auth path: %s", DefaultLogPaths[LogTypeAuth])
	}
}

// --- StreamLogs with claworc paths ---

func TestStreamLogs_OpenClawLog(t *testing.T) {
	srv := newTestLogServer()
	client, cleanup := startTestSSHServer(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := StreamLogs(ctx, client, LogPathOpenClaw, StreamOptions{Tail: 100})
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
	if lines[0] != "oc-line1" {
		t.Errorf("expected 'oc-line1', got %q", lines[0])
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

// --- StreamOptions tests ---

func TestDefaultStreamOptions(t *testing.T) {
	opts := DefaultStreamOptions()
	if opts.Tail != 100 {
		t.Errorf("expected Tail=100, got %d", opts.Tail)
	}
	if !opts.Follow {
		t.Error("expected Follow=true")
	}
	if opts.FollowName == nil || !*opts.FollowName {
		t.Error("expected FollowName=true")
	}
}

func TestStreamOptions_FollowByName_Default(t *testing.T) {
	// nil FollowName should default to true (rotation-aware)
	opts := StreamOptions{Follow: true}
	if !opts.followByName() {
		t.Error("expected followByName()=true when FollowName is nil")
	}
}

func TestStreamOptions_FollowByName_Explicit(t *testing.T) {
	followName := true
	opts := StreamOptions{Follow: true, FollowName: &followName}
	if !opts.followByName() {
		t.Error("expected followByName()=true")
	}

	followName = false
	opts.FollowName = &followName
	if opts.followByName() {
		t.Error("expected followByName()=false")
	}
}

// --- Log rotation awareness tests ---

func TestStreamLogs_Follow_UsesDashF_ByDefault(t *testing.T) {
	srv := newTestLogServer()
	client, cleanup := startTestSSHServer(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := StreamLogs(ctx, client, "/var/log/syslog", StreamOptions{Tail: 1, Follow: true})
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	// Read one line to ensure the command executed
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for line")
	}

	cancel()
	for range ch {
	}

	// Verify the command used -F (follow by name, rotation-aware)
	srv.mu.Lock()
	defer srv.mu.Unlock()

	if len(srv.commands) == 0 {
		t.Fatal("no commands recorded")
	}
	cmd := srv.commands[0]
	if !strings.Contains(cmd, " -F ") {
		t.Errorf("expected command to contain ' -F ' for rotation-aware following, got: %s", cmd)
	}
	if strings.Contains(cmd, " -f ") {
		t.Errorf("expected command NOT to contain ' -f ' (descriptor following), got: %s", cmd)
	}
}

func TestStreamLogs_Follow_UsesDashSmallF_WhenFollowNameDisabled(t *testing.T) {
	srv := newTestLogServer()
	client, cleanup := startTestSSHServer(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	followName := false
	ch, err := StreamLogs(ctx, client, "/var/log/syslog", StreamOptions{
		Tail:       1,
		Follow:     true,
		FollowName: &followName,
	})
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	// Read one line to ensure the command executed
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for line")
	}

	cancel()
	for range ch {
	}

	// Verify the command used -f (follow by descriptor, NOT rotation-aware)
	srv.mu.Lock()
	defer srv.mu.Unlock()

	if len(srv.commands) == 0 {
		t.Fatal("no commands recorded")
	}
	cmd := srv.commands[0]
	if !strings.Contains(cmd, " -f ") {
		t.Errorf("expected command to contain ' -f ' for descriptor following, got: %s", cmd)
	}
	if strings.Contains(cmd, " -F ") {
		t.Errorf("expected command NOT to contain ' -F ' (name following), got: %s", cmd)
	}
}

func TestStreamLogs_NonFollow_NeitherFFlag(t *testing.T) {
	srv := newTestLogServer()
	client, cleanup := startTestSSHServer(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := StreamLogs(ctx, client, "/var/log/syslog", StreamOptions{Tail: 1})
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	for range ch {
	}

	// Verify the command does not use -F or -f (non-follow mode)
	srv.mu.Lock()
	defer srv.mu.Unlock()

	if len(srv.commands) == 0 {
		t.Fatal("no commands recorded")
	}
	cmd := srv.commands[0]
	if strings.Contains(cmd, " -F ") || strings.Contains(cmd, " -f ") {
		t.Errorf("expected command to have neither -F nor -f in non-follow mode, got: %s", cmd)
	}
}

func TestStreamLogs_TailDefaultsTo100_WhenZero(t *testing.T) {
	srv := newTestLogServer()
	client, cleanup := startTestSSHServer(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := StreamLogs(ctx, client, "/var/log/syslog", StreamOptions{})
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	for range ch {
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()

	if len(srv.commands) == 0 {
		t.Fatal("no commands recorded")
	}
	cmd := srv.commands[0]
	if !strings.Contains(cmd, "-n 100") {
		t.Errorf("expected command to contain '-n 100' when Tail=0, got: %s", cmd)
	}
}
