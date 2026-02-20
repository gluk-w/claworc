package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/tunnel"
)

// newBufioReader is a test helper that wraps a net.Conn in a bufio.Reader.
func newBufioReader(conn net.Conn) *bufio.Reader {
	return bufio.NewReader(conn)
}

// TestOpenGatewayChannel_WritesCorrectHeader verifies that opening a gateway
// channel over the tunnel writes the "gateway\n" channel header and the
// agent-side mock receives requests on that stream.
func TestOpenGatewayChannel_WritesCorrectHeader(t *testing.T) {
	var receivedPath string
	gwHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		fmt.Fprint(w, "ok")
	})
	setupGatewayTunnel(t, 42, gwHandler)

	conn, err := openGatewayChannel(t.Context(), 42)
	if err != nil {
		t.Fatalf("openGatewayChannel: %v", err)
	}

	// Write a minimal HTTP request over the raw tunnel stream.
	req, _ := http.NewRequest("GET", "/gateway/test", nil)
	req.Host = "gateway"
	if err := req.Write(conn); err != nil {
		t.Fatalf("write request: %v", err)
	}

	// Read response — the mock handler should have served it.
	resp, err := http.ReadResponse(newBufioReader(conn), req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	conn.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if receivedPath != "/gateway/test" {
		t.Errorf("expected /gateway/test, got %s", receivedPath)
	}
}

// TestChatGatewayWebSocket_ViaTunnel verifies that a WebSocket connection
// to the gateway works through the tunnel stream transport, completing the
// OpenClaw handshake (challenge → connect → hello-ok) and bidirectional relay.
func TestChatGatewayWebSocket_ViaTunnel(t *testing.T) {
	// Mock gateway that performs the OpenClaw handshake protocol.
	gwHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "not a websocket", http.StatusBadRequest)
			return
		}

		gwConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer gwConn.CloseNow()

		ctx := r.Context()

		// Phase 1: Send challenge
		challenge, _ := json.Marshal(map[string]interface{}{
			"type": "event",
			"event": map[string]interface{}{
				"type": "connect.challenge",
			},
		})
		gwConn.Write(ctx, websocket.MessageText, challenge)

		// Phase 2: Read connect request
		_, connectData, err := gwConn.Read(ctx)
		if err != nil {
			return
		}

		var connectMsg map[string]interface{}
		json.Unmarshal(connectData, &connectMsg)

		// Phase 3: Send hello-ok response
		helloOk, _ := json.Marshal(map[string]interface{}{
			"type": "res",
			"id":   connectMsg["id"],
			"ok":   true,
		})
		gwConn.Write(ctx, websocket.MessageText, helloOk)

		// Echo back any messages received (for testing relay)
		for {
			msgType, data, err := gwConn.Read(ctx)
			if err != nil {
				return
			}
			if err := gwConn.Write(ctx, msgType, data); err != nil {
				return
			}
		}
	})
	setupGatewayTunnel(t, 1, gwHandler)

	// Open gateway channel and create tunnel-backed WebSocket connection.
	conn, err := openGatewayChannel(t.Context(), 1)
	if err != nil {
		t.Fatalf("openGatewayChannel: %v", err)
	}

	streamUsed := false
	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			if streamUsed {
				return nil, fmt.Errorf("tunnel stream already consumed")
			}
			streamUsed = true
			return conn, nil
		},
	}

	wsConn, _, err := websocket.Dial(t.Context(), "ws://gateway/gateway", &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		conn.Close()
		t.Fatalf("websocket dial: %v", err)
	}
	defer wsConn.CloseNow()

	wsConn.SetReadLimit(4 * 1024 * 1024)

	ctx := t.Context()

	// Phase 1: Read challenge
	_, challengeData, err := wsConn.Read(ctx)
	if err != nil {
		t.Fatalf("read challenge: %v", err)
	}
	var challenge map[string]interface{}
	json.Unmarshal(challengeData, &challenge)
	if challenge["type"] != "event" {
		t.Fatalf("expected event frame, got: %s", string(challengeData))
	}

	// Phase 2: Send connect request
	connectFrame, _ := json.Marshal(map[string]interface{}{
		"type":   "req",
		"id":     "test-connect-1",
		"method": "connect",
		"params": map[string]interface{}{
			"minProtocol": 3,
			"maxProtocol": 3,
			"auth":        map[string]interface{}{"token": "test"},
		},
	})
	if err := wsConn.Write(ctx, websocket.MessageText, connectFrame); err != nil {
		t.Fatalf("write connect: %v", err)
	}

	// Phase 3: Read hello-ok response
	_, helloData, err := wsConn.Read(ctx)
	if err != nil {
		t.Fatalf("read hello-ok: %v", err)
	}
	var helloResp map[string]interface{}
	json.Unmarshal(helloData, &helloResp)
	if helloResp["type"] != "res" {
		t.Fatalf("expected res frame, got: %s", string(helloData))
	}
	if ok, _ := helloResp["ok"].(bool); !ok {
		t.Fatalf("expected ok=true, got: %s", string(helloData))
	}

	// Phase 4: Test bidirectional relay — send a message and expect echo.
	testMsg, _ := json.Marshal(map[string]interface{}{
		"type":   "req",
		"id":     "chat-1",
		"method": "chat.send",
		"params": map[string]interface{}{
			"message": "hello from test",
		},
	})
	if err := wsConn.Write(ctx, websocket.MessageText, testMsg); err != nil {
		t.Fatalf("write test message: %v", err)
	}

	_, echoData, err := wsConn.Read(ctx)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(echoData) != string(testMsg) {
		t.Errorf("expected echo of test message, got: %s", string(echoData))
	}

	wsConn.Close(websocket.StatusNormalClosure, "")
}

// TestChatGatewayWebSocket_NoTunnel verifies the error path when no tunnel
// is connected for the requested instance.
func TestChatGatewayWebSocket_NoTunnel(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()

	tunnel.Manager = tunnel.NewTunnelManager()

	conn, err := openGatewayChannel(t.Context(), 999)
	if err == nil {
		conn.Close()
		t.Fatal("expected error for missing tunnel")
	}
	if !strings.Contains(err.Error(), "no tunnel connected") {
		t.Errorf("unexpected error: %v", err)
	}
}
