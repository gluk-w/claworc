package handlers

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/go-chi/chi/v5"
)

const connectingPageTmpl = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Connecting to agent UI...</title>
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
  <h1>Connecting to agent UI&hellip;</h1>
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

// controlUISpecTTL bounds how long a resolved ControlUISpec is reused. The
// spec (port, base path, auth) only changes with the image or the agent
// token, but resolving it costs an SSH exec (`control-ui-auth`), which must
// not run for every proxied asset request.
const controlUISpecTTL = 30 * time.Second

type controlUISpecEntry struct {
	spec agentshim.ControlUISpec
	at   time.Time
}

var controlUISpecCache sync.Map // uint -> controlUISpecEntry

// resolveControlUISpec returns the instance agent's ControlUISpec, cached for
// controlUISpecTTL.
func resolveControlUISpec(ctx context.Context, instanceID uint) (agentshim.ControlUISpec, error) {
	if v, ok := controlUISpecCache.Load(instanceID); ok {
		if e := v.(controlUISpecEntry); time.Since(e.at) < controlUISpecTTL {
			return e.spec, nil
		}
	}
	client, err := agentClientFor(ctx, instanceID)
	if err != nil {
		return agentshim.ControlUISpec{}, err
	}
	spec, err := client.ControlUI(ctx)
	if err != nil {
		return agentshim.ControlUISpec{}, err
	}
	controlUISpecCache.Store(instanceID, controlUISpecEntry{spec: spec, at: time.Now()})
	return spec, nil
}

// controlUITunnelPort returns the local port of a reverse tunnel to the
// agent's control UI port, created on demand. A seam for tests.
var controlUITunnelPort = func(ctx context.Context, instanceID uint, remotePort int) (int, error) {
	if TunnelMgr == nil {
		return 0, fmt.Errorf("tunnel manager not initialized")
	}
	return TunnelMgr.EnsureReverseTunnel(ctx, instanceID, "ControlUI", remotePort)
}

// ControlProxy proxies HTTP and WebSocket requests to the web UI the agent
// serves inside its container (the agent's meta `control_ui`, e.g. the
// OpenClaw Control UI), via an on-demand SSH tunnel. Everything
// agent-specific — the port, the upstream path prefix, and the auth
// query/headers injected into WebSocket upgrades — comes from
// agentshim.Client.ControlUI.
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

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	isWebSocket := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	// unavailable reports a not-yet-reachable agent: WebSocket clients can't
	// display HTML, so they get a plain error; browsers get the auto-refreshing
	// connecting page.
	unavailable := func(err error) {
		if isWebSocket {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeConnectingPage(w, id)
	}

	spec, err := resolveControlUISpec(r.Context(), inst.ID)
	if errors.Is(err, agentshim.ErrControlUIUnsupported) {
		// A clean 404 rather than an eternally-spinning "connecting" page.
		writeError(w, http.StatusNotFound, "This agent does not provide a web control UI.")
		return
	}
	if err != nil {
		unavailable(err)
		return
	}

	localPort, err := controlUITunnelPort(r.Context(), inst.ID, spec.Port)
	if err != nil {
		unavailable(err)
		return
	}

	// Browser-facing prefix: /openclaw/{id}/ (kept for existing links). The
	// upstream prefix is the agent's declared base path.
	basePath := fmt.Sprintf("/openclaw/%d/", id)
	wildcardPath := chi.URLParam(r, "*")
	upstreamBase := strings.TrimPrefix(spec.BasePath, "/")
	if upstreamBase != "" && !strings.HasSuffix(upstreamBase, "/") {
		upstreamBase += "/"
	}
	fullPath := upstreamBase + wildcardPath

	if isWebSocket {
		headers := http.Header{}
		for k, v := range spec.Headers {
			headers.Set(k, v)
		}
		if len(spec.Query) > 0 {
			q := r.URL.Query()
			for k, v := range spec.Query {
				q.Set(k, v)
			}
			r.URL.RawQuery = q.Encode()
		}
		websocketProxyToLocalPort(w, r, localPort, fullPath, headers)
		return
	}

	// Try the prefixed path first (e.g. openclaw/26/favicon.svg). If the
	// agent returns 404, retry with just the resource path (e.g. favicon.svg).
	//
	// Why: when an HTML page is served under /openclaw/{id}/ we inject a
	// <base href="/openclaw/{id}/"> tag so relative asset URLs resolve under
	// the proxy prefix. But some resources — notably /favicon.svg, which
	// browsers request automatically from the document root independent of
	// the <base> tag — live at the root of the UI and are NOT served under
	// the base path. Without this fallback those requests 404. The fallback
	// only fires on 404, so correctly-prefixed responses (200, 304,
	// redirects, etc.) are passed through unchanged.
	resp, err := doProxyRequest(r, localPort, fullPath)
	if err != nil {
		writeConnectingPage(w, id)
		return
	}

	if resp.StatusCode == http.StatusNotFound && wildcardPath != "" && upstreamBase != "" {
		// Discard the 404 body and retry against the UI root.
		resp.Body.Close()
		fallbackResp, fbErr := doProxyRequest(r, localPort, wildcardPath)
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
