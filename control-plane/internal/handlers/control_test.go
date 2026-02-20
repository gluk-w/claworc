package handlers

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/tunnel"
	"github.com/hashicorp/yamux"
)

// mockGatewayServer reads the channel header from accepted yamux streams and
// serves HTTP responses through the gateway channel. It simulates the
// agent-side HTTPChannelHandler for the gateway channel.
func mockGatewayServer(t *testing.T, srv *yamux.Session, handler http.Handler) {
	t.Helper()
	go func() {
		for {
			stream, err := srv.AcceptStream()
			if err != nil {
				return // session closed
			}
			go func(s net.Conn) {
				defer s.Close()

				// Read channel header byte-by-byte until newline.
				var buf []byte
				b := make([]byte, 1)
				for {
					_, err := s.Read(b)
					if err != nil {
						return
					}
					if b[0] == '\n' {
						break
					}
					buf = append(buf, b[0])
				}

				if string(buf) != tunnel.ChannelGateway {
					return
				}

				// Serve a single HTTP connection (like agent HTTPChannelHandler).
				hsrv := &http.Server{Handler: handler}
				ln := &singleAcceptListener{conn: s}
				hsrv.Serve(ln)
			}(stream)
		}
	}()
}

// setupGatewayTunnel creates a yamux pair, registers a TunnelClient in the
// global manager, and starts a mock gateway server.
func setupGatewayTunnel(t *testing.T, instanceID uint, handler http.Handler) {
	t.Helper()

	old := tunnel.Manager
	t.Cleanup(func() { tunnel.Manager = old })

	tunnel.Manager = tunnel.NewTunnelManager()

	cli, srv := newYamuxPair(t)
	tc := tunnel.NewTunnelClient(instanceID, "test")
	tc.SetSession(cli)
	tunnel.Manager.Set(instanceID, tc)

	mockGatewayServer(t, srv, handler)
}

// ---- openGatewayChannel tests ----

func TestOpenGatewayChannel_NoManager(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()

	tunnel.Manager = nil
	_, err := openGatewayChannel(t.Context(), 1)
	if err == nil {
		t.Fatal("expected error when Manager is nil")
	}
	if !strings.Contains(err.Error(), "not initialised") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOpenGatewayChannel_NoTunnel(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()

	tunnel.Manager = tunnel.NewTunnelManager()
	_, err := openGatewayChannel(t.Context(), 999)
	if err == nil {
		t.Fatal("expected error when no tunnel connected")
	}
	if !strings.Contains(err.Error(), "no tunnel connected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOpenGatewayChannel_Success(t *testing.T) {
	gwHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})
	setupGatewayTunnel(t, 1, gwHandler)

	conn, err := openGatewayChannel(t.Context(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	conn.Close()
}

// ---- controlHTTPProxy tests ----

func TestControlHTTPProxy_FullRoundTrip(t *testing.T) {
	gwHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "gw-path=%s", r.URL.Path)
	})
	setupGatewayTunnel(t, 1, gwHandler)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/instances/1/control/some/path", nil)
	req = withChiParams(req, map[string]string{"id": "1", "*": "some/path"})

	controlHTTPProxy(rr, req, 1)

	resp := rr.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "gw-path=/gateway/some/path") {
		t.Errorf("expected gw-path=/gateway/some/path in body, got: %s", string(body))
	}

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("expected Content-Type text/plain, got %s", ct)
	}
}

func TestControlHTTPProxy_NoTunnel(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()

	tunnel.Manager = tunnel.NewTunnelManager()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/instances/1/control/", nil)
	req = withChiParams(req, map[string]string{"id": "1", "*": ""})

	controlHTTPProxy(rr, req, 1)

	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rr.Code)
	}
}

func TestControlHTTPProxy_QueryString(t *testing.T) {
	gwHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "query=%s", r.URL.RawQuery)
	})
	setupGatewayTunnel(t, 1, gwHandler)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/instances/1/control/api/status?format=json", nil)
	req = withChiParams(req, map[string]string{"id": "1", "*": "api/status"})

	controlHTTPProxy(rr, req, 1)

	body, _ := io.ReadAll(rr.Result().Body)
	if !strings.Contains(string(body), "format=json") {
		t.Errorf("expected query string in body, got: %s", string(body))
	}
}

func TestControlHTTPProxy_RootPath(t *testing.T) {
	gwHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "path=%s", r.URL.Path)
	})
	setupGatewayTunnel(t, 1, gwHandler)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/instances/1/control/", nil)
	req = withChiParams(req, map[string]string{"id": "1", "*": ""})

	controlHTTPProxy(rr, req, 1)

	body, _ := io.ReadAll(rr.Result().Body)
	if !strings.Contains(string(body), "path=/gateway/") {
		t.Errorf("expected path=/gateway/ in body, got: %s", string(body))
	}
}

func TestControlHTTPProxy_ForwardsResponseHeaders(t *testing.T) {
	gwHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", `"abc123"`)
		fmt.Fprint(w, `{"ok":true}`)
	})
	setupGatewayTunnel(t, 1, gwHandler)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/instances/1/control/api/status", nil)
	req.Header.Set("Accept", "application/json")
	req = withChiParams(req, map[string]string{"id": "1", "*": "api/status"})

	controlHTTPProxy(rr, req, 1)

	resp := rr.Result()
	if resp.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("expected Cache-Control no-cache, got %s", resp.Header.Get("Cache-Control"))
	}
	if resp.Header.Get("ETag") != `"abc123"` {
		t.Errorf("expected ETag, got %s", resp.Header.Get("ETag"))
	}
}

func TestControlHTTPProxy_ForwardsRequestHeaders(t *testing.T) {
	gwHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "accept=%s|ct=%s", r.Header.Get("Accept"), r.Header.Get("Content-Type"))
	})
	setupGatewayTunnel(t, 1, gwHandler)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/instances/1/control/api/data", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "text/html")
	req = withChiParams(req, map[string]string{"id": "1", "*": "api/data"})

	controlHTTPProxy(rr, req, 1)

	body, _ := io.ReadAll(rr.Result().Body)
	if !strings.Contains(string(body), "accept=application/json") {
		t.Errorf("expected Accept header forwarded, got: %s", string(body))
	}
	if !strings.Contains(string(body), "ct=text/html") {
		t.Errorf("expected Content-Type header forwarded, got: %s", string(body))
	}
}
