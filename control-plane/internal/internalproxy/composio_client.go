// composio_client.go is the control-plane-side Composio REST client used by the
// connection wizard handlers (NOT by instances). It talks to the Composio API
// with the real x-api-key. Instances never see this client or the key — they
// reach Composio only through the /connections/ proxy (see composio.go).
//
// NOTE: Composio's public docs are inconsistent about a few path/field names
// (e.g. auth_configs vs auth-configs, the id field on a link response). The base
// URL and paths are centralized here and responses are parsed leniently so they
// can be adjusted against the live API without touching call sites.

package internalproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ComposioBaseURL is the Composio REST API base. Overridable in tests.
var ComposioBaseURL = "https://backend.composio.dev/api/v3"

// ComposioClient is a thin REST client for the Composio API.
type ComposioClient struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// NewComposioClient builds a client with the given (real) Composio API key.
func NewComposioClient(apiKey string) *ComposioClient {
	return &ComposioClient{
		apiKey:  apiKey,
		baseURL: ComposioBaseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Toolkit is a connectable Composio integration (Gmail, Google Analytics, …).
type Toolkit struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Logo string `json:"logo,omitempty"`
}

// ConnectedAccount is the status view of a Composio connected account.
type ConnectedAccount struct {
	ID     string
	Status string
	Label  string
}

func (c *ComposioClient) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, respBody, parseComposioError(method, path, resp.StatusCode, respBody)
	}
	return resp.StatusCode, respBody, nil
}

// ComposioAPIError is a structured Composio error response. Slug carries
// Composio's machine-readable error identifier (e.g.
// "Auth_Config_DefaultAuthConfigNotFound") so callers can branch on it.
type ComposioAPIError struct {
	Status  int
	Slug    string
	Message string
	Raw     string
}

func (e *ComposioAPIError) Error() string {
	if e.Slug != "" {
		return fmt.Sprintf("composio: %s (%s, status %d)", e.Message, e.Slug, e.Status)
	}
	return fmt.Sprintf("composio: status %d: %s", e.Status, truncate(e.Raw, 300))
}

// ComposioErrorSlug returns Composio's error slug from err, or "" if err is not
// a *ComposioAPIError.
func ComposioErrorSlug(err error) string {
	var apiErr *ComposioAPIError
	if errors.As(err, &apiErr) {
		return apiErr.Slug
	}
	return ""
}

func parseComposioError(method, path string, status int, body []byte) error {
	var env struct {
		Error struct {
			Message string `json:"message"`
			Slug    string `json:"slug"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	return &ComposioAPIError{
		Status:  status,
		Slug:    env.Error.Slug,
		Message: env.Error.Message,
		Raw:     string(body),
	}
}

// ListOAuthToolkits returns Composio-managed (OAuth) toolkits available to the project.
func (c *ComposioClient) ListOAuthToolkits(ctx context.Context) ([]Toolkit, error) {
	q := url.Values{"managed_by": {"composio"}, "sort_by": {"usage"}}
	_, body, err := c.do(ctx, http.MethodGet, "/toolkits?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	// Tolerate {items:[...]} or {data:[...]} or a bare array, and a nested logo.
	var env struct {
		Items []json.RawMessage `json:"items"`
		Data  []json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(body, &env)
	raws := env.Items
	if len(raws) == 0 {
		raws = env.Data
	}
	if len(raws) == 0 {
		_ = json.Unmarshal(body, &raws)
	}
	out := make([]Toolkit, 0, len(raws))
	for _, r := range raws {
		var t struct {
			Slug string `json:"slug"`
			Name string `json:"name"`
			Logo string `json:"logo"`
			Meta struct {
				Logo string `json:"logo"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(r, &t); err != nil {
			continue
		}
		logo := t.Logo
		if logo == "" {
			logo = t.Meta.Logo
		}
		if t.Slug == "" {
			continue
		}
		out = append(out, Toolkit{Slug: t.Slug, Name: firstNonEmpty(t.Name, t.Slug), Logo: logo})
	}
	return out, nil
}

// CreateAuthConfig creates a Composio-managed auth config for a toolkit and
// returns its id.
func (c *ComposioClient) CreateAuthConfig(ctx context.Context, toolkitSlug string) (string, error) {
	reqBody := map[string]any{
		"toolkit": map[string]any{"slug": toolkitSlug},
		"auth_config": map[string]any{
			"type": "use_composio_managed_auth",
			"name": "claworc-" + toolkitSlug,
		},
	}
	_, body, err := c.do(ctx, http.MethodPost, "/auth_configs", reqBody)
	if err != nil {
		return "", err
	}
	id := extractID(body, "auth_config_id", "id")
	if id == "" {
		return "", fmt.Errorf("composio: auth config response missing id: %s", truncate(string(body), 200))
	}
	return id, nil
}

// InitiateConnection starts an OAuth connection for a user and returns the
// connected-account id plus the hosted redirect URL the user must visit.
func (c *ComposioClient) InitiateConnection(ctx context.Context, userID, authConfigID, callbackURL string) (connectedAccountID, redirectURL string, err error) {
	reqBody := map[string]any{
		"user_id":        userID,
		"auth_config_id": authConfigID,
	}
	if callbackURL != "" {
		reqBody["callback_url"] = callbackURL
	}
	_, body, err := c.do(ctx, http.MethodPost, "/connected_accounts/link", reqBody)
	if err != nil {
		return "", "", err
	}
	connectedAccountID = extractID(body, "connected_account_id", "id", "connectedAccountId")
	redirectURL = extractID(body, "redirect_url", "redirectUrl", "redirect_uri")
	if redirectURL == "" {
		return "", "", fmt.Errorf("composio: link response missing redirect_url: %s", truncate(string(body), 200))
	}
	return connectedAccountID, redirectURL, nil
}

// GetConnectedAccount fetches the current status of a connected account.
func (c *ComposioClient) GetConnectedAccount(ctx context.Context, id string) (*ConnectedAccount, error) {
	_, body, err := c.do(ctx, http.MethodGet, "/connected_accounts/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	acct := &ConnectedAccount{
		ID:     id,
		Status: strings.ToUpper(stringField(raw, "status")),
		Label:  firstNonEmpty(stringField(raw, "label"), stringField(raw, "name"), stringField(raw, "email")),
	}
	return acct, nil
}

// DeleteConnectedAccount removes a connected account at Composio.
func (c *ComposioClient) DeleteConnectedAccount(ctx context.Context, id string) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/connected_accounts/"+url.PathEscape(id), nil)
	return err
}

// --- small JSON helpers -----------------------------------------------------

func extractID(body []byte, keys ...string) string {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return ""
	}
	for _, k := range keys {
		if v := stringField(raw, k); v != "" {
			return v
		}
	}
	// Some responses nest under "connectionRequest" / "data" / "auth_config".
	for _, nestKey := range []string{"connectionRequest", "connection_request", "data", "auth_config"} {
		if nested, ok := raw[nestKey].(map[string]any); ok {
			for _, k := range keys {
				if v := stringField(nested, k); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
