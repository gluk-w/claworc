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
