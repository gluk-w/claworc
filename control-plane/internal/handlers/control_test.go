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

// --- ControlProxy HTTP tests ---

func TestControlProxy_InvalidID(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/instances/notanumber/control/status", user, map[string]string{"id": "notanumber", "*": "status"})
	w := httptest.NewRecorder()

	ControlProxy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestControlProxy_Forbidden(t *testing.T) {
	setupTestDB(t)

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "user") // non-admin, not assigned

	req := buildRequest(t, "GET", "/api/v1/instances/1/control/status", user, map[string]string{
		"id": fmt.Sprintf("%d", inst.ID),
		"*":  "status",
	})
	w := httptest.NewRecorder()

	ControlProxy(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}
}

func TestControlProxy_NoTunnelManager(t *testing.T) {
	setupTestDB(t)

	TunnelMgr = nil

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/control/status", user, map[string]string{
		"id": fmt.Sprintf("%d", inst.ID),
		"*":  "status",
	})
	w := httptest.NewRecorder()

	ControlProxy(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html content type, got %s", ct)
	}
	if !strings.Contains(w.Body.String(), "Connecting to OpenClaw") {
		t.Fatalf("expected connecting page HTML, got: %s", w.Body.String())
	}
}

func TestControlProxy_NoActiveTunnel(t *testing.T) {
	setupTestDB(t)

	mgr := sshproxy.NewSSHManager(nil, "")
	TunnelMgr = sshproxy.NewTunnelManager(mgr)
	defer func() { TunnelMgr = nil }()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/control/status", user, map[string]string{
		"id": fmt.Sprintf("%d", inst.ID),
		"*":  "status",
	})
	w := httptest.NewRecorder()

	ControlProxy(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html content type, got %s", ct)
	}
	if !strings.Contains(w.Body.String(), "Connecting to OpenClaw") {
		t.Fatalf("expected connecting page HTML, got: %s", w.Body.String())
	}
}

func TestControlProxy_HTTPProxy(t *testing.T) {
	setupTestDB(t)

	// Start a backend HTTP server simulating the gateway service
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"path":"%s","query":"%s"}`, r.URL.Path, r.URL.RawQuery)
	}))
	defer backend.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	var backendPort int
	fmt.Sscanf(portStr, "%d", &backendPort)

	// Set up SSH infrastructure and create a gateway tunnel
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

	// Create a gateway tunnel pointing to our backend's port
	gwPort, err := tm.CreateTunnelForGateway(context.Background(), inst.ID, backendPort)
	if err != nil {
		t.Fatalf("create gateway tunnel: %v", err)
	}

	// Verify tunnel port was allocated
	if gwPort == 0 {
		t.Fatal("expected non-zero gateway tunnel port")
	}

	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/control/some/path?key=val", user, map[string]string{
		"id": fmt.Sprintf("%d", inst.ID),
		"*":  "some/path",
	})
	// Set query string on the request URL
	req.URL.RawQuery = "key=val"
	w := httptest.NewRecorder()

	ControlProxy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}
}

// --- ControlProxy WebSocket tests ---

func TestControlProxy_WebSocketProxy(t *testing.T) {
	setupTestDB(t)

	// Start a WebSocket echo server simulating the gateway service
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
			if err := c.Write(ctx, msgType, append([]byte("gw:"), data...)); err != nil {
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

	_, err = tm.CreateTunnelForGateway(context.Background(), inst.ID, backendPort)
	if err != nil {
		t.Fatalf("create gateway tunnel: %v", err)
	}

	user := createTestUser(t, "admin")

	// Create a proxy server that wraps ControlProxy with proper chi routing
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Inject chi URL params and user context
		req := buildRequest(t, r.Method, r.URL.String(), user, map[string]string{
			"id": fmt.Sprintf("%d", inst.ID),
			"*":  "",
		})
		// Copy over the websocket upgrade headers
		for k, v := range r.Header {
			req.Header[k] = v
		}
		// Use the original response writer to support WebSocket upgrade
		ControlProxy(w, req)
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
	err = conn.Write(ctx, websocket.MessageText, []byte("test-control"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read echo response
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if string(data) != "gw:test-control" {
		t.Errorf("expected 'gw:test-control', got '%s'", string(data))
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestControlProxy_WebSocketNoTunnel(t *testing.T) {
	setupTestDB(t)

	TunnelMgr = nil

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	// Create a proxy server
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := buildRequest(t, r.Method, r.URL.String(), user, map[string]string{
			"id": fmt.Sprintf("%d", inst.ID),
			"*":  "",
		})
		for k, v := range r.Header {
			req.Header[k] = v
		}
		ControlProxy(w, req)
	}))
	defer proxyServer.Close()

	// When TunnelMgr is nil, the handler returns 502 before accepting the WebSocket.
	// The WebSocket dial should fail.
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
