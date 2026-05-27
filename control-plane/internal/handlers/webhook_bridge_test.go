package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// fakeGateway runs a minimal OpenClaw gateway over WebSocket.
// It completes the DialGateway handshake, captures the first chat.send frame,
// sends a lifecycle/end event so RunWebhookBridge can return, then closes.
// The captured chat.send params map is written to paramsCh.
func fakeGateway(t *testing.T) (srv *httptest.Server, paramsCh <-chan map[string]any) {
	t.Helper()
	ch := make(chan map[string]any, 1)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Logf("ws accept: %v", err)
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()

		// Phase 1: send connect.challenge
		challenge, _ := json.Marshal(map[string]any{"type": "event", "payload": map[string]any{"stream": "connect.challenge"}})
		if err := conn.Write(ctx, websocket.MessageText, challenge); err != nil {
			t.Logf("write challenge: %v", err)
			return
		}

		// Phase 2: read connect frame (discard)
		if _, _, err := conn.Read(ctx); err != nil {
			t.Logf("read connect: %v", err)
			return
		}

		// Phase 3: send hello-ok
		helloOK, _ := json.Marshal(map[string]any{"type": "res", "ok": true})
		if err := conn.Write(ctx, websocket.MessageText, helloOK); err != nil {
			t.Logf("write hello-ok: %v", err)
			return
		}

		// Phase 4: read chat.send frame
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Logf("read chat.send: %v", err)
			return
		}
		var frame map[string]any
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Logf("unmarshal frame: %v", err)
			return
		}
		params, _ := frame["params"].(map[string]any)
		ch <- params

		// Phase 5: send lifecycle/end so RunWebhookBridge returns
		end, _ := json.Marshal(map[string]any{
			"type": "event",
			"payload": map[string]any{
				"stream": "lifecycle",
				"data":   map[string]any{"phase": "end"},
			},
		})
		conn.Write(ctx, websocket.MessageText, end) //nolint:errcheck
	}))
	return srv, ch
}

func TestRunWebhookBridge_SessionKeyHasPrefix(t *testing.T) {
	setupTestDB(t)
	if err := database.DB.AutoMigrate(&database.WebhookApiKey{}, &database.WebhookLog{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	inst := database.Instance{
		UUID:        "bridge-prefix-test",
		Name:        "bot-bridge-prefix-test",
		DisplayName: "Bridge Prefix Test",
		Status:      "running",
	}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	srv, paramsCh := fakeGateway(t)
	defer srv.Close()

	// Derive the port from the test server URL.
	addr := srv.Listener.Addr().String()
	portStr := addr[strings.LastIndex(addr, ":")+1:]
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}

	origGetTunnelPort := webhookGetTunnelPort
	webhookGetTunnelPort = func(_ uint, _ string) (int, error) { return port, nil }
	t.Cleanup(func() { webhookGetTunnelPort = origGetTunnelPort })

	_, bridgeErr := RunWebhookBridge(context.Background(), inst.ID, "my-task", "hello", nil)
	if bridgeErr != nil {
		t.Fatalf("RunWebhookBridge: %v", bridgeErr)
	}

	params := <-paramsCh
	sessionKey, _ := params["sessionKey"].(string)
	if sessionKey != "claworc-webhook-my-task" {
		t.Fatalf("sessionKey = %q, want %q", sessionKey, "claworc-webhook-my-task")
	}
	idempotencyKey, _ := params["idempotencyKey"].(string)
	if !strings.HasPrefix(idempotencyKey, "claworc-webhook-my-task-") {
		t.Fatalf("idempotencyKey = %q, want prefix %q", idempotencyKey, "claworc-webhook-my-task-")
	}
}
