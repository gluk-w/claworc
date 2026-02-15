package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/crypto"
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

// controlTargetCache caches resolved control proxy targets to avoid repeated
// orchestrator API calls and token decryption for the same instance.
var controlTargetCache = struct {
	sync.RWMutex
	entries map[uint]controlCacheEntry
}{entries: make(map[uint]controlCacheEntry)}

type controlCacheEntry struct {
	httpURL   string
	wsURL     string
	token     string
	expiresAt time.Time
}

const controlCacheTTL = 30 * time.Second

func resolveControlTarget(ctx context.Context, instanceID int) (httpURL, wsURL, token string, err error) {
	uid := uint(instanceID)

	// Check cache
	controlTargetCache.RLock()
	if entry, ok := controlTargetCache.entries[uid]; ok && time.Now().Before(entry.expiresAt) {
		controlTargetCache.RUnlock()
		return entry.httpURL, entry.wsURL, entry.token, nil
	}
	controlTargetCache.RUnlock()

	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return "", "", "", fmt.Errorf("instance not found")
	}

	orch := orchestrator.Get()
	if orch == nil {
		return "", "", "", fmt.Errorf("no orchestrator available")
	}

	status, _ := orch.GetInstanceStatus(ctx, inst.Name)
	if status != "running" {
		return "", "", "", fmt.Errorf("instance not running")
	}

	gwURL, err := orch.GetGatewayWSURL(ctx, inst.Name)
	if err != nil {
		return "", "", "", err
	}

	// Convert ws(s):// → http(s)://
	httpBase := strings.Replace(gwURL, "wss://", "https://", 1)
	httpBase = strings.Replace(httpBase, "ws://", "http://", 1)

	// Decrypt gateway token
	var tok string
	if inst.GatewayToken != "" {
		tok, _ = crypto.Decrypt(inst.GatewayToken)
	}

	// Store in cache
	controlTargetCache.Lock()
	controlTargetCache.entries[uid] = controlCacheEntry{
		httpURL:   httpBase,
		wsURL:     gwURL,
		token:     tok,
		expiresAt: time.Now().Add(controlCacheTTL),
	}
	controlTargetCache.Unlock()

	return httpBase, gwURL, tok, nil
}

// gatewayHost derives the gateway's internal host:port from the WS URL.
// For K8s API proxy URLs it reconstructs the cluster DNS name;
// for direct URLs it returns scheme+host as-is.
func gatewayHost(gwURL string) (origin, host string) {
	u, err := url.Parse(gwURL)
	if err != nil {
		return "", ""
	}

	// K8s API proxy: .../api/v1/namespaces/{ns}/services/{svc}:{port}/proxy
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	var ns, svc, port string
	for i := 0; i < len(parts)-1; i++ {
		switch parts[i] {
		case "namespaces":
			ns = parts[i+1]
		case "services":
			sp := strings.SplitN(parts[i+1], ":", 2)
			svc = sp[0]
			if len(sp) > 1 {
				port = sp[1]
			}
		}
	}
	if ns != "" && svc != "" && port != "" {
		h := fmt.Sprintf("%s.%s.svc.cluster.local:%s", svc, ns, port)
		return "http://" + h, h
	}

	// Direct URL (Docker / in-cluster)
	scheme := "http"
	if u.Scheme == "wss" || u.Scheme == "https" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, u.Host), u.Host
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

	path := chi.URLParam(r, "*")

	httpURL, _, _, err := resolveControlTarget(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	targetURL := fmt.Sprintf("%s/%s", httpURL, path)

	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	log.Printf("Control proxy: %s → %s", r.URL.Path, targetURL)
	resp, err := getProxyClient().Get(targetURL)
	if err != nil {
		log.Printf("Control proxy error: %v", err)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("Cannot connect to gateway service: %v", err))
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

	_, wsURL, token, err := resolveControlTarget(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// Append token to upstream WS URL for authentication
	if token != "" {
		if strings.Contains(wsURL, "?") {
			wsURL += "&token=" + token
		} else {
			wsURL += "?token=" + token
		}
	}

	// Accept client WebSocket
	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("Control WS proxy: accept error: %v", err)
		return
	}
	defer clientConn.CloseNow()

	// Connect to upstream gateway (no Origin/Host overrides, matching ChatProxy)
	ctx := r.Context()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	origin, host := gatewayHost(wsURL)
	dialOpts := &websocket.DialOptions{
		Host:       host,
		HTTPHeader: http.Header{},
	}
	if origin != "" {
		dialOpts.HTTPHeader.Set("Origin", origin)
	}
	orch := orchestrator.Get()
	if orch != nil {
		if t := orch.GetHTTPTransport(); t != nil {
			dialOpts.HTTPClient = &http.Client{Transport: t}
		}
	}

	log.Printf("Control WS proxy: %s → %s", r.URL.Path, wsURL)
	upstreamConn, _, err := websocket.Dial(dialCtx, wsURL, dialOpts)
	if err != nil {
		log.Printf("Control WS proxy: upstream dial error: %v", err)
		clientConn.Close(4502, "Cannot connect to gateway")
		return
	}
	defer upstreamConn.CloseNow()
	log.Printf("Control WS proxy: upstream connected")

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
