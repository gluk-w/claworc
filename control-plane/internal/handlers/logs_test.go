package handlers

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
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
