package llmgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// setupDB initialises in-memory SQLite DBs and points database.DB and database.LogsDB at them.
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
	); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}

	database.LogsDB, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open in-memory logs DB: %v", err)
	}
	if err := database.LogsDB.AutoMigrate(&database.LLMRequestLog{}); err != nil {
		t.Fatalf("auto-migrate logs DB: %v", err)
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

// mustProviderAPIKey encrypts realKey and stores it on the LLMProvider.APIKey column.
func mustProviderAPIKey(t *testing.T, providerID uint, realKey string) {
	t.Helper()
	enc, err := utils.Encrypt(realKey)
	if err != nil {
		t.Fatalf("encrypt API key: %v", err)
	}
	if err := database.DB.Model(&database.LLMProvider{}).Where("id = ?", providerID).Update("api_key", enc).Error; err != nil {
		t.Fatalf("set API key on provider: %v", err)
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
			mustProviderAPIKey(t, p.ID, "real-key")
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
		{"openai-responses", "Authorization", "Bearer real-key"},
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
			mustProviderAPIKey(t, p.ID, "real-key")

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
	mustProviderAPIKey(t, p.ID, "real-key")

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
		mustProviderAPIKey(t, p.ID, "real-key")

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
		mustProviderAPIKey(t, p.ID, "real-key")

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

// --- 5b. openai-responses path prefix injection ---

func TestOpenAIResponses_PathPrefixed(t *testing.T) {
	// When api_type is "openai-responses", OpenClaw appends /v1 to the gateway base URL before
	// initialising the SDK, so the SDK sends paths like /responses (not /v1/responses).
	// The gateway must prepend /v1 so the upstream receives /v1/responses.
	t.Run("baseURL without /v1 — /v1 prepended", func(t *testing.T) {
		var capturedPath string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		}))
		defer upstream.Close()

		setupDB(t)
		p := mustProvider(t, "openai", "openai-responses", upstream.URL)
		token := mustGatewayKey(t, 1, p.ID)
		mustProviderAPIKey(t, p.ID, "real-key")

		req := httptest.NewRequest("POST", "/responses", strings.NewReader(`{"model":"gpt-5"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handleProxy(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		if capturedPath != "/v1/responses" {
			t.Errorf("upstream path: got %q, want /v1/responses", capturedPath)
		}
	})

	t.Run("baseURL with /v1 — no double prefix", func(t *testing.T) {
		var capturedPath string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		}))
		defer upstream.Close()

		setupDB(t)
		p := mustProvider(t, "openai", "openai-responses", upstream.URL+"/v1")
		token := mustGatewayKey(t, 1, p.ID)
		mustProviderAPIKey(t, p.ID, "real-key")

		req := httptest.NewRequest("POST", "/responses", strings.NewReader(`{"model":"gpt-5"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handleProxy(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		if capturedPath != "/v1/responses" {
			t.Errorf("upstream path: got %q, want /v1/responses", capturedPath)
		}
		if strings.Contains(capturedPath, "/v1/v1/") {
			t.Errorf("double /v1 detected: %q", capturedPath)
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
	mustProviderAPIKey(t, p.ID, "real-key")

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
		apiType    string
		body       string
		wantInput  int
		wantOutput int
	}{
		{
			"openai",
			"openai-completions",
			`{"usage":{"prompt_tokens":10,"completion_tokens":20}}`,
			10, 20,
		},
		{
			"anthropic",
			"anthropic-messages",
			`{"usage":{"input_tokens":5,"output_tokens":15}}`,
			5, 15,
		},
		{
			"google",
			"google-generative-ai",
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
			p := mustProvider(t, "prov", tc.apiType, upstream.URL)
			token := mustGatewayKey(t, 1, p.ID)
			mustProviderAPIKey(t, p.ID, "real-key")

			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			handleProxy(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rr.Code)
			}

			var log database.LLMRequestLog
			if err := database.LogsDB.First(&log).Error; err != nil {
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
	mustProviderAPIKey(t, p.ID, "real-key")

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleProxy(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var log database.LLMRequestLog
	if err := database.LogsDB.First(&log).Error; err != nil {
		t.Fatalf("no log row: %v", err)
	}
	if log.InputTokens != 0 || log.OutputTokens != 0 {
		t.Errorf("expected 0 tokens for streaming, got input=%d output=%d", log.InputTokens, log.OutputTokens)
	}
}

// --- 8b. Streaming response — Flush() is called ---

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushCount int
}

func (f *flushRecorder) Flush() { f.flushCount++ }

func TestStreaming_Flushed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {}\n\n"))
		w.Write([]byte("data: {}\n\n"))
		w.Write([]byte("data: {}\n\n"))
	}))
	defer upstream.Close()

	setupDB(t)
	p := mustProvider(t, "prov", "openai-completions", upstream.URL)
	token := mustGatewayKey(t, 1, p.ID)
	mustProviderAPIKey(t, p.ID, "real-key")

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	fr := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	handleProxy(fr, req)

	if fr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", fr.Code)
	}
	if fr.flushCount == 0 {
		t.Error("expected Flush() to be called at least once for streaming response")
	}
}

// --- 8c. Streaming response — X-Accel-Buffering: no is set ---

func TestStreaming_XAccelBuffering(t *testing.T) {
	makeUpstream := func(contentType string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", contentType)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("data: {}\n\n"))
		}))
	}

	t.Run("streaming — X-Accel-Buffering: no present", func(t *testing.T) {
		upstream := makeUpstream("text/event-stream")
		defer upstream.Close()

		setupDB(t)
		p := mustProvider(t, "prov", "openai-completions", upstream.URL)
		token := mustGatewayKey(t, 1, p.ID)
		mustProviderAPIKey(t, p.ID, "real-key")

		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handleProxy(rr, req)

		if got := rr.Header().Get("X-Accel-Buffering"); got != "no" {
			t.Errorf("X-Accel-Buffering: got %q, want \"no\"", got)
		}
	})

	t.Run("non-streaming — X-Accel-Buffering absent", func(t *testing.T) {
		upstream := makeUpstream("application/json")
		defer upstream.Close()

		setupDB(t)
		p := mustProvider(t, "prov", "openai-completions", upstream.URL)
		token := mustGatewayKey(t, 1, p.ID)
		mustProviderAPIKey(t, p.ID, "real-key")

		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handleProxy(rr, req)

		if got := rr.Header().Get("X-Accel-Buffering"); got != "" {
			t.Errorf("X-Accel-Buffering should be absent for non-streaming, got %q", got)
		}
	})
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
	mustProviderAPIKey(t, p.ID, "real-key")

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleProxy(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rr.Code)
	}

	var log database.LLMRequestLog
	if err := database.LogsDB.First(&log).Error; err != nil {
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
	mustProviderAPIKey(t, p.ID, "real-key")

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleProxy(rr, req)

	var log database.LLMRequestLog
	if err := database.LogsDB.First(&log).Error; err != nil {
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
	mustProviderAPIKey(t, p.ID, "global-real-key")

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

// --- 14. Cached token extraction — all three provider formats ---

func TestCachedTokenExtraction(t *testing.T) {
	cases := []struct {
		name       string
		apiType    string
		body       string
		wantCached int
		wantInput  int
		wantOutput int
	}{
		{
			"openai cached",
			"openai-completions",
			`{"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":40}}}`,
			40, 60, 20,
		},
		{
			"anthropic cached",
			"anthropic-messages",
			`{"usage":{"input_tokens":50,"output_tokens":15,"cache_read_input_tokens":10}}`,
			10, 50, 15,
		},
		{
			"google cached",
			"google-generative-ai",
			`{"usageMetadata":{"promptTokenCount":80,"candidatesTokenCount":12,"cachedContentTokenCount":30}}`,
			30, 80, 12,
		},
		{
			"no cache",
			"openai-completions",
			`{"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
			0, 10, 5,
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
			p := mustProvider(t, "prov", tc.apiType, upstream.URL)
			token := mustGatewayKey(t, 1, p.ID)
			mustProviderAPIKey(t, p.ID, "real-key")

			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4"}`))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			handleProxy(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rr.Code)
			}

			var log database.LLMRequestLog
			if err := database.LogsDB.First(&log).Error; err != nil {
				t.Fatalf("no log row: %v", err)
			}
			if log.CachedInputTokens != tc.wantCached {
				t.Errorf("cached_input_tokens: got %d, want %d", log.CachedInputTokens, tc.wantCached)
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

// --- 15. Cost calculation ---

func mustProviderWithModels(t *testing.T, key, apiType, baseURL string, models []database.ProviderModel) database.LLMProvider {
	t.Helper()
	modelsJSON, _ := json.Marshal(models)
	p := database.LLMProvider{Key: key, Name: key, APIType: apiType, BaseURL: baseURL, Models: string(modelsJSON)}
	if err := database.DB.Create(&p).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return p
}

func TestCostCalculation(t *testing.T) {
	cases := []struct {
		name           string
		inputTokens    int
		outputTokens   int
		cachedTokens   int
		inputRate      float64
		outputRate     float64
		cacheReadRate  float64
		wantCostApprox float64
	}{
		{
			"no cached tokens",
			// 1000 input @ $3/M, 500 output @ $15/M = $0.003 + $0.0075 = $0.0105
			1000, 500, 0, 3.0, 15.0, 0.3, 0.0105,
		},
		{
			"with cached tokens",
			// 1000 input (400 cached) @ $3/M non-cached, $0.3/M cached; 200 output @ $15/M
			// = (600*3 + 400*0.3 + 200*15) / 1_000_000 = (1800+120+3000)/1_000_000 = 0.00492
			1000, 200, 400, 3.0, 15.0, 0.3, 0.00492,
		},
		{
			"zero tokens",
			0, 0, 0, 3.0, 15.0, 0.3, 0.0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			respBody := fmt.Sprintf(`{"usage":{"prompt_tokens":%d,"completion_tokens":%d,"prompt_tokens_details":{"cached_tokens":%d}}}`,
				tc.inputTokens, tc.outputTokens, tc.cachedTokens)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(respBody))
			}))
			defer upstream.Close()

			setupDB(t)
			models := []database.ProviderModel{
				{
					ID:   "test-model",
					Name: "Test Model",
					Cost: &database.ProviderModelCost{
						Input:      tc.inputRate,
						Output:     tc.outputRate,
						CacheRead:  tc.cacheReadRate,
						CacheWrite: 0,
					},
				},
			}
			p := mustProviderWithModels(t, "prov", "openai-completions", upstream.URL, models)
			token := mustGatewayKey(t, 1, p.ID)
			mustProviderAPIKey(t, p.ID, "real-key")

			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-model"}`))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			handleProxy(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rr.Code)
			}

			var log database.LLMRequestLog
			if err := database.LogsDB.First(&log).Error; err != nil {
				t.Fatalf("no log row: %v", err)
			}
			if log.CachedInputTokens != tc.cachedTokens {
				t.Errorf("cached_input_tokens: got %d, want %d", log.CachedInputTokens, tc.cachedTokens)
			}
			const epsilon = 0.000001
			diff := log.CostUSD - tc.wantCostApprox
			if diff < -epsilon || diff > epsilon {
				t.Errorf("cost_usd: got %f, want %f", log.CostUSD, tc.wantCostApprox)
			}
		})
	}
}

// --- 16. buildTargetURL — pure function tests ---

func TestBuildTargetURL(t *testing.T) {
	cases := []struct {
		name        string
		baseURL     string
		requestPath string
		query       string
		apiType     string
		want        string
	}{
		{
			"/v1 dedup: baseURL ends with /v1, path starts with /v1/",
			"https://api.example.com/v1",
			"/v1/chat/completions",
			"",
			"openai-completions",
			"https://api.example.com/v1/chat/completions",
		},
		{
			"no dedup: baseURL without /v1 suffix",
			"https://api.example.com",
			"/v1/chat/completions",
			"",
			"openai-completions",
			"https://api.example.com/v1/chat/completions",
		},
		{
			"key= stripped from query",
			"https://api.example.com",
			"/v1/models",
			"key=claworc-vk-abc",
			"openai-completions",
			"https://api.example.com/v1/models",
		},
		{
			"key= stripped, other params preserved",
			"https://api.example.com",
			"/v1/models",
			"key=claworc-vk-abc&foo=bar",
			"openai-completions",
			"https://api.example.com/v1/models?foo=bar",
		},
		{
			"openai-responses: /v1 prepended when baseURL has no /v1 suffix",
			"https://api.openai.com",
			"/responses",
			"",
			"openai-responses",
			"https://api.openai.com/v1/responses",
		},
		{
			"openai-responses: no double /v1 when baseURL ends with /v1",
			"https://api.openai.com/v1",
			"/responses",
			"",
			"openai-responses",
			"https://api.openai.com/v1/responses",
		},
		{
			"openai-responses: path already has /v1/ prefix — no double prepend",
			"https://api.openai.com",
			"/v1/responses",
			"",
			"openai-responses",
			"https://api.openai.com/v1/responses",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vals, _ := url.ParseQuery(tc.query)
			got := buildTargetURL(tc.baseURL, tc.requestPath, GetAPIType(tc.apiType), vals)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// --- 17. buildUpstreamRequest — pure function tests ---

func TestBuildUpstreamRequest(t *testing.T) {
	ctx := context.Background()
	body := []byte(`{"model":"test"}`)

	t.Run("openai-completions → Authorization Bearer", func(t *testing.T) {
		req, err := buildUpstreamRequest(ctx, "POST", "https://api.example.com/v1/chat/completions", body, http.Header{}, "my-key", GetAPIType("openai-completions"))
		if err != nil {
			t.Fatal(err)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer my-key" {
			t.Errorf("got %q, want %q", got, "Bearer my-key")
		}
	})

	t.Run("anthropic-messages → x-api-key", func(t *testing.T) {
		req, err := buildUpstreamRequest(ctx, "POST", "https://api.anthropic.com/v1/messages", body, http.Header{}, "ant-key", GetAPIType("anthropic-messages"))
		if err != nil {
			t.Fatal(err)
		}
		if got := req.Header.Get("x-api-key"); got != "ant-key" {
			t.Errorf("got %q, want %q", got, "ant-key")
		}
		if req.Header.Get("Authorization") != "" {
			t.Errorf("Authorization should be empty for anthropic-messages")
		}
	})

	t.Run("google-generative-ai → x-goog-api-key", func(t *testing.T) {
		req, err := buildUpstreamRequest(ctx, "POST", "https://generativelanguage.googleapis.com/v1/models", body, http.Header{}, "goog-key", GetAPIType("google-generative-ai"))
		if err != nil {
			t.Fatal(err)
		}
		if got := req.Header.Get("x-goog-api-key"); got != "goog-key" {
			t.Errorf("got %q, want %q", got, "goog-key")
		}
	})

	t.Run("empty apiType → defaults to Bearer", func(t *testing.T) {
		req, err := buildUpstreamRequest(ctx, "POST", "https://api.example.com/v1/chat/completions", body, http.Header{}, "def-key", GetAPIType(""))
		if err != nil {
			t.Fatal(err)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer def-key" {
			t.Errorf("got %q, want %q", got, "Bearer def-key")
		}
	})

	t.Run("incoming auth headers stripped", func(t *testing.T) {
		orig := http.Header{
			"Authorization":  []string{"Bearer evil"},
			"X-Api-Key":      []string{"evil"},
			"X-Goog-Api-Key": []string{"evil"},
			"Host":           []string{"evil.example.com"},
			"X-Custom":       []string{"keep-me"},
		}
		req, err := buildUpstreamRequest(ctx, "POST", "https://api.example.com/", body, orig, "real-key", GetAPIType("openai-completions"))
		if err != nil {
			t.Fatal(err)
		}
		if req.Header.Get("Authorization") == "Bearer evil" {
			t.Error("Authorization was forwarded")
		}
		if req.Header.Get("x-api-key") == "evil" {
			t.Error("x-api-key was forwarded")
		}
		if req.Header.Get("x-goog-api-key") == "evil" {
			t.Error("x-goog-api-key was forwarded")
		}
		if req.Header.Get("X-Custom") != "keep-me" {
			t.Errorf("X-Custom not forwarded: got %q", req.Header.Get("X-Custom"))
		}
	})

	t.Run("default Content-Type set when missing", func(t *testing.T) {
		req, err := buildUpstreamRequest(ctx, "POST", "https://api.example.com/", body, http.Header{}, "", GetAPIType(""))
		if err != nil {
			t.Fatal(err)
		}
		if got := req.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type: got %q, want application/json", got)
		}
	})

	t.Run("explicit Content-Type preserved", func(t *testing.T) {
		orig := http.Header{"Content-Type": []string{"text/plain"}}
		req, err := buildUpstreamRequest(ctx, "POST", "https://api.example.com/", body, orig, "", GetAPIType(""))
		if err != nil {
			t.Fatal(err)
		}
		if got := req.Header.Get("Content-Type"); got != "text/plain" {
			t.Errorf("Content-Type: got %q, want text/plain", got)
		}
	})
}

// --- 18. ParseUsage — pure function tests ---

func TestParseUsage(t *testing.T) {
	cases := []struct {
		name       string
		apiType    string
		body       string
		wantInput  int
		wantOutput int
		wantCached int
	}{
		{
			"openai",
			"openai-completions",
			`{"usage":{"prompt_tokens":10,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":3}}}`,
			7, 20, 3,
		},
		{
			"anthropic",
			"anthropic-messages",
			`{"usage":{"input_tokens":5,"output_tokens":15,"cache_read_input_tokens":2}}`,
			5, 15, 2,
		},
		{
			"google",
			"google-generative-ai",
			`{"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":12,"cachedContentTokenCount":4}}`,
			8, 12, 4,
		},
		{
			"invalid JSON → all zeros",
			"openai-completions",
			`not json`,
			0, 0, 0,
		},
		{
			"empty body → all zeros",
			"openai-completions",
			``,
			0, 0, 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in, out, cached := ParseUsage(tc.apiType, []byte(tc.body))
			if in != tc.wantInput {
				t.Errorf("input: got %d, want %d", in, tc.wantInput)
			}
			if out != tc.wantOutput {
				t.Errorf("output: got %d, want %d", out, tc.wantOutput)
			}
			if cached != tc.wantCached {
				t.Errorf("cached: got %d, want %d", cached, tc.wantCached)
			}
		})
	}
}

// --- 19. calculateCost — pure function tests ---

func TestCalculateCost(t *testing.T) {
	models := []database.ProviderModel{
		{
			ID:   "known-model",
			Name: "Known Model",
			Cost: &database.ProviderModelCost{
				Input:     3.0,
				Output:    15.0,
				CacheRead: 0.3,
			},
		},
	}

	cases := []struct {
		name     string
		modelID  string
		input    int
		output   int
		cached   int
		wantCost float64
	}{
		{
			"no model cost config → 0",
			"unknown-model", 1000, 500, 0, 0,
		},
		{
			"no cached tokens",
			// 1000*3 + 500*15 = 3000+7500 = 10500 / 1e6 = 0.0105
			"known-model", 1000, 500, 0, 0.0105,
		},
		{
			"with cached tokens",
			// input is non-cached: 600*3 + 400*0.3 + 200*15 = 1800+120+3000 = 4920 / 1e6 = 0.00492
			"known-model", 600, 200, 400, 0.00492,
		},
		{
			"zero tokens → 0",
			"known-model", 0, 0, 0, 0,
		},
	}

	const epsilon = 0.000001
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := calculateCost(models, tc.modelID, tc.input, tc.output, tc.cached)
			diff := got - tc.wantCost
			if diff < -epsilon || diff > epsilon {
				t.Errorf("cost: got %f, want %f", got, tc.wantCost)
			}
		})
	}
}

// --- 8d. Streaming — tokens and cost stored in DB ---

func TestStreamingDB_OpenAICompletions(t *testing.T) {
	// Stream with a usage chunk (stream_options: include_usage).
	// Expected: InputTokens=23, OutputTokens=30, CachedInputTokens=5, CostUSD≈0.0005055
	const stream = "" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":23,\"completion_tokens\":30,\"prompt_tokens_details\":{\"cached_tokens\":5},\"total_tokens\":58}}\n\n" +
		"data: [DONE]\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(stream))
	}))
	defer upstream.Close()

	setupDB(t)
	models := []database.ProviderModel{
		{
			ID:   "test-model",
			Name: "Test Model",
			Cost: &database.ProviderModelCost{Input: 3.0, Output: 15.0, CacheRead: 0.3},
		},
	}
	p := mustProviderWithModels(t, "prov", "openai-completions", upstream.URL, models)
	token := mustGatewayKey(t, 1, p.ID)
	mustProviderAPIKey(t, p.ID, "real-key")

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-model"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleProxy(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var log database.LLMRequestLog
	if err := database.LogsDB.First(&log).Error; err != nil {
		t.Fatalf("no log row: %v", err)
	}
	if log.InputTokens != 18 {
		t.Errorf("input_tokens: got %d, want 18", log.InputTokens)
	}
	if log.OutputTokens != 30 {
		t.Errorf("output_tokens: got %d, want 30", log.OutputTokens)
	}
	if log.CachedInputTokens != 5 {
		t.Errorf("cached_input_tokens: got %d, want 5", log.CachedInputTokens)
	}
	// cost = 18*3/1e6 + 5*0.3/1e6 + 30*15/1e6 = 54/1e6 + 1.5/1e6 + 450/1e6 = 0.0005055
	const wantCost = 0.0005055
	const epsilon = 0.000001
	if diff := log.CostUSD - wantCost; diff < -epsilon || diff > epsilon {
		t.Errorf("cost_usd: got %f, want %f", log.CostUSD, wantCost)
	}
}

func TestStreamingDB_AnthropicMessages(t *testing.T) {
	// Stream with message_start (input tokens + cache) and message_delta (final output tokens).
	// Expected: InputTokens=20, OutputTokens=10, CachedInputTokens=4
	const stream = "" +
		"event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":20,\"cache_read_input_tokens\":4,\"output_tokens\":1}}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{},\"usage\":{\"output_tokens\":10}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(stream))
	}))
	defer upstream.Close()

	setupDB(t)
	p := mustProvider(t, "prov", "anthropic-messages", upstream.URL)
	token := mustGatewayKey(t, 1, p.ID)
	mustProviderAPIKey(t, p.ID, "real-key")

	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"claude-3-5-sonnet"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleProxy(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var log database.LLMRequestLog
	if err := database.LogsDB.First(&log).Error; err != nil {
		t.Fatalf("no log row: %v", err)
	}
	if log.InputTokens != 20 {
		t.Errorf("input_tokens: got %d, want 20", log.InputTokens)
	}
	if log.OutputTokens != 10 {
		t.Errorf("output_tokens: got %d, want 10", log.OutputTokens)
	}
	if log.CachedInputTokens != 4 {
		t.Errorf("cached_input_tokens: got %d, want 4", log.CachedInputTokens)
	}
}

func TestStreamingDB_OpenAIResponses(t *testing.T) {
	// Stream with a response.completed event carrying usage.
	// Expected: InputTokens=13, OutputTokens=17, CachedInputTokens=0
	const stream = "" +
		"event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":13,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":17}}}\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(stream))
	}))
	defer upstream.Close()

	setupDB(t)
	p := mustProvider(t, "prov", "openai-responses", upstream.URL)
	token := mustGatewayKey(t, 1, p.ID)
	mustProviderAPIKey(t, p.ID, "real-key")

	req := httptest.NewRequest("POST", "/responses", strings.NewReader(`{"model":"gpt-5"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleProxy(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var log database.LLMRequestLog
	if err := database.LogsDB.First(&log).Error; err != nil {
		t.Fatalf("no log row: %v", err)
	}
	if log.InputTokens != 13 {
		t.Errorf("input_tokens: got %d, want 13", log.InputTokens)
	}
	if log.OutputTokens != 17 {
		t.Errorf("output_tokens: got %d, want 17", log.OutputTokens)
	}
	if log.CachedInputTokens != 0 {
		t.Errorf("cached_input_tokens: got %d, want 0", log.CachedInputTokens)
	}
}

// Ensure json import is used (token count parsing uses it)
var _ = json.Marshal
