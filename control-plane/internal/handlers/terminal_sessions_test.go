package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/gluk-w/claworc/control-plane/internal/sshterminal"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
	"github.com/go-chi/chi/v5"
	gossh "golang.org/x/crypto/ssh"
)

// setupManagedTerminalServer creates a test server with session manager wired up.
func setupManagedTerminalServer(t *testing.T, admin *database.User) (*httptest.Server, func()) {
	t.Helper()
	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, middleware.WithUserForTest(r, admin))
		})
	})
	mux.Get("/api/v1/instances/{id}/terminal", TerminalWSProxy)
	mux.Get("/api/v1/instances/{id}/terminal/sessions", ListTerminalSessions)
	mux.Delete("/api/v1/instances/{id}/terminal/sessions/{sessionId}", DeleteTerminalSession)
	mux.Get("/api/v1/instances/{id}/terminal/sessions/{sessionId}/recording", GetTerminalRecording)
	ts := httptest.NewServer(mux)
	return ts, ts.Close
}

// setupSessionManager sets up the global session manager for tests.
func setupSessionManager(t *testing.T, instName string, sshClient *gossh.Client) func() {
	t.Helper()
	sm := sshmanager.NewSSHManager(0)
	sm.SetClient(instName, sshClient)
	tm := sshtunnel.NewTunnelManager(sm)
	sessMgr := sshterminal.NewSessionManager()
	sshtunnel.SetGlobalForTestWithSessions(sm, tm, sessMgr)
	return func() {
		sshtunnel.ResetGlobalForTest()
	}
}

func setupSessionManagerWithRecording(t *testing.T, instName string, sshClient *gossh.Client) func() {
	t.Helper()
	sm := sshmanager.NewSSHManager(0)
	sm.SetClient(instName, sshClient)
	tm := sshtunnel.NewTunnelManager(sm)
	sessMgr := sshterminal.NewSessionManager()
	sessMgr.RecordingEnabled = true
	sshtunnel.SetGlobalForTestWithSessions(sm, tm, sessMgr)
	return func() {
		sshtunnel.ResetGlobalForTest()
	}
}

// --- Tests ---

func TestManagedSession_NewSessionReturnsSessionID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-sess-new", "Sess New")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			ch.Write([]byte("hello\r\n"))
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
	})
	defer sshCleanup()

	smCleanup := setupSessionManager(t, "bot-sess-new", sshClient)
	defer smCleanup()

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

	// First message should be session metadata
	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read session metadata: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Errorf("expected text message for session metadata, got %v", msgType)
	}

	var msg termMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("failed to parse session metadata: %v", err)
	}
	if msg.Type != "session" {
		t.Errorf("expected type 'session', got %q", msg.Type)
	}
	if msg.SessionID == "" {
		t.Error("expected non-empty session ID")
	}
	if msg.State != "active" {
		t.Errorf("expected state 'active', got %q", msg.State)
	}

	// Second message should be terminal output
	msgType, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read output: %v", err)
	}
	if msgType != websocket.MessageBinary {
		t.Errorf("expected binary message, got %v", msgType)
	}
	if !strings.Contains(string(data), "hello") {
		t.Errorf("expected output containing 'hello', got %q", data)
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestManagedSession_MultipleConcurrentSessions(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-sess-multi", "Sess Multi")
	admin := createTestAdmin(t)

	var mu sync.Mutex
	sessionCounter := 0

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			mu.Lock()
			sessionCounter++
			id := sessionCounter
			mu.Unlock()
			ch.Write([]byte(fmt.Sprintf("session-%d\r\n", id)))
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
	})
	defer sshCleanup()

	smCleanup := setupSessionManager(t, "bot-sess-multi", sshClient)
	defer smCleanup()

	ts, tsCleanup := setupManagedTerminalServer(t, admin)
	defer tsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/terminal",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)

	// Open two concurrent connections
	conn1, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("first dial failed: %v", err)
	}
	defer conn1.CloseNow()

	conn2, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("second dial failed: %v", err)
	}
	defer conn2.CloseNow()

	// Read session metadata from both
	readSessionID := func(conn *websocket.Conn) string {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("failed to read session metadata: %v", err)
		}
		var msg termMsg
		json.Unmarshal(data, &msg)
		return msg.SessionID
	}

	id1 := readSessionID(conn1)
	id2 := readSessionID(conn2)

	if id1 == "" || id2 == "" {
		t.Fatal("expected non-empty session IDs")
	}
	if id1 == id2 {
		t.Error("expected different session IDs for concurrent connections")
	}

	// Both should receive output
	for _, conn := range []*websocket.Conn{conn1, conn2} {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("failed to read output: %v", err)
		}
		if !strings.Contains(string(data), "session-") {
			t.Errorf("expected output containing 'session-', got %q", data)
		}
	}

	conn1.Close(websocket.StatusNormalClosure, "")
	conn2.Close(websocket.StatusNormalClosure, "")
}

func TestManagedSession_ReconnectToDetachedSession(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-sess-recon", "Sess Reconnect")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			ch.Write([]byte("initial-output\r\n"))
			buf := make([]byte, 4096)
			for {
				n, err := ch.Read(buf)
				if n > 0 {
					ch.Write([]byte("echo: "))
					ch.Write(buf[:n])
				}
				if err != nil {
					break
				}
			}
		},
	})
	defer sshCleanup()

	smCleanup := setupSessionManager(t, "bot-sess-recon", sshClient)
	defer smCleanup()

	ts, tsCleanup := setupManagedTerminalServer(t, admin)
	defer tsCleanup()

	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/terminal",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)

	// First connection
	ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel1()

	conn1, _, err := websocket.Dial(ctx1, wsURL, nil)
	if err != nil {
		t.Fatalf("first dial failed: %v", err)
	}

	// Read session metadata
	_, data, err := conn1.Read(ctx1)
	if err != nil {
		t.Fatalf("failed to read session metadata: %v", err)
	}
	var sessionMeta termMsg
	json.Unmarshal(data, &sessionMeta)
	sessionID := sessionMeta.SessionID

	// Read initial output
	_, data, err = conn1.Read(ctx1)
	if err != nil {
		t.Fatalf("failed to read initial output: %v", err)
	}
	if !strings.Contains(string(data), "initial-output") {
		t.Errorf("expected initial output, got %q", data)
	}

	// Disconnect first connection (session should detach, not close)
	conn1.Close(websocket.StatusNormalClosure, "")
	time.Sleep(200 * time.Millisecond)

	// Reconnect with session_id
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	reconnectURL := fmt.Sprintf("%s?session_id=%s", wsURL, sessionID)
	conn2, _, err := websocket.Dial(ctx2, reconnectURL, nil)
	if err != nil {
		t.Fatalf("reconnect dial failed: %v", err)
	}
	defer conn2.CloseNow()

	// Should get session metadata
	_, data, err = conn2.Read(ctx2)
	if err != nil {
		t.Fatalf("failed to read reconnect session metadata: %v", err)
	}
	var reconnectMeta termMsg
	json.Unmarshal(data, &reconnectMeta)
	if reconnectMeta.SessionID != sessionID {
		t.Errorf("expected session ID %q, got %q", sessionID, reconnectMeta.SessionID)
	}

	// Should get scrollback replay containing the initial output
	_, data, err = conn2.Read(ctx2)
	if err != nil {
		t.Fatalf("failed to read scrollback: %v", err)
	}
	if !strings.Contains(string(data), "initial-output") {
		t.Errorf("scrollback replay missing initial output: %q", data)
	}

	conn2.Close(websocket.StatusNormalClosure, "")
}

func TestManagedSession_ReconnectToNonExistentSession(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-sess-norecon", "Sess NoRecon")
	admin := createTestAdmin(t)

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
	})
	defer sshCleanup()

	smCleanup := setupSessionManager(t, "bot-sess-norecon", sshClient)
	defer smCleanup()

	ts, tsCleanup := setupManagedTerminalServer(t, admin)
	defer tsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reconnectURL := fmt.Sprintf("ws%s/api/v1/instances/%d/terminal?session_id=non-existent",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, reconnectURL, nil)
	if err != nil {
		return // Dial failed with close code
	}
	defer conn.CloseNow()

	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
	closeErr := websocket.CloseStatus(err)
	if closeErr != 4404 {
		t.Errorf("expected close code 4404, got %d", closeErr)
	}
}

func TestListTerminalSessions(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-sess-list", "Sess List")
	admin := createTestAdmin(t)

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
	})
	defer sshCleanup()

	smCleanup := setupSessionManager(t, "bot-sess-list", sshClient)
	defer smCleanup()

	ts, tsCleanup := setupManagedTerminalServer(t, admin)
	defer tsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a session by connecting
	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/terminal",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	// Read session metadata
	conn.Read(ctx)
	conn.Close(websocket.StatusNormalClosure, "")
	time.Sleep(100 * time.Millisecond)

	// List sessions via API
	listURL := fmt.Sprintf("%s/api/v1/instances/%d/terminal/sessions", ts.URL, inst.ID)
	resp, err := http.Get(listURL)
	if err != nil {
		t.Fatalf("GET sessions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string][]sessionInfo
	json.NewDecoder(resp.Body).Decode(&result)

	sessions := result["sessions"]
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	if sessions[0].InstanceID != inst.ID {
		t.Errorf("expected instance ID %d, got %d", inst.ID, sessions[0].InstanceID)
	}
	if sessions[0].State != "detached" {
		t.Errorf("expected state 'detached', got %q", sessions[0].State)
	}
}

func TestDeleteTerminalSession(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-sess-del", "Sess Delete")
	admin := createTestAdmin(t)

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
	})
	defer sshCleanup()

	smCleanup := setupSessionManager(t, "bot-sess-del", sshClient)
	defer smCleanup()

	ts, tsCleanup := setupManagedTerminalServer(t, admin)
	defer tsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a session
	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/terminal",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}

	// Read session ID
	_, data, _ := conn.Read(ctx)
	var meta termMsg
	json.Unmarshal(data, &meta)
	sessionID := meta.SessionID

	conn.Close(websocket.StatusNormalClosure, "")
	time.Sleep(100 * time.Millisecond)

	// Delete the session
	deleteURL := fmt.Sprintf("%s/api/v1/instances/%d/terminal/sessions/%s", ts.URL, inst.ID, sessionID)
	req, _ := http.NewRequest("DELETE", deleteURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE session: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify it's gone from list
	listURL := fmt.Sprintf("%s/api/v1/instances/%d/terminal/sessions", ts.URL, inst.ID)
	resp2, _ := http.Get(listURL)
	defer resp2.Body.Close()

	var result map[string][]sessionInfo
	json.NewDecoder(resp2.Body).Decode(&result)
	if len(result["sessions"]) != 0 {
		t.Errorf("expected 0 sessions after delete, got %d", len(result["sessions"]))
	}
}

func TestGetTerminalRecording(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-sess-rec", "Sess Recording")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			ch.Write([]byte("recorded-data\r\n"))
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
	})
	defer sshCleanup()

	smCleanup := setupSessionManagerWithRecording(t, "bot-sess-rec", sshClient)
	defer smCleanup()

	ts, tsCleanup := setupManagedTerminalServer(t, admin)
	defer tsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a session
	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/terminal",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}

	// Read session ID
	_, data, _ := conn.Read(ctx)
	var meta termMsg
	json.Unmarshal(data, &meta)
	sessionID := meta.SessionID

	// Read some output to allow recording
	conn.Read(ctx)
	time.Sleep(100 * time.Millisecond)

	conn.Close(websocket.StatusNormalClosure, "")
	time.Sleep(100 * time.Millisecond)

	// Get recording
	recURL := fmt.Sprintf("%s/api/v1/instances/%d/terminal/sessions/%s/recording", ts.URL, inst.ID, sessionID)
	resp, err := http.Get(recURL)
	if err != nil {
		t.Fatalf("GET recording: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var entries []sshterminal.RecordingEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatalf("decode recording: %v", err)
	}

	if len(entries) == 0 {
		t.Fatal("expected recording entries")
	}

	hasRecordedData := false
	for _, e := range entries {
		if strings.Contains(e.Data, "recorded-data") {
			hasRecordedData = true
			break
		}
	}
	if !hasRecordedData {
		t.Error("recording missing expected output 'recorded-data'")
	}
}

func TestListTerminalSessions_NoSessionManager(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-sess-nosm", "Sess NoSM")
	admin := createTestAdmin(t)

	sshtunnel.ResetGlobalForTest()

	ts, tsCleanup := setupManagedTerminalServer(t, admin)
	defer tsCleanup()

	listURL := fmt.Sprintf("%s/api/v1/instances/%d/terminal/sessions", ts.URL, inst.ID)
	resp, err := http.Get(listURL)
	if err != nil {
		t.Fatalf("GET sessions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string][]sessionInfo
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result["sessions"]) != 0 {
		t.Errorf("expected empty sessions, got %d", len(result["sessions"]))
	}
}
