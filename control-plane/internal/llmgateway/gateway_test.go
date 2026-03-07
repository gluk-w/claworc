package llmgateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// setupDB initialises an in-memory SQLite DB and points database.DB at it.
func setupDB(t *testing.T) {
	t.Helper()
	var err error
	database.DB, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open in-memory DB: %v", err)
	}
	if err := database.DB.AutoMigrate(
		&database.Setting{},
		&database.LLMProvider{},
		&database.LLMGatewayKey{},
		&database.LLMRequestLog{},
		&database.InstanceAPIKey{},
	); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
}

// mustProvider creates an LLMProvider and returns it.
func mustProvider(t *testing.T, key, apiType, baseURL string) database.LLMProvider {
	t.Helper()
	p := database.LLMProvider{Key: key, Name: key, APIType: apiType, BaseURL: baseURL}
	if err := database.DB.Create(&p).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return p
}

// mustGatewayKey creates an LLMGatewayKey and returns the gateway token.
func mustGatewayKey(t *testing.T, instanceID, providerID uint) string {
	t.Helper()
	token := fmt.Sprintf("claworc-vk-test-%d-%d", instanceID, providerID)
	row := database.LLMGatewayKey{InstanceID: instanceID, ProviderID: providerID, GatewayKey: token}
	if err := database.DB.Create(&row).Error; err != nil {
		t.Fatalf("create gateway key: %v", err)
	}
	return token
}

// mustAPIKey encrypts realKey and stores it as a per-instance override.
func mustAPIKey(t *testing.T, instanceID uint, providerKey, realKey string) {
	t.Helper()
	keyName := strings.ToUpper(strings.ReplaceAll(providerKey, "-", "_")) + "_API_KEY"
	enc, err := utils.Encrypt(realKey)
	if err != nil {
		t.Fatalf("encrypt API key: %v", err)
	}
	row := database.InstanceAPIKey{InstanceID: instanceID, KeyName: keyName, KeyValue: enc}
	if err := database.DB.Create(&row).Error; err != nil {
		t.Fatalf("create instance API key: %v", err)
	}
}

// mustGlobalAPIKey encrypts realKey and stores it as a global setting.
func mustGlobalAPIKey(t *testing.T, providerKey, realKey string) {
	t.Helper()
	keyName := strings.ToUpper(strings.ReplaceAll(providerKey, "-", "_")) + "_API_KEY"
	enc, err := utils.Encrypt(realKey)
	if err != nil {
		t.Fatalf("encrypt global API key: %v", err)
	}
	if err := database.SetSetting("api_key:"+keyName, enc); err != nil {
		t.Fatalf("set global API key setting: %v", err)
	}
}

// doRequest sends a request through handleProxy using httptest.ResponseRecorder.
func doRequest(t *testing.T, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	handleProxy(rr, req)
	return rr
}

// --- 1. Auth extraction — all four formats accepted ---

func TestAuthExtraction_AllFormats(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	formats := []struct {
		name    string
		headers map[string]string
		query   string
	}{
		{"Authorization Bearer", map[string]string{}, ""},
		{"x-api-key", map[string]string{}, ""},
		{"x-goog-api-key", map[string]string{}, ""},
		{"query key param", map[string]string{}, ""},
	}

	for _, tc := range formats {
		t.Run(tc.name, func(t *testing.T) {
			setupDB(t)
			p := mustProvider(t, "test-provider", "openai-completions", upstream.URL)
			token := mustGatewayKey(t, 1, p.ID)
			mustAPIKey(t, 1, "test-provider", "real-key")
			upstreamCalled = false

			var req *http.Request
			switch tc.name {
			case "Authorization Bearer":
				req = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
				req.Header.Set("Authorization", "Bearer "+token)
			case "x-api-key":
				req = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
				req.Header.Set("x-api-key", token)
			case "x-goog-api-key":
				req = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
				req.Header.Set("x-goog-api-key", token)
			case "query key param":
				req = httptest.NewRequest("POST", "/v1/chat/completions?key="+token, strings.NewReader(`{}`))
			}
			req.Header.Set("Content-Type", "application/json")

			rr := httptest.NewRecorder()
			handleProxy(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", rr.Code)
			}
			if !upstreamCalled {
				t.Error("upstream was not called")
			}
		})
	}
}

// --- 2. Missing / invalid token → 401 ---

func TestAuth_MissingOrInvalid(t *testing.T) {
	setupDB(t)

	cases := []struct {
		name    string
		headers map[string]string
		path    string
	}{
		{"no auth", map[string]string{}, "/v1/chat/completions"},
		{"non-gw bearer", map[string]string{"Authorization": "Bearer regular-key"}, "/v1/chat/completions"},
		{"non-gw x-api-key", map[string]string{"x-api-key": "sk-ant-not-a-gw-token"}, "/v1/chat/completions"},
		{"valid prefix not in DB", map[string]string{"Authorization": "Bearer claworc-vk-nonexistent"}, "/v1/chat/completions"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", tc.path, strings.NewReader(`{}`))
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rr := httptest.NewRecorder()
			handleProxy(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", rr.Code)
			}
		})
	}
}

// --- 3. Outgoing auth header by apiType ---

func TestOutgoingAuthHeader_ByAPIType(t *testing.T) {
	cases := []struct {
		apiType        string
		expectedHeader string
		expectedValue  string
	}{
		{"openai-completions", "Authorization", "Bearer real-key"},
		{"anthropic-messages", "x-api-key", "real-key"},
		{"google-generative-ai", "x-goog-api-key", "real-key"},
		{"", "Authorization", "Bearer real-key"}, // empty → default
	}

	for _, tc := range cases {
		t.Run(tc.apiType+"/"+tc.expectedHeader, func(t *testing.T) {
			var capturedReq *http.Request
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedReq = r
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			}))
			defer upstream.Close()

			setupDB(t)
			apiType := tc.apiType
			if apiType == "" {
				// store empty string to test default fallback
			}
			p := mustProvider(t, "prov", apiType, upstream.URL)
			token := mustGatewayKey(t, 1, p.ID)
			mustAPIKey(t, 1, "prov", "real-key")

			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			handleProxy(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rr.Code)
			}
			if capturedReq == nil {
				t.Fatal("upstream never received request")
			}
			got := capturedReq.Header.Get(tc.expectedHeader)
			if got != tc.expectedValue {
				t.Errorf("header %q: got %q, want %q", tc.expectedHeader, got, tc.expectedValue)
			}
		})
	}
}

// --- 4. Incoming auth headers are stripped ---

func TestIncomingAuthHeaders_Stripped(t *testing.T) {
	var capturedReq *http.Request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	setupDB(t)
	p := mustProvider(t, "prov", "openai-completions", upstream.URL)
	token := mustGatewayKey(t, 1, p.ID)
	mustAPIKey(t, 1, "prov", "real-key")

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("x-api-key", "evil-key")
	req.Header.Set("x-goog-api-key", "also-evil")
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handleProxy(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	// Gateway token must NOT be forwarded
	if capturedReq.Header.Get("Authorization") == "Bearer "+token {
		t.Error("gateway token was forwarded upstream")
	}
	// x-api-key and x-goog-api-key sent by client must be stripped
	if capturedReq.Header.Get("x-api-key") == "evil-key" {
		t.Error("x-api-key was forwarded upstream")
	}
	if capturedReq.Header.Get("x-goog-api-key") == "also-evil" {
		t.Error("x-goog-api-key was forwarded upstream")
	}
	// Correct outgoing auth must be set
	if capturedReq.Header.Get("Authorization") != "Bearer real-key" {
		t.Errorf("expected Authorization: Bearer real-key, got %q", capturedReq.Header.Get("Authorization"))
	}
}

// --- 5. URL construction — /v1 deduplication ---

func TestURL_V1Deduplication(t *testing.T) {
	// When baseURL ends with /v1 and request path starts with /v1, the gateway must
	// strip the leading /v1 from the path to avoid sending /v1/v1/... to the upstream.
	t.Run("baseURL ends with /v1, path starts with /v1 — no double /v1", func(t *testing.T) {
		var capturedPath string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		}))
		defer upstream.Close()

		setupDB(t)
		p := mustProvider(t, "prov", "openai-completions", upstream.URL+"/v1")
		token := mustGatewayKey(t, 1, p.ID)
		mustAPIKey(t, 1, "prov", "real-key")

		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handleProxy(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		// Upstream URL is baseURL+"/chat/completions" = "http://upstream/v1/chat/completions",
		// so upstream sees path "/v1/chat/completions" — importantly NOT "/v1/v1/chat/completions".
		if strings.Contains(capturedPath, "/v1/v1/") {
			t.Errorf("double /v1 detected in upstream path: %q", capturedPath)
		}
		if capturedPath != "/v1/chat/completions" {
			t.Errorf("unexpected upstream path: got %q, want /v1/chat/completions", capturedPath)
		}
	})

	t.Run("baseURL no /v1 suffix — path forwarded as-is", func(t *testing.T) {
		var capturedPath string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		}))
		defer upstream.Close()

		setupDB(t)
		p := mustProvider(t, "prov", "openai-completions", upstream.URL)
		token := mustGatewayKey(t, 1, p.ID)
		mustAPIKey(t, 1, "prov", "real-key")

		req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handleProxy(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		if capturedPath != "/v1/messages" {
			t.Errorf("upstream path: got %q, want /v1/messages", capturedPath)
		}
	})
}

// --- 6. Query string — ?key= is stripped ---

func TestQueryString_KeyStripped(t *testing.T) {
	var capturedQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	setupDB(t)
	p := mustProvider(t, "prov", "openai-completions", upstream.URL)
	token := mustGatewayKey(t, 1, p.ID)
	mustAPIKey(t, 1, "prov", "real-key")

	req := httptest.NewRequest("POST", "/v1/chat/completions?key="+token+"&model=gpt-4", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleProxy(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if strings.Contains(capturedQuery, "key=") {
		t.Errorf("key= still present in upstream query: %q", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "model=gpt-4") {
		t.Errorf("model= missing from upstream query: %q", capturedQuery)
	}
}

// --- 7. Token count extraction — all three formats ---

func TestTokenCount_AllFormats(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantInput  int
		wantOutput int
	}{
		{
			"openai",
			`{"usage":{"prompt_tokens":10,"completion_tokens":20}}`,
			10, 20,
		},
		{
			"anthropic",
			`{"usage":{"input_tokens":5,"output_tokens":15}}`,
			5, 15,
		},
		{
			"google",
			`{"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":12}}`,
			8, 12,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(tc.body))
			}))
			defer upstream.Close()

			setupDB(t)
			p := mustProvider(t, "prov", "openai-completions", upstream.URL)
			token := mustGatewayKey(t, 1, p.ID)
			mustAPIKey(t, 1, "prov", "real-key")

			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			handleProxy(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rr.Code)
			}

			var log database.LLMRequestLog
			if err := database.DB.First(&log).Error; err != nil {
				t.Fatalf("no log row: %v", err)
			}
			if log.InputTokens != tc.wantInput {
				t.Errorf("input_tokens: got %d, want %d", log.InputTokens, tc.wantInput)
			}
			if log.OutputTokens != tc.wantOutput {
				t.Errorf("output_tokens: got %d, want %d", log.OutputTokens, tc.wantOutput)
			}
		})
	}
}

// --- 8. Streaming response — no token count logged ---

func TestStreaming_NoTokenCount(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {}\n\n"))
	}))
	defer upstream.Close()

	setupDB(t)
	p := mustProvider(t, "prov", "openai-completions", upstream.URL)
	token := mustGatewayKey(t, 1, p.ID)
	mustAPIKey(t, 1, "prov", "real-key")

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleProxy(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var log database.LLMRequestLog
	if err := database.DB.First(&log).Error; err != nil {
		t.Fatalf("no log row: %v", err)
	}
	if log.InputTokens != 0 || log.OutputTokens != 0 {
		t.Errorf("expected 0 tokens for streaming, got input=%d output=%d", log.InputTokens, log.OutputTokens)
	}
}

// --- 9. Upstream error — logged, 502 returned ---

func TestUpstreamError_502(t *testing.T) {
	// Server that closes connection immediately
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer upstream.Close()

	setupDB(t)
	p := mustProvider(t, "prov", "openai-completions", upstream.URL)
	token := mustGatewayKey(t, 1, p.ID)
	mustAPIKey(t, 1, "prov", "real-key")

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleProxy(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rr.Code)
	}

	var log database.LLMRequestLog
	if err := database.DB.First(&log).Error; err != nil {
		t.Fatalf("no log row: %v", err)
	}
	if log.StatusCode != http.StatusBadGateway {
		t.Errorf("log status_code: got %d, want 502", log.StatusCode)
	}
	if log.ErrorMessage == "" {
		t.Error("expected non-empty error_message in log")
	}
}

// --- 10. 4xx upstream — error body captured in log (truncated to 500 bytes) ---

func TestUpstream4xx_ErrorBodyTruncated(t *testing.T) {
	longBody := strings.Repeat("x", 600)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(longBody))
	}))
	defer upstream.Close()

	setupDB(t)
	p := mustProvider(t, "prov", "openai-completions", upstream.URL)
	token := mustGatewayKey(t, 1, p.ID)
	mustAPIKey(t, 1, "prov", "real-key")

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleProxy(rr, req)

	var log database.LLMRequestLog
	if err := database.DB.First(&log).Error; err != nil {
		t.Fatalf("no log row: %v", err)
	}
	if len(log.ErrorMessage) != 500 {
		t.Errorf("error_message length: got %d, want 500", len(log.ErrorMessage))
	}
}

// --- 11. extractGatewayToken — unit tests (no DB needed) ---

func TestExtractGatewayToken(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(r *http.Request)
		expected string
	}{
		{
			"Authorization Bearer claworc-vk-",
			func(r *http.Request) { r.Header.Set("Authorization", "Bearer claworc-vk-mytoken") },
			"claworc-vk-mytoken",
		},
		{
			"x-api-key claworc-vk-",
			func(r *http.Request) { r.Header.Set("x-api-key", "claworc-vk-mytoken") },
			"claworc-vk-mytoken",
		},
		{
			"x-goog-api-key claworc-vk-",
			func(r *http.Request) { r.Header.Set("x-goog-api-key", "claworc-vk-mytoken") },
			"claworc-vk-mytoken",
		},
		{
			"query key param",
			func(r *http.Request) {
				q := r.URL.Query()
				q.Set("key", "claworc-vk-mytoken")
				r.URL.RawQuery = q.Encode()
			},
			"claworc-vk-mytoken",
		},
		{
			"non-gw bearer",
			func(r *http.Request) { r.Header.Set("Authorization", "Bearer regular-key") },
			"",
		},
		{
			"non-gw x-api-key",
			func(r *http.Request) { r.Header.Set("x-api-key", "sk-ant-notgw") },
			"",
		},
		{
			"empty",
			func(r *http.Request) {},
			"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			tc.setup(req)
			got := extractGatewayToken(req)
			if got != tc.expected {
				t.Errorf("got %q, want %q", got, tc.expected)
			}
		})
	}
}

// --- 12. resolveRealAPIKey — global fallback ---

func TestResolveAPIKey_GlobalFallback(t *testing.T) {
	var capturedAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	setupDB(t)
	p := mustProvider(t, "my-provider", "openai-completions", upstream.URL)
	token := mustGatewayKey(t, 1, p.ID)
	// No per-instance key; set global key only
	mustGlobalAPIKey(t, "my-provider", "global-real-key")

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleProxy(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if capturedAuth != "Bearer global-real-key" {
		t.Errorf("upstream auth: got %q, want \"Bearer global-real-key\"", capturedAuth)
	}
}

// --- 13. resolveRealAPIKey — instance key takes precedence over global ---

func TestResolveAPIKey_InstanceOverridesPrecedence(t *testing.T) {
	var capturedAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	setupDB(t)
	p := mustProvider(t, "my-provider", "openai-completions", upstream.URL)
	token := mustGatewayKey(t, 1, p.ID)
	mustGlobalAPIKey(t, "my-provider", "global-real-key")
	mustAPIKey(t, 1, "my-provider", "instance-real-key")

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleProxy(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if capturedAuth != "Bearer instance-real-key" {
		t.Errorf("upstream auth: got %q, want \"Bearer instance-real-key\"", capturedAuth)
	}
}

// Ensure json import is used (token count parsing uses it)
var _ = json.Marshal
