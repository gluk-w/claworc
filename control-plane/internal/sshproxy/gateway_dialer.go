package sshproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// Range of OpenClaw gateway protocol versions this control plane can speak. The
// gateway accepts the handshake only when its own PROTOCOL_VERSION falls inside
// [GatewayMinProtocol, GatewayMaxProtocol], so advertising a range keeps us
// compatible with both older and current agent images.
const (
	GatewayMinProtocol = 3 // OpenClaw <= 2026.5.x
	GatewayMaxProtocol = 4 // OpenClaw 2026.6.x
)

// DialGateway opens a WebSocket connection to an OpenClaw gateway via the
// already-established local SSH tunnel listening on 127.0.0.1:localPort and
// completes the connect handshake. The returned conn is ready for chat.send /
// sessions.reset / chat.abort frames.
//
// This helper has no claworc-internal dependencies (database, utils,
// handlers, etc.) so it can be reused safely from both HTTP handlers and the
// moderator service. Callers are responsible for tunnel port lookup and
// gateway token decryption.
func DialGateway(ctx context.Context, localPort int, gatewayToken string) (*websocket.Conn, error) {
	gwURL := fmt.Sprintf("ws://127.0.0.1:%d/gateway", localPort)
	if gatewayToken != "" {
		gwURL += "?token=" + gatewayToken
	}
	gwOrigin := fmt.Sprintf("http://127.0.0.1:%d", localPort)

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(dialCtx, gwURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{gwOrigin}},
	})
	if err != nil {
		return nil, fmt.Errorf("dial gateway: %w", err)
	}
	conn.SetReadLimit(4 * 1024 * 1024)

	hsCtx, hsCancel := context.WithTimeout(ctx, 10*time.Second)
	defer hsCancel()

	// Phase 1: read connect.challenge
	if _, _, err := conn.Read(hsCtx); err != nil {
		conn.CloseNow()
		return nil, fmt.Errorf("read challenge: %w", err)
	}

	// Phase 2: send connect request
	connectFrame := map[string]any{
		"type":   "req",
		"id":     fmt.Sprintf("connect-%d", time.Now().UnixNano()),
		"method": "connect",
		"params": map[string]any{
			"minProtocol": GatewayMinProtocol,
			"maxProtocol": GatewayMaxProtocol,
			"client": map[string]any{
				"id":       "openclaw-control-ui",
				"version":  "1.0.0",
				"platform": "linux",
				"mode":     "webchat",
			},
			"role":   "operator",
			"scopes": []string{"operator.admin"},
			"auth":   map[string]any{"token": gatewayToken},
		},
	}
	connectJSON, _ := json.Marshal(connectFrame)
	if err := conn.Write(ctx, websocket.MessageText, connectJSON); err != nil {
		conn.CloseNow()
		return nil, fmt.Errorf("send connect: %w", err)
	}

	// Phase 3: wait for hello-ok response, skipping event frames
	for {
		_, data, err := conn.Read(hsCtx)
		if err != nil {
			conn.CloseNow()
			return nil, fmt.Errorf("handshake read: %w", err)
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		if resp["type"] == "event" {
			continue
		}
		if resp["type"] == "res" {
			if ok, _ := resp["ok"].(bool); !ok {
				conn.CloseNow()
				errObj, _ := resp["error"].(map[string]any)
				return nil, handshakeError(errObj)
			}
			return conn, nil
		}
	}
}

// handshakeError turns a rejected connect response into an error. A protocol
// mismatch is reported as a bare "protocol mismatch" by the gateway, which says
// nothing about which side is out of date, so we name both versions using the
// expectedProtocol the gateway reports in error.details.
func handshakeError(errObj map[string]any) error {
	msg := "gateway auth failed"
	if errObj != nil {
		if m, _ := errObj["message"].(string); m != "" {
			msg = m
		}
		if details, _ := errObj["details"].(map[string]any); details != nil {
			if expected, ok := details["expectedProtocol"].(float64); ok {
				return fmt.Errorf("gateway protocol mismatch: agent speaks protocol %d, claworc supports %d-%d",
					int(expected), GatewayMinProtocol, GatewayMaxProtocol)
			}
		}
	}
	return fmt.Errorf("%s", msg)
}
