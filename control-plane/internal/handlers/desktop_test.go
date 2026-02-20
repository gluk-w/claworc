package handlers

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/tunnel"
	"github.com/go-chi/chi/v5"
	"github.com/hashicorp/yamux"
)

// newYamuxPair creates a connected yamux client/server pair over net.Pipe.
func newYamuxPair(t *testing.T) (*yamux.Session, *yamux.Session) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })

	srv, err := yamux.Server(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })

	cli, err := yamux.Client(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cli.Close() })

	return cli, srv
}

// withChiParams returns a new request with chi URL params set.
func withChiParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// mockNekoServer reads the channel header from accepted yamux streams and
// serves HTTP responses. It simulates the agent-side HTTPChannelHandler.
func mockNekoServer(t *testing.T, srv *yamux.Session, handler http.Handler) {
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

				if string(buf) != tunnel.ChannelNeko {
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

// singleAcceptListener yields one connection then blocks until closed.
type singleAcceptListener struct {
	conn net.Conn
	once sync.Once
	ch   chan struct{}
}

func (l *singleAcceptListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() {
		conn = l.conn
		l.ch = make(chan struct{})
	})
	if conn != nil {
		return conn, nil
	}
	if l.ch != nil {
		<-l.ch
	}
	return nil, net.ErrClosed
}

func (l *singleAcceptListener) Close() error {
	if l.ch != nil {
		select {
		case <-l.ch:
		default:
			close(l.ch)
		}
	}
	return nil
}

func (l *singleAcceptListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}
}

// setupTunnel creates a yamux pair, registers a TunnelClient in the global
// manager, and starts a mock Neko server. Returns a cleanup function.
func setupTunnel(t *testing.T, instanceID uint, handler http.Handler) {
	t.Helper()

	old := tunnel.Manager
	t.Cleanup(func() { tunnel.Manager = old })

	tunnel.Manager = tunnel.NewTunnelManager()

	cli, srv := newYamuxPair(t)
	tc := tunnel.NewTunnelClient(instanceID, "test")
	tc.SetSession(cli)
	tunnel.Manager.Set(instanceID, tc)

	mockNekoServer(t, srv, handler)
}

// ---- openNekoChannel tests ----

func TestOpenNekoChannel_NoManager(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()

	tunnel.Manager = nil
	_, err := openNekoChannel(t.Context(), 1)
	if err == nil {
		t.Fatal("expected error when Manager is nil")
	}
	if !strings.Contains(err.Error(), "not initialised") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOpenNekoChannel_NoTunnel(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()

	tunnel.Manager = tunnel.NewTunnelManager()
	_, err := openNekoChannel(t.Context(), 999)
	if err == nil {
		t.Fatal("expected error when no tunnel connected")
	}
	if !strings.Contains(err.Error(), "no tunnel connected") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- desktopHTTPProxy tests ----

func TestDesktopHTTPProxy_FullRoundTrip(t *testing.T) {
	nekoHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "neko-path=%s", r.URL.Path)
	})
	setupTunnel(t, 1, nekoHandler)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/instances/1/desktop/some/path", nil)
	req = withChiParams(req, map[string]string{"*": "some/path"})

	desktopHTTPProxy(rr, req, 1)

	resp := rr.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "neko-path=/some/path") {
		t.Errorf("expected neko-path=/some/path in body, got: %s", string(body))
	}

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("expected Content-Type text/plain, got %s", ct)
	}
}

func TestDesktopHTTPProxy_NoTunnel(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()

	tunnel.Manager = tunnel.NewTunnelManager()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/instances/1/desktop/", nil)
	req = withChiParams(req, map[string]string{"*": ""})

	desktopHTTPProxy(rr, req, 1)

	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rr.Code)
	}
}

func TestDesktopHTTPProxy_QueryString(t *testing.T) {
	nekoHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "query=%s", r.URL.RawQuery)
	})
	setupTunnel(t, 1, nekoHandler)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/instances/1/desktop/ws?token=abc&mode=play", nil)
	req = withChiParams(req, map[string]string{"*": "ws"})

	desktopHTTPProxy(rr, req, 1)

	body, _ := io.ReadAll(rr.Result().Body)
	if !strings.Contains(string(body), "query=token=abc&mode=play") {
		t.Errorf("expected query string in body, got: %s", string(body))
	}
}

func TestDesktopHTTPProxy_RootPath(t *testing.T) {
	nekoHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "path=%s", r.URL.Path)
	})
	setupTunnel(t, 1, nekoHandler)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/instances/1/desktop/", nil)
	req = withChiParams(req, map[string]string{"*": ""})

	desktopHTTPProxy(rr, req, 1)

	body, _ := io.ReadAll(rr.Result().Body)
	if !strings.Contains(string(body), "path=/") {
		t.Errorf("expected path=/ in body, got: %s", string(body))
	}
}

func TestDesktopHTTPProxy_ForwardsResponseHeaders(t *testing.T) {
	nekoHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", `"abc123"`)
		fmt.Fprint(w, `{"ok":true}`)
	})
	setupTunnel(t, 1, nekoHandler)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/instances/1/desktop/api/status", nil)
	req.Header.Set("Accept", "application/json")
	req = withChiParams(req, map[string]string{"*": "api/status"})

	desktopHTTPProxy(rr, req, 1)

	resp := rr.Result()
	if resp.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("expected Cache-Control no-cache, got %s", resp.Header.Get("Cache-Control"))
	}
	if resp.Header.Get("ETag") != `"abc123"` {
		t.Errorf("expected ETag, got %s", resp.Header.Get("ETag"))
	}
}
