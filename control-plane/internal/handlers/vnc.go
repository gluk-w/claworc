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
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/go-chi/chi/v5"
)

// defaultTransport is the fallback for in-cluster / Docker connectivity.
var defaultTransport http.RoundTripper = &http.Transport{
	MaxIdleConns:        50,
	MaxIdleConnsPerHost: 10,
	IdleConnTimeout:     90 * time.Second,
}

// getProxyClient returns an HTTP client that can reach service URLs.
// When the orchestrator provides a custom transport (e.g. K8s API proxy
// for out-of-cluster dev), it is used instead of the default.
func getProxyClient() *http.Client {
	orch := orchestrator.Get()
	transport := defaultTransport
	if orch != nil {
		if t := orch.GetHTTPTransport(); t != nil {
			transport = t
		}
	}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
	}
}

// vncTargetCache caches resolved VNC targets to avoid repeated orchestrator
// API calls when a page loads many assets from the same instance.
var vncTargetCache = struct {
	sync.RWMutex
	entries map[string]vncCacheEntry
}{entries: make(map[string]vncCacheEntry)}

type vncCacheEntry struct {
	baseURL   string
	name      string
	expiresAt time.Time
}

const vncCacheTTL = 30 * time.Second

func resolveVNCTarget(ctx context.Context, instanceID int, display string) (string, string, error) {
	if display != "chrome" {
		return "", "", fmt.Errorf("invalid display type")
	}

	// Check cache first
	cacheKey := fmt.Sprintf("%d:%s", instanceID, display)
	vncTargetCache.RLock()
	if entry, ok := vncTargetCache.entries[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		vncTargetCache.RUnlock()
		return entry.baseURL, entry.name, nil
	}
	vncTargetCache.RUnlock()

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

	baseURL, err := orch.GetVNCBaseURL(ctx, inst.Name, display)
	if err != nil {
		return "", "", err
	}

	// Store in cache
	vncTargetCache.Lock()
	vncTargetCache.entries[cacheKey] = vncCacheEntry{
		baseURL:   baseURL,
		name:      inst.Name,
		expiresAt: time.Now().Add(vncCacheTTL),
	}
	vncTargetCache.Unlock()

	return baseURL, inst.Name, nil
}

func VNCHTTPProxy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	display := chi.URLParam(r, "display")
	path := chi.URLParam(r, "*")

	baseURL, _, err := resolveVNCTarget(r.Context(), id, display)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	targetURL := fmt.Sprintf("%s/%s", baseURL, path)
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	resp, err := getProxyClient().Get(targetURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Cannot connect to VNC service")
		return
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)

	// Forward cache-related headers so browsers can cache static noVNC assets
	for _, h := range []string{"Cache-Control", "ETag", "Last-Modified", "Content-Length"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func VNCWSProxy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid instance ID", http.StatusBadRequest)
		return
	}

	if !middleware.CanAccessInstance(r, uint(id)) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	display := chi.URLParam(r, "display")

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
		log.Printf("Failed to accept VNC websocket: %v", err)
		return
	}
	defer clientConn.CloseNow()

	ctx := r.Context()

	baseURL, _, err := resolveVNCTarget(ctx, id, display)
	if err != nil {
		clientConn.Close(4400, err.Error())
		return
	}

	// Convert http(s):// to ws(s)://
	wsURL := strings.Replace(baseURL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
	wsURL += "/websockify"

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dialOpts := &websocket.DialOptions{
		Subprotocols: []string{"binary"},
	}
	// Use orchestrator's transport for K8s API proxy TLS
	orch := orchestrator.Get()
	if orch != nil {
		if t := orch.GetHTTPTransport(); t != nil {
			dialOpts.HTTPClient = &http.Client{Transport: t}
		}
	}

	upstreamConn, _, err := websocket.Dial(dialCtx, wsURL, dialOpts)
	if err != nil {
		log.Printf("Failed to connect to VNC websocket at %s: %v", wsURL, err)
		clientConn.Close(4502, "Cannot connect to VNC service")
		return
	}
	defer upstreamConn.CloseNow()

	// Increase read limit for VNC traffic
	clientConn.SetReadLimit(4 * 1024 * 1024) // 4MB
	upstreamConn.SetReadLimit(4 * 1024 * 1024)

	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	// Browser → VNC
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

	// VNC → Browser
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
