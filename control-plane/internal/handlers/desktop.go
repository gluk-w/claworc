package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/logutil"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/go-chi/chi/v5"
)

// desktopTargetCache caches resolved desktop targets to avoid repeated
// orchestrator API calls when a page loads many assets from the same instance.
var desktopTargetCache = struct {
	sync.RWMutex
	entries map[int]desktopCacheEntry
}{entries: make(map[int]desktopCacheEntry)}

type desktopCacheEntry struct {
	baseURL   string
	name      string
	expiresAt time.Time
}

const desktopCacheTTL = 30 * time.Second

func resolveDesktopTarget(ctx context.Context, instanceID int) (string, string, error) {
	// Check cache first
	desktopTargetCache.RLock()
	if entry, ok := desktopTargetCache.entries[instanceID]; ok && time.Now().Before(entry.expiresAt) {
		desktopTargetCache.RUnlock()
		return entry.baseURL, entry.name, nil
	}
	desktopTargetCache.RUnlock()

	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return "", "", fmt.Errorf("instance not found")
	}

	orch := orchestrator.Get()
	if orch == nil {
		return "", "", fmt.Errorf("no orchestrator available")
	}

	status, _ := orch.GetInstanceStatus(ctx, inst.Name)
	if status != "running" {
		return "", "", fmt.Errorf("instance not running")
	}

	baseURL, err := orch.GetVNCBaseURL(ctx, inst.Name, "chrome")
	if err != nil {
		return "", "", err
	}

	// Store in cache
	desktopTargetCache.Lock()
	desktopTargetCache.entries[instanceID] = desktopCacheEntry{
		baseURL:   baseURL,
		name:      inst.Name,
		expiresAt: time.Now().Add(desktopCacheTTL),
	}
	desktopTargetCache.Unlock()

	return baseURL, inst.Name, nil
}

// DesktopProxy proxies HTTP and WebSocket requests to the Selkies streaming UI
// running on port 3000 inside the agent container.
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

	// Detect WebSocket upgrade and delegate
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		desktopWSProxy(w, r, id)
		return
	}

	baseURL, _, err := resolveDesktopTarget(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	path := chi.URLParam(r, "*")
	targetURL := fmt.Sprintf("%s/%s", baseURL, path)
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create proxy request")
		return
	}

	// Forward relevant headers
	for _, h := range []string{"Accept", "Accept-Encoding", "Accept-Language", "Content-Type", "Range", "If-None-Match", "If-Modified-Since"} {
		if v := r.Header.Get(h); v != "" {
			proxyReq.Header.Set(h, v)
		}
	}

	resp, err := getProxyClient().Do(proxyReq)
	if err != nil {
		log.Printf("Desktop proxy error: %v", err)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("Cannot connect to desktop service: %v", err))
		return
	}
	defer resp.Body.Close()

	// Forward response headers
	for _, h := range []string{"Content-Type", "Cache-Control", "ETag", "Last-Modified", "Content-Length", "Content-Encoding"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func desktopWSProxy(w http.ResponseWriter, r *http.Request, instanceID int) {
	baseURL, _, err := resolveDesktopTarget(r.Context(), instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	path := chi.URLParam(r, "*")

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
		log.Printf("Desktop WS proxy: accept error: %v", err)
		return
	}
	defer clientConn.CloseNow()

	// Convert http(s):// to ws(s)://
	wsURL := strings.Replace(baseURL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
	wsURL += "/" + path
	if r.URL.RawQuery != "" {
		wsURL += "?" + r.URL.RawQuery
	}

	ctx := r.Context()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dialOpts := &websocket.DialOptions{
		Subprotocols: subprotocols,
	}
	orch := orchestrator.Get()
	if orch != nil {
		if t := orch.GetHTTPTransport(); t != nil {
			dialOpts.HTTPClient = &http.Client{Transport: t}
		}
	}

	upstreamConn, _, err := websocket.Dial(dialCtx, wsURL, dialOpts)
	if err != nil {
		log.Printf("Desktop WS proxy: upstream dial error for %s: %v", logutil.SanitizeForLog(wsURL), err)
		clientConn.Close(4502, "Cannot connect to desktop service")
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
