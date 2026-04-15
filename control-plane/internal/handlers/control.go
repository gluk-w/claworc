package handlers

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
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

	// Look up gateway token so we can inject it into upstream WebSocket requests
	var gatewayToken string
	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err == nil && inst.GatewayToken != "" {
		if tok, err := utils.Decrypt(inst.GatewayToken); err == nil && tok != "" {
			gatewayToken = tok
		}
	}

	wildcardPath := chi.URLParam(r, "*")
	// Forward the full path including the basePath prefix so that the gateway
	// (when configured with gateway.controlUi.basePath) can match the request.
	// Old images without basePath configured still work because the gateway
	// ignores the prefix and serves from root; the <base href> injection
	// in proxyToLocalPort handles relative asset resolution.
	fullPath := fmt.Sprintf("openclaw/%d/%s", id, wildcardPath)

	// Detect WebSocket upgrade and delegate
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		// Set Origin to match the gateway's local address so its origin
		// check passes. Without this, the gateway sees the random tunnel
		// port as the origin and rejects the WebSocket handshake.
		gatewayOrigin := fmt.Sprintf("http://localhost:%d", info.remotePort)
		headers := http.Header{
			"Origin": []string{gatewayOrigin},
		}
		// Inject gateway token so the upstream gateway authenticates the connection
		if gatewayToken != "" {
			q := r.URL.Query()
			q.Set("token", gatewayToken)
			r.URL.RawQuery = q.Encode()
		}
		websocketProxyToLocalPort(w, r, info.localPort, fullPath, headers)
		return
	}

	basePath := fmt.Sprintf("/openclaw/%d/", id)

	// Try the prefixed path first (e.g. openclaw/26/favicon.svg). If the
	// gateway returns 404, retry with just the resource path (e.g. favicon.svg).
	//
	// Why: when an HTML page is served under /openclaw/{id}/ we inject a
	// <base href="/openclaw/{id}/"> tag so relative asset URLs resolve under
	// the proxy prefix. But some resources — notably /favicon.svg, which
	// browsers request automatically from the document root independent of
	// the <base> tag — live at the root of the instance and are NOT served
	// under /openclaw/{id}/. Without this fallback those requests 404.
	// Retrying with the bare resource path lets the gateway serve them from
	// its root. The fallback only fires on 404, so correctly-prefixed
	// responses (200, 304, redirects, etc.) are passed through unchanged.
	resp, err := doProxyRequest(r, info.localPort, fullPath)
	if err != nil {
		writeConnectingPage(w, id)
		return
	}

	if resp.StatusCode == http.StatusNotFound && wildcardPath != "" {
		// Discard the 404 body and retry against the instance root.
		resp.Body.Close()
		fallbackResp, fbErr := doProxyRequest(r, info.localPort, wildcardPath)
		if fbErr == nil {
			resp = fallbackResp
		} else {
			// Fallback couldn't even reach the tunnel — show the connecting page.
			writeConnectingPage(w, id)
			return
		}
	}

	if err := writeProxyResponse(w, resp, basePath); err != nil {
		// writeProxyResponse only returns an error after it has already
		// started writing the response, so we can't switch to a different
		// response here — the error is already logged inside the helper.
		_ = err
	}
}
