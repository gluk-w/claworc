package llmproxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/config"
)

func setupMockProxy(t *testing.T) (*httptest.Server, func()) {
	t.Helper()

	mux := http.NewServeMux()

	// Token endpoints
	mux.HandleFunc("POST /admin/tokens", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["instance_name"] == "" || body["token"] == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "created"})
	})

	mux.HandleFunc("DELETE /admin/tokens/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("PUT /admin/tokens/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
	})

	// Keys endpoint
	mux.HandleFunc("PUT /admin/keys", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "synced"})
	})

	// Usage endpoints
	mux.HandleFunc("GET /admin/usage", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		results := []map[string]interface{}{
			{
				"group":              "total",
				"requests":           float64(100),
				"input_tokens":       float64(10000),
				"output_tokens":      float64(5000),
				"estimated_cost_usd": "$0.150000",
			},
		}
		json.NewEncoder(w).Encode(results)
	})

	mux.HandleFunc("GET /admin/usage/instances/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		results := []map[string]interface{}{
			{
				"provider":          "anthropic",
				"model":             "claude-3-5-sonnet-20241022",
				"requests":          float64(50),
				"input_tokens":      float64(5000),
				"output_tokens":     float64(2500),
				"estimated_cost_usd": "$0.075000",
			},
		}
		json.NewEncoder(w).Encode(results)
	})

	// Limits endpoints
	mux.HandleFunc("GET /admin/limits/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		result := map[string]interface{}{
			"budget": map[string]interface{}{
				"limit_micro":     float64(10000000),
				"period_type":     "monthly",
				"alert_threshold": 0.8,
				"hard_limit":      true,
			},
			"rate_limits": []map[string]interface{}{
				{
					"provider":            "*",
					"requests_per_minute": float64(60),
					"tokens_per_minute":   float64(50000),
				},
			},
		}
		json.NewEncoder(w).Encode(result)
	})

	mux.HandleFunc("PUT /admin/limits/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
	})

	server := httptest.NewServer(mux)

	// Set config to point to mock server
	config.Cfg.ProxyURL = server.URL
	config.Cfg.ProxySecret = "test-secret"

	cleanup := func() {
		server.Close()
	}

	return server, cleanup
}

func TestRegisterInstance(t *testing.T) {
	_, cleanup := setupMockProxy(t)
	defer cleanup()

	err := RegisterInstance("bot-test", "test-token-123")
	if err != nil {
		t.Errorf("RegisterInstance failed: %v", err)
	}
}

func TestRevokeInstance(t *testing.T) {
	_, cleanup := setupMockProxy(t)
	defer cleanup()

	err := RevokeInstance("bot-test")
	if err != nil {
		t.Errorf("RevokeInstance failed: %v", err)
	}
}

func TestDisableEnableInstance(t *testing.T) {
	_, cleanup := setupMockProxy(t)
	defer cleanup()

	if err := DisableInstance("bot-test"); err != nil {
		t.Errorf("DisableInstance failed: %v", err)
	}

	if err := EnableInstance("bot-test"); err != nil {
		t.Errorf("EnableInstance failed: %v", err)
	}
}

func TestSyncAPIKeys(t *testing.T) {
	_, cleanup := setupMockProxy(t)
	defer cleanup()

	keys := []syncKey{
		{Provider: "anthropic", Scope: "global", Key: "sk-ant-test"},
		{Provider: "openai", Scope: "bot-test", Key: "sk-openai-test"},
	}

	err := SyncAPIKeys(keys)
	if err != nil {
		t.Errorf("SyncAPIKeys failed: %v", err)
	}
}

func TestSyncInstanceKeys(t *testing.T) {
	_, cleanup := setupMockProxy(t)
	defer cleanup()

	apiKeys := map[string]string{
		"ANTHROPIC_API_KEY": "sk-ant-123",
		"OPENAI_API_KEY":    "sk-openai-456",
		"BRAVE_API_KEY":     "brave-789", // Should be ignored
	}

	err := SyncInstanceKeys("bot-test", apiKeys)
	if err != nil {
		t.Errorf("SyncInstanceKeys failed: %v", err)
	}
}

func TestEnvVarToProvider(t *testing.T) {
	tests := []struct {
		envVar   string
		expected string
	}{
		{"ANTHROPIC_API_KEY", "anthropic"},
		{"OPENAI_API_KEY", "openai"},
		{"GOOGLE_API_KEY", "google"},
		{"GEMINI_API_KEY", "google"},
		{"BRAVE_API_KEY", ""},
		{"UNKNOWN_KEY", ""},
	}

	for _, tt := range tests {
		result := envVarToProvider(tt.envVar)
		if result != tt.expected {
			t.Errorf("envVarToProvider(%s) = %s, want %s", tt.envVar, result, tt.expected)
		}
	}
}

func TestProviderMappings(t *testing.T) {
	tests := []struct {
		provider        string
		wantBaseURLEnv  string
		wantAPIKeyEnv   string
	}{
		{"anthropic", "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY"},
		{"openai", "OPENAI_BASE_URL", "OPENAI_API_KEY"},
		{"google", "GOOGLE_API_BASE_URL", "GOOGLE_API_KEY"},
		{"ollama", "OLLAMA_BASE_URL", "OLLAMA_API_KEY"},
	}

	for _, tt := range tests {
		baseURL := ProviderToBaseURLEnv(tt.provider)
		if baseURL != tt.wantBaseURLEnv {
			t.Errorf("ProviderToBaseURLEnv(%s) = %s, want %s", tt.provider, baseURL, tt.wantBaseURLEnv)
		}

		apiKey := ProviderToAPIKeyEnv(tt.provider)
		if apiKey != tt.wantAPIKeyEnv {
			t.Errorf("ProviderToAPIKeyEnv(%s) = %s, want %s", tt.provider, apiKey, tt.wantAPIKeyEnv)
		}
	}
}

func TestGetUsage(t *testing.T) {
	_, cleanup := setupMockProxy(t)
	defer cleanup()

	results, err := GetUsage("2026-01-01", "2026-02-01", "provider")
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Expected usage results")
	}
	if results[0].Group != "total" {
		t.Errorf("group = %s, want total", results[0].Group)
	}
}

func TestGetInstanceUsage(t *testing.T) {
	_, cleanup := setupMockProxy(t)
	defer cleanup()

	results, err := GetInstanceUsage("bot-test", "", "")
	if err != nil {
		t.Fatalf("GetInstanceUsage failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Expected usage results")
	}
	if results[0].Provider != "anthropic" {
		t.Errorf("provider = %s, want anthropic", results[0].Provider)
	}
}

func TestGetLimits(t *testing.T) {
	_, cleanup := setupMockProxy(t)
	defer cleanup()

	limits, err := GetLimits("bot-test")
	if err != nil {
		t.Fatalf("GetLimits failed: %v", err)
	}
	if limits == nil {
		t.Fatal("Expected limits response")
	}
}

func TestSetLimits(t *testing.T) {
	_, cleanup := setupMockProxy(t)
	defer cleanup()

	body := map[string]interface{}{
		"budget": map[string]interface{}{
			"limit_micro": 5000000,
			"period_type": "daily",
			"hard_limit":  true,
		},
	}

	err := SetLimits("bot-test", body)
	if err != nil {
		t.Errorf("SetLimits failed: %v", err)
	}
}
