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
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
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
