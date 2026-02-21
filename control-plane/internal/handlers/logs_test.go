package handlers

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
	gossh "golang.org/x/crypto/ssh"
)

func TestStreamLogs_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/abc/logs", map[string]string{"id": "abc"})
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestStreamLogs_InstanceNotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/999/logs", map[string]string{"id": "999"})
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestStreamLogs_Forbidden(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-logs-forbid", "Logs Forbidden")
	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, viewer)
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestStreamLogs_UnknownLogType(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-logs-badtype", "Logs BadType")
	admin := createTestAdmin(t)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs?log_type=nonexistent", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Unknown log type") {
		t.Errorf("expected 'Unknown log type' in response, got %q", w.Body.String())
	}
}

func TestStreamLogs_NoSSHManager(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-logs-nossh", "Logs NoSSH")
	admin := createTestAdmin(t)

	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestStreamLogs_NoSSHConnection(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-logs-noconn", "Logs NoConn")
	admin := createTestAdmin(t)

	sshCleanup := setupSSHManagerEmpty(t)
	defer sshCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestStreamLogs_DefaultLogType(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-logs-default", "Logs Default")
	admin := createTestAdmin(t)

	var capturedCmd string
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		capturedCmd = cmd
		return "log line 1\nlog line 2\n", "", 0
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-logs-default", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs?follow=false", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify SSE content type
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}

	// Verify default log type → openclaw path used
	if !strings.Contains(capturedCmd, "/var/log/openclaw.log") {
		t.Errorf("expected openclaw log path in command, got %q", capturedCmd)
	}

	// Verify SSE data lines
	body := w.Body.String()
	if !strings.Contains(body, "data: log line 1") {
		t.Errorf("expected 'data: log line 1' in body, got %q", body)
	}
	if !strings.Contains(body, "data: log line 2") {
		t.Errorf("expected 'data: log line 2' in body, got %q", body)
	}
}

func TestStreamLogs_BrowserLogType(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-logs-browser", "Logs Browser")
	admin := createTestAdmin(t)

	var capturedCmd string
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		capturedCmd = cmd
		return "browser log\n", "", 0
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-logs-browser", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs?log_type=browser&follow=false", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if !strings.Contains(capturedCmd, "/tmp/browser.log") {
		t.Errorf("expected browser log path in command, got %q", capturedCmd)
	}
}

func TestStreamLogs_SystemLogType(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-logs-system", "Logs System")
	admin := createTestAdmin(t)

	var capturedCmd string
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		capturedCmd = cmd
		return "sshd log\n", "", 0
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-logs-system", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs?log_type=system&follow=false", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if !strings.Contains(capturedCmd, "/var/log/sshd.log") {
		t.Errorf("expected system log path in command, got %q", capturedCmd)
	}
}

func TestStreamLogs_CustomLogPaths(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{
		Name:        "bot-logs-custom",
		DisplayName: "Logs Custom",
		Status:      "running",
		LogPaths:    `{"openclaw":"/custom/my-openclaw.log"}`,
	}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	admin := createTestAdmin(t)

	var capturedCmd string
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		capturedCmd = cmd
		return "custom log line\n", "", 0
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-logs-custom", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs?follow=false", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Should use the custom path instead of default
	if !strings.Contains(capturedCmd, "/custom/my-openclaw.log") {
		t.Errorf("expected custom openclaw log path in command, got %q", capturedCmd)
	}
}

func TestStreamLogs_TailParameter(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-logs-tail", "Logs Tail")
	admin := createTestAdmin(t)

	var capturedCmd string
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		capturedCmd = cmd
		return "line\n", "", 0
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-logs-tail", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs?tail=25&follow=false", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if !strings.Contains(capturedCmd, "-n 25") {
		t.Errorf("expected '-n 25' in command, got %q", capturedCmd)
	}
}

func TestStreamLogs_SSEFormat(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-logs-sse", "Logs SSE")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "first\nsecond\nthird\n", "", 0
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-logs-sse", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs?follow=false", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	// Verify SSE headers
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	if xab := w.Header().Get("X-Accel-Buffering"); xab != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", xab)
	}

	// Parse SSE events
	scanner := bufio.NewScanner(strings.NewReader(w.Body.String()))
	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		}
	}

	if len(dataLines) != 3 {
		t.Fatalf("expected 3 SSE data lines, got %d: %v", len(dataLines), dataLines)
	}
	expected := []string{"first", "second", "third"}
	for i, want := range expected {
		if dataLines[i] != want {
			t.Errorf("data line %d = %q, want %q", i, dataLines[i], want)
		}
	}
}

func TestStreamLogs_FollowMode(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-logs-follow", "Logs Follow")
	admin := createTestAdmin(t)

	var capturedCmd string
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		capturedCmd = cmd
		return "streaming line 1\nstreaming line 2\n", "", 0
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-logs-follow", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs?follow=true", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify the -f flag was used
	if !strings.Contains(capturedCmd, "-f") {
		t.Errorf("expected -f flag in command for follow mode, got %q", capturedCmd)
	}

	body := w.Body.String()
	if !strings.Contains(body, "data: streaming line 1") {
		t.Errorf("expected streaming line 1 in output, got %q", body)
	}
	if !strings.Contains(body, "data: streaming line 2") {
		t.Errorf("expected streaming line 2 in output, got %q", body)
	}
}

// --- Integration / scenario tests ---

// TestStreamLogs_ClientDisconnect verifies that when the HTTP client disconnects
// mid-stream (context cancelled), the handler exits promptly and data was
// being streamed before the disconnect.
func TestStreamLogs_ClientDisconnect(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-logs-disconnect", "Logs Disconnect")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startStreamingTestSSHServer(t, func(cmd string, ch streamingChannel) {
		// Continuously write lines to simulate tail -f with active log output.
		for i := 0; ; i++ {
			_, err := fmt.Fprintf(ch, "log line %d\n", i)
			if err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-logs-disconnect", sshClient)
	defer smCleanup()

	// Build request, then wrap its context with cancellation to simulate disconnect
	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs?follow=true", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	ctx, cancel := context.WithCancel(r.Context())
	r = r.WithContext(ctx)

	w := httptest.NewRecorder()

	// Run handler in a goroutine since it blocks in follow mode
	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		StreamLogs(w, r)
	}()

	// Wait for some data to flow, then cancel context (simulating disconnect)
	time.Sleep(100 * time.Millisecond)
	cancel()

	// Handler should exit promptly after context cancellation
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not exit after client disconnect")
	}

	// Verify some SSE data was delivered before the disconnect
	body := w.Body.String()
	if !strings.Contains(body, "data: log line") {
		t.Error("expected some log data before disconnect")
	}

	// Verify SSE headers were set
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}

// TestStreamLogs_AllLogTypes verifies that all three log types (openclaw,
// browser, system) can be streamed through the handler, each resolving to
// the correct file path on the agent.
func TestStreamLogs_AllLogTypes(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-logs-alltypes", "Logs AllTypes")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "openclaw") {
			return "openclaw output\n", "", 0
		}
		if strings.Contains(cmd, "browser") {
			return "browser output\n", "", 0
		}
		if strings.Contains(cmd, "sshd") {
			return "system output\n", "", 0
		}
		return "", "unknown log file", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-logs-alltypes", sshClient)
	defer smCleanup()

	logTypes := []struct {
		param    string
		expected string
	}{
		{"openclaw", "openclaw output"},
		{"browser", "browser output"},
		{"system", "system output"},
	}

	for _, lt := range logTypes {
		t.Run(lt.param, func(t *testing.T) {
			r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs?log_type=%s&follow=false", inst.ID, lt.param),
				map[string]string{"id": fmt.Sprint(inst.ID)})
			r = middleware.WithUserForTest(r, admin)
			w := httptest.NewRecorder()
			StreamLogs(w, r)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}

			body := w.Body.String()
			if !strings.Contains(body, "data: "+lt.expected) {
				t.Errorf("expected %q in body, got %q", lt.expected, body)
			}
		})
	}
}

// TestStreamLogs_FileNotExist verifies behavior when the log file doesn't exist
// on the remote agent. The tail command writes to stderr and exits with code 1,
// so the SSE stream should return 200 with an empty body (no data lines).
func TestStreamLogs_FileNotExist(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-logs-nofile", "Logs NoFile")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		// Simulate tail error for missing file (output to stderr, nothing to stdout)
		return "", "tail: cannot open '/var/log/openclaw.log' for reading: No such file or directory\n", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-logs-nofile", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs?follow=false", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	// Should return 200 with SSE headers but no data lines
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	// Parse SSE events — should have no data lines
	scanner := bufio.NewScanner(strings.NewReader(w.Body.String()))
	var dataLines int
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			dataLines++
		}
	}
	if dataLines != 0 {
		t.Errorf("expected 0 data lines for missing file, got %d", dataLines)
	}
}

// TestStreamLogs_LargeVolumeSSE verifies that a large number of log lines are
// correctly delivered through the SSE endpoint without data loss.
func TestStreamLogs_LargeVolumeSSE(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-logs-volume", "Logs Volume")
	admin := createTestAdmin(t)

	const lineCount = 1000
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		var sb strings.Builder
		for i := 0; i < lineCount; i++ {
			fmt.Fprintf(&sb, "log entry %04d\n", i)
		}
		return sb.String(), "", 0
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-logs-volume", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs?tail=%d&follow=false", inst.ID, lineCount),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Count and verify SSE data lines
	scanner := bufio.NewScanner(strings.NewReader(w.Body.String()))
	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		}
	}

	if len(dataLines) != lineCount {
		t.Fatalf("expected %d SSE data lines, got %d", lineCount, len(dataLines))
	}

	// Verify first and last entries
	if dataLines[0] != "log entry 0000" {
		t.Errorf("first line: expected 'log entry 0000', got %q", dataLines[0])
	}
	if dataLines[lineCount-1] != fmt.Sprintf("log entry %04d", lineCount-1) {
		t.Errorf("last line: expected 'log entry %04d', got %q", lineCount-1, dataLines[lineCount-1])
	}
}

// TestStreamLogs_DefaultTailValue verifies that the default tail value of 100
// is used when no tail parameter is specified.
func TestStreamLogs_DefaultTailValue(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-logs-deftail", "Logs DefTail")
	admin := createTestAdmin(t)

	var capturedCmd string
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		capturedCmd = cmd
		return "line\n", "", 0
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-logs-deftail", sshClient)
	defer smCleanup()

	// No tail parameter specified
	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/logs?follow=false", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	StreamLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Default tail is 100
	if !strings.Contains(capturedCmd, "-n 100") {
		t.Errorf("expected default '-n 100' in command, got %q", capturedCmd)
	}
}

// --- Streaming test SSH server (for follow-mode handler tests) ---

// streamingChannel wraps gossh.Channel for the streaming handler tests.
type streamingChannel = gossh.Channel

// streamingSessionHandler receives the parsed command and the SSH channel with
// full streaming control.
type streamingSessionHandler func(cmd string, ch streamingChannel)

// startStreamingTestSSHServer starts a test SSH server where the handler has
// direct streaming control over the channel (unlike startFileTestSSHServer which
// collects all output then sends it at once).
func startStreamingTestSSHServer(t *testing.T, handler streamingSessionHandler) (*gossh.Client, func()) {
	t.Helper()

	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := gossh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("create host signer: %v", err)
	}

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
			go handleStreamingTestSSHConn(conn, serverCfg, handler)
		}
	}()

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

func handleStreamingTestSSHConn(netConn net.Conn, config *gossh.ServerConfig, handler streamingSessionHandler) {
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
		go handleStreamingExecSession(ch, requests, handler)
	}
}

func handleStreamingExecSession(ch gossh.Channel, reqs <-chan *gossh.Request, handler streamingSessionHandler) {
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
