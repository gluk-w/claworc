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
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/crypto"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/go-chi/chi/v5"
)

// resolveGatewayToken looks up the instance and decrypts its gateway token.
// Returns an empty token (not an error) when the DB is unavailable or the
// instance has no token — the proxy can still function without auth.
func resolveGatewayToken(instanceID int) (string, error) {
	if database.DB == nil {
		return "", nil
	}

	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return "", fmt.Errorf("instance not found")
	}

	if inst.GatewayToken == "" {
		return "", nil
	}

	tok, err := crypto.Decrypt(inst.GatewayToken)
	if err != nil {
		return "", nil // return empty token rather than error
	}
	return tok, nil
}

func ControlProxy(w http.ResponseWriter, r *http.Request) {
	// Check access before anything (covers both HTTP and WS paths)
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	// Detect WebSocket upgrade and delegate
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		controlWSProxy(w, r)
		return
	}

	controlHTTPProxy(w, r, uint(id))
}

// controlHTTPProxy handles regular HTTP requests by opening a gateway tunnel
// stream and performing a single HTTP round-trip over it.
func controlHTTPProxy(w http.ResponseWriter, r *http.Request, instanceID uint) {
	conn, err := openGatewayChannel(r.Context(), instanceID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer conn.Close()

	path := chi.URLParam(r, "*")
	targetURL := "/gateway/" + path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	token, _ := resolveGatewayToken(id)
	if token != "" {
		sep := "?"
		if strings.Contains(targetURL, "?") {
			sep = "&"
		}
		targetURL += sep + "token=" + token
	}

	// Write the HTTP request over the tunnel stream.
	proxyReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create proxy request")
		return
	}
	proxyReq.Host = "gateway"
	for _, h := range []string{"Accept", "Accept-Encoding", "Accept-Language", "Content-Type", "Range", "If-None-Match", "If-Modified-Since"} {
		if v := r.Header.Get(h); v != "" {
			proxyReq.Header.Set(h, v)
		}
	}

	log.Printf("Control proxy: %s → tunnel (instance %d)", r.URL.Path, instanceID)
	if err := proxyReq.Write(conn); err != nil {
		log.Printf("Control proxy: write request error: %v", err)
		writeError(w, http.StatusBadGateway, "failed to send request to gateway service")
		return
	}

	// Read the HTTP response from the tunnel stream.
	resp, err := http.ReadResponse(bufio.NewReader(conn), proxyReq)
	if err != nil {
		log.Printf("Control proxy: read response error: %v", err)
		writeError(w, http.StatusBadGateway, "failed to read response from gateway service")
		return
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)

	for _, h := range []string{"Cache-Control", "ETag", "Last-Modified", "Content-Length"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func controlWSProxy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid instance ID", http.StatusBadRequest)
		return
	}

	conn, err := openGatewayChannel(r.Context(), uint(id))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// conn is closed when the upstream WebSocket is closed below.

	token, _ := resolveGatewayToken(id)

	// Build WebSocket URL with token for the agent-side gateway proxy.
	path := chi.URLParam(r, "*")
	wsURL := "ws://gateway/gateway/" + path
	if r.URL.RawQuery != "" {
		wsURL += "?" + r.URL.RawQuery
	}
	if token != "" {
		sep := "?"
		if strings.Contains(wsURL, "?") {
			sep = "&"
		}
		wsURL += sep + "token=" + token
	}

	// Accept client WebSocket
	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("Control WS proxy: accept error: %v", err)
		conn.Close()
		return
	}
	defer clientConn.CloseNow()

	// Dial the upstream gateway WebSocket through the tunnel stream using a
	// custom transport whose DialContext returns the pre-opened stream.
	ctx := r.Context()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

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

	log.Printf("Control WS proxy: %s → tunnel (instance %d)", r.URL.Path, id)
	upstreamConn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		log.Printf("Control WS proxy: upstream dial error: %v", err)
		clientConn.Close(4502, "Cannot connect to gateway")
		conn.Close()
		return
	}
	defer upstreamConn.CloseNow()
	log.Printf("Control WS proxy: upstream connected via tunnel")

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
				log.Printf("Control WS proxy: upstream read error: %v", err)
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
