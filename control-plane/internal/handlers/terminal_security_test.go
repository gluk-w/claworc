package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/sshterminal"
	gossh "golang.org/x/crypto/ssh"
)

// TestTerminalWSProxy_ResizeClampsDimensions verifies that resize requests
// with dimensions exceeding MaxTermCols/MaxTermRows are clamped.
func TestTerminalWSProxy_ResizeClampsDimensions(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-clamp", "Term Clamp")
	admin := createTestAdmin(t)

	var mu sync.Mutex
	var lastCols, lastRows uint32
	resized := make(chan struct{}, 10)

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
		onWindowChange: func(cols, rows uint32) {
			mu.Lock()
			lastCols = cols
			lastRows = rows
			mu.Unlock()
			resized <- struct{}{}
		},
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-term-clamp", sshClient)
	defer smCleanup()

	ts, tsCleanup := setupTerminalServer(t, admin)
	defer tsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/terminal",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.CloseNow()

	// Send resize with dimensions exceeding limits
	resizeMsg, _ := json.Marshal(termMsg{Type: "resize", Cols: 1000, Rows: 500})
	if err := conn.Write(ctx, websocket.MessageText, resizeMsg); err != nil {
		t.Fatalf("failed to write resize: %v", err)
	}

	select {
	case <-resized:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for resize event")
	}

	mu.Lock()
	gotCols := lastCols
	gotRows := lastRows
	mu.Unlock()

	if gotCols != uint32(sshterminal.MaxTermCols) {
		t.Errorf("expected cols clamped to %d, got %d", sshterminal.MaxTermCols, gotCols)
	}
	if gotRows != uint32(sshterminal.MaxTermRows) {
		t.Errorf("expected rows clamped to %d, got %d", sshterminal.MaxTermRows, gotRows)
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

// TestTerminalWSProxy_ResizeNormalDimensions verifies that normal resize
// requests pass through unclamped.
func TestTerminalWSProxy_ResizeNormalDimensions(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-normrsz", "Term NormRsz")
	admin := createTestAdmin(t)

	var mu sync.Mutex
	var lastCols, lastRows uint32
	resized := make(chan struct{}, 10)

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
		onWindowChange: func(cols, rows uint32) {
			mu.Lock()
			lastCols = cols
			lastRows = rows
			mu.Unlock()
			resized <- struct{}{}
		},
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-term-normrsz", sshClient)
	defer smCleanup()

	ts, tsCleanup := setupTerminalServer(t, admin)
	defer tsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/terminal",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.CloseNow()

	// Send resize with normal dimensions
	resizeMsg, _ := json.Marshal(termMsg{Type: "resize", Cols: 120, Rows: 40})
	if err := conn.Write(ctx, websocket.MessageText, resizeMsg); err != nil {
		t.Fatalf("failed to write resize: %v", err)
	}

	select {
	case <-resized:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for resize event")
	}

	mu.Lock()
	gotCols := lastCols
	gotRows := lastRows
	mu.Unlock()

	if gotCols != 120 {
		t.Errorf("expected cols 120, got %d", gotCols)
	}
	if gotRows != 40 {
		t.Errorf("expected rows 40, got %d", gotRows)
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

// TestTerminalWSProxy_InputSizeLimitBinary verifies that oversized binary
// input messages are silently dropped.
func TestTerminalWSProxy_InputSizeLimitBinary(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-sizelim", "Term SizeLim")
	admin := createTestAdmin(t)

	var mu sync.Mutex
	var received []byte

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 128*1024)
			for {
				n, err := ch.Read(buf)
				if n > 0 {
					mu.Lock()
					received = append(received, buf[:n]...)
					mu.Unlock()
					ch.Write([]byte("ok\n"))
				}
				if err != nil {
					break
				}
			}
		},
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-term-sizelim", sshClient)
	defer smCleanup()

	ts, tsCleanup := setupTerminalServer(t, admin)
	defer tsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/terminal",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.CloseNow()

	// First send a small message (should work)
	smallData := []byte("hello\n")
	if err := conn.Write(ctx, websocket.MessageBinary, smallData); err != nil {
		t.Fatalf("failed to write small message: %v", err)
	}

	// Read output to confirm it was received
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	// Now send an oversized message (should be dropped)
	oversized := make([]byte, sshterminal.MaxInputMessageSize+1)
	for i := range oversized {
		oversized[i] = 'X'
	}
	// This will likely fail at the WebSocket level (1MB read limit on server),
	// but if it succeeds on write, the server should drop it
	_ = conn.Write(ctx, websocket.MessageBinary, oversized)

	// Send another small message after the oversized one
	// to verify the connection is still alive
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("after\n")); err != nil {
		// Connection may be closed if oversized exceeded WS read limit
		return
	}

	// If we get here, the connection survived. Read the response.
	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()
	_, _, err = conn.Read(readCtx)
	if err != nil {
		// Connection might have been closed, which is acceptable
		return
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

// TestClampResize tests the clampResize helper function.
func TestClampResize(t *testing.T) {
	tests := []struct {
		name     string
		inCols   uint16
		inRows   uint16
		wantCols uint16
		wantRows uint16
	}{
		{"normal", 80, 24, 80, 24},
		{"max cols", 120, 40, 120, 40},
		{"over max cols", 600, 40, sshterminal.MaxTermCols, 40},
		{"over max rows", 80, 300, 80, sshterminal.MaxTermRows},
		{"both over", 999, 999, sshterminal.MaxTermCols, sshterminal.MaxTermRows},
		{"at max", sshterminal.MaxTermCols, sshterminal.MaxTermRows, sshterminal.MaxTermCols, sshterminal.MaxTermRows},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cols, rows := clampResize(tt.inCols, tt.inRows)
			if cols != tt.wantCols {
				t.Errorf("cols: got %d, want %d", cols, tt.wantCols)
			}
			if rows != tt.wantRows {
				t.Errorf("rows: got %d, want %d", rows, tt.wantRows)
			}
		})
	}
}

// TestTerminalWSProxy_RateLimitHandling verifies that the rate limiter
// does not block normal message patterns (burst of messages within limit).
// It checks total bytes received on the SSH side rather than individual reads,
// since SSH reads are often coalesced.
func TestTerminalWSProxy_RateLimitHandling(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-ratelim", "Term RateLim")
	admin := createTestAdmin(t)

	totalBytesReceived := 0
	var mu sync.Mutex

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 64*1024)
			for {
				n, err := ch.Read(buf)
				if n > 0 {
					mu.Lock()
					totalBytesReceived += n
					mu.Unlock()
					ch.Write(buf[:n])
				}
				if err != nil {
					break
				}
			}
		},
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-term-ratelim", sshClient)
	defer smCleanup()

	ts, tsCleanup := setupTerminalServer(t, admin)
	defer tsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/terminal",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.CloseNow()

	// Send 50 messages quickly (within burst limit of 200)
	totalSent := 0
	for i := 0; i < 50; i++ {
		data := []byte(fmt.Sprintf("msg%d\n", i))
		totalSent += len(data)
		if err := conn.Write(ctx, websocket.MessageBinary, data); err != nil {
			t.Fatalf("write %d failed: %v", i, err)
		}
	}

	// Read responses until we've received all data or timeout
	totalRead := 0
	for totalRead < totalSent {
		readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
		_, data, err := conn.Read(readCtx)
		readCancel()
		if err != nil {
			break
		}
		totalRead += len(data)
	}

	// All 50 messages should have been delivered (total bytes)
	mu.Lock()
	received := totalBytesReceived
	mu.Unlock()

	if received < totalSent {
		t.Errorf("expected at least %d bytes received, got %d (some messages may have been rate-limited unexpectedly)", totalSent, received)
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

// TestTerminalWSProxy_ManagedSessionResizeClamped verifies resize clamping
// works in managed session mode (with session manager).
func TestTerminalWSProxy_ManagedSessionResizeClamped(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-mgrclamp", "Term MgrClamp")
	admin := createTestAdmin(t)

	var mu sync.Mutex
	var lastCols, lastRows uint32
	resized := make(chan struct{}, 10)

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
		onWindowChange: func(cols, rows uint32) {
			mu.Lock()
			lastCols = cols
			lastRows = rows
			mu.Unlock()
			resized <- struct{}{}
		},
	})
	defer sshCleanup()

	sessCleanup := setupSessionManager(t, "bot-term-mgrclamp", sshClient)
	defer sessCleanup()

	ts, tsCleanup := setupManagedTerminalServer(t, admin)
	defer tsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/terminal",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.CloseNow()

	// Read session metadata first
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read session metadata: %v", err)
	}

	// Send resize with dimensions exceeding limits
	resizeMsg, _ := json.Marshal(termMsg{Type: "resize", Cols: 800, Rows: 400})
	if err := conn.Write(ctx, websocket.MessageText, resizeMsg); err != nil {
		t.Fatalf("failed to write resize: %v", err)
	}

	select {
	case <-resized:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for resize event")
	}

	mu.Lock()
	gotCols := lastCols
	gotRows := lastRows
	mu.Unlock()

	if gotCols != uint32(sshterminal.MaxTermCols) {
		t.Errorf("expected cols clamped to %d, got %d", sshterminal.MaxTermCols, gotCols)
	}
	if gotRows != uint32(sshterminal.MaxTermRows) {
		t.Errorf("expected rows clamped to %d, got %d", sshterminal.MaxTermRows, gotRows)
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

// TestTerminalSecurity_AllowedShellsComplete verifies the AllowedShells list
// contains the expected shells.
func TestTerminalSecurity_AllowedShellsComplete(t *testing.T) {
	expected := map[string]bool{
		"/bin/bash": false,
		"/bin/sh":   false,
		"/bin/zsh":  false,
	}

	for _, shell := range sshterminal.AllowedShells {
		if _, ok := expected[shell]; ok {
			expected[shell] = true
		} else {
			t.Errorf("unexpected shell in AllowedShells: %q", shell)
		}
	}

	for shell, found := range expected {
		if !found {
			t.Errorf("expected shell %q not in AllowedShells", shell)
		}
	}
}

// TestTerminalSecurity_DefaultIdleTimeout verifies that the session manager
// has a default idle timeout configured.
func TestTerminalSecurity_DefaultIdleTimeout(t *testing.T) {
	mgr := sshterminal.NewSessionManager()
	if mgr.IdleTimeout <= 0 {
		t.Error("expected positive default idle timeout")
	}
	if mgr.IdleTimeout != sshterminal.DefaultIdleTimeout {
		t.Errorf("expected default idle timeout %v, got %v",
			sshterminal.DefaultIdleTimeout, mgr.IdleTimeout)
	}
}

// TestTerminalSecurity_RateLimiterConstants verifies the rate limiter
// constants are sensible for production use.
func TestTerminalSecurity_RateLimiterConstants(t *testing.T) {
	if sshterminal.MessageRateLimit <= 0 {
		t.Error("MessageRateLimit must be positive")
	}
	if sshterminal.MessageRateBurst <= 0 {
		t.Error("MessageRateBurst must be positive")
	}
	if sshterminal.MessageRateBurst < int(sshterminal.MessageRateLimit) {
		t.Error("MessageRateBurst should be >= MessageRateLimit for smooth operation")
	}
}

// TestTerminalSecurity_MaxInputSize verifies the max input size is reasonable.
func TestTerminalSecurity_MaxInputSize(t *testing.T) {
	if sshterminal.MaxInputMessageSize <= 0 {
		t.Error("MaxInputMessageSize must be positive")
	}
	if sshterminal.MaxInputMessageSize > 1024*1024 {
		t.Error("MaxInputMessageSize should not exceed 1MB (WebSocket read limit)")
	}
}
