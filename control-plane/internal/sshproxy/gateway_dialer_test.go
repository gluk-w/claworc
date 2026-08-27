package sshproxy

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeGateway serves a minimal OpenClaw gateway handshake: it emits a
// connect.challenge event, captures the client's connect frame and replies with
// the caller-supplied response. It returns the local port to dial and a channel
// carrying the connect params the client advertised.
func fakeGateway(t *testing.T, response map[string]any) (int, <-chan map[string]any) {
	t.Helper()

	params := make(chan map[string]any, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.CloseNow()

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		challenge, _ := json.Marshal(map[string]any{
			"type":    "event",
			"event":   "connect.challenge",
			"payload": map[string]any{"nonce": "test-nonce"},
		})
		if err := conn.Write(ctx, websocket.MessageText, challenge); err != nil {
			return
		}

		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var frame map[string]any
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Errorf("connect frame is not JSON: %v", err)
			return
		}
		p, _ := frame["params"].(map[string]any)
		params <- p

		resp := map[string]any{"type": "res", "id": frame["id"]}
		for k, v := range response {
			resp[k] = v
		}
		respJSON, _ := json.Marshal(resp)
		conn.Write(ctx, websocket.MessageText, respJSON)

		// Keep the socket open long enough for the client to finish reading.
		<-ctx.Done()
	}))
	t.Cleanup(srv.Close)

	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("parse test server URL %q: %v", srv.URL, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse test server port %q: %v", portStr, err)
	}
	return port, params
}

func TestDialGatewayAdvertisesProtocolRange(t *testing.T) {
	port, params := fakeGateway(t, map[string]any{"ok": true})

	conn, err := DialGateway(context.Background(), port, "test-token")
	if err != nil {
		t.Fatalf("DialGateway: %v", err)
	}
	defer conn.CloseNow()

	p := <-params
	minProto, ok := p["minProtocol"].(float64)
	if !ok {
		t.Fatalf("minProtocol missing from connect params: %#v", p)
	}
	maxProto, ok := p["maxProtocol"].(float64)
	if !ok {
		t.Fatalf("maxProtocol missing from connect params: %#v", p)
	}
	if int(minProto) != GatewayMinProtocol || int(maxProto) != GatewayMaxProtocol {
		t.Fatalf("advertised protocol range = %d-%d, want %d-%d",
			int(minProto), int(maxProto), GatewayMinProtocol, GatewayMaxProtocol)
	}

	// Both gateway generations we support must accept the advertised range.
	// OpenClaw's rule is: PROTOCOL_VERSION must fall inside [min, max].
	for _, gatewayVersion := range []int{3, 4} {
		if gatewayVersion < int(minProto) || gatewayVersion > int(maxProto) {
			t.Errorf("gateway PROTOCOL_VERSION %d rejected by advertised range %d-%d",
				gatewayVersion, int(minProto), int(maxProto))
		}
	}
}

func TestDialGatewayHandshakeErrors(t *testing.T) {
	tests := []struct {
		name     string
		errObj   map[string]any
		wantSubs []string
	}{
		{
			name: "protocol mismatch names both versions",
			errObj: map[string]any{
				"code":    "INVALID_REQUEST",
				"message": "protocol mismatch",
				"details": map[string]any{
					"code":             "PROTOCOL_MISMATCH",
					"expectedProtocol": 5,
				},
			},
			wantSubs: []string{"protocol mismatch", "protocol 5", "3-4"},
		},
		{
			name: "rejection without details keeps gateway message",
			errObj: map[string]any{
				"code":    "INVALID_REQUEST",
				"message": "unauthorized: gateway token mismatch",
			},
			wantSubs: []string{"unauthorized: gateway token mismatch"},
		},
		{
			name:     "rejection without error object falls back",
			errObj:   nil,
			wantSubs: []string{"gateway auth failed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := map[string]any{"ok": false}
			if tt.errObj != nil {
				resp["error"] = tt.errObj
			}
			port, _ := fakeGateway(t, resp)

			conn, err := DialGateway(context.Background(), port, "test-token")
			if err == nil {
				conn.CloseNow()
				t.Fatal("DialGateway succeeded, want handshake error")
			}
			for _, want := range tt.wantSubs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}
