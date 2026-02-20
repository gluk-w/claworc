package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// postTestProviderKey sends a POST request to TestProviderKey and returns the recorder.
func postTestProviderKey(t *testing.T, payload interface{}) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/test-provider-key", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	TestProviderKey(rec, req)
	return rec
}

func parseTestResponse(t *testing.T, rec *httptest.ResponseRecorder) testProviderKeyResponse {
	t.Helper()
	var resp testProviderKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

func TestTestProviderKey_InvalidBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/test-provider-key", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	TestProviderKey(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	resp := parseTestResponse(t, rec)
	if resp.Success {
		t.Fatal("expected failure for invalid body")
	}
}

func TestTestProviderKey_MissingFields(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]string
	}{
		{"missing provider", map[string]string{"api_key": "test-key"}},
		{"missing api_key", map[string]string{"provider": "openai"}},
		{"both empty", map[string]string{"provider": "", "api_key": ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := postTestProviderKey(t, tt.payload)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", rec.Code)
			}
			resp := parseTestResponse(t, rec)
			if resp.Success {
				t.Fatal("expected failure for missing fields")
			}
		})
	}
}

func TestTestProviderKey_UnknownProvider(t *testing.T) {
	rec := postTestProviderKey(t, testProviderKeyRequest{
		Provider: "nonexistent-provider",
		APIKey:   "test-key",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	resp := parseTestResponse(t, rec)
	if resp.Success {
		t.Fatal("expected failure for unknown provider")
	}
	if resp.Message == "" {
		t.Fatal("expected error message")
	}
}

func TestTestProviderKey_AllProvidersConfigured(t *testing.T) {
	// Verify that all provider IDs in providerConfigs are valid and have required fields
	for id, cfg := range providerConfigs {
		t.Run(id, func(t *testing.T) {
			if cfg.BaseURL == "" {
				t.Fatalf("provider %s has empty BaseURL", id)
			}
			if cfg.Path == "" {
				t.Fatalf("provider %s has empty Path", id)
			}
			if cfg.Method == "" {
				t.Fatalf("provider %s has empty Method", id)
			}
			if cfg.Method != "GET" && cfg.Method != "POST" {
				t.Fatalf("provider %s has unsupported method: %s", id, cfg.Method)
			}
			// All providers except Google should have auth headers
			if id != "google" && cfg.AuthHeader == "" {
				t.Fatalf("provider %s (non-Google) missing AuthHeader", id)
			}
		})
	}
}

func TestTestProviderKey_ExpectedProviderCoverage(t *testing.T) {
	expected := []string{
		"anthropic", "openai", "google", "mistral", "groq",
		"deepseek", "together", "fireworks", "cerebras",
		"xai", "cohere", "perplexity", "openrouter", "brave",
	}
	for _, id := range expected {
		if _, ok := providerConfigs[id]; !ok {
			t.Errorf("expected provider config for %s", id)
		}
	}
}

func TestClassifyResponse_Success(t *testing.T) {
	resp := classifyResponse(200, []byte(`{"models":[]}`), "openai")
	if !resp.Success {
		t.Fatalf("expected success for 200 response, got: %s", resp.Message)
	}
}

func TestClassifyResponse_Unauthorized(t *testing.T) {
	resp := classifyResponse(401, []byte(`{"error":"invalid_api_key"}`), "openai")
	if resp.Success {
		t.Fatal("expected failure for 401 response")
	}
	if resp.Message != "Invalid API key" {
		t.Fatalf("expected 'Invalid API key', got: %s", resp.Message)
	}
}

func TestClassifyResponse_Forbidden(t *testing.T) {
	resp := classifyResponse(403, []byte(`{"error":{"message":"Insufficient permissions"}}`), "openai")
	if resp.Success {
		t.Fatal("expected failure for 403 response")
	}
	if resp.Message != "Access forbidden" {
		t.Fatalf("expected 'Access forbidden', got: %s", resp.Message)
	}
}

func TestClassifyResponse_RateLimited(t *testing.T) {
	resp := classifyResponse(429, []byte(`{}`), "openai")
	if resp.Success {
		t.Fatal("expected failure for 429 response")
	}
	if resp.Message != "Rate limited" {
		t.Fatalf("expected 'Rate limited', got: %s", resp.Message)
	}
}

func TestClassifyResponse_ServerError(t *testing.T) {
	for _, code := range []int{500, 502, 503} {
		t.Run(fmt.Sprintf("HTTP_%d", code), func(t *testing.T) {
			resp := classifyResponse(code, []byte("Internal Server Error"), "openai")
			if resp.Success {
				t.Fatalf("expected failure for %d response", code)
			}
			if resp.Message != "Provider server error" {
				t.Fatalf("expected 'Provider server error', got: %s", resp.Message)
			}
		})
	}
}

func TestClassifyResponse_NotFound(t *testing.T) {
	resp := classifyResponse(404, []byte("Not Found"), "openai")
	if resp.Success {
		t.Fatal("expected failure for 404 response")
	}
	if resp.Message != "API endpoint not found" {
		t.Fatalf("expected 'API endpoint not found', got: %s", resp.Message)
	}
}

func TestClassifyResponse_Anthropic400NonAuth(t *testing.T) {
	// For Anthropic, a 400 that isn't about an invalid key means auth succeeded
	body := []byte(`{"error":{"message":"max_tokens must be greater than 0"}}`)
	resp := classifyResponse(400, body, "anthropic")
	if !resp.Success {
		t.Fatalf("expected success for Anthropic 400 non-auth error, got: %s", resp.Message)
	}
}

func TestClassifyResponse_Anthropic400InvalidKey(t *testing.T) {
	body := []byte(`{"error":{"message":"invalid x-api-key"}}`)
	resp := classifyResponse(400, body, "anthropic")
	if resp.Success {
		t.Fatal("expected failure for Anthropic 400 with invalid key")
	}
}

func TestClassifyResponse_BillingError(t *testing.T) {
	body := []byte(`{"error":{"message":"Your credit balance is too low"}}`)
	resp := classifyResponse(400, body, "openai")
	if resp.Success {
		t.Fatal("expected failure for billing error")
	}
	if resp.Message != "Billing issue" {
		t.Fatalf("expected 'Billing issue', got: %s", resp.Message)
	}
}

func TestExtractErrorMessage_JSONWithMessage(t *testing.T) {
	msg := extractErrorMessage([]byte(`{"message":"Something went wrong"}`))
	if msg != "Something went wrong" {
		t.Fatalf("expected 'Something went wrong', got: %s", msg)
	}
}

func TestExtractErrorMessage_NestedError(t *testing.T) {
	msg := extractErrorMessage([]byte(`{"error":{"message":"Nested error message"}}`))
	if msg != "Nested error message" {
		t.Fatalf("expected 'Nested error message', got: %s", msg)
	}
}

func TestExtractErrorMessage_StringError(t *testing.T) {
	msg := extractErrorMessage([]byte(`{"error":"simple string error"}`))
	if msg != "simple string error" {
		t.Fatalf("expected 'simple string error', got: %s", msg)
	}
}

func TestExtractErrorMessage_NonJSON(t *testing.T) {
	msg := extractErrorMessage([]byte("plain text error"))
	if msg != "plain text error" {
		t.Fatalf("expected 'plain text error', got: %s", msg)
	}
}

// TestTestProviderKey_WithMockServer tests the full endpoint with a mock HTTP server.
func TestTestProviderKey_WithMockServer(t *testing.T) {
	// Create a mock API server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "Bearer valid-key" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"models":["gpt-4"]}`))
		} else if authHeader == "Bearer rate-limited-key" {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limited"}`))
		} else {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid_api_key"}`))
		}
	}))
	defer mockServer.Close()

	// Temporarily override the openai config to point to our mock server
	originalConfig := providerConfigs["openai"]
	providerConfigs["openai"] = providerTestConfig{
		BaseURL:    mockServer.URL,
		Path:       "/v1/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	}
	defer func() { providerConfigs["openai"] = originalConfig }()

	t.Run("valid key", func(t *testing.T) {
		rec := postTestProviderKey(t, testProviderKeyRequest{
			Provider: "openai",
			APIKey:   "valid-key",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		resp := parseTestResponse(t, rec)
		if !resp.Success {
			t.Fatalf("expected success, got: %s (details: %s)", resp.Message, resp.Details)
		}
	})

	t.Run("invalid key", func(t *testing.T) {
		rec := postTestProviderKey(t, testProviderKeyRequest{
			Provider: "openai",
			APIKey:   "invalid-key",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		resp := parseTestResponse(t, rec)
		if resp.Success {
			t.Fatal("expected failure for invalid key")
		}
		if resp.Message != "Invalid API key" {
			t.Fatalf("expected 'Invalid API key', got: %s", resp.Message)
		}
	})

	t.Run("rate limited key", func(t *testing.T) {
		rec := postTestProviderKey(t, testProviderKeyRequest{
			Provider: "openai",
			APIKey:   "rate-limited-key",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		resp := parseTestResponse(t, rec)
		if resp.Success {
			t.Fatal("expected failure for rate limited key")
		}
		if resp.Message != "Rate limited" {
			t.Fatalf("expected 'Rate limited', got: %s", resp.Message)
		}
	})
}

// TestTestProviderKey_CustomBaseURL tests that custom base URL is used when provided.
func TestTestProviderKey_CustomBaseURL(t *testing.T) {
	// Create a mock server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"models":[]}`))
	}))
	defer mockServer.Close()

	// Override OpenAI config to use a URL that will fail
	originalConfig := providerConfigs["openai"]
	providerConfigs["openai"] = providerTestConfig{
		BaseURL:    "http://127.0.0.1:1", // will fail to connect
		Path:       "/v1/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	}
	defer func() { providerConfigs["openai"] = originalConfig }()

	// The custom base_url should override the failing default
	rec := postTestProviderKey(t, map[string]interface{}{
		"provider": "openai",
		"api_key":  "test-key",
		"base_url": mockServer.URL,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	resp := parseTestResponse(t, rec)
	if !resp.Success {
		t.Fatalf("expected success with custom base URL, got: %s (details: %s)", resp.Message, resp.Details)
	}
}
