package handlers

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/tunnel"
	"github.com/go-chi/chi/v5"
)

// openNekoChannel looks up the tunnel for the given instance and opens a
// yamux stream with the "neko" channel header. The returned net.Conn is
// positioned after the header — ready for HTTP request/response I/O.
func openNekoChannel(ctx context.Context, instanceID uint) (net.Conn, error) {
	if tunnel.Manager == nil {
		return nil, fmt.Errorf("tunnel manager not initialised")
	}

	tc := tunnel.Manager.Get(instanceID)
	if tc == nil {
		return nil, fmt.Errorf("no tunnel connected for instance %d", instanceID)
	}

	return tc.OpenChannel(ctx, tunnel.ChannelNeko)
}

// DesktopProxy proxies HTTP and WebSocket requests to the Neko VNC server
// embedded in the agent process, reached via a yamux tunnel stream.
func DesktopProxy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	// Verify instance exists and is running.
	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}

	// Detect WebSocket upgrade and delegate.
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		desktopWSProxy(w, r, uint(id))
		return
	}

	desktopHTTPProxy(w, r, uint(id))
}

// desktopHTTPProxy handles regular HTTP requests by opening a tunnel stream
// and performing a single HTTP round-trip over it.
func desktopHTTPProxy(w http.ResponseWriter, r *http.Request, instanceID uint) {
	conn, err := openNekoChannel(r.Context(), instanceID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer conn.Close()

	// Build the request path that Neko sees (prefix is stripped by the agent router).
	path := chi.URLParam(r, "*")
	targetURL := "/" + path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	// Write the HTTP request over the tunnel stream.
	proxyReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create proxy request")
		return
	}
	proxyReq.Host = "neko"
	for _, h := range []string{"Accept", "Accept-Encoding", "Accept-Language", "Content-Type", "Range", "If-None-Match", "If-Modified-Since"} {
		if v := r.Header.Get(h); v != "" {
			proxyReq.Header.Set(h, v)
		}
	}

	if err := proxyReq.Write(conn); err != nil {
		log.Printf("Desktop proxy: write request error: %v", err)
		writeError(w, http.StatusBadGateway, "failed to send request to desktop service")
		return
	}

	// Read the HTTP response from the tunnel stream.
	resp, err := http.ReadResponse(bufio.NewReader(conn), proxyReq)
	if err != nil {
		log.Printf("Desktop proxy: read response error: %v", err)
		writeError(w, http.StatusBadGateway, "failed to read response from desktop service")
		return
	}
	defer resp.Body.Close()

	// Forward response headers.
	for _, h := range []string{"Content-Type", "Cache-Control", "ETag", "Last-Modified", "Content-Length", "Content-Encoding"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// desktopWSProxy handles WebSocket upgrades by opening a tunnel stream,
// performing an HTTP upgrade over it, then relaying WebSocket frames
// bidirectionally between the browser client and the agent's Neko server.
func desktopWSProxy(w http.ResponseWriter, r *http.Request, instanceID uint) {
	conn, err := openNekoChannel(r.Context(), instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// conn is closed when the upstream WebSocket is closed below.

	path := chi.URLParam(r, "*")

	// Accept client WebSocket with requested subprotocol.
	requestedProtocol := r.Header.Get("Sec-WebSocket-Protocol")
	var subprotocols []string
	if requestedProtocol != "" {
		subprotocols = strings.Split(requestedProtocol, ", ")
	}

	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       subprotocols,
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("Desktop WS proxy: accept error: %v", err)
		conn.Close()
		return
	}
	defer clientConn.CloseNow()

	// Dial the upstream Neko WebSocket through the tunnel stream using a
	// custom transport whose DialContext returns the pre-opened stream.
	upstreamURL := "ws://neko/" + path
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	ctx := r.Context()
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

	upstreamConn, _, err := websocket.Dial(ctx, upstreamURL, &websocket.DialOptions{
		Subprotocols: subprotocols,
		HTTPClient:   &http.Client{Transport: transport},
	})
	if err != nil {
		log.Printf("Desktop WS proxy: upstream dial error: %v", err)
		clientConn.Close(4502, "Cannot connect to desktop service")
		conn.Close()
		return
	}
	defer upstreamConn.CloseNow()

	clientConn.SetReadLimit(4 * 1024 * 1024)
	upstreamConn.SetReadLimit(4 * 1024 * 1024)

	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	// Client → Upstream
	go func() {
		defer relayCancel()
		for {
			msgType, data, err := clientConn.Read(relayCtx)
			if err != nil {
				return
			}
			if err := upstreamConn.Write(relayCtx, msgType, data); err != nil {
				return
			}
		}
	}()

	// Upstream → Client
	func() {
		defer relayCancel()
		for {
			msgType, data, err := upstreamConn.Read(relayCtx)
			if err != nil {
				return
			}
			if err := clientConn.Write(relayCtx, msgType, data); err != nil {
				return
			}
		}
	}()

	clientConn.Close(websocket.StatusNormalClosure, "")
	upstreamConn.Close(websocket.StatusNormalClosure, "")
}
