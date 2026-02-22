package handlers

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/go-chi/chi/v5"
)

const connectingPageTmpl = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Connecting to OpenClaw...</title>
<style>
  body { display:flex; justify-content:center; align-items:center; min-height:100vh; margin:0; background:#0f172a; color:#e2e8f0; font-family:system-ui,sans-serif; }
  .box { text-align:center; }
  .spinner { width:48px; height:48px; border:4px solid #334155; border-top-color:#38bdf8; border-radius:50%; animation:spin 0.8s linear infinite; margin:0 auto 1.5rem; }
  @keyframes spin { to { transform:rotate(360deg); } }
  h1 { font-size:1.25rem; font-weight:500; margin:0 0 0.5rem; }
  p  { font-size:0.875rem; color:#94a3b8; margin:0 0 1.5rem; }
  a  { color:#38bdf8; font-size:0.8125rem; text-decoration:none; }
  a:hover { text-decoration:underline; }
</style>
</head>
<body>
<div class="box">
  <div class="spinner"></div>
  <h1>Connecting to OpenClaw&hellip;</h1>
  <p>The agent is starting up. This page will refresh automatically.</p>
  <a href="/instances/{{.InstanceID}}#logs">View instance logs</a>
</div>
<script>
setInterval(function(){
  fetch(location.href,{method:"HEAD"}).then(function(r){if(r.ok)location.reload()}).catch(function(){});
},1000);
</script>
</body>
</html>`

var connectingPageTemplate = template.Must(template.New("connecting").Parse(connectingPageTmpl))

func writeConnectingPage(w http.ResponseWriter, instanceID int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusServiceUnavailable)
	connectingPageTemplate.Execute(w, struct{ InstanceID int }{instanceID})
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

	info, err := getTunnelPortInfo(uint(id), "gateway")
	if err != nil {
		// WebSocket clients can't display HTML — return plain error
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeConnectingPage(w, id)
		return
	}

	path := chi.URLParam(r, "*")

	// Detect WebSocket upgrade and delegate
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		// Set Origin to match the gateway's local address so its origin
		// check passes. Without this, the gateway sees the random tunnel
		// port as the origin and rejects the WebSocket handshake.
		gatewayOrigin := fmt.Sprintf("http://localhost:%d", info.remotePort)
		headers := http.Header{
			"Origin": []string{gatewayOrigin},
		}
		websocketProxyToLocalPort(w, r, info.localPort, path, headers)
		return
	}

	if err := proxyToLocalPort(w, r, info.localPort, path); err != nil {
		writeConnectingPage(w, id)
	}
}
