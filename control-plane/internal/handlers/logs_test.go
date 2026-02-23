package handlers

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"golang.org/x/crypto/ssh"
)

// logTestSSHServer runs an in-process SSH server that handles tail and file-check
// commands, mirroring the test server in sshlogs/logs_test.go.
type logTestSSHServer struct {
	mu    sync.Mutex
	files map[string]string
}

func newLogTestSSHServer() *logTestSSHServer {
	return &logTestSSHServer{
		files: map[string]string{
			"/var/log/claworc/openclaw.log": "oc-line1\noc-line2\noc-line3\n",
			"/var/log/claworc/sshd.log":     "sshd-line1\nsshd-line2\n",
			"/var/log/syslog":               "sys-line1\nsys-line2\nsys-line3\nsys-line4\nsys-line5\n",
			"/var/log/auth.log":             "auth-line1\nauth-line2\n",
		},
	}
}

func startLogSSHServer(t *testing.T, authorizedKey ssh.PublicKey, srv *logTestSSHServer) (string, func()) {
	t.Helper()

	_, hostKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.ParsePrivateKey(hostKeyPEM)
	if err != nil {
		t.Fatalf("parse host key: %v", err)
	}

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if ssh.FingerprintSHA256(key) == ssh.FingerprintSHA256(authorizedKey) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	cfg.AddHostKey(hostSigner)

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
			go handleLogServerConn(netConn, cfg, srv)
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

	return listener.Addr().String(), cleanup
}

func handleLogServerConn(netConn net.Conn, cfg *ssh.ServerConfig, srv *logTestSSHServer) {
	sshConn, chans, reqs, err := ssh.NewServerConn(netConn, cfg)
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
		go handleLogSession(ch, requests, srv)
	}
}

func handleLogSession(ch ssh.Channel, requests <-chan *ssh.Request, srv *logTestSSHServer) {
	defer ch.Close()
	for req := range requests {
		if req.Type != "exec" {
			if req.WantReply {
				req.Reply(true, nil)
			}
			continue
		}

		cmdLen := uint32(req.Payload[0])<<24 | uint32(req.Payload[1])<<16 | uint32(req.Payload[2])<<8 | uint32(req.Payload[3])
		cmd := string(req.Payload[4 : 4+cmdLen])

		if req.WantReply {
			req.Reply(true, nil)
		}

		srv.mu.Lock()

		if strings.HasPrefix(cmd, "tail ") {
			handleLogTailCmd(ch, cmd, srv)
			srv.mu.Unlock()
			return
		}

		srv.mu.Unlock()

		ch.Stderr().Write([]byte(fmt.Sprintf("unknown command: %s", cmd)))
		exitPayload := []byte{0, 0, 0, 127}
		ch.SendRequest("exit-status", false, exitPayload)
		return
	}
}

func handleLogTailCmd(ch ssh.Channel, cmd string, srv *logTestSSHServer) {
	follow := strings.Contains(cmd, " -F ")

	tailN := 100
	if idx := strings.Index(cmd, "-n "); idx >= 0 {
		rest := cmd[idx+3:]
		fmt.Sscanf(rest, "%d", &tailN)
	}

	// Extract path (last single-quoted arg)
	path := ""
	start := strings.LastIndex(cmd, "'")
	if start > 0 {
		beforeLast := cmd[:start]
		prevQuote := strings.LastIndex(beforeLast, "'")
		if prevQuote >= 0 {
			path = cmd[prevQuote+1 : start]
		}
	}

	content, ok := srv.files[path]
	if !ok {
		ch.Stderr().Write([]byte(fmt.Sprintf("tail: cannot open '%s' for reading: No such file or directory\n", path)))
		exitPayload := []byte{0, 0, 0, 1}
		ch.SendRequest("exit-status", false, exitPayload)
		return
	}

	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if tailN < len(lines) {
		lines = lines[len(lines)-tailN:]
	}

	for _, line := range lines {
		ch.Write([]byte(line + "\n"))
	}

	if !follow {
		exitPayload := []byte{0, 0, 0, 0}
		ch.SendRequest("exit-status", false, exitPayload)
		return
	}

	// Follow mode: block until channel close
	buf := make([]byte, 1)
	for {
		_, err := ch.Read(buf)
		if err != nil {
			break
		}
	}
}

// setupLogTest sets up a complete test environment with SSH server, manager,
// orchestrator, DB instance, and admin user. Returns a cleanup function.
func setupLogTest(t *testing.T) (cleanup func()) {
	t.Helper()

	setupTestDB(t)

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	srv := newLogTestSSHServer()
	addr, sshCleanup := startLogSSHServer(t, signer.PublicKey(), srv)

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr

	mock := &mockOrchestrator{sshHost: host, sshPort: port}
	orchestrator.Set(mock)

	return func() {
		mgr.CloseAll()
		sshCleanup()
		orchestrator.Set(nil)
	}
}

// readSSELines reads SSE "data: " lines from the response body until the
// context is cancelled or the body is closed. Returns collected lines.
func readSSELines(t *testing.T, resp *http.Response, maxLines int, timeout time.Duration) []string {
	t.Helper()

	var lines []string
	done := make(chan struct{})

	go func() {
		defer close(done)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				lines = append(lines, strings.TrimPrefix(line, "data: "))
				if maxLines > 0 && len(lines) >= maxLines {
					return
				}
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(timeout):
	}

	return lines
}

// --- StreamLogs handler tests ---

func TestStreamLogs_DefaultType_OpenClaw(t *testing.T) {
	cleanup := setupLogTest(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/logs?follow=false", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "follow=false"
	w := httptest.NewRecorder()

	StreamLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %s", ct)
	}

	// Parse SSE lines
	body := w.Body.String()
	var lines []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "data: ") {
			lines = append(lines, strings.TrimPrefix(l, "data: "))
		}
	}

	// Default is openclaw log which has 3 lines
	if len(lines) != 3 {
		t.Fatalf("expected 3 SSE lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "oc-line1" {
		t.Errorf("expected 'oc-line1', got %q", lines[0])
	}
}

func TestStreamLogs_SystemType(t *testing.T) {
	cleanup := setupLogTest(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/logs?type=system&follow=false", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "type=system&follow=false"
	w := httptest.NewRecorder()

	StreamLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	var lines []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "data: ") {
			lines = append(lines, strings.TrimPrefix(l, "data: "))
		}
	}

	if len(lines) != 5 {
		t.Fatalf("expected 5 SSE lines for system log, got %d: %v", len(lines), lines)
	}
	if lines[0] != "sys-line1" {
		t.Errorf("expected 'sys-line1', got %q", lines[0])
	}
}

func TestStreamLogs_SSHDType(t *testing.T) {
	cleanup := setupLogTest(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/logs?type=sshd&follow=false", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "type=sshd&follow=false"
	w := httptest.NewRecorder()

	StreamLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	var lines []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "data: ") {
			lines = append(lines, strings.TrimPrefix(l, "data: "))
		}
	}

	if len(lines) != 2 {
		t.Fatalf("expected 2 SSE lines for sshd log, got %d: %v", len(lines), lines)
	}
	if lines[0] != "sshd-line1" {
		t.Errorf("expected 'sshd-line1', got %q", lines[0])
	}
}

func TestStreamLogs_AuthType(t *testing.T) {
	cleanup := setupLogTest(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/logs?type=auth&follow=false", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "type=auth&follow=false"
	w := httptest.NewRecorder()

	StreamLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	var lines []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "data: ") {
			lines = append(lines, strings.TrimPrefix(l, "data: "))
		}
	}

	if len(lines) != 2 {
		t.Fatalf("expected 2 SSE lines for auth log, got %d: %v", len(lines), lines)
	}
	if lines[0] != "auth-line1" {
		t.Errorf("expected 'auth-line1', got %q", lines[0])
	}
}

func TestStreamLogs_UnknownType(t *testing.T) {
	cleanup := setupLogTest(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/logs?type=bogus&follow=false", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "type=bogus&follow=false"
	w := httptest.NewRecorder()

	StreamLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d; body: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	detail, _ := result["detail"].(string)
	if !strings.Contains(detail, "Unknown log type") {
		t.Errorf("expected error about unknown log type, got %q", detail)
	}
}

func TestStreamLogs_TailParameter(t *testing.T) {
	cleanup := setupLogTest(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/logs?type=system&tail=2&follow=false", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "type=system&tail=2&follow=false"
	w := httptest.NewRecorder()

	StreamLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	var lines []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "data: ") {
			lines = append(lines, strings.TrimPrefix(l, "data: "))
		}
	}

	if len(lines) != 2 {
		t.Fatalf("expected 2 SSE lines with tail=2, got %d: %v", len(lines), lines)
	}
	if lines[0] != "sys-line4" {
		t.Errorf("expected 'sys-line4', got %q", lines[0])
	}
	if lines[1] != "sys-line5" {
		t.Errorf("expected 'sys-line5', got %q", lines[1])
	}
}

func TestStreamLogs_SSEFormat(t *testing.T) {
	cleanup := setupLogTest(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/logs?type=auth&follow=false", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "type=auth&follow=false"
	w := httptest.NewRecorder()

	StreamLogs(w, req)

	// Verify SSE headers
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %s", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("expected Cache-Control no-cache, got %s", cc)
	}
	if conn := w.Header().Get("Connection"); conn != "keep-alive" {
		t.Errorf("expected Connection keep-alive, got %s", conn)
	}
	if xab := w.Header().Get("X-Accel-Buffering"); xab != "no" {
		t.Errorf("expected X-Accel-Buffering no, got %s", xab)
	}

	// Verify SSE format: each line is "data: <content>\n\n"
	body := w.Body.String()
	if !strings.Contains(body, "data: auth-line1\n\n") {
		t.Errorf("expected SSE format 'data: auth-line1\\n\\n' in body: %q", body)
	}
	if !strings.Contains(body, "data: auth-line2\n\n") {
		t.Errorf("expected SSE format 'data: auth-line2\\n\\n' in body: %q", body)
	}
}

func TestStreamLogs_FollowWithContextCancel(t *testing.T) {
	cleanup := setupLogTest(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/logs?type=auth&follow=true&tail=1", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "type=auth&follow=true&tail=1"

	// Wrap the existing context (which has chi params and user) with cancel
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		StreamLogs(w, req)
	}()

	// Wait for the handler to establish the SSH connection and start streaming
	time.Sleep(500 * time.Millisecond)

	// Cancel the context to simulate client disconnect
	cancel()

	// Handler should return within a reasonable time
	select {
	case <-done:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after context cancellation")
	}

	// Should have SSE content type since streaming started before cancel
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %s", ct)
	}
}

func TestStreamLogs_InstanceNotFound(t *testing.T) {
	cleanup := setupLogTest(t)
	defer cleanup()

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/instances/999/logs", user, map[string]string{"id": "999"})
	w := httptest.NewRecorder()

	StreamLogs(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}
}

func TestStreamLogs_Forbidden(t *testing.T) {
	cleanup := setupLogTest(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "user") // non-admin, not assigned

	req := buildRequest(t, "GET", "/api/v1/instances/1/logs", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	StreamLogs(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}
}

func TestStreamLogs_InvalidID(t *testing.T) {
	cleanup := setupLogTest(t)
	defer cleanup()

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/instances/notanumber/logs", user, map[string]string{"id": "notanumber"})
	w := httptest.NewRecorder()

	StreamLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestStreamLogs_NoOrchestrator(t *testing.T) {
	cleanup := setupLogTest(t)
	defer cleanup()
	orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/logs?follow=false", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "follow=false"
	w := httptest.NewRecorder()

	StreamLogs(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}
}

func TestStreamLogs_NoSSHManager(t *testing.T) {
	cleanup := setupLogTest(t)
	defer cleanup()
	SSHMgr = nil

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/logs?follow=false", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "follow=false"
	w := httptest.NewRecorder()

	StreamLogs(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}

	result := parseResponse(t, w)
	if result["detail"] != "SSH manager not initialized" {
		t.Errorf("expected 'SSH manager not initialized', got %v", result["detail"])
	}
}
