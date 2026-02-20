package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/agent/config"
)

func TestHealthEndpoint(t *testing.T) {
	cfg := config.Config{
		ListenAddr:  ":0",
		GatewayAddr: "127.0.0.1:1", // unused for health check
	}
	srv := New(cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var data map[string]string
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if data["status"] != "ok" {
		t.Errorf("status = %q, want %q", data["status"], "ok")
	}
	if data["service"] != "claworc-agent-proxy" {
		t.Errorf("service = %q, want %q", data["service"], "claworc-agent-proxy")
	}
}

func TestServerTimeouts(t *testing.T) {
	cfg := config.Config{
		ListenAddr:  ":9999",
		GatewayAddr: "127.0.0.1:1",
	}
	srv := New(cfg, nil)

	if srv.Addr != ":9999" {
		t.Errorf("Addr = %q, want %q", srv.Addr, ":9999")
	}
	if srv.ReadTimeout.Seconds() != 30 {
		t.Errorf("ReadTimeout = %v, want 30s", srv.ReadTimeout)
	}
	if srv.WriteTimeout.Seconds() != 30 {
		t.Errorf("WriteTimeout = %v, want 30s", srv.WriteTimeout)
	}
	if srv.IdleTimeout.Seconds() != 120 {
		t.Errorf("IdleTimeout = %v, want 120s", srv.IdleTimeout)
	}
}

func TestGatewayRoutesRegistered(t *testing.T) {
	// Stand up a fake upstream so the proxy has somewhere to forward.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("upstream"))
	}))
	defer upstream.Close()

	addr := upstream.Listener.Addr().String()
	cfg := config.Config{
		ListenAddr:  ":0",
		GatewayAddr: addr,
	}
	srv := New(cfg, nil)

	for _, path := range []string{"/gateway/", "/websocket/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, req)

		resp := w.Result()
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d, want %d", path, resp.StatusCode, http.StatusOK)
		}
	}
}

func TestNekoRouteRegistered(t *testing.T) {
	nekoHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("neko:" + r.URL.Path))
	})

	cfg := config.Config{
		ListenAddr:  ":0",
		GatewayAddr: "127.0.0.1:1",
	}
	srv := New(cfg, nekoHandler)

	// The route strips the /neko prefix, so Neko sees "/" for "/neko/".
	req := httptest.NewRequest(http.MethodGet, "/neko/", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := string(body); got != "neko:/" {
		t.Errorf("body = %q, want %q", got, "neko:/")
	}
}

func TestNekoWSRouteStripPrefix(t *testing.T) {
	nekoHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("path:" + r.URL.Path))
	})

	cfg := config.Config{
		ListenAddr:  ":0",
		GatewayAddr: "127.0.0.1:1",
	}
	srv := New(cfg, nekoHandler)

	// /neko/ws should be seen by the handler as /ws.
	req := httptest.NewRequest(http.MethodGet, "/neko/ws", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := string(body); got != "path:/ws" {
		t.Errorf("body = %q, want %q", got, "path:/ws")
	}
}

func TestNekoNilHandlerNotRegistered(t *testing.T) {
	cfg := config.Config{
		ListenAddr:  ":0",
		GatewayAddr: "127.0.0.1:1",
	}
	srv := New(cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/neko/", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	resp := w.Result()
	resp.Body.Close()

	// With no neko handler, /neko/ should 404.
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}
