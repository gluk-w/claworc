package handlers

import (
	"fmt"
	"net/http"
	"net/url"
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

// gatewayHost derives the gateway's internal host:port from the WS URL.
// For K8s API proxy URLs it reconstructs the cluster DNS name;
// for direct URLs it returns scheme+host as-is.
// NOTE: Still used by chat.go for direct gateway WebSocket connections.
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
