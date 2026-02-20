package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type testProviderKeyRequest struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
	BaseURL  string `json:"base_url,omitempty"`
}

type testProviderKeyResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// providerTestConfig defines how to test a specific provider's API key.
type providerTestConfig struct {
	// BaseURL is the default API base URL for the provider.
	BaseURL string
	// Path is the API path to call for testing (typically list models).
	Path string
	// AuthHeader is the HTTP header name used for authentication.
	AuthHeader string
	// AuthPrefix is prepended to the API key in the auth header value.
	AuthPrefix string
	// Method is the HTTP method to use (GET or POST).
	Method string
	// Body is the request body for POST requests.
	Body string
}

// providerConfigs maps provider IDs to their test configurations.
var providerConfigs = map[string]providerTestConfig{
	"anthropic": {
		BaseURL:    "https://api.anthropic.com",
		Path:       "/v1/messages",
		AuthHeader: "x-api-key",
		Method:     "POST",
		Body:       `{"model":"claude-3-haiku-20240307","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`,
	},
	"openai": {
		BaseURL:    "https://api.openai.com",
		Path:       "/v1/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	},
	"google": {
		BaseURL: "https://generativelanguage.googleapis.com",
		Path:    "/v1beta/models",
		Method:  "GET",
		// Google uses query param for auth, handled specially
	},
	"mistral": {
		BaseURL:    "https://api.mistral.ai",
		Path:       "/v1/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	},
	"groq": {
		BaseURL:    "https://api.groq.com",
		Path:       "/openai/v1/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	},
	"deepseek": {
		BaseURL:    "https://api.deepseek.com",
		Path:       "/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	},
	"together": {
		BaseURL:    "https://api.together.xyz",
		Path:       "/v1/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	},
	"fireworks": {
		BaseURL:    "https://api.fireworks.ai",
		Path:       "/inference/v1/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	},
	"cerebras": {
		BaseURL:    "https://api.cerebras.ai",
		Path:       "/v1/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	},
	"xai": {
		BaseURL:    "https://api.x.ai",
		Path:       "/v1/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	},
	"cohere": {
		BaseURL:    "https://api.cohere.com",
		Path:       "/v2/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	},
	"perplexity": {
		BaseURL:    "https://api.perplexity.ai",
		Path:       "/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	},
	"openrouter": {
		BaseURL:    "https://openrouter.ai",
		Path:       "/api/v1/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	},
	"brave": {
		BaseURL:    "https://api.search.brave.com",
		Path:       "/res/v1/web/search?q=test&count=1",
		AuthHeader: "X-Subscription-Token",
		Method:     "GET",
	},
}

// TestProviderKey handles POST /api/v1/settings/test-provider-key.
// It makes a real API call to the provider to verify the key works.
func TestProviderKey(w http.ResponseWriter, r *http.Request) {
	var req testProviderKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, testProviderKeyResponse{
			Success: false,
			Message: "Invalid request body",
		})
		return
	}

	if req.Provider == "" || req.APIKey == "" {
		writeJSON(w, http.StatusBadRequest, testProviderKeyResponse{
			Success: false,
			Message: "Provider and api_key are required",
		})
		return
	}

	cfg, ok := providerConfigs[req.Provider]
	if !ok {
		writeJSON(w, http.StatusBadRequest, testProviderKeyResponse{
			Success: false,
			Message: fmt.Sprintf("Unknown provider: %s", req.Provider),
		})
		return
	}

	// Use custom base URL if provided (only for providers that support it)
	baseURL := cfg.BaseURL
	if req.BaseURL != "" {
		baseURL = strings.TrimRight(req.BaseURL, "/")
	}

	result := testProviderAPI(baseURL, cfg, req.APIKey, req.Provider)
	writeJSON(w, http.StatusOK, result)
}

// testProviderAPI makes a real HTTP request to the provider's API to test the key.
func testProviderAPI(baseURL string, cfg providerTestConfig, apiKey string, providerID string) testProviderKeyResponse {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := baseURL + cfg.Path

	// Google uses query param for auth
	if providerID == "google" {
		if strings.Contains(url, "?") {
			url += "&key=" + apiKey
		} else {
			url += "?key=" + apiKey
		}
	}

	var bodyReader io.Reader
	if cfg.Body != "" {
		bodyReader = strings.NewReader(cfg.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, cfg.Method, url, bodyReader)
	if err != nil {
		return testProviderKeyResponse{
			Success: false,
			Message: "Failed to create request",
			Details: err.Error(),
		}
	}

	// Set auth header (skip for Google, which uses query param)
	if cfg.AuthHeader != "" && providerID != "google" {
		httpReq.Header.Set(cfg.AuthHeader, cfg.AuthPrefix+apiKey)
	}

	if cfg.Body != "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	// Anthropic requires a version header
	if providerID == "anthropic" {
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return testProviderKeyResponse{
				Success: false,
				Message: "Connection timed out after 5 seconds",
				Details: "The provider's API did not respond in time. Check your network connection.",
			}
		}
		return testProviderKeyResponse{
			Success: false,
			Message: "Network error",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	// Read response body (limit to 4KB to avoid memory issues)
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	return classifyResponse(resp.StatusCode, body, providerID)
}

// classifyResponse interprets the HTTP status and body to produce a user-friendly result.
func classifyResponse(statusCode int, body []byte, providerID string) testProviderKeyResponse {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return testProviderKeyResponse{
			Success: true,
			Message: "API key is valid. Connection successful!",
		}

	case statusCode == 401:
		return testProviderKeyResponse{
			Success: false,
			Message: "Invalid API key",
			Details: "The provider rejected the API key. Please verify you copied the full key.",
		}

	case statusCode == 403:
		detail := "The API key does not have permission to access this resource."
		// Try to extract error message from response body
		if msg := extractErrorMessage(body); msg != "" {
			detail = msg
		}
		return testProviderKeyResponse{
			Success: false,
			Message: "Access forbidden",
			Details: detail,
		}

	case statusCode == 429:
		return testProviderKeyResponse{
			Success: false,
			Message: "Rate limited",
			Details: "The API key is valid but rate-limited. Try again in a moment.",
		}

	// Anthropic returns 400 for overloaded errors and similar
	case statusCode == 400:
		msg := extractErrorMessage(body)
		// Some providers return 400 for billing issues
		if strings.Contains(strings.ToLower(msg), "billing") ||
			strings.Contains(strings.ToLower(msg), "credit") ||
			strings.Contains(strings.ToLower(msg), "payment") {
			return testProviderKeyResponse{
				Success: false,
				Message: "Billing issue",
				Details: msg,
			}
		}
		// For Anthropic, a 400 with "invalid" messages usually means auth worked
		// but request format issues, which still proves the key is valid
		if providerID == "anthropic" && !strings.Contains(strings.ToLower(msg), "invalid x-api-key") &&
			!strings.Contains(strings.ToLower(msg), "invalid api key") {
			return testProviderKeyResponse{
				Success: true,
				Message: "API key is valid. Connection successful!",
			}
		}
		return testProviderKeyResponse{
			Success: false,
			Message: "Request error",
			Details: msg,
		}

	case statusCode == 404:
		return testProviderKeyResponse{
			Success: false,
			Message: "API endpoint not found",
			Details: "The provider's API endpoint could not be reached. This may indicate a base URL issue.",
		}

	case statusCode >= 500:
		return testProviderKeyResponse{
			Success: false,
			Message: "Provider server error",
			Details: fmt.Sprintf("The provider returned a %d error. This is likely a temporary issue on their end.", statusCode),
		}

	default:
		return testProviderKeyResponse{
			Success: false,
			Message: fmt.Sprintf("Unexpected response (HTTP %d)", statusCode),
			Details: truncate(string(body), 200),
		}
	}
}

// extractErrorMessage tries to extract an error message from a JSON response body.
func extractErrorMessage(body []byte) string {
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return truncate(string(body), 200)
	}

	// Try common error message fields
	for _, key := range []string{"message", "error"} {
		if val, ok := data[key]; ok {
			switch v := val.(type) {
			case string:
				return v
			case map[string]interface{}:
				if msg, ok := v["message"].(string); ok {
					return msg
				}
			}
		}
	}

	// Try nested error.message (OpenAI style)
	if errObj, ok := data["error"].(map[string]interface{}); ok {
		if msg, ok := errObj["message"].(string); ok {
			return msg
		}
	}

	return truncate(string(body), 200)
}

