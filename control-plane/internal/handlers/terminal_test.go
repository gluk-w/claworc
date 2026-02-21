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
