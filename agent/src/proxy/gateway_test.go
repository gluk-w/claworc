package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func TestGatewayHandler_HTTP_RewritesHeaders(t *testing.T) {
	// Upstream fake: capture the request and return 200.
	var gotHost, gotRealIP, gotForwardedFor string
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotRealIP = r.Header.Get("X-Real-IP")
		gotForwardedFor = r.Header.Get("X-Forwarded-For")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("upstream-ok"))
	}))
	defer upstream.Close()

	// Strip the http:// prefix to get host:port.
	addr := strings.TrimPrefix(upstream.URL, "http://")

	handler := GatewayHandler(addr)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/gateway/some/path?q=1")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "upstream-ok" {
		t.Errorf("body = %q, want %q", body, "upstream-ok")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if gotHost != "localhost" {
		t.Errorf("upstream Host = %q, want %q", gotHost, "localhost")
	}
	if gotRealIP != "127.0.0.1" {
		t.Errorf("upstream X-Real-IP = %q, want %q", gotRealIP, "127.0.0.1")
	}
	if gotForwardedFor != "127.0.0.1" {
		t.Errorf("upstream X-Forwarded-For = %q, want %q", gotForwardedFor, "127.0.0.1")
	}
	if gotPath != "/gateway/some/path" {
		t.Errorf("upstream path = %q, want %q", gotPath, "/gateway/some/path")
	}
}

func TestGatewayHandler_HTTP_UpstreamDown(t *testing.T) {
	// Point at a closed port — proxy should return 502.
	handler := GatewayHandler("127.0.0.1:1")
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/gateway/")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
}

func TestGatewayHandler_WebSocket_Relay(t *testing.T) {
	// Upstream WebSocket echo server.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Logf("upstream accept error: %v", err)
			return
		}
		defer conn.CloseNow()

		// Verify Host was rewritten to localhost.
		if got := r.Header.Get("Host"); got != "" && got != "localhost" {
			t.Errorf("upstream ws Host = %q, want %q", got, "localhost")
		}

		ctx := r.Context()
		for {
			msgType, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if err := conn.Write(ctx, msgType, append([]byte("echo:"), data...)); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()

	addr := strings.TrimPrefix(upstream.URL, "http://")

	handler := GatewayHandler(addr)
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	wsURL := "ws" + strings.TrimPrefix(proxyServer.URL, "http") + "/websocket/test"
	ctx := t.Context()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxy ws: %v", err)
	}
	defer conn.CloseNow()

	// Send a message and verify echo.
	if err := conn.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}

	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Errorf("msgType = %v, want Text", msgType)
	}
	if string(data) != "echo:hello" {
		t.Errorf("data = %q, want %q", data, "echo:hello")
	}

	conn.Close(websocket.StatusNormalClosure, "")
}
