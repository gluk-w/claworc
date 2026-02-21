package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/go-chi/chi/v5"
)

// DesktopProxy proxies HTTP and WebSocket requests to the Selkies streaming UI
// running on port 3000 inside the agent container via SSH tunnel.
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

	port, err := getTunnelPort(uint(id), "vnc")
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
