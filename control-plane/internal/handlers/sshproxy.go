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
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
)

// getTunnelPort looks up the active SSH tunnel for the given instance and
// service type, returning the local port that forwards to the agent.
func getTunnelPort(instanceID uint, serviceType string) (int, error) {
	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return 0, fmt.Errorf("instance not found")
	}

	tm := sshtunnel.GetTunnelManager()
	if tm == nil {
		return 0, fmt.Errorf("tunnel manager not available")
	}

	tunnels := tm.GetTunnels(inst.Name)
	for _, t := range tunnels {
		if t.IsClosed() {
			continue
		}
		if string(t.Config.Service) == serviceType {
			return t.LocalPort, nil
		}
	}

	return 0, fmt.Errorf("no active %s tunnel for instance %d", serviceType, instanceID)
}

// proxyToLocalPort proxies an HTTP request to localhost:port/path.
// It forwards relevant request and response headers and streams the response body.
func proxyToLocalPort(w http.ResponseWriter, r *http.Request, port int, path string) {
	targetURL := fmt.Sprintf("http://127.0.0.1:%d/%s", port, path)
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create proxy request")
		return
	}

	// Forward relevant request headers
	for _, h := range []string{
		"Accept", "Accept-Encoding", "Accept-Language",
		"Content-Type", "Content-Length",
		"Range", "If-None-Match", "If-Modified-Since",
		"Cache-Control",
	} {
		if v := r.Header.Get(h); v != "" {
			proxyReq.Header.Set(h, v)
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(proxyReq)
	if err != nil {
		log.Printf("SSH proxy HTTP error (port %d): %v", port, err)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("Cannot connect to tunnel service: %v", err))
		return
	}
	defer resp.Body.Close()

	// Forward response headers
	for _, h := range []string{
		"Content-Type", "Content-Encoding", "Content-Length",
		"Cache-Control", "ETag", "Last-Modified",
	} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// websocketProxyToLocalPort establishes a WebSocket proxy between the client
// and localhost:port/path. It handles subprotocol negotiation and performs a
// bidirectional relay.
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
		log.Printf("SSH WS proxy: accept error: %v", err)
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

	dialOpts := &websocket.DialOptions{
		Subprotocols: subprotocols,
	}

	upstreamConn, _, err := websocket.Dial(dialCtx, wsURL, dialOpts)
	if err != nil {
		log.Printf("SSH WS proxy: local dial error (port %d): %v", port, err)
		clientConn.Close(4502, "Cannot connect to tunnel service")
		return
	}
	defer upstreamConn.CloseNow()

	clientConn.SetReadLimit(4 * 1024 * 1024)
	upstreamConn.SetReadLimit(4 * 1024 * 1024)

	// Transparent bidirectional relay
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
