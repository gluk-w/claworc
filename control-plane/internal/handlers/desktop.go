package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
	"github.com/go-chi/chi/v5"
)

// DesktopProxy proxies HTTP and WebSocket requests to the noVNC/websockify
// server providing the browser desktop.
//
// For legacy instances the noVNC server runs inside the agent container at
// port 3000 and the control plane reaches it via the existing VNC reverse
// SSH tunnel.
//
// For non-legacy instances the noVNC server lives in a separate browser pod
// reached over cluster networking; the BrowserBridge ensures the pod is
// running and we proxy directly to its Service.
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

	var inst database.Instance
	if err := database.DB.First(&inst, uint(id)).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	path := chi.URLParam(r, "*")
	wantsWS := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")

	if database.IsLegacyEmbedded(inst.ContainerImage) {
		port, err := getTunnelPort(uint(id), "vnc")
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if wantsWS {
			websocketProxyToLocalPort(w, r, port, path)
			return
		}
		if err := proxyToLocalPort(w, r, port, path); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
		}
		return
	}

	// Non-legacy: ensure the browser pod is running and proxy to it directly.
	if BrowserBridgeRef == nil {
		writeError(w, http.StatusServiceUnavailable, "browser bridge not configured")
		return
	}
	user := middleware.GetUser(r)
	var userID uint
	if user != nil {
		userID = user.ID
	}
	if err := BrowserBridgeRef.EnsureSession(r.Context(), uint(id), userID); err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("browser session not ready: %v", err))
		return
	}
	orch := orchestrator.Get()
	if orch == nil {
		writeError(w, http.StatusServiceUnavailable, "orchestrator not configured")
		return
	}
	endpoint, err := orch.GetBrowserPodEndpoint(r.Context(), uint(id))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	BrowserBridgeRef.Touch(uint(id))

	if wantsWS {
		websocketProxyToHost(w, r, endpoint.Host, endpoint.VNCPort, path)
		return
	}
	if err := proxyToHost(w, r, endpoint.Host, endpoint.VNCPort, path); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

// proxyToHost proxies an HTTP request to http://host:port/path. It mirrors
// proxyToLocalPort but accepts an arbitrary host (e.g. cluster Service DNS).
func proxyToHost(w http.ResponseWriter, r *http.Request, host string, port int, path string) error {
	targetURL := fmt.Sprintf("http://%s:%d/%s", host, port, path)
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create proxy request")
		return nil
	}
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
		log.Printf("Browser-pod proxy: %v", err)
		return fmt.Errorf("cannot connect to browser pod: %w", err)
	}
	return writeProxyResponse(w, resp, "")
}

// websocketProxyToHost proxies a WebSocket connection to ws://host:port/path.
// Mirrors websocketProxyToLocalPort but with an arbitrary host.
func websocketProxyToHost(w http.ResponseWriter, r *http.Request, host string, port int, path string) {
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
		log.Printf("Browser-pod WS proxy: accept error: %v", err)
		return
	}
	defer clientConn.CloseNow()

	wsURL := fmt.Sprintf("ws://%s:%d/%s", host, port, path)
	if r.URL.RawQuery != "" {
		wsURL += "?" + r.URL.RawQuery
	}

	ctx := r.Context()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	log.Printf("Browser-pod WS proxy: %s → %s", utils.SanitizeForLog(r.URL.Path), utils.SanitizeForLog(wsURL))
	dialOpts := &websocket.DialOptions{Subprotocols: subprotocols}
	upstreamConn, _, err := websocket.Dial(dialCtx, wsURL, dialOpts)
	if err != nil {
		log.Printf("Browser-pod WS proxy: dial error %s: %v", utils.SanitizeForLog(wsURL), err)
		clientConn.Close(4502, "Cannot connect to browser pod")
		return
	}
	defer upstreamConn.CloseNow()

	clientConn.SetReadLimit(4 * 1024 * 1024)
	upstreamConn.SetReadLimit(4 * 1024 * 1024)

	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

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
