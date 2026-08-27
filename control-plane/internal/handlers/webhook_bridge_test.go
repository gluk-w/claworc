package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// fakeGatewayFunc runs a minimal OpenClaw gateway over WebSocket. It completes
// the DialGateway handshake, reads the chat.send frame, and then hands control
// to afterSend, which decides what events (if any) to stream back. The gateway's
// request context is passed through so afterSend can observe client disconnect.
func fakeGatewayFunc(t *testing.T, afterSend func(ctx context.Context, conn *websocket.Conn, params map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

		afterSend(ctx, conn, params)
	}))
}

// fakeGateway is the default gateway: it captures the chat.send params and
// immediately sends a lifecycle/end event so RunWebhookBridge returns.
func fakeGateway(t *testing.T) (srv *httptest.Server, paramsCh <-chan map[string]any) {
	t.Helper()
	ch := make(chan map[string]any, 1)
	srv = fakeGatewayFunc(t, func(ctx context.Context, conn *websocket.Conn, params map[string]any) {
		ch <- params
		conn.Write(ctx, websocket.MessageText, lifecycleEndEvent()) //nolint:errcheck
	})
	return srv, ch
}

// assistantEvent builds an OpenClaw assistant event carrying a cumulative
// snapshot in data.text.
func assistantEvent(text string) []byte {
	b, _ := json.Marshal(map[string]any{
		"type": "event",
		"payload": map[string]any{
			"stream": "assistant",
			"data":   map[string]any{"text": text},
		},
	})
	return b
}

// lifecycleEndEvent builds the lifecycle/end event that signals completion.
func lifecycleEndEvent() []byte {
	b, _ := json.Marshal(map[string]any{
		"type": "event",
		"payload": map[string]any{
			"stream": "lifecycle",
			"data":   map[string]any{"phase": "end"},
		},
	})
	return b
}

// gatewayPort extracts the listening port from a fake gateway test server.
func gatewayPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	addr := srv.Listener.Addr().String()
	portStr := addr[strings.LastIndex(addr, ":")+1:]
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	return port
}

// pointTunnelTo overrides webhookGetTunnelPort to resolve to srv's port for the
// duration of the test.
func pointTunnelTo(t *testing.T, srv *httptest.Server) {
	t.Helper()
	port := gatewayPort(t, srv)
	orig := webhookGetTunnelPort
	webhookGetTunnelPort = func(_ uint, _ string) (int, error) { return port, nil }
	t.Cleanup(func() { webhookGetTunnelPort = orig })
}

// setBridgeIdleTimeout sets the webhook idle timeout for the test and restores
// the previous value on cleanup.
func setBridgeIdleTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	orig := config.Cfg.WebhookIdleTimeout
	config.Cfg.WebhookIdleTimeout = d
	t.Cleanup(func() { config.Cfg.WebhookIdleTimeout = orig })
}

// newBridgeInstance creates a running instance row for bridge tests.
func newBridgeInstance(t *testing.T, uuid string) database.Instance {
	t.Helper()
	if err := database.DB.AutoMigrate(&database.WebhookApiKey{}, &database.WebhookLog{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	inst := database.Instance{
		UUID:        uuid,
		Name:        "bot-" + uuid,
		DisplayName: uuid,
		Status:      "running",
	}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	return inst
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

// TestRunWebhookBridge_IdleTimeout: a gateway that sends one event then goes
// silent must trip the idle timeout rather than blocking forever.
func TestRunWebhookBridge_IdleTimeout(t *testing.T) {
	setupTestDB(t)
	inst := newBridgeInstance(t, "bridge-idle-test")
	setBridgeIdleTimeout(t, 200*time.Millisecond)

	srv := fakeGatewayFunc(t, func(ctx context.Context, conn *websocket.Conn, _ map[string]any) {
		// One event, then deliberate silence (never sends lifecycle/end).
		conn.Write(ctx, websocket.MessageText, assistantEvent("working...")) //nolint:errcheck
		<-ctx.Done()
	})
	defer srv.Close()
	pointTunnelTo(t, srv)

	_, err := RunWebhookBridge(context.Background(), inst.ID, "idle-task", "hello", nil)
	if err == nil {
		t.Fatalf("expected idle timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "openclaw idle timeout") {
		t.Fatalf("error = %q, want idle timeout", err.Error())
	}
}

// TestRunWebhookBridge_HeartbeatKeepsAlive: a gateway streaming events at an
// interval shorter than the idle window — but for far longer than that window
// in total — must NOT be cut off, proving the deadline re-arms per frame.
func TestRunWebhookBridge_HeartbeatKeepsAlive(t *testing.T) {
	setupTestDB(t)
	inst := newBridgeInstance(t, "bridge-heartbeat-test")
	setBridgeIdleTimeout(t, 200*time.Millisecond)

	srv := fakeGatewayFunc(t, func(ctx context.Context, conn *websocket.Conn, _ map[string]any) {
		// 12 events @ 50ms = ~600ms total, well past the 200ms idle window,
		// but each gap (50ms) stays under it. Last snapshot is the reply.
		for i := 0; i < 12; i++ {
			text := "chunk-" + strconv.Itoa(i)
			if err := conn.Write(ctx, websocket.MessageText, assistantEvent(text)); err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
		conn.Write(ctx, websocket.MessageText, lifecycleEndEvent()) //nolint:errcheck
	})
	defer srv.Close()
	pointTunnelTo(t, srv)

	reply, err := RunWebhookBridge(context.Background(), inst.ID, "hb-task", "hello", nil)
	if err != nil {
		t.Fatalf("RunWebhookBridge: %v", err)
	}
	if reply != "chunk-11" {
		t.Fatalf("reply = %q, want last snapshot %q", reply, "chunk-11")
	}
}

// TestRunWebhookBridge_ClientDisconnect: cancelling the request context mid-
// stream returns context.Canceled, not the idle-timeout error.
func TestRunWebhookBridge_ClientDisconnect(t *testing.T) {
	setupTestDB(t)
	inst := newBridgeInstance(t, "bridge-disconnect-test")
	setBridgeIdleTimeout(t, 5*time.Second) // generous, so idle never fires first

	started := make(chan struct{}, 1)
	srv := fakeGatewayFunc(t, func(ctx context.Context, conn *websocket.Conn, _ map[string]any) {
		conn.Write(ctx, websocket.MessageText, assistantEvent("working...")) //nolint:errcheck
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
	})
	defer srv.Close()
	pointTunnelTo(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		_, err := RunWebhookBridge(ctx, inst.ID, "disc-task", "hello", nil)
		resCh <- result{err}
	}()

	<-started
	cancel()

	select {
	case res := <-resCh:
		if !errors.Is(res.err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", res.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunWebhookBridge did not return after cancel")
	}
}
