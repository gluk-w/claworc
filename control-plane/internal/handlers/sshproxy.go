package handlers

import (
	"bytes"
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

// tunnelPortInfo holds local and remote port information for an active tunnel.
type tunnelPortInfo struct {
	localPort  int
	remotePort int
}

// getTunnelPort looks up the active SSH tunnel for an instance and returns
// the local port for the given service type ("vnc" or "gateway").
func getTunnelPort(instanceID uint, serviceType string) (int, error) {
	info, err := getTunnelPortInfo(instanceID, serviceType)
	if err != nil {
		return 0, err
	}
	return info.localPort, nil
}

// getTunnelPortInfo looks up the active SSH tunnel for an instance and returns
// both the local and remote ports for the given service type.
func getTunnelPortInfo(instanceID uint, serviceType string) (tunnelPortInfo, error) {
	if TunnelMgr == nil {
		return tunnelPortInfo{}, fmt.Errorf("tunnel manager not initialized")
	}

	tunnels := TunnelMgr.GetTunnelsForInstance(instanceID)
	label := ""
	switch strings.ToLower(serviceType) {
	case "vnc":
		label = "VNC"
	case "gateway":
		label = "Gateway"
	default:
		return tunnelPortInfo{}, fmt.Errorf("unknown service type: %s", serviceType)
	}

	for _, t := range tunnels {
		if t.Label == label && t.Status == "active" {
			return tunnelPortInfo{
				localPort:  t.LocalPort,
				remotePort: t.Config.RemotePort,
			}, nil
		}
	}

	return tunnelPortInfo{}, fmt.Errorf("no active %s tunnel for instance %d", serviceType, instanceID)
}

// tunnelProxyClient is a shared HTTP client configured for local tunnel traffic.
// Since tunnels are on localhost, no custom transport is needed. The default
// transport provides connection pooling and keep-alives which reduces TCP
// connection overhead for repeated requests to the same tunnel port.
var tunnelProxyClient = &http.Client{
	Timeout: 30 * time.Second,
}

// proxyToLocalPort proxies an HTTP request to localhost:port/path.
// It forwards relevant headers and streams the response back.
//
// If rewriteBase is provided and the response Content-Type is text/html,
// a <base href="{rewriteBase}"> tag is injected after <head> so that
// relative paths in the HTML resolve correctly under the proxy path.
//
// Performance: ~67µs direct to localhost, ~124µs via SSH tunnel (~57µs tunnel overhead).
// Supports 20+ concurrent requests through a single SSH tunnel without errors.
func proxyToLocalPort(w http.ResponseWriter, r *http.Request, port int, path string, rewriteBase ...string) error {
	targetURL := fmt.Sprintf("http://127.0.0.1:%d/%s", port, path)
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	log.Printf("Tunnel proxy: %s → %s", logutil.SanitizeForLog(r.URL.Path), logutil.SanitizeForLog(targetURL))

	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create proxy request")
		return nil
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
		return fmt.Errorf("cannot connect to service via tunnel: %w", err)
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

	// Inject <base> tag into HTML responses when rewriteBase is supplied.
	if len(rewriteBase) > 0 && rewriteBase[0] != "" &&
		strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			log.Printf("Tunnel proxy: error reading HTML body: %v", readErr)
			return fmt.Errorf("error reading response body: %w", readErr)
		}
		baseTag := `<base href="` + rewriteBase[0] + `">`
		body = bytes.Replace(body, []byte("<head>"), []byte("<head>"+baseTag), 1)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
		return nil
	}

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
	return nil
}

// websocketProxyToLocalPort proxies a WebSocket connection to localhost:port/path.
// It accepts the client WebSocket, dials the local tunnel endpoint, and runs
// a bidirectional relay between them.
//
// Performance: ~420µs per round-trip message (including WebSocket frame overhead).
// Supports 10+ concurrent WebSocket connections through a single SSH tunnel.
// Each connection uses two goroutines for bidirectional relay (client→upstream, upstream→client).
func websocketProxyToLocalPort(w http.ResponseWriter, r *http.Request, port int, path string, upstreamHeaders ...http.Header) {
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

	dialOpts := &websocket.DialOptions{
		Subprotocols: subprotocols,
	}
	if len(upstreamHeaders) > 0 && upstreamHeaders[0] != nil {
		dialOpts.HTTPHeader = upstreamHeaders[0]
	}

	upstreamConn, _, err := websocket.Dial(dialCtx, wsURL, dialOpts)
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
