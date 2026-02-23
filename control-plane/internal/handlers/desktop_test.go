package handlers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

// --- DesktopProxy HTTP tests ---

func TestDesktopProxy_InvalidID(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/instances/notanumber/desktop/", user, map[string]string{"id": "notanumber", "*": ""})
	w := httptest.NewRecorder()

	DesktopProxy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestDesktopProxy_Forbidden(t *testing.T) {
	setupTestDB(t)

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "user") // non-admin, not assigned

	req := buildRequest(t, "GET", "/api/v1/instances/1/desktop/", user, map[string]string{
		"id": fmt.Sprintf("%d", inst.ID),
		"*":  "",
	})
	w := httptest.NewRecorder()

	DesktopProxy(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}
}

func TestDesktopProxy_NoTunnelManager(t *testing.T) {
	setupTestDB(t)

	TunnelMgr = nil

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/desktop/", user, map[string]string{
		"id": fmt.Sprintf("%d", inst.ID),
		"*":  "",
	})
	w := httptest.NewRecorder()

	DesktopProxy(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d", w.Code)
	}
}

func TestDesktopProxy_NoActiveTunnel(t *testing.T) {
	setupTestDB(t)

	mgr := sshproxy.NewSSHManager(nil, "")
	TunnelMgr = sshproxy.NewTunnelManager(mgr)
	defer func() { TunnelMgr = nil }()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/desktop/", user, map[string]string{
		"id": fmt.Sprintf("%d", inst.ID),
		"*":  "",
	})
	w := httptest.NewRecorder()

	DesktopProxy(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d", w.Code)
	}
}

func TestDesktopProxy_HTTPProxy(t *testing.T) {
	setupTestDB(t)

	// Start a backend HTTP server simulating the Selkies streaming UI
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `<html><body>VNC UI path=%s query=%s</body></html>`, r.URL.Path, r.URL.RawQuery)
	}))
	defer backend.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	var backendPort int
	fmt.Sscanf(portStr, "%d", &backendPort)

	// Set up SSH infrastructure and create a VNC tunnel
	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, cleanup := testSSHServer(t, signer.PublicKey())
	defer cleanup()

	host, sshPortStr, _ := net.SplitHostPort(addr)
	var sshPort int
	fmt.Sscanf(sshPortStr, "%d", &sshPort)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	defer mgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")

	_, err = mgr.Connect(context.Background(), inst.ID, host, sshPort)
	if err != nil {
		t.Fatalf("SSH connect: %v", err)
	}

	tm := sshproxy.NewTunnelManager(mgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	// Create a VNC tunnel pointing to our backend's port (use CreateReverseTunnel
	// directly so we can specify the test backend port instead of the default 3000)
	vncPort, err := tm.CreateReverseTunnel(context.Background(), inst.ID, "VNC", backendPort, 0)
	if err != nil {
		t.Fatalf("create VNC tunnel: %v", err)
	}

	if vncPort == 0 {
		t.Fatal("expected non-zero VNC tunnel port")
	}

	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/desktop/some/path?key=val", user, map[string]string{
		"id": fmt.Sprintf("%d", inst.ID),
		"*":  "some/path",
	})
	req.URL.RawQuery = "key=val"
	w := httptest.NewRecorder()

	DesktopProxy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	if ct := w.Header().Get("Content-Type"); ct != "text/html" {
		t.Errorf("expected Content-Type text/html, got %s", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "VNC UI") {
		t.Errorf("expected VNC UI content in body, got: %s", body)
	}
}

// --- DesktopProxy WebSocket tests ---

func TestDesktopProxy_WebSocketProxy(t *testing.T) {
	setupTestDB(t)

	// Start a WebSocket echo server simulating the Selkies VNC stream
	echoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer c.CloseNow()

		ctx := r.Context()
		for {
			msgType, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if err := c.Write(ctx, msgType, append([]byte("vnc:"), data...)); err != nil {
				return
			}
		}
	}))
	defer echoServer.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(echoServer.URL, "http://"))
	var backendPort int
	fmt.Sscanf(portStr, "%d", &backendPort)

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, cleanup := testSSHServer(t, signer.PublicKey())
	defer cleanup()

	host, sshPortStr, _ := net.SplitHostPort(addr)
	var sshPort int
	fmt.Sscanf(sshPortStr, "%d", &sshPort)

	sshMgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	defer sshMgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")

	_, err = sshMgr.Connect(context.Background(), inst.ID, host, sshPort)
	if err != nil {
		t.Fatalf("SSH connect: %v", err)
	}

	tm := sshproxy.NewTunnelManager(sshMgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	_, err = tm.CreateReverseTunnel(context.Background(), inst.ID, "VNC", backendPort, 0)
	if err != nil {
		t.Fatalf("create VNC tunnel: %v", err)
	}

	user := createTestUser(t, "admin")

	// Create a proxy server that wraps DesktopProxy with proper chi routing
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := buildRequest(t, r.Method, r.URL.String(), user, map[string]string{
			"id": fmt.Sprintf("%d", inst.ID),
			"*":  "",
		})
		// Copy over the websocket upgrade headers
		for k, v := range r.Header {
			req.Header[k] = v
		}
		DesktopProxy(w, req)
	}))
	defer proxyServer.Close()

	// Connect as a WebSocket client to the proxy
	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	// Send a message
	err = conn.Write(ctx, websocket.MessageText, []byte("test-vnc"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read echo response
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if string(data) != "vnc:test-vnc" {
		t.Errorf("expected 'vnc:test-vnc', got '%s'", string(data))
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestDesktopProxy_WebSocketBinaryFrames(t *testing.T) {
	setupTestDB(t)

	// VNC streams use binary WebSocket frames; verify they pass through correctly
	echoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer c.CloseNow()

		ctx := r.Context()
		msgType, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		c.Write(ctx, msgType, data)
	}))
	defer echoServer.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(echoServer.URL, "http://"))
	var backendPort int
	fmt.Sscanf(portStr, "%d", &backendPort)

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, cleanup := testSSHServer(t, signer.PublicKey())
	defer cleanup()

	host, sshPortStr, _ := net.SplitHostPort(addr)
	var sshPort int
	fmt.Sscanf(sshPortStr, "%d", &sshPort)

	sshMgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	defer sshMgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")

	_, err = sshMgr.Connect(context.Background(), inst.ID, host, sshPort)
	if err != nil {
		t.Fatalf("SSH connect: %v", err)
	}

	tm := sshproxy.NewTunnelManager(sshMgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	_, err = tm.CreateReverseTunnel(context.Background(), inst.ID, "VNC", backendPort, 0)
	if err != nil {
		t.Fatalf("create VNC tunnel: %v", err)
	}

	user := createTestUser(t, "admin")

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := buildRequest(t, r.Method, r.URL.String(), user, map[string]string{
			"id": fmt.Sprintf("%d", inst.ID),
			"*":  "",
		})
		for k, v := range r.Header {
			req.Header[k] = v
		}
		DesktopProxy(w, req)
	}))
	defer proxyServer.Close()

	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	// Send binary data (simulating VNC frame data)
	binaryData := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xAA, 0xBB}
	err = conn.Write(ctx, websocket.MessageBinary, binaryData)
	if err != nil {
		t.Fatalf("write binary: %v", err)
	}

	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if msgType != websocket.MessageBinary {
		t.Errorf("expected binary message type, got %v", msgType)
	}
	if string(data) != string(binaryData) {
		t.Errorf("binary data mismatch")
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestDesktopProxy_WebSocketNoTunnel(t *testing.T) {
	setupTestDB(t)

	TunnelMgr = nil

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := buildRequest(t, r.Method, r.URL.String(), user, map[string]string{
			"id": fmt.Sprintf("%d", inst.ID),
			"*":  "",
		})
		for k, v := range r.Header {
			req.Header[k] = v
		}
		DesktopProxy(w, req)
	}))
	defer proxyServer.Close()

	// When TunnelMgr is nil, the handler returns 502 before accepting the WebSocket.
	wsURL := strings.Replace(proxyServer.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		// Expected: the server returns a non-101 status
		return
	}
	defer conn.CloseNow()

	// If we somehow connected, the proxy should close us quickly
	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected error reading from proxy with no tunnel")
	}
}
