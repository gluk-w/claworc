package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

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

// ControlProxy proxies HTTP and WebSocket requests to the gateway service
// running inside the agent container via SSH tunnel.
func ControlProxy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	port, err := getTunnelPort(uint(id), "gateway")
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	path := chi.URLParam(r, "*")

	// Detect WebSocket upgrade and delegate
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		websocketProxyToLocalPort(w, r, port, path)
		return
	}

	proxyToLocalPort(w, r, port, path)
}
