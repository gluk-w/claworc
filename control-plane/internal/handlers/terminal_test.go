package handlers

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/sshterminal"
	"golang.org/x/crypto/ssh"
)

// testTerminalSSHServer starts an in-process SSH server with PTY and shell support.
// The server echoes stdin with an "echo:" prefix and reports resize events.
func testTerminalSSHServer(t *testing.T, authorizedKey ssh.PublicKey) (addr string, cleanup func()) {
	t.Helper()

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
			if ssh.FingerprintSHA256(key) == ssh.FingerprintSHA256(authorizedKey) {
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

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			netConn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleTerminalTestConn(netConn, config)
		}
	}()

	return listener.Addr().String(), func() {
		listener.Close()
		<-done
	}
}

func handleTerminalTestConn(netConn net.Conn, config *ssh.ServerConfig) {
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
		go handleTerminalTestSession(ch, requests)
	}
}

func handleTerminalTestSession(ch ssh.Channel, requests <-chan *ssh.Request) {
	defer ch.Close()

	var hasPTY bool

	for req := range requests {
		switch req.Type {
		case "pty-req":
			hasPTY = true
			if req.WantReply {
				req.Reply(true, nil)
			}

		case "window-change":
			if len(req.Payload) >= 8 {
				cols := binary.BigEndian.Uint32(req.Payload[0:4])
				rows := binary.BigEndian.Uint32(req.Payload[4:8])
				ch.Write([]byte(fmt.Sprintf("resize:%dx%d\n", cols, rows)))
			}
			if req.WantReply {
				req.Reply(true, nil)
			}

		case "exec", "shell":
			if req.WantReply {
				req.Reply(true, nil)
			}
			if hasPTY {
				ch.Write([]byte("PTY:true\n"))
			} else {
				ch.Write([]byte("PTY:false\n"))
			}
			// Echo stdin back with prefix
			go func() {
				buf := make([]byte, 4096)
				for {
					n, err := ch.Read(buf)
					if n > 0 {
						ch.Write([]byte("echo:"))
						ch.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}()

		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// setupTerminalTest sets up SSH infrastructure and returns the proxy httptest.Server.
// It registers all necessary cleanup functions via t.Cleanup.
func setupTerminalTest(t *testing.T) *httptest.Server {
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

	addr, cleanup := testTerminalSSHServer(t, signer.PublicKey())
	t.Cleanup(cleanup)

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	t.Cleanup(func() { mgr.CloseAll() })
	SSHMgr = mgr
	t.Cleanup(func() { SSHMgr = nil })

	mock := &mockOrchestrator{sshHost: host, sshPort: port}
	orchestrator.Set(mock)
	t.Cleanup(func() { orchestrator.Set(nil) })

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	// Pre-connect SSH
	_, err = mgr.Connect(context.Background(), inst.ID, host, port)
	if err != nil {
		t.Fatalf("SSH connect: %v", err)
	}

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := buildRequest(t, r.Method, r.URL.String(), user, map[string]string{
			"id": fmt.Sprintf("%d", inst.ID),
		})
		for k, v := range r.Header {
			req.Header[k] = v
		}
		TerminalWSProxy(w, req)
	}))
	t.Cleanup(proxyServer.Close)

	return proxyServer
}

func TestTerminalWSProxy_ConnectsAndReceivesOutput(t *testing.T) {
	proxyServer := setupTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	// The SSH server sends "PTY:true\n" on session start; read until we get it
	var accumulated string
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for PTY:true, got: %q", accumulated)
		default:
		}

		readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
		_, data, err := conn.Read(readCtx)
		readCancel()
		if err != nil {
			t.Fatalf("read error: %v, accumulated: %q", err, accumulated)
		}
		accumulated += string(data)
		if strings.Contains(accumulated, "PTY:true") {
			break
		}
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_InputOutputRelay(t *testing.T) {
	proxyServer := setupTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	// Consume initial PTY:true output
	readUntilWS(t, conn, ctx, "PTY:true", 3*time.Second)

	// Send binary input data
	testInput := "hello terminal"
	if err := conn.Write(ctx, websocket.MessageBinary, []byte(testInput)); err != nil {
		t.Fatalf("write input: %v", err)
	}

	// Read back echoed output
	readUntilWS(t, conn, ctx, "echo:"+testInput, 3*time.Second)

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_ResizeMessage(t *testing.T) {
	proxyServer := setupTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	// Consume initial PTY:true output
	readUntilWS(t, conn, ctx, "PTY:true", 3*time.Second)

	// Send resize control message as text JSON
	resizeMsg, _ := json.Marshal(termResizeMsg{
		Type: "resize",
		Cols: 120,
		Rows: 40,
	})
	if err := conn.Write(ctx, websocket.MessageText, resizeMsg); err != nil {
		t.Fatalf("write resize: %v", err)
	}

	// SSH server echoes resize confirmation
	readUntilWS(t, conn, ctx, "resize:120x40", 3*time.Second)

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_InvalidID(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t, "admin")

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := buildRequest(t, r.Method, r.URL.String(), user, map[string]string{
			"id": "notanumber",
		})
		for k, v := range r.Header {
			req.Header[k] = v
		}
		TerminalWSProxy(w, req)
	}))
	defer proxyServer.Close()

	// Should get HTTP error before WebSocket upgrade
	resp, err := http.Get(proxyServer.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", resp.StatusCode)
	}
}

func TestTerminalWSProxy_NoSSHManager(t *testing.T) {
	setupTestDB(t)

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	mock := &mockOrchestrator{sshHost: "127.0.0.1", sshPort: 22}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	SSHMgr = nil

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := buildRequest(t, r.Method, r.URL.String(), user, map[string]string{
			"id": fmt.Sprintf("%d", inst.ID),
		})
		for k, v := range r.Header {
			req.Header[k] = v
		}
		TerminalWSProxy(w, req)
	}))
	defer proxyServer.Close()

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		// Expected: handler closes the WebSocket before we can connect or right after
		return
	}
	defer conn.CloseNow()

	// If we connected, the handler should close us with error code
	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected error reading from terminal with no SSH manager")
	}
}

func TestTerminalWSProxy_NoOrchestrator(t *testing.T) {
	setupTestDB(t)

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	orchestrator.Set(nil)
	SSHMgr = sshproxy.NewSSHManager(nil, "")
	defer func() { SSHMgr = nil }()

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := buildRequest(t, r.Method, r.URL.String(), user, map[string]string{
			"id": fmt.Sprintf("%d", inst.ID),
		})
		for k, v := range r.Header {
			req.Header[k] = v
		}
		TerminalWSProxy(w, req)
	}))
	defer proxyServer.Close()

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected error reading from terminal with no orchestrator")
	}
}

func TestTerminalWSProxy_MultipleResizes(t *testing.T) {
	proxyServer := setupTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	readUntilWS(t, conn, ctx, "PTY:true", 3*time.Second)

	resizes := []struct{ cols, rows uint16 }{
		{80, 24},
		{120, 40},
		{200, 50},
	}

	for _, r := range resizes {
		msg, _ := json.Marshal(termResizeMsg{Type: "resize", Cols: r.cols, Rows: r.rows})
		if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
			t.Fatalf("write resize %dx%d: %v", r.cols, r.rows, err)
		}
	}

	// Verify last resize arrives (all should be processed)
	readUntilWS(t, conn, ctx, "resize:200x50", 3*time.Second)

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_Forbidden(t *testing.T) {
	setupTestDB(t)

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "user") // non-admin, not assigned

	orchestrator.Set(&mockOrchestrator{})
	defer orchestrator.Set(nil)
	SSHMgr = sshproxy.NewSSHManager(nil, "")
	defer func() { SSHMgr = nil }()

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := buildRequest(t, r.Method, r.URL.String(), user, map[string]string{
			"id": fmt.Sprintf("%d", inst.ID),
		})
		for k, v := range r.Header {
			req.Header[k] = v
		}
		TerminalWSProxy(w, req)
	}))
	defer proxyServer.Close()

	resp, err := http.Get(proxyServer.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", resp.StatusCode)
	}
}

// readUntilWS reads binary WebSocket messages until the accumulated data contains target.
func readUntilWS(t *testing.T, conn *websocket.Conn, ctx context.Context, target string, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	var accumulated string
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for %q, got: %q", target, accumulated)
		default:
		}

		readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
		_, data, err := conn.Read(readCtx)
		readCancel()
		if err != nil {
			t.Fatalf("read error waiting for %q: %v, accumulated: %q", target, err, accumulated)
		}
		accumulated += string(data)
		if strings.Contains(accumulated, target) {
			return accumulated
		}
	}
}

func TestTerminalWSProxy_ANSIEscapeCodesPreserved(t *testing.T) {
	proxyServer := setupTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	readUntilWS(t, conn, ctx, "PTY:true", 3*time.Second)

	// Send ANSI color codes and escape sequences as binary data
	ansiInput := "\x1b[31mRedText\x1b[0m"
	if err := conn.Write(ctx, websocket.MessageBinary, []byte(ansiInput)); err != nil {
		t.Fatalf("write ANSI input: %v", err)
	}

	// The echo server returns "echo:" + input, so ANSI codes must be intact
	output := readUntilWS(t, conn, ctx, "echo:\x1b[31mRedText\x1b[0m", 3*time.Second)

	// Verify the escape bytes survived the WebSocket round-trip
	if !strings.Contains(output, "\x1b[31m") {
		t.Error("ANSI color start sequence was corrupted")
	}
	if !strings.Contains(output, "\x1b[0m") {
		t.Error("ANSI reset sequence was corrupted")
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_SpecialKeys(t *testing.T) {
	proxyServer := setupTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	readUntilWS(t, conn, ctx, "PTY:true", 3*time.Second)

	// Test Ctrl+C (ETX byte 0x03)
	if err := conn.Write(ctx, websocket.MessageBinary, []byte{0x03}); err != nil {
		t.Fatalf("write Ctrl+C: %v", err)
	}
	readUntilWS(t, conn, ctx, "echo:\x03", 3*time.Second)

	// Test arrow keys (ESC [ A/B/C/D)
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("\x1b[A")); err != nil {
		t.Fatalf("write ArrowUp: %v", err)
	}
	readUntilWS(t, conn, ctx, "echo:\x1b[A", 3*time.Second)

	// Test Tab (0x09)
	if err := conn.Write(ctx, websocket.MessageBinary, []byte{0x09}); err != nil {
		t.Fatalf("write Tab: %v", err)
	}
	readUntilWS(t, conn, ctx, "echo:\x09", 3*time.Second)

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_RapidInput(t *testing.T) {
	proxyServer := setupTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1024 * 1024)

	readUntilWS(t, conn, ctx, "PTY:true", 3*time.Second)

	// Send 50 rapid messages without waiting for responses
	const messageCount = 50
	for i := 0; i < messageCount; i++ {
		msg := fmt.Sprintf("r%d_", i)
		if err := conn.Write(ctx, websocket.MessageBinary, []byte(msg)); err != nil {
			t.Fatalf("write rapid message %d: %v", i, err)
		}
	}

	// Send a unique end marker to verify all prior data was relayed
	marker := "RAPID_DONE"
	if err := conn.Write(ctx, websocket.MessageBinary, []byte(marker)); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	// Look for the marker without "echo:" prefix since rapid writes coalesce
	readUntilWS(t, conn, ctx, marker, 5*time.Second)

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_LongRunningStreaming(t *testing.T) {
	proxyServer := setupTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1024 * 1024)

	readUntilWS(t, conn, ctx, "PTY:true", 3*time.Second)

	// Simulate a long-running command by sending large data and verifying streaming
	largePayload := strings.Repeat("A", 4096) + "STREAM_END"
	if err := conn.Write(ctx, websocket.MessageBinary, []byte(largePayload)); err != nil {
		t.Fatalf("write large payload: %v", err)
	}

	// Verify the end marker arrives (all data was streamed through)
	readUntilWS(t, conn, ctx, "STREAM_END", 5*time.Second)

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_DisconnectAndReconnect(t *testing.T) {
	proxyServer := setupTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)

	// First connection
	ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel1()

	conn1, _, err := websocket.Dial(ctx1, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy (1st): %v", err)
	}

	readUntilWS(t, conn1, ctx1, "PTY:true", 3*time.Second)

	// Send data to verify this session works
	if err := conn1.Write(ctx1, websocket.MessageBinary, []byte("session1")); err != nil {
		t.Fatalf("write session1: %v", err)
	}
	readUntilWS(t, conn1, ctx1, "echo:session1", 3*time.Second)

	// Disconnect
	conn1.Close(websocket.StatusNormalClosure, "")

	// Small delay to ensure server cleans up
	time.Sleep(100 * time.Millisecond)

	// Second connection — should get a new session
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	conn2, _, err := websocket.Dial(ctx2, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy (2nd): %v", err)
	}
	defer conn2.CloseNow()

	// New session should send fresh PTY:true (proving it's a new session)
	readUntilWS(t, conn2, ctx2, "PTY:true", 3*time.Second)

	// Verify the new session is functional
	if err := conn2.Write(ctx2, websocket.MessageBinary, []byte("session2")); err != nil {
		t.Fatalf("write session2: %v", err)
	}
	readUntilWS(t, conn2, ctx2, "echo:session2", 3*time.Second)

	conn2.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_InteractiveREPL(t *testing.T) {
	proxyServer := setupTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	readUntilWS(t, conn, ctx, "PTY:true", 3*time.Second)

	// Simulate interactive REPL-style input/output (command, response, command, response)
	exchanges := []string{
		"print('hello')\n",
		"x = 42\n",
		"print(x)\n",
		"exit()\n",
	}

	for _, cmd := range exchanges {
		if err := conn.Write(ctx, websocket.MessageBinary, []byte(cmd)); err != nil {
			t.Fatalf("write REPL command %q: %v", cmd, err)
		}
		// Verify the command is echoed back (proving it passed through the terminal)
		readUntilWS(t, conn, ctx, "echo:"+cmd, 3*time.Second)
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalWSProxy_InvalidResizeIgnored(t *testing.T) {
	proxyServer := setupTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	readUntilWS(t, conn, ctx, "PTY:true", 3*time.Second)

	// Send resize with zero dimensions (should be ignored per handler code)
	zeroResize, _ := json.Marshal(termResizeMsg{Type: "resize", Cols: 0, Rows: 0})
	if err := conn.Write(ctx, websocket.MessageText, zeroResize); err != nil {
		t.Fatalf("write zero resize: %v", err)
	}

	// Send invalid JSON text message (should be silently ignored)
	if err := conn.Write(ctx, websocket.MessageText, []byte("not json")); err != nil {
		t.Fatalf("write invalid json: %v", err)
	}

	// Send unknown message type (should be ignored)
	unknownMsg, _ := json.Marshal(map[string]interface{}{"type": "unknown"})
	if err := conn.Write(ctx, websocket.MessageText, unknownMsg); err != nil {
		t.Fatalf("write unknown type: %v", err)
	}

	// Verify session is still functional after invalid messages
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("still_alive")); err != nil {
		t.Fatalf("write after invalid: %v", err)
	}
	readUntilWS(t, conn, ctx, "echo:still_alive", 3*time.Second)

	conn.Close(websocket.StatusNormalClosure, "")
}

// --- Managed Session Tests ---
// These tests verify the SessionManager-powered terminal features:
// session persistence, reconnection, multiple concurrent sessions, history replay,
// session listing, and session closing.

// setupManagedTerminalTest is like setupTerminalTest but also configures a
// SessionManager for session persistence and multi-session support.
func setupManagedTerminalTest(t *testing.T) (*httptest.Server, *database.Instance) {
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

	addr, cleanup := testTerminalSSHServer(t, signer.PublicKey())
	t.Cleanup(cleanup)

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	t.Cleanup(func() { mgr.CloseAll() })
	SSHMgr = mgr
	t.Cleanup(func() { SSHMgr = nil })

	sm := sshterminal.NewSessionManager(sshterminal.SessionManagerConfig{
		HistoryLines: 500,
		IdleTimeout:  5 * time.Minute,
	})
	TermSessionMgr = sm
	t.Cleanup(func() {
		sm.Stop()
		TermSessionMgr = nil
	})

	mock := &mockOrchestrator{sshHost: host, sshPort: port}
	orchestrator.Set(mock)
	t.Cleanup(func() { orchestrator.Set(nil) })

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	_, err = mgr.Connect(context.Background(), inst.ID, host, port)
	if err != nil {
		t.Fatalf("SSH connect: %v", err)
	}

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := buildRequest(t, r.Method, r.URL.String(), user, map[string]string{
			"id":        fmt.Sprintf("%d", inst.ID),
			"sessionId": extractSessionID(r.URL.Path),
		})
		for k, v := range r.Header {
			req.Header[k] = v
		}
		// Route based on path
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/sessions") && r.Method == "GET":
			ListTerminalSessions(w, req)
		case strings.Contains(path, "/sessions/") && r.Method == "DELETE":
			CloseTerminalSession(w, req)
		default:
			TerminalWSProxy(w, req)
		}
	}))
	t.Cleanup(proxyServer.Close)

	return proxyServer, &inst
}

// extractSessionID extracts the session ID from paths like /sessions/{sessionId}
func extractSessionID(path string) string {
	parts := strings.Split(path, "/sessions/")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

// readSessionInfoWS reads messages until we find the session_info text message
// and returns the session ID.
func readSessionInfoWS(t *testing.T, conn *websocket.Conn, ctx context.Context) string {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for session_info message")
		default:
		}

		readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
		msgType, data, err := conn.Read(readCtx)
		readCancel()
		if err != nil {
			t.Fatalf("read error waiting for session_info: %v", err)
		}

		if msgType == websocket.MessageText {
			var info map[string]string
			if err := json.Unmarshal(data, &info); err == nil {
				if info["type"] == "session_info" && info["session_id"] != "" {
					return info["session_id"]
				}
			}
		}
	}
}

func TestManagedTerminal_SessionInfoSent(t *testing.T) {
	proxyServer, _ := setupManagedTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	// First message should be session_info with a session ID
	sessionID := readSessionInfoWS(t, conn, ctx)
	if sessionID == "" {
		t.Fatal("session_info should include a non-empty session_id")
	}

	// After session_info, we should get PTY:true
	readUntilWS(t, conn, ctx, "PTY:true", 3*time.Second)

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestManagedTerminal_InputOutputRelay(t *testing.T) {
	proxyServer, _ := setupManagedTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	readSessionInfoWS(t, conn, ctx)
	readUntilWS(t, conn, ctx, "PTY:true", 3*time.Second)

	testInput := "managed_hello"
	if err := conn.Write(ctx, websocket.MessageBinary, []byte(testInput)); err != nil {
		t.Fatalf("write input: %v", err)
	}

	readUntilWS(t, conn, ctx, "echo:"+testInput, 3*time.Second)

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestManagedTerminal_SessionReconnect(t *testing.T) {
	proxyServer, _ := setupManagedTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)

	// First connection: create a session and send some data
	ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel1()

	conn1, _, err := websocket.Dial(ctx1, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy (1st): %v", err)
	}

	sessionID := readSessionInfoWS(t, conn1, ctx1)
	readUntilWS(t, conn1, ctx1, "PTY:true", 3*time.Second)

	// Send some data that will be in the history
	if err := conn1.Write(ctx1, websocket.MessageBinary, []byte("persist_this")); err != nil {
		t.Fatalf("write persist_this: %v", err)
	}
	readUntilWS(t, conn1, ctx1, "echo:persist_this", 3*time.Second)

	// Disconnect (session persists in manager)
	conn1.Close(websocket.StatusNormalClosure, "")
	time.Sleep(200 * time.Millisecond)

	// Second connection: reconnect with session_id
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	reconnectURL := wsURL + "?session_id=" + sessionID
	conn2, _, err := websocket.Dial(ctx2, reconnectURL, nil)
	if err != nil {
		t.Fatalf("dial proxy (reconnect): %v", err)
	}
	defer conn2.CloseNow()

	// Should get session_info with the same session ID
	reconnectedID := readSessionInfoWS(t, conn2, ctx2)
	if reconnectedID != sessionID {
		t.Errorf("reconnected session ID = %q, want %q", reconnectedID, sessionID)
	}

	// Should get history replay containing previous data
	output := readUntilWS(t, conn2, ctx2, "persist_this", 3*time.Second)
	if !strings.Contains(output, "PTY:true") {
		t.Error("history replay missing PTY:true")
	}

	// Session should still be functional after reconnect
	if err := conn2.Write(ctx2, websocket.MessageBinary, []byte("after_reconnect")); err != nil {
		t.Fatalf("write after_reconnect: %v", err)
	}
	readUntilWS(t, conn2, ctx2, "echo:after_reconnect", 3*time.Second)

	conn2.Close(websocket.StatusNormalClosure, "")
}

func TestManagedTerminal_MultipleConcurrentSessions(t *testing.T) {
	proxyServer, _ := setupManagedTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)

	// Create two concurrent WebSocket connections (each gets its own session)
	ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel1()
	conn1, _, err := websocket.Dial(ctx1, wsURL, nil)
	if err != nil {
		t.Fatalf("dial conn1: %v", err)
	}
	defer conn1.CloseNow()

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	conn2, _, err := websocket.Dial(ctx2, wsURL, nil)
	if err != nil {
		t.Fatalf("dial conn2: %v", err)
	}
	defer conn2.CloseNow()

	// Both should get different session IDs
	sid1 := readSessionInfoWS(t, conn1, ctx1)
	sid2 := readSessionInfoWS(t, conn2, ctx2)

	if sid1 == sid2 {
		t.Error("concurrent sessions should have different IDs")
	}

	// Both should be functional independently
	readUntilWS(t, conn1, ctx1, "PTY:true", 3*time.Second)
	readUntilWS(t, conn2, ctx2, "PTY:true", 3*time.Second)

	conn1.Write(ctx1, websocket.MessageBinary, []byte("conn1_data"))
	conn2.Write(ctx2, websocket.MessageBinary, []byte("conn2_data"))

	readUntilWS(t, conn1, ctx1, "echo:conn1_data", 3*time.Second)
	readUntilWS(t, conn2, ctx2, "echo:conn2_data", 3*time.Second)

	conn1.Close(websocket.StatusNormalClosure, "")
	conn2.Close(websocket.StatusNormalClosure, "")
}

func TestManagedTerminal_ListSessions(t *testing.T) {
	proxyServer, _ := setupManagedTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)

	// Create a session
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	sessionID := readSessionInfoWS(t, conn, ctx)
	readUntilWS(t, conn, ctx, "PTY:true", 3*time.Second)

	// List sessions via REST API
	resp, err := http.Get(proxyServer.URL + "/sessions")
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list sessions status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		Sessions []struct {
			ID        string `json:"id"`
			Shell     string `json:"shell"`
			Attached  bool   `json:"attached"`
			CreatedAt string `json:"created_at"`
		} `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(body.Sessions) != 1 {
		t.Fatalf("session count = %d, want 1", len(body.Sessions))
	}

	if body.Sessions[0].ID != sessionID {
		t.Errorf("session ID = %q, want %q", body.Sessions[0].ID, sessionID)
	}
	if !body.Sessions[0].Attached {
		t.Error("session should be attached while WebSocket is open")
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestManagedTerminal_CloseSession(t *testing.T) {
	proxyServer, _ := setupManagedTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)

	// Create a session
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}

	sessionID := readSessionInfoWS(t, conn, ctx)
	readUntilWS(t, conn, ctx, "PTY:true", 3*time.Second)

	// Disconnect so we can close the detached session
	conn.Close(websocket.StatusNormalClosure, "")
	time.Sleep(200 * time.Millisecond)

	// Close the session via REST API
	req, _ := http.NewRequest("DELETE", proxyServer.URL+"/sessions/"+sessionID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("close session: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close session status = %d, want 200", resp.StatusCode)
	}

	// Verify session no longer listed
	listResp, err := http.Get(proxyServer.URL + "/sessions")
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	defer listResp.Body.Close()

	var body struct {
		Sessions []struct{ ID string } `json:"sessions"`
	}
	json.NewDecoder(listResp.Body).Decode(&body)

	if len(body.Sessions) != 0 {
		t.Errorf("sessions after close = %d, want 0", len(body.Sessions))
	}
}

func TestManagedTerminal_SessionAlreadyAttached(t *testing.T) {
	proxyServer, _ := setupManagedTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)

	// Create a session
	ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel1()

	conn1, _, err := websocket.Dial(ctx1, wsURL, nil)
	if err != nil {
		t.Fatalf("dial conn1: %v", err)
	}
	defer conn1.CloseNow()

	sessionID := readSessionInfoWS(t, conn1, ctx1)
	readUntilWS(t, conn1, ctx1, "PTY:true", 3*time.Second)

	// Try to reconnect to the same session while it's still attached
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()

	reconnectURL := wsURL + "?session_id=" + sessionID
	conn2, _, err := websocket.Dial(ctx2, reconnectURL, nil)
	if err != nil {
		// Expected: server rejects because session is already attached
		return
	}
	defer conn2.CloseNow()

	// If we connected, the handler should close us with error code 4409
	_, _, err = conn2.Read(ctx2)
	if err == nil {
		t.Fatal("expected error reading from already-attached session")
	}

	conn1.Close(websocket.StatusNormalClosure, "")
}

func TestManagedTerminal_ResizeAfterReconnect(t *testing.T) {
	proxyServer, _ := setupManagedTerminalTest(t)

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)

	// Create session
	ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel1()

	conn1, _, err := websocket.Dial(ctx1, wsURL, nil)
	if err != nil {
		t.Fatalf("dial conn1: %v", err)
	}

	sessionID := readSessionInfoWS(t, conn1, ctx1)
	readUntilWS(t, conn1, ctx1, "PTY:true", 3*time.Second)

	// Disconnect
	conn1.Close(websocket.StatusNormalClosure, "")
	time.Sleep(200 * time.Millisecond)

	// Reconnect
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	reconnectURL := wsURL + "?session_id=" + sessionID
	conn2, _, err := websocket.Dial(ctx2, reconnectURL, nil)
	if err != nil {
		t.Fatalf("dial reconnect: %v", err)
	}
	defer conn2.CloseNow()

	readSessionInfoWS(t, conn2, ctx2)

	// Drain history replay (contains PTY:true from before)
	readUntilWS(t, conn2, ctx2, "PTY:true", 3*time.Second)

	// Resize should work after reconnect
	resizeMsg, _ := json.Marshal(termResizeMsg{Type: "resize", Cols: 100, Rows: 30})
	if err := conn2.Write(ctx2, websocket.MessageText, resizeMsg); err != nil {
		t.Fatalf("write resize: %v", err)
	}

	readUntilWS(t, conn2, ctx2, "resize:100x30", 3*time.Second)

	conn2.Close(websocket.StatusNormalClosure, "")
}
