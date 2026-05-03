package llmgateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// makeAccessJWT builds a fake unsigned JWT whose payload contains the OpenAI
// claims our refresh code reads. The signature segment is junk because we
// never verify it — only the payload is parsed.
func makeAccessJWT(t *testing.T, accountID, email string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	claims := map[string]interface{}{
		"https://api.openai.com/auth": map[string]interface{}{
			"chatgpt_account_id": accountID,
		},
		"https://api.openai.com/profile": map[string]interface{}{
			"email": email,
		},
	}
	payloadBytes, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload + ".sig"
}

// stubCodexTokenServer returns an httptest server whose /oauth/token endpoint
// answers refresh_token grant requests with the given access/refresh/expires_in.
// hits is incremented atomically on each request — used to assert mutex
// serialization. The returned cleanup function restores codexTokenURL.
func stubCodexTokenServer(t *testing.T, accessToken, refreshToken string, expiresIn int, hits *int32) (*httptest.Server, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if r.Form.Get("grant_type") != "refresh_token" && r.Form.Get("grant_type") != "authorization_code" {
			http.Error(w, "bad grant_type", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":%q,"expires_in":%d,"token_type":"Bearer"}`,
			accessToken, refreshToken, expiresIn)
	}))
	prev := codexTokenURL
	codexTokenURL = srv.URL
	cleanup := func() {
		srv.Close()
		codexTokenURL = prev
		// Reset per-provider locks across tests.
		providerOAuthLocks = sync.Map{}
	}
	return srv, cleanup
}

func mustOAuthProvider(t *testing.T, refresh, access string, expiresAt int64, accountID string) database.LLMProvider {
	t.Helper()
	p := database.LLMProvider{
		Key: "openai-codex", Name: "Codex",
		APIType: "openai-codex-responses",
		BaseURL: "https://chatgpt.com/backend-api",
	}
	if err := database.DB.Create(&p).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	encA, _ := utils.Encrypt(access)
	encR, _ := utils.Encrypt(refresh)
	if err := database.DB.Model(&database.LLMProvider{}).Where("id=?", p.ID).Updates(map[string]interface{}{
		"oauth_access_token":  encA,
		"oauth_refresh_token": encR,
		"oauth_expires_at":    expiresAt,
		"oauth_account_id":    accountID,
	}).Error; err != nil {
		t.Fatalf("set oauth columns: %v", err)
	}
	database.DB.First(&p, p.ID)
	return p
}

func TestEnsureFreshOAuthToken_NotExpired(t *testing.T) {
	setupDB(t)
	var hits int32
	_, cleanup := stubCodexTokenServer(t, "new-access", "new-refresh", 3600, &hits)
	defer cleanup()

	expiresAt := time.Now().Add(10 * time.Minute).UnixMilli()
	p := mustOAuthProvider(t, "old-refresh", "old-access", expiresAt, "acct-1")

	access, accountID, err := EnsureFreshOAuthToken(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("EnsureFreshOAuthToken: %v", err)
	}
	if access != "old-access" {
		t.Errorf("access: got %q, want old-access (no refresh expected)", access)
	}
	if accountID != "acct-1" {
		t.Errorf("accountID: got %q, want acct-1", accountID)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("token endpoint should not be hit; got %d calls", got)
	}
}

func TestEnsureFreshOAuthToken_RefreshesExpired(t *testing.T) {
	setupDB(t)
	newAccess := makeAccessJWT(t, "acct-rotated", "user@example.com")
	var hits int32
	_, cleanup := stubCodexTokenServer(t, newAccess, "next-refresh", 3600, &hits)
	defer cleanup()

	// expired
	expiresAt := time.Now().Add(-1 * time.Minute).UnixMilli()
	p := mustOAuthProvider(t, "old-refresh", "old-access", expiresAt, "acct-old")

	access, accountID, err := EnsureFreshOAuthToken(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("EnsureFreshOAuthToken: %v", err)
	}
	if access != newAccess {
		t.Errorf("access: got %q, want %q", access, newAccess)
	}
	if accountID != "acct-rotated" {
		t.Errorf("accountID: got %q, want acct-rotated (re-decoded from JWT)", accountID)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("token endpoint hits: got %d, want 1", got)
	}

	// Verify DB row updated and tokens are re-encrypted (decryptable).
	var p2 database.LLMProvider
	database.DB.First(&p2, p.ID)
	if p2.OAuthAccountID != "acct-rotated" {
		t.Errorf("DB account_id: got %q, want acct-rotated", p2.OAuthAccountID)
	}
	if p2.OAuthExpiresAt <= time.Now().UnixMilli() {
		t.Errorf("DB expires_at not advanced: %d", p2.OAuthExpiresAt)
	}
	dec, err := utils.Decrypt(p2.OAuthAccessToken)
	if err != nil || dec != newAccess {
		t.Errorf("re-encrypted access token mismatch: dec=%q err=%v", dec, err)
	}
	dec2, err := utils.Decrypt(p2.OAuthRefreshToken)
	if err != nil || dec2 != "next-refresh" {
		t.Errorf("re-encrypted refresh token mismatch: dec=%q err=%v", dec2, err)
	}
}

func TestEnsureFreshOAuthToken_LockSerializesConcurrent(t *testing.T) {
	setupDB(t)
	var hits int32
	// Slow server to widen the race window.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"new-access","refresh_token":"next-refresh","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer srv.Close()
	prev := codexTokenURL
	codexTokenURL = srv.URL
	defer func() {
		codexTokenURL = prev
		providerOAuthLocks = sync.Map{}
	}()

	p := mustOAuthProvider(t, "old-refresh", "old-access", time.Now().Add(-time.Minute).UnixMilli(), "acct")

	const N = 5
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_, _, err := EnsureFreshOAuthToken(context.Background(), p.ID)
			if err != nil {
				t.Errorf("EnsureFreshOAuthToken: %v", err)
			}
		}()
	}
	wg.Wait()

	// Only the first goroutine should have called the token endpoint;
	// subsequent goroutines see the freshly-refreshed expiry and skip.
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("token endpoint hits: got %d, want 1 (lock should serialize and short-circuit)", got)
	}
}

func TestEnsureFreshOAuthToken_NotLinked(t *testing.T) {
	setupDB(t)
	p := database.LLMProvider{Key: "x", Name: "x", APIType: "openai-codex-responses", BaseURL: "https://chatgpt.com/backend-api"}
	database.DB.Create(&p)
	_, _, err := EnsureFreshOAuthToken(context.Background(), p.ID)
	if err == nil {
		t.Fatal("expected error when refresh token absent")
	}
}

func TestCodexResponses_SetAuthHeader(t *testing.T) {
	at := GetAPIType("openai-codex-responses")
	req, _ := http.NewRequest("POST", "https://chatgpt.com/backend-api/codex/responses", nil)
	at.SetAuthHeader(req, AuthMaterial{OAuthAccess: "the-access", OAuthAccount: "acct-123"})

	if got := req.Header.Get("Authorization"); got != "Bearer the-access" {
		t.Errorf("Authorization: got %q, want Bearer the-access", got)
	}
	if got := req.Header.Get("chatgpt-account-id"); got != "acct-123" {
		t.Errorf("chatgpt-account-id: got %q, want acct-123", got)
	}
	if got := req.Header.Get("OpenAI-Beta"); got != "responses=experimental" {
		t.Errorf("OpenAI-Beta: got %q", got)
	}
	if got := req.Header.Get("originator"); got != "pi" {
		t.Errorf("originator: got %q", got)
	}
}

// TestGateway_ProxiesCodexWithOAuthHeaders is an integration test that runs
// the full proxy pipeline: claworc-vk-* token in → OAuth refresh → upstream
// hit with the right headers. It verifies the original `Authorization: Bearer
// claworc-vk-*` is stripped and replaced with `Bearer <oauth-access>`.
func TestGateway_ProxiesCodexWithOAuthHeaders(t *testing.T) {
	setupDB(t)

	// Stub auth.openai.com
	newAccess := makeAccessJWT(t, "acct-int", "u@x.com")
	var refreshHits int32
	_, cleanup := stubCodexTokenServer(t, newAccess, "next-refresh", 3600, &refreshHits)
	defer cleanup()

	// Stub chatgpt.com/backend-api
	type capture struct {
		Authorization string
		AccountID     string
		OpenAIBeta    string
		Originator    string
		HasVK         bool
	}
	var got capture
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Authorization = r.Header.Get("Authorization")
		got.AccountID = r.Header.Get("chatgpt-account-id")
		got.OpenAIBeta = r.Header.Get("OpenAI-Beta")
		got.Originator = r.Header.Get("originator")
		got.HasVK = false
		// "Bearer claworc-vk-..." should never reach the upstream.
		if v := r.Header.Get("Authorization"); len(v) > 17 && v[:17] == "Bearer claworc-vk" {
			got.HasVK = true
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","model":"gpt-5.3-codex"}`)
	}))
	defer upstream.Close()

	// Provider points its base URL at the stub upstream so the gateway proxies there.
	p := mustOAuthProvider(t, "old-refresh", "old-access",
		time.Now().Add(-1*time.Minute).UnixMilli(), "acct-old")
	database.DB.Model(&p).Update("base_url", upstream.URL)
	token := mustGatewayKey(t, 7, p.ID)

	// Spin up the gateway handler in-process.
	ts := httptest.NewServer(http.HandlerFunc(handleProxy))
	defer ts.Close()

	body := []byte(`{"model":"gpt-5.3-codex","input":[]}`)
	req, _ := http.NewRequest("POST", ts.URL+"/codex/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("upstream status: %d", resp.StatusCode)
	}

	if got.Authorization != "Bearer "+newAccess {
		t.Errorf("upstream Authorization: got %q, want Bearer %q", got.Authorization, newAccess)
	}
	if got.AccountID != "acct-int" {
		t.Errorf("upstream chatgpt-account-id: got %q, want acct-int", got.AccountID)
	}
	if got.OpenAIBeta != "responses=experimental" {
		t.Errorf("upstream OpenAI-Beta: %q", got.OpenAIBeta)
	}
	if got.Originator != "pi" {
		t.Errorf("upstream originator: %q", got.Originator)
	}
	if got.HasVK {
		t.Error("claworc-vk-* token leaked to upstream")
	}
	if atomic.LoadInt32(&refreshHits) != 1 {
		t.Errorf("expected exactly one refresh, got %d", refreshHits)
	}
}

var _ = strings.Builder{}
