// oauth_codex.go: OAuth access-token resolution and refresh for the
// openai-codex-responses api_type (ChatGPT subscription endpoint).
//
// Credentials live on the LLMProvider row (oauth_access_token, oauth_refresh_token,
// oauth_expires_at, oauth_account_id). On each gateway request the access token
// is checked against expiry; if it is within the refresh window, a refresh is
// performed against https://auth.openai.com/oauth/token, the new tokens are
// re-encrypted, and the row is updated. Concurrency is serialized per provider
// id via a per-provider sync.Mutex so a burst of in-flight requests triggers
// only one network refresh.

package llmgateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

const (
	// CodexOAuthClientID is the public OAuth client id used by the openclaw
	// CLI for "openclaw models auth login --provider openai-codex". It is the
	// only client id the auth.openai.com app accepts for this flow.
	CodexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	// CodexOAuthRedirectURI must match a registered redirect URI on the
	// CodexOAuthClientID app. Only http://localhost:1455/auth/callback is
	// accepted. The control plane does NOT bind this port — see oauth_login.go
	// for the manual-paste flow that lets users complete login without
	// reachability to the control plane.
	CodexOAuthRedirectURI = "http://localhost:1455/auth/callback"
	CodexOAuthAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	CodexOAuthTokenURL     = "https://auth.openai.com/oauth/token"
	CodexOAuthScope        = "openid profile email offline_access"

	// codexRefreshSkew is how long before expiry we proactively refresh the
	// access token. Any request landing inside this window triggers a refresh.
	codexRefreshSkew = 60 * time.Second
)

// providerOAuthLocks serializes refreshes per provider id.
var providerOAuthLocks sync.Map // providerID(uint) -> *sync.Mutex

func providerOAuthLock(providerID uint) *sync.Mutex {
	v, _ := providerOAuthLocks.LoadOrStore(providerID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// EnsureFreshOAuthToken returns a non-expired access token (and the
// chatgpt-account-id) for the given provider, refreshing against
// auth.openai.com if necessary. The provider must already have a stored
// refresh token; otherwise an error is returned and the caller should surface
// a 401 with a hint to re-link the account.
func EnsureFreshOAuthToken(ctx context.Context, providerID uint) (access, accountID string, err error) {
	mu := providerOAuthLock(providerID)
	mu.Lock()
	defer mu.Unlock()

	var p database.LLMProvider
	if dbErr := database.DB.First(&p, providerID).Error; dbErr != nil {
		return "", "", fmt.Errorf("provider not found: %w", dbErr)
	}
	if p.OAuthRefreshToken == "" {
		return "", "", fmt.Errorf("ChatGPT account not linked for this provider")
	}

	// Re-read inside the lock for the freshest expires_at; another goroutine
	// holding the same lock may have just refreshed.
	now := time.Now().UnixMilli()
	if p.OAuthExpiresAt-now > codexRefreshSkew.Milliseconds() && p.OAuthAccessToken != "" {
		access, dErr := utils.Decrypt(p.OAuthAccessToken)
		if dErr != nil {
			// fall through to refresh on decrypt failure
		} else {
			return access, p.OAuthAccountID, nil
		}
	}

	refresh, dErr := utils.Decrypt(p.OAuthRefreshToken)
	if dErr != nil {
		return "", "", fmt.Errorf("decrypt refresh token: %w", dErr)
	}

	resp, refErr := refreshCodexToken(ctx, refresh)
	if refErr != nil {
		return "", "", refErr
	}

	encAccess, eErr := utils.Encrypt(resp.AccessToken)
	if eErr != nil {
		return "", "", fmt.Errorf("encrypt access token: %w", eErr)
	}
	encRefresh := p.OAuthRefreshToken
	if resp.RefreshToken != "" && resp.RefreshToken != refresh {
		ec, err := utils.Encrypt(resp.RefreshToken)
		if err != nil {
			return "", "", fmt.Errorf("encrypt refresh token: %w", err)
		}
		encRefresh = ec
	}

	newAccountID := p.OAuthAccountID
	if id := decodeAccountIDFromAccess(resp.AccessToken); id != "" {
		newAccountID = id
	}
	expiresAt := time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second).UnixMilli()

	if uErr := database.DB.Model(&database.LLMProvider{}).
		Where("id = ?", providerID).
		Updates(map[string]interface{}{
			"oauth_access_token":  encAccess,
			"oauth_refresh_token": encRefresh,
			"oauth_expires_at":    expiresAt,
			"oauth_account_id":    newAccountID,
		}).Error; uErr != nil {
		return "", "", fmt.Errorf("persist refreshed tokens: %w", uErr)
	}

	return resp.AccessToken, newAccountID, nil
}

// CodexTokenResponse is the subset of the OAuth token endpoint response we use.
type CodexTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// codexHTTPClient is the HTTP client used to talk to auth.openai.com. Override
// in tests to point at httptest.Server.
var codexHTTPClient = &http.Client{Timeout: 30 * time.Second}

// codexTokenURL is the override-able token endpoint URL. Tests substitute the
// httptest server URL.
var codexTokenURL = CodexOAuthTokenURL

// OverrideCodexTokenURLForTest swaps the token endpoint URL and returns the
// previous value so the caller can restore it. Test-only.
func OverrideCodexTokenURLForTest(u string) string {
	prev := codexTokenURL
	codexTokenURL = u
	return prev
}

func refreshCodexToken(ctx context.Context, refreshToken string) (*CodexTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", CodexOAuthClientID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := codexHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, snippet)
	}

	var out CodexTokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("token endpoint returned empty access_token")
	}
	return &out, nil
}

// ExchangeCodexAuthCode exchanges an authorization code (from the local
// callback) for tokens. Used by the login flow only.
func ExchangeCodexAuthCode(ctx context.Context, code, codeVerifier, redirectURI string) (*CodexTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", CodexOAuthClientID)
	form.Set("code", code)
	form.Set("code_verifier", codeVerifier)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := codexHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, snippet)
	}
	var out CodexTokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("token endpoint returned empty access_token")
	}
	return &out, nil
}

// decodeJWTPayload parses an unverified JWT and returns the decoded claims as
// a generic map. Returns nil on malformed input. We do NOT verify the
// signature because OpenAI itself signed the token and we are only inspecting
// claims for our own use.
func decodeJWTPayload(token string) map[string]interface{} {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// some tokens use standard base64; try that as a fallback
		raw, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil
		}
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil
	}
	return claims
}

// decodeAccountIDFromAccess extracts chatgpt_account_id from the
// "https://api.openai.com/auth" claim of the access JWT. Empty if absent.
func decodeAccountIDFromAccess(accessToken string) string {
	claims := decodeJWTPayload(accessToken)
	if claims == nil {
		return ""
	}
	auth, ok := claims["https://api.openai.com/auth"].(map[string]interface{})
	if !ok {
		return ""
	}
	id, _ := auth["chatgpt_account_id"].(string)
	return id
}

// decodeEmailFromAccess extracts the email from the
// "https://api.openai.com/profile" claim of the access/id JWT. Empty if absent.
func decodeEmailFromAccess(token string) string {
	claims := decodeJWTPayload(token)
	if claims == nil {
		return ""
	}
	if profile, ok := claims["https://api.openai.com/profile"].(map[string]interface{}); ok {
		if email, _ := profile["email"].(string); email != "" {
			return email
		}
	}
	if email, _ := claims["email"].(string); email != "" {
		return email
	}
	return ""
}

// ExtractCodeAndState pulls the OAuth `code` and `state` query parameters out
// of whatever the user pasted. Accepts either a full URL, a leading "?...",
// or a bare "code=...&state=..." query string.
func ExtractCodeAndState(pasted string) (code, state string, err error) {
	pasted = strings.TrimSpace(pasted)
	if pasted == "" {
		return "", "", errors.New("redirect URL is empty")
	}
	if !strings.Contains(pasted, "://") {
		pasted = strings.TrimPrefix(pasted, "?")
		v, perr := url.ParseQuery(pasted)
		if perr != nil {
			return "", "", fmt.Errorf("could not parse pasted text as redirect URL")
		}
		if oauthErr := v.Get("error"); oauthErr != "" {
			return "", "", fmt.Errorf("OpenAI returned error: %s", oauthErr)
		}
		return v.Get("code"), v.Get("state"), nil
	}
	u, perr := url.Parse(pasted)
	if perr != nil {
		return "", "", fmt.Errorf("could not parse pasted URL: %w", perr)
	}
	q := u.Query()
	if oauthErr := q.Get("error"); oauthErr != "" {
		return "", "", fmt.Errorf("OpenAI returned error: %s", oauthErr)
	}
	return q.Get("code"), q.Get("state"), nil
}

// BuildOAuthFieldsFromTokens turns a freshly-exchanged Codex token response
// into the column values to persist on an LLMProvider row: encrypted access
// and refresh tokens, decoded email and account id, and the absolute
// expiry timestamp in milliseconds.
func BuildOAuthFieldsFromTokens(tok *CodexTokenResponse) (
	encAccess, encRefresh, email, accountID string,
	expiresAt int64,
	err error,
) {
	encAccess, err = utils.Encrypt(tok.AccessToken)
	if err != nil {
		return "", "", "", "", 0, fmt.Errorf("encrypt access token: %w", err)
	}
	encRefresh, err = utils.Encrypt(tok.RefreshToken)
	if err != nil {
		return "", "", "", "", 0, fmt.Errorf("encrypt refresh token: %w", err)
	}
	accountID = decodeAccountIDFromAccess(tok.AccessToken)
	email = decodeEmailFromAccess(tok.AccessToken)
	if email == "" && tok.IDToken != "" {
		email = decodeEmailFromAccess(tok.IDToken)
	}
	expiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).UnixMilli()
	return encAccess, encRefresh, email, accountID, expiresAt, nil
}
