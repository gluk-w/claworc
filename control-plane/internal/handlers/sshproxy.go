package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/logutil"
)

// getTunnelPort looks up the active SSH tunnel for an instance and returns
// the local port for the given service type ("vnc" or "gateway").
func getTunnelPort(instanceID uint, serviceType string) (int, error) {
	if TunnelMgr == nil {
		return 0, fmt.Errorf("tunnel manager not initialized")
	}

	var port int
	switch strings.ToLower(serviceType) {
	case "vnc":
		port = TunnelMgr.GetVNCLocalPort(instanceID)
	case "gateway":
		port = TunnelMgr.GetGatewayLocalPort(instanceID)
	default:
		return 0, fmt.Errorf("unknown service type: %s", serviceType)
	}

	if port == 0 {
		return 0, fmt.Errorf("no active %s tunnel for instance %d", serviceType, instanceID)
	}

	return port, nil
}

// tunnelProxyClient is an HTTP client configured for local tunnel traffic.
// Since tunnels are on localhost, no custom transport is needed.
var tunnelProxyClient = &http.Client{
	Timeout: 30 * time.Second,
}

// proxyToLocalPort proxies an HTTP request to localhost:port/path.
// It forwards relevant headers and streams the response back.
func proxyToLocalPort(w http.ResponseWriter, r *http.Request, port int, path string) {
	targetURL := fmt.Sprintf("http://127.0.0.1:%d/%s", port, path)
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	log.Printf("Tunnel proxy: %s → %s", logutil.SanitizeForLog(r.URL.Path), logutil.SanitizeForLog(targetURL))

	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create proxy request")
		return
	}

	// Forward relevant headers
	for _, h := range []string{
		"Accept", "Accept-Encoding", "Accept-Language",
		"Content-Type", "Content-Length",
		"Range", "If-None-Match", "If-Modified-Since",
	} {
		if v := r.Header.Get(h); v != "" {
			proxyReq.Header.Set(h, v)
		}
	}

	resp, err := tunnelProxyClient.Do(proxyReq)
	if err != nil {
		log.Printf("Tunnel proxy error: %v", err)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("Cannot connect to service via tunnel: %v", err))
		return
	}
	defer resp.Body.Close()

	// Forward response headers
	for _, h := range []string{
		"Content-Type", "Content-Length", "Content-Encoding",
		"Cache-Control", "ETag", "Last-Modified",
	} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// websocketProxyToLocalPort proxies a WebSocket connection to localhost:port/path.
// It accepts the client WebSocket, dials the local tunnel endpoint, and runs
// a bidirectional relay between them.
func websocketProxyToLocalPort(w http.ResponseWriter, r *http.Request, port int, path string) {
	// Accept with client's requested subprotocol
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
		log.Printf("Tunnel WS proxy: accept error: %v", err)
		return
	}
	defer clientConn.CloseNow()

	// Build local WebSocket URL
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/%s", port, path)
	if r.URL.RawQuery != "" {
		wsURL += "?" + r.URL.RawQuery
	}

	ctx := r.Context()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	log.Printf("Tunnel WS proxy: %s → %s", logutil.SanitizeForLog(r.URL.Path), logutil.SanitizeForLog(wsURL))

	upstreamConn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		Subprotocols: subprotocols,
	})
	if err != nil {
		log.Printf("Tunnel WS proxy: local dial error for %s: %v", logutil.SanitizeForLog(wsURL), err)
		clientConn.Close(4502, "Cannot connect to service via tunnel")
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
