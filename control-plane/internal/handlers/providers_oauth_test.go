package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/llmgateway"
)

// setupProvidersTestDB augments the shared setupTestDB with the LLMProvider
// table that providers handlers operate on.
func setupProvidersTestDB(t *testing.T) {
	t.Helper()
	setupTestDB(t)
	if err := database.DB.AutoMigrate(&database.LLMProvider{}); err != nil {
		t.Fatalf("auto-migrate LLMProvider: %v", err)
	}
}

// stubTokenServer points the llmgateway token endpoint at a local httptest
// server with the given handler. Returns a cleanup that restores the URL.
func stubTokenServer(t *testing.T, h http.HandlerFunc) func() {
	t.Helper()
	srv := httptest.NewServer(h)
	prev := llmgateway.OverrideCodexTokenURLForTest(srv.URL)
	return func() {
		srv.Close()
		llmgateway.OverrideCodexTokenURLForTest(prev)
	}
}

// makeFakeAccessJWT builds an unsigned JWT containing the OpenAI claims
// BuildOAuthFieldsFromTokens reads (chatgpt_account_id and email).
func makeFakeAccessJWT(t *testing.T, accountID, email string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
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

func TestCreateProvider_OAuth_Success(t *testing.T) {
	setupProvidersTestDB(t)
	access := makeFakeAccessJWT(t, "acct-123", "user@example.com")
	cleanup := stubTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" {
			http.Error(w, "bad grant_type", 400)
			return
		}
		if r.Form.Get("code") == "" || r.Form.Get("code_verifier") == "" {
			http.Error(w, "missing pkce", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"refresh-tok","expires_in":3600,"token_type":"Bearer"}`, access)
	})
	defer cleanup()

	body := map[string]interface{}{
		"key":      "openai-codex",
		"provider": "openai-codex",
		"name":     "OpenAI Codex",
		"base_url": "https://chatgpt.com/backend-api",
		"api_type": "openai-codex-responses",
		"oauth": map[string]string{
			"code_verifier": "verifier-abc",
			"redirect_url":  "http://localhost:1455/auth/callback?code=auth-code-xyz&state=somestate",
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/llm/providers", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	CreateProvider(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var p database.LLMProvider
	if err := database.DB.First(&p, "key = ?", "openai-codex").Error; err != nil {
		t.Fatalf("provider not created: %v", err)
	}
	if p.OAuthAccessToken == "" || p.OAuthRefreshToken == "" {
		t.Errorf("oauth tokens not stored: access=%q refresh=%q", p.OAuthAccessToken, p.OAuthRefreshToken)
	}
	if p.OAuthEmail != "user@example.com" {
		t.Errorf("oauth email = %q want user@example.com", p.OAuthEmail)
	}
	if p.OAuthAccountID != "acct-123" {
		t.Errorf("oauth account id = %q want acct-123", p.OAuthAccountID)
	}
	if p.OAuthExpiresAt <= 0 {
		t.Errorf("oauth expires_at = %d want > 0", p.OAuthExpiresAt)
	}
}

func TestCreateProvider_OAuth_TokenEndpointFailure_NoRow(t *testing.T) {
	setupProvidersTestDB(t)
	cleanup := stubTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid_grant", http.StatusBadRequest)
	})
	defer cleanup()

	body := map[string]interface{}{
		"key":      "openai-codex",
		"provider": "openai-codex",
		"name":     "OpenAI Codex",
		"base_url": "https://chatgpt.com/backend-api",
		"api_type": "openai-codex-responses",
		"oauth": map[string]string{
			"code_verifier": "verifier-abc",
			"redirect_url":  "http://localhost:1455/auth/callback?code=auth-code-xyz&state=somestate",
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/llm/providers", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	CreateProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	var count int64
	database.DB.Model(&database.LLMProvider{}).Count(&count)
	if count != 0 {
		t.Errorf("provider rows = %d, want 0", count)
	}
}

func TestCreateProvider_OAuth_MissingFields_NoRow(t *testing.T) {
	setupProvidersTestDB(t)

	body := map[string]interface{}{
		"key":      "openai-codex",
		"provider": "openai-codex",
		"name":     "OpenAI Codex",
		"base_url": "https://chatgpt.com/backend-api",
		"api_type": "openai-codex-responses",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/llm/providers", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	CreateProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	var count int64
	database.DB.Model(&database.LLMProvider{}).Count(&count)
	if count != 0 {
		t.Errorf("provider rows = %d, want 0", count)
	}
}

func TestCreateProvider_OAuth_RedirectURLMissingCode_NoRow(t *testing.T) {
	setupProvidersTestDB(t)

	body := map[string]interface{}{
		"key":      "openai-codex",
		"provider": "openai-codex",
		"name":     "OpenAI Codex",
		"base_url": "https://chatgpt.com/backend-api",
		"api_type": "openai-codex-responses",
		"oauth": map[string]string{
			"code_verifier": "verifier-abc",
			"redirect_url":  "http://localhost:1455/auth/callback?state=somestate",
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/llm/providers", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	CreateProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	var count int64
	database.DB.Model(&database.LLMProvider{}).Count(&count)
	if count != 0 {
		t.Errorf("provider rows = %d, want 0", count)
	}
}
