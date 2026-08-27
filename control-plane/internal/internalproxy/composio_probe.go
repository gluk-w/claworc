// composio_probe.go verifies that a Composio API key carries every permission
// Claworc needs. Composio supports *scoped* project API keys whose permission
// areas are fixed at creation time, and it exposes no introspection endpoint for
// a key's own scopes — so the only way to know is to probe the routes we use.
//
// Read areas are probed with harmless GETs. Write areas are probed with
// deliberately INVALID request bodies: Composio rejects a missing scope with
// 401/403 at the auth layer, while an authorized-but-malformed request comes
// back 400/404/422. An empty body can never produce a valid create, so a passing
// write probe leaves nothing behind at Composio.

package internalproxy

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// permissionProbeTimeout bounds the whole permission check.
const permissionProbeTimeout = 15 * time.Second

// permissionProbeToolSlug is a tool slug that does not exist, used so the
// tool-execution probe can never actually run a tool.
const permissionProbeToolSlug = "CLAWORC_PERMISSION_PROBE"

// PermissionCheck is the outcome of probing one Composio permission area.
type PermissionCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Status int    `json:"status,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// PermissionReport is the full result of CheckPermissions.
type PermissionReport struct {
	OK         bool              `json:"ok"`
	InvalidKey bool              `json:"invalid_key"`
	Error      string            `json:"error,omitempty"`
	Checks     []PermissionCheck `json:"checks"`
}

// writeProbePassed classifies the response to a deliberately-invalid write
// request. 401/403 mean the key lacks the scope; anything else (including a
// validation error) means the request got past the auth layer, so the
// permission is present.
//
// This is the one piece of Composio behaviour the probes depend on — it is
// isolated here so it can be retuned without touching the probe table.
func writeProbePassed(status int) bool {
	return status != http.StatusUnauthorized && status != http.StatusForbidden
}

// composioProbe describes a single permission probe.
type composioProbe struct {
	id     string
	label  string
	method string
	path   string
	body   any
	// passed reports whether the observed status means the permission is granted.
	passed func(status int) bool
}

func statusIs2xx(status int) bool { return status >= 200 && status < 300 }

// toolkitsReadProbe is run first and on its own: a 401 there means the key
// itself is bad, which makes every other probe redundant.
var toolkitsReadProbe = composioProbe{
	id:     "toolkits_read",
	label:  "Toolkits — read",
	method: http.MethodGet,
	path:   "/toolkits?managed_by=composio&sort_by=usage",
	passed: statusIs2xx,
}

// composioProbes are the remaining permission areas Claworc requires. Each maps
// to real call sites — see docs/connections.md.
var composioProbes = []composioProbe{
	{
		id:     "tools_read",
		label:  "Tools — read",
		method: http.MethodGet,
		path:   "/tools?limit=1",
		passed: statusIs2xx,
	},
	{
		id:     "connected_accounts_read",
		label:  "Connected accounts — read",
		method: http.MethodGet,
		path:   "/connected_accounts?limit=1",
		passed: statusIs2xx,
	},
	{
		id:     "auth_configs_write",
		label:  "Auth configs — write",
		method: http.MethodPost,
		path:   "/auth_configs",
		body:   map[string]any{},
		passed: writeProbePassed,
	},
	{
		id:     "connected_accounts_write",
		label:  "Connected accounts — write",
		method: http.MethodPost,
		path:   "/connected_accounts/link",
		body:   map[string]any{},
		passed: writeProbePassed,
	},
	{
		id:     "tool_execution",
		label:  "Tool execution",
		method: http.MethodPost,
		path:   "/tools/execute/" + permissionProbeToolSlug,
		body:   map[string]any{"arguments": map[string]any{}},
		passed: writeProbePassed,
	},
}

// CheckPermissions probes every Composio permission area Claworc depends on and
// reports which ones the key actually grants. It never mutates anything at
// Composio.
func (c *ComposioClient) CheckPermissions(ctx context.Context) PermissionReport {
	ctx, cancel := context.WithTimeout(ctx, permissionProbeTimeout)
	defer cancel()

	first := c.runProbe(ctx, toolkitsReadProbe)
	// A 401 on the very first call means the key is not accepted at all;
	// probing the rest would only repeat the same failure. A 403 is different:
	// the key is valid but lacks the Toolkits scope, so keep going.
	if first.Status == http.StatusUnauthorized {
		return PermissionReport{
			OK:         false,
			InvalidKey: true,
			Error:      "Composio rejected the API key",
			Checks:     []PermissionCheck{first},
		}
	}

	rest := make([]PermissionCheck, len(composioProbes))
	var wg sync.WaitGroup
	for i, p := range composioProbes {
		wg.Add(1)
		go func(i int, p composioProbe) {
			defer wg.Done()
			rest[i] = c.runProbe(ctx, p)
		}(i, p)
	}
	wg.Wait()

	report := PermissionReport{OK: true, Checks: append([]PermissionCheck{first}, rest...)}
	for _, chk := range report.Checks {
		if !chk.OK {
			report.OK = false
			break
		}
	}
	return report
}

// runProbe issues one probe request and classifies the response. Transport
// failures surface as a failed check with status 0 rather than aborting the run.
func (c *ComposioClient) runProbe(ctx context.Context, p composioProbe) PermissionCheck {
	chk := PermissionCheck{ID: p.id, Label: p.label}
	status, _, err := c.do(ctx, p.method, p.path, p.body)
	chk.Status = status
	if status == 0 {
		chk.Detail = probeErrorDetail(err)
		return chk
	}
	chk.OK = p.passed(status)
	if !chk.OK {
		chk.Detail = probeErrorDetail(err)
	}
	return chk
}

// probeErrorDetail renders a short, user-facing reason from a probe error,
// preferring Composio's own message/slug over the raw body.
func probeErrorDetail(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *ComposioAPIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Message != "" && apiErr.Slug != "":
			return apiErr.Message + " (" + apiErr.Slug + ")"
		case apiErr.Message != "":
			return apiErr.Message
		case apiErr.Slug != "":
			return apiErr.Slug
		}
		return truncate(apiErr.Raw, 200)
	}
	return truncate(err.Error(), 200)
}
