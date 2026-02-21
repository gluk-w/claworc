package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/go-chi/chi/v5"
)

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
