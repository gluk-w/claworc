// composio.go implements the /connections/ subtree of the internal proxy: the
// data-plane broker that lets an OpenClaw instance reach Composio's REST API
// without ever holding the Composio API key.
//
// The instance authenticates with its CLAWORC_CONNECTION_SECRET (Bearer). The
// proxy resolves the owning instance, strips the secret, injects the real
// x-api-key and the server-derived user_id, and forwards a narrow allowlist of
// Composio endpoints:
//
//	GET  /connections/tools                 → list tools for the instance's connected toolkits
//	POST /connections/tools/execute/{slug}  → execute a tool
//
// Everything else is rejected. The route is registered on the gateway mux via
// RegisterRoute("/connections/", HandleConnections) in main.go.

package internalproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// ConnectionsPrefix is the gateway mux route prefix for the Composio broker.
const ConnectionsPrefix = "/connections/"

// composioHTTPClient is the upstream client used by the proxy. 300s timeout to
// accommodate slow tool executions, mirroring the LLM gateway's client.
var composioHTTPClient = &http.Client{Timeout: 300 * time.Second}

// ComposioUserID derives the stable, non-enumerable Composio user_id for an
// instance. All of an instance's connected accounts live under this user_id.
func ComposioUserID(inst database.Instance) string {
	return "claworc-inst-" + inst.UUID
}

// composioAPIKey returns the decrypted global Composio API key, or an error if
// none is configured.
func composioAPIKey() (string, error) {
	enc, err := database.GetSetting("composio_api_key")
	if err != nil || enc == "" {
		return "", fmt.Errorf("composio api key not configured")
	}
	key, err := utils.Decrypt(enc)
	if err != nil || key == "" {
		return "", fmt.Errorf("composio api key unreadable")
	}
	return key, nil
}

func extractConnectionSecret(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer claworc-cs-") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if v := r.Header.Get("x-api-key"); strings.HasPrefix(v, "claworc-cs-") {
		return v
	}
	return ""
}

func connectionsError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": msg}})
}

// HandleConnections is the entrypoint for all /connections/ requests.
func HandleConnections(w http.ResponseWriter, r *http.Request) {
	secret := extractConnectionSecret(r)
	if secret == "" {
		connectionsError(w, http.StatusUnauthorized, "missing connection secret")
		return
	}
	inst, err := resolveInstanceBySecret(secret)
	if err != nil {
		log.Printf("[connections] auth failed: %s path=%s", err, safeLog(r.URL.Path))
		connectionsError(w, http.StatusUnauthorized, "authentication failed")
		return
	}

	apiKey, err := composioAPIKey()
	if err != nil {
		connectionsError(w, http.StatusServiceUnavailable, "composio not configured")
		return
	}

	userID := ComposioUserID(*inst)
	sub := strings.TrimPrefix(r.URL.Path, ConnectionsPrefix) // e.g. "tools" or "tools/execute/GMAIL_SEND_EMAIL"

	switch {
	case r.Method == http.MethodGet && sub == "tools":
		handleListTools(w, r, inst.ID, userID, apiKey)
	case r.Method == http.MethodPost && strings.HasPrefix(sub, "tools/execute/"):
		slug := strings.TrimPrefix(sub, "tools/execute/")
		if slug == "" || strings.Contains(slug, "/") {
			connectionsError(w, http.StatusBadRequest, "invalid tool slug")
			return
		}
		handleExecuteTool(w, r, slug, userID, apiKey)
	default:
		connectionsError(w, http.StatusNotFound, "not found")
	}
}

// handleListTools forwards GET /tools to Composio, scoped to the instance's
// ACTIVE connected toolkits and user_id.
func handleListTools(w http.ResponseWriter, r *http.Request, instanceID uint, userID, apiKey string) {
	slugs := activeToolkitSlugs(instanceID)
	if len(slugs) == 0 {
		// No connections → no tools. Avoid leaking the whole catalog.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write([]byte(`{"items":[]}`))
		return
	}
	q := url.Values{
		"user_id":       {userID},
		"toolkit_slugs": {strings.Join(slugs, ",")},
	}
	forwardToComposio(w, r.Context(), http.MethodGet, "/tools?"+q.Encode(), nil, apiKey)
}

// handleExecuteTool forwards POST /tools/execute/{slug}, injecting user_id into
// the request body and ignoring any client-supplied user_id/connected_account_id.
func handleExecuteTool(w http.ResponseWriter, r *http.Request, slug, userID, apiKey string) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		connectionsError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	payload := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			connectionsError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	if _, ok := payload["arguments"]; !ok {
		payload["arguments"] = map[string]any{}
	}
	// Server controls identity — never trust the client for these.
	delete(payload, "connected_account_id")
	payload["user_id"] = userID

	body, _ := json.Marshal(payload)
	forwardToComposio(w, r.Context(), http.MethodPost, "/tools/execute/"+url.PathEscape(slug), body, apiKey)
}

// forwardToComposio performs the upstream request with the real x-api-key and
// copies the JSON response back to the client.
func forwardToComposio(w http.ResponseWriter, ctx context.Context, method, path string, body []byte, apiKey string) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	// Use context.Background so a client disconnect doesn't sever an in-flight
	// tool execution (mirrors the LLM gateway).
	req, err := http.NewRequestWithContext(context.Background(), method, ComposioBaseURL+path, rdr)
	if err != nil {
		connectionsError(w, http.StatusInternalServerError, "failed to build upstream request")
		return
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := composioHTTPClient.Do(req)
	if err != nil {
		connectionsError(w, http.StatusBadGateway, "upstream request failed")
		return
	}
	defer resp.Body.Close()

	// Only relay JSON; reject anything else to avoid content sniffing surprises.
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/json") {
		connectionsError(w, http.StatusBadGateway, "unexpected upstream content type")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 8<<20))
}

// activeToolkitSlugs returns the distinct toolkit slugs of an instance's ACTIVE
// connections.
func activeToolkitSlugs(instanceID uint) []string {
	var conns []database.ComposioConnection
	database.DB.Where("instance_id = ? AND status = ?", instanceID, "ACTIVE").Find(&conns)
	seen := map[string]bool{}
	var out []string
	for _, c := range conns {
		if c.ToolkitSlug != "" && !seen[c.ToolkitSlug] {
			seen[c.ToolkitSlug] = true
			out = append(out, c.ToolkitSlug)
		}
	}
	return out
}
