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
// that exposes only sshd externally. The BrowserBridge opens an SSH session
// to the pod and we route every HTTP/WebSocket request through ssh.Client.Dial
// to 127.0.0.1:3000 inside the pod.
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

	// Non-legacy: tunnel HTTP/WebSocket traffic into the browser pod over SSH.
	// The pod's sshd is the only externally reachable port; noVNC binds
	// 127.0.0.1:3000 inside the pod and we reach it via ssh.Client.Dial.
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
	dial, err := BrowserBridgeRef.VNCDialer(r.Context(), uint(id))
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("browser ssh: %v", err))
		return
	}
	BrowserBridgeRef.Touch(uint(id))

	transport := &http.Transport{
		DialContext:           dial,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	defer transport.CloseIdleConnections()

	if wantsWS {
		websocketProxyOverDialer(w, r, transport, path)
		return
	}
	if err := proxyOverDialer(w, r, transport, path); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

// proxyOverDialer proxies an HTTP request through transport, which is
// expected to dial into the browser pod's noVNC port (127.0.0.1:3000) over
// SSH. The target host in the URL is a placeholder — the transport's
// DialContext ignores the address and always dials the SSH-tunnelled port.
func proxyOverDialer(w http.ResponseWriter, r *http.Request, transport *http.Transport, path string) error {
	targetURL := fmt.Sprintf("http://browser-pod/%s", path)
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
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	resp, err := client.Do(proxyReq)
	if err != nil {
		log.Printf("Browser-pod proxy: %v", err)
		return fmt.Errorf("cannot connect to browser pod: %w", err)
	}
	return writeProxyResponse(w, resp, "")
}

// websocketProxyOverDialer proxies a WebSocket connection through transport,
// which dials into the browser pod's noVNC port over SSH.
func websocketProxyOverDialer(w http.ResponseWriter, r *http.Request, transport *http.Transport, path string) {
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

	wsURL := fmt.Sprintf("ws://browser-pod/%s", path)
	if r.URL.RawQuery != "" {
		wsURL += "?" + r.URL.RawQuery
	}

	ctx := r.Context()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	log.Printf("Browser-pod WS proxy: %s → %s", utils.SanitizeForLog(r.URL.Path), utils.SanitizeForLog(wsURL))
	dialOpts := &websocket.DialOptions{
		Subprotocols: subprotocols,
		HTTPClient:   &http.Client{Transport: transport},
	}
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
