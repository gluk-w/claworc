package handlers

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshterminal"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
	"github.com/go-chi/chi/v5"
	gossh "golang.org/x/crypto/ssh"
)

// --- Terminal PTY test SSH server ---

// termPTYHandler configures the behavior of the test PTY SSH server.
type termPTYHandler struct {
	// onShell is called when a shell starts (after PTY request).
	// Receives the SSH channel for I/O.
	onShell func(ch gossh.Channel)

	// onExec is called when an exec request starts (after PTY request).
	onExec func(cmd string, ch gossh.Channel)

	// onWindowChange is called when a resize request is received.
	onWindowChange func(cols, rows uint32)
}

// startTermPTYServer starts a test SSH server with PTY support for terminal handler tests.
func startTermPTYServer(t *testing.T, handler termPTYHandler) (*gossh.Client, func()) {
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
			go handleTermPTYConn(conn, serverCfg, handler)
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

func handleTermPTYConn(netConn net.Conn, config *gossh.ServerConfig, handler termPTYHandler) {
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
		go handleTermPTYSession(ch, requests, handler)
	}
}

func handleTermPTYSession(ch gossh.Channel, reqs <-chan *gossh.Request, handler termPTYHandler) {
	defer ch.Close()

	for req := range reqs {
		switch req.Type {
		case "pty-req":
			if req.WantReply {
				req.Reply(true, nil)
			}

		case "shell":
			if req.WantReply {
				req.Reply(true, nil)
			}
			go handleTermWindowChange(reqs, handler)
			if handler.onShell != nil {
				handler.onShell(ch)
			}
			return

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
			if req.WantReply {
				req.Reply(true, nil)
			}
			go handleTermWindowChange(reqs, handler)
			if handler.onExec != nil {
				handler.onExec(cmd, ch)
			}
			return

		case "window-change":
			if len(req.Payload) >= 8 {
				cols := binary.BigEndian.Uint32(req.Payload[0:4])
				rows := binary.BigEndian.Uint32(req.Payload[4:8])
				if handler.onWindowChange != nil {
					handler.onWindowChange(cols, rows)
				}
			}
			if req.WantReply {
				req.Reply(true, nil)
			}

		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

func handleTermWindowChange(reqs <-chan *gossh.Request, handler termPTYHandler) {
	for req := range reqs {
		switch req.Type {
		case "window-change":
			if len(req.Payload) >= 8 {
				cols := binary.BigEndian.Uint32(req.Payload[0:4])
				rows := binary.BigEndian.Uint32(req.Payload[4:8])
				if handler.onWindowChange != nil {
					handler.onWindowChange(cols, rows)
				}
			}
			if req.WantReply {
				req.Reply(true, nil)
			}
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// setupTerminalServer creates an httptest.Server with the TerminalWSProxy handler
// wired up, and returns the server and a cleanup function.
func setupTerminalServer(t *testing.T, admin *database.User) (*httptest.Server, func()) {
	t.Helper()
	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, middleware.WithUserForTest(r, admin))
		})
	})
	mux.Get("/api/v1/instances/{id}/terminal", TerminalWSProxy)
	ts := httptest.NewServer(mux)
	return ts, ts.Close
}

// --- Tests ---

func TestTerminalWSProxy_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/abc/terminal", map[string]string{"id": "abc"})
	w := httptest.NewRecorder()
	TerminalWSProxy(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestTerminalWSProxy_Forbidden(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-forbid", "Term Forbidden")
	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/terminal", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, viewer)
	w := httptest.NewRecorder()
	TerminalWSProxy(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestTerminalWSProxy_InstanceNotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	admin := createTestAdmin(t)

	ts, tsCleanup := setupTerminalServer(t, admin)
	defer tsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws%s/api/v1/instances/9999/terminal",
		strings.TrimPrefix(ts.URL, "http"))
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		// Dial may fail with the close code, which is also acceptable
		return
	}
	defer conn.CloseNow()

	// The handler should close the connection with code 4004
	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected error reading from WebSocket for non-existent instance")
	}
	closeErr := websocket.CloseStatus(err)
	if closeErr != 4004 {
		t.Errorf("expected close code 4004, got %d (err: %v)", closeErr, err)
	}
}

func TestTerminalWSProxy_NoSSHManager(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-nossh", "Term NoSSH")
	admin := createTestAdmin(t)

	sshtunnel.ResetGlobalForTest()

	ts, tsCleanup := setupTerminalServer(t, admin)
	defer tsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/terminal",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return // Dial failed with close code, acceptable
	}
	defer conn.CloseNow()

	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected error when SSH manager not initialized")
	}
	closeErr := websocket.CloseStatus(err)
	if closeErr != 4500 {
		t.Errorf("expected close code 4500, got %d (err: %v)", closeErr, err)
	}
}

func TestTerminalWSProxy_NoSSHConnection(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-noconn", "Term NoConn")
	admin := createTestAdmin(t)

	sshCleanup := setupSSHManagerEmpty(t)
	defer sshCleanup()

	ts, tsCleanup := setupTerminalServer(t, admin)
	defer tsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/terminal",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return // Dial failed with close code, acceptable
	}
	defer conn.CloseNow()

	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected error when no SSH connection exists for instance")
	}
	closeErr := websocket.CloseStatus(err)
	if closeErr != 4500 {
		t.Errorf("expected close code 4500, got %d (err: %v)", closeErr, err)
	}
}

func TestTerminalWSProxy_EchoSession(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-echo", "Term Echo")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			// Echo server: write back whatever is received
			buf := make([]byte, 4096)
			for {
				n, err := ch.Read(buf)
				if n > 0 {
					ch.Write(buf[:n])
				}
				if err != nil {
					break
				}
			}
		},
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-term-echo", sshClient)
	defer smCleanup()

	ts, tsCleanup := setupTerminalServer(t, admin)
	defer tsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws%s/api/v1/instances/%d/terminal",
		strings.TrimPrefix(ts.URL, "http"), inst.ID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial terminal WebSocket: %v", err)
	}
	defer conn.CloseNow()

	// Send binary input
	testData := []byte("hello terminal\n")
	if err := conn.Write(ctx, websocket.MessageBinary, testData); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	// Read echoed output (binary)
	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if msgType != websocket.MessageBinary {
		t.Errorf("expected binary message, got %v", msgType)
	}
	if string(data) != string(testData) {
		t.Errorf("expected %q, got %q", testData, data)
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_JSONInputMessage(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-jsonin", "Term JSONIn")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 4096)
			for {
				n, err := ch.Read(buf)
				if n > 0 {
					ch.Write(buf[:n])
				}
				if err != nil {
					break
				}
			}
		},
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-term-jsonin", sshClient)
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

	// Send JSON input message
	inputMsg, _ := json.Marshal(termMsg{Type: "input", Data: "ls -la\n"})
	if err := conn.Write(ctx, websocket.MessageText, inputMsg); err != nil {
		t.Fatalf("failed to write JSON input: %v", err)
	}

	// Read echoed output
	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if msgType != websocket.MessageBinary {
		t.Errorf("expected binary message, got %v", msgType)
	}
	if string(data) != "ls -la\n" {
		t.Errorf("expected 'ls -la\\n', got %q", data)
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_Resize(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-resize", "Term Resize")
	admin := createTestAdmin(t)

	var mu sync.Mutex
	var resizeCols, resizeRows uint32
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
			resizeCols = cols
			resizeRows = rows
			mu.Unlock()
			resized <- struct{}{}
		},
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-term-resize", sshClient)
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

	// Send resize message
	resizeMsg, _ := json.Marshal(termMsg{Type: "resize", Cols: 120, Rows: 40})
	if err := conn.Write(ctx, websocket.MessageText, resizeMsg); err != nil {
		t.Fatalf("failed to write resize: %v", err)
	}

	// Wait for resize event
	select {
	case <-resized:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for resize event")
	}

	mu.Lock()
	if resizeCols != 120 {
		t.Errorf("expected 120 cols, got %d", resizeCols)
	}
	if resizeRows != 40 {
		t.Errorf("expected 40 rows, got %d", resizeRows)
	}
	mu.Unlock()

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_PingPong(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-ping", "Term Ping")
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

	smCleanup := setupSSHManagerWithClient(t, "bot-term-ping", sshClient)
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

	// Send ping
	pingMsg, _ := json.Marshal(termMsg{Type: "ping"})
	if err := conn.Write(ctx, websocket.MessageText, pingMsg); err != nil {
		t.Fatalf("failed to write ping: %v", err)
	}

	// Read pong
	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read pong: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Errorf("expected text message for pong, got %v", msgType)
	}

	var pong termMsg
	if err := json.Unmarshal(data, &pong); err != nil {
		t.Fatalf("failed to parse pong: %v", err)
	}
	if pong.Type != "pong" {
		t.Errorf("expected type 'pong', got %q", pong.Type)
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_OutputStreaming(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-output", "Term Output")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			// Write output then close
			ch.Write([]byte("line 1\r\n"))
			ch.Write([]byte("line 2\r\n"))
			ch.Write([]byte("line 3\r\n"))
			exitPayload := gossh.Marshal(struct{ Status uint32 }{0})
			ch.SendRequest("exit-status", false, exitPayload)
		},
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-term-output", sshClient)
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

	// Collect all output
	var output strings.Builder
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		output.Write(data)
	}

	result := output.String()
	if !strings.Contains(result, "line 1") {
		t.Errorf("output missing 'line 1': %q", result)
	}
	if !strings.Contains(result, "line 2") {
		t.Errorf("output missing 'line 2': %q", result)
	}
	if !strings.Contains(result, "line 3") {
		t.Errorf("output missing 'line 3': %q", result)
	}
}

func TestTerminalWSProxy_ANSIPreserved(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-ansi", "Term ANSI")
	admin := createTestAdmin(t)

	redText := "\033[31mred\033[0m"
	boldText := "\033[1mbold\033[0m"

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			ch.Write([]byte(redText))
			ch.Write([]byte(boldText))
			exitPayload := gossh.Marshal(struct{ Status uint32 }{0})
			ch.SendRequest("exit-status", false, exitPayload)
		},
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-term-ansi", sshClient)
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

	var output strings.Builder
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		output.Write(data)
	}

	result := output.String()
	if !strings.Contains(result, "\033[31m") {
		t.Errorf("ANSI red code not preserved: %q", result)
	}
	if !strings.Contains(result, "\033[1m") {
		t.Errorf("ANSI bold code not preserved: %q", result)
	}
}

func TestTerminalWSProxy_ClientDisconnectCleansUp(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-disc", "Term Disconnect")
	admin := createTestAdmin(t)

	shellDone := make(chan struct{})

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			defer close(shellDone)
			// Keep writing until channel closes
			for {
				_, err := ch.Write([]byte("output\n"))
				if err != nil {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		},
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-term-disc", sshClient)
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

	// Read some data to confirm the connection is active
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read initial data: %v", err)
	}

	// Abruptly close WebSocket
	conn.CloseNow()

	// Verify the SSH session cleans up
	select {
	case <-shellDone:
		// SSH session was cleaned up
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for SSH session cleanup after WebSocket disconnect")
	}
}

func TestTerminalWSProxy_SpecialKeys(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-skeys", "Term SpecialKeys")
	admin := createTestAdmin(t)

	var mu sync.Mutex
	var received []byte

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 4096)
			for {
				n, err := ch.Read(buf)
				if n > 0 {
					mu.Lock()
					received = append(received, buf[:n]...)
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

	smCleanup := setupSSHManagerWithClient(t, "bot-term-skeys", sshClient)
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

	// Send special keys as binary messages: Ctrl+C (0x03), Ctrl+D (0x04), Ctrl+Z (0x1a)
	specialKeys := []byte{0x03, 0x04, 0x1a}
	for _, key := range specialKeys {
		if err := conn.Write(ctx, websocket.MessageBinary, []byte{key}); err != nil {
			t.Fatalf("failed to write special key 0x%02x: %v", key, err)
		}
	}

	// Send arrow keys and tab as JSON input messages
	arrowSequences := []string{"\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D", "\x09"}
	for _, seq := range arrowSequences {
		msg, _ := json.Marshal(termMsg{Type: "input", Data: seq})
		if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
			t.Fatalf("failed to write escape sequence: %v", err)
		}
	}

	// Read echoed output to allow processing time
	readCtx, readCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer readCancel()
	for {
		_, _, err := conn.Read(readCtx)
		if err != nil {
			break
		}
	}

	mu.Lock()
	defer mu.Unlock()

	// Verify control characters arrived
	for _, key := range specialKeys {
		found := false
		for _, r := range received {
			if r == key {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("special key 0x%02x not received by SSH shell", key)
		}
	}

	// Verify escape sequences arrived
	receivedStr := string(received)
	for _, seq := range arrowSequences {
		if !strings.Contains(receivedStr, seq) {
			t.Errorf("escape sequence %q not found in received data", seq)
		}
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_InteractiveREPL(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-repl", "Term REPL")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			// Simulate a REPL: prompt → input → output → prompt
			ch.Write([]byte(">>> "))

			buf := make([]byte, 4096)
			var line []byte
			for {
				n, err := ch.Read(buf)
				if err != nil {
					return
				}
				for i := 0; i < n; i++ {
					if buf[i] == '\n' || buf[i] == '\r' {
						input := string(line)
						line = nil
						ch.Write([]byte(fmt.Sprintf("\r\nresult: %s\r\n>>> ", input)))
					} else {
						line = append(line, buf[i])
					}
				}
			}
		},
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-term-repl", sshClient)
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

	// Read initial prompt
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read initial prompt: %v", err)
	}
	if !strings.Contains(string(data), ">>>") {
		t.Errorf("expected REPL prompt '>>>', got %q", data)
	}

	// Send expression via JSON input
	inputMsg, _ := json.Marshal(termMsg{Type: "input", Data: "2+2\n"})
	if err := conn.Write(ctx, websocket.MessageText, inputMsg); err != nil {
		t.Fatalf("failed to write input: %v", err)
	}

	// Read REPL output - may come in multiple messages
	var output strings.Builder
	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()
	for {
		_, data, err := conn.Read(readCtx)
		if err != nil {
			break
		}
		output.Write(data)
		if strings.Contains(output.String(), "result:") {
			break
		}
	}

	if !strings.Contains(output.String(), "result: 2+2") {
		t.Errorf("expected REPL result 'result: 2+2', got %q", output.String())
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_LongRunningStreaming(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-lrun", "Term LongRun")
	admin := createTestAdmin(t)

	const numLines = 20

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			// Simulate long-running command with periodic output
			for i := 0; i < numLines; i++ {
				ch.Write([]byte(fmt.Sprintf("progress %d/%d\r\n", i+1, numLines)))
				time.Sleep(10 * time.Millisecond)
			}
			ch.Write([]byte("complete\r\n"))
			exitPayload := gossh.Marshal(struct{ Status uint32 }{0})
			ch.SendRequest("exit-status", false, exitPayload)
		},
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-term-lrun", sshClient)
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

	// Collect all streaming output
	var output strings.Builder
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		output.Write(data)
	}

	result := output.String()
	if !strings.Contains(result, "progress 1/20") {
		t.Errorf("missing first progress line")
	}
	if !strings.Contains(result, "progress 20/20") {
		t.Errorf("missing last progress line")
	}
	if !strings.Contains(result, "complete") {
		t.Errorf("missing completion marker")
	}
}

func TestTerminalWSProxy_RapidInput(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-rapid", "Term Rapid")
	admin := createTestAdmin(t)

	var mu sync.Mutex
	var totalReceived int

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 32 * 1024)
			for {
				n, err := ch.Read(buf)
				if n > 0 {
					mu.Lock()
					totalReceived += n
					mu.Unlock()
				}
				if err != nil {
					break
				}
			}
		},
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-term-rapid", sshClient)
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

	// Send 500 rapid binary messages.
	// Due to rate limiting (100 msg/sec, 200 burst), messages beyond the
	// burst allowance may be dropped. This is expected security behavior.
	const numMessages = 500
	message := []byte("rapid-input-test\n")
	totalSent := 0

	for i := 0; i < numMessages; i++ {
		if err := conn.Write(ctx, websocket.MessageBinary, message); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		totalSent += len(message)
	}

	// Give time for all data to be processed through WebSocket → SSH pipeline
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	got := totalReceived
	mu.Unlock()

	// With rate limiting, at least the burst amount should get through.
	minExpected := sshterminal.MessageRateBurst * len(message)
	if got < minExpected {
		t.Errorf("sent %d bytes but SSH shell received only %d bytes (expected at least %d for burst); %.1f%% delivered",
			totalSent, got, minExpected, float64(got)/float64(totalSent)*100)
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_DisconnectReconnectNewSession(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-reconn", "Term Reconnect")
	admin := createTestAdmin(t)

	var mu sync.Mutex
	sessionCounter := 0

	sshClient, sshCleanup := startTermPTYServer(t, termPTYHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			mu.Lock()
			sessionCounter++
			id := sessionCounter
			mu.Unlock()

			// Each session writes its unique ID
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

	smCleanup := setupSSHManagerWithClient(t, "bot-term-reconn", sshClient)
	defer smCleanup()

	ts, tsCleanup := setupTerminalServer(t, admin)
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

	_, data1, err := conn1.Read(ctx1)
	if err != nil {
		t.Fatalf("first read failed: %v", err)
	}
	if !strings.Contains(string(data1), "session-1") {
		t.Errorf("first connection: expected 'session-1', got %q", data1)
	}

	// Disconnect
	conn1.Close(websocket.StatusNormalClosure, "")
	time.Sleep(100 * time.Millisecond)

	// Second connection - should get a NEW session
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	conn2, _, err := websocket.Dial(ctx2, wsURL, nil)
	if err != nil {
		t.Fatalf("second dial failed: %v", err)
	}
	defer conn2.CloseNow()

	_, data2, err := conn2.Read(ctx2)
	if err != nil {
		t.Fatalf("second read failed: %v", err)
	}
	if !strings.Contains(string(data2), "session-2") {
		t.Errorf("second connection: expected 'session-2', got %q", data2)
	}

	mu.Lock()
	if sessionCounter != 2 {
		t.Errorf("expected 2 sessions created, got %d", sessionCounter)
	}
	mu.Unlock()

	conn2.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_MultipleResizes(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-term-mresize", "Term MultiResize")
	admin := createTestAdmin(t)

	var mu sync.Mutex
	resizeHistory := make([]struct{ cols, rows uint32 }, 0)
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
			resizeHistory = append(resizeHistory, struct{ cols, rows uint32 }{cols, rows})
			mu.Unlock()
			resized <- struct{}{}
		},
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-term-mresize", sshClient)
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

	sizes := []struct{ cols, rows uint16 }{
		{100, 30},
		{200, 50},
		{80, 24},
	}

	for _, s := range sizes {
		msg, _ := json.Marshal(termMsg{Type: "resize", Cols: s.cols, Rows: s.rows})
		if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
			t.Fatalf("failed to write resize: %v", err)
		}
		select {
		case <-resized:
		case <-time.After(3 * time.Second):
			t.Fatalf("timeout waiting for resize to %dx%d", s.cols, s.rows)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if len(resizeHistory) != 3 {
		t.Fatalf("expected 3 resize events, got %d", len(resizeHistory))
	}

	for i, s := range sizes {
		if resizeHistory[i].cols != uint32(s.cols) || resizeHistory[i].rows != uint32(s.rows) {
			t.Errorf("resize %d: expected %dx%d, got %dx%d",
				i, s.cols, s.rows, resizeHistory[i].cols, resizeHistory[i].rows)
		}
	}

	conn.Close(websocket.StatusNormalClosure, "")
}
