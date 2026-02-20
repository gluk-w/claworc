package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupE2EDB creates a fresh in-memory SQLite database with all tables needed for e2e tests.
func setupE2EDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	database.DB = db
	if err := db.AutoMigrate(&database.Setting{}, &database.ProviderTelemetry{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

// === E2E: Connection testing with valid and invalid keys ===

func TestE2E_ConnectionTesting_ValidKey(t *testing.T) {
	setupE2EDB(t)

	// Mock server simulating a successful provider API
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "Bearer valid-test-key-12345" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"models":["gpt-4"]}`))
		} else {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid_api_key"}`))
		}
	}))
	defer mockServer.Close()

	// Override the OpenAI config to point to mock
	origConfig := providerConfigs["openai"]
	providerConfigs["openai"] = providerTestConfig{
		BaseURL:    mockServer.URL,
		Path:       "/v1/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	}
	defer func() { providerConfigs["openai"] = origConfig }()

	// Test with valid key
	rec := postTestProviderKey(t, testProviderKeyRequest{
		Provider: "openai",
		APIKey:   "valid-test-key-12345",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	resp := parseTestResponse(t, rec)
	if !resp.Success {
		t.Fatalf("expected success with valid key, got: %s", resp.Message)
	}
	if resp.Message == "" {
		t.Fatal("expected non-empty success message")
	}

	// Verify telemetry was recorded
	var count int64
	database.DB.Model(&database.ProviderTelemetry{}).Where("provider = ?", "openai").Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 telemetry entry for openai, got %d", count)
	}

	var telemetry database.ProviderTelemetry
	database.DB.Where("provider = ?", "openai").First(&telemetry)
	if telemetry.IsError {
		t.Fatal("expected non-error telemetry for successful test")
	}
	if telemetry.StatusCode != 200 {
		t.Fatalf("expected status code 200, got %d", telemetry.StatusCode)
	}
}

func TestE2E_ConnectionTesting_InvalidKey(t *testing.T) {
	setupE2EDB(t)

	// Mock server simulating an authentication failure
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_api_key"}`))
	}))
	defer mockServer.Close()

	origConfig := providerConfigs["openai"]
	providerConfigs["openai"] = providerTestConfig{
		BaseURL:    mockServer.URL,
		Path:       "/v1/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	}
	defer func() { providerConfigs["openai"] = origConfig }()

	rec := postTestProviderKey(t, testProviderKeyRequest{
		Provider: "openai",
		APIKey:   "bad-key",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	resp := parseTestResponse(t, rec)
	if resp.Success {
		t.Fatal("expected failure with invalid key")
	}
	if resp.Message != "Invalid API key" {
		t.Fatalf("expected 'Invalid API key', got: %s", resp.Message)
	}

	// Verify error telemetry was recorded
	var telemetry database.ProviderTelemetry
	database.DB.Where("provider = ?", "openai").First(&telemetry)
	if !telemetry.IsError {
		t.Fatal("expected error telemetry for invalid key test")
	}
	if telemetry.StatusCode != 401 {
		t.Fatalf("expected status code 401, got %d", telemetry.StatusCode)
	}
}

func TestE2E_ConnectionTesting_CommonFailures(t *testing.T) {
	setupE2EDB(t)

	tests := []struct {
		name           string
		statusCode     int
		body           string
		expectedMsg    string
		expectedFail   bool
	}{
		{"rate limited", 429, `{"error":"rate_limit"}`, "Rate limited", true},
		{"forbidden", 403, `{"error":"forbidden"}`, "Access forbidden", true},
		{"server error", 500, `{"error":"internal"}`, "Provider server error", true},
		{"not found", 404, `Not Found`, "API endpoint not found", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupE2EDB(t)

			mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.body))
			}))
			defer mockServer.Close()

			origConfig := providerConfigs["openai"]
			providerConfigs["openai"] = providerTestConfig{
				BaseURL:    mockServer.URL,
				Path:       "/v1/models",
				AuthHeader: "Authorization",
				AuthPrefix: "Bearer ",
				Method:     "GET",
			}
			defer func() { providerConfigs["openai"] = origConfig }()

			rec := postTestProviderKey(t, testProviderKeyRequest{
				Provider: "openai",
				APIKey:   "test-key",
			})
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}
			resp := parseTestResponse(t, rec)
			if !tt.expectedFail && !resp.Success {
				t.Fatalf("expected success, got failure: %s", resp.Message)
			}
			if tt.expectedFail && resp.Success {
				t.Fatal("expected failure")
			}
			if resp.Message != tt.expectedMsg {
				t.Fatalf("expected '%s', got: %s", tt.expectedMsg, resp.Message)
			}
		})
	}
}

// === E2E: Base URL persistence ===

func TestE2E_BaseURLPersistence_SaveAndRetrieve(t *testing.T) {
	setupE2EDB(t)

	// Step 1: Save an API key with a custom base URL
	rec := putSettings(t, map[string]interface{}{
		"api_keys": map[string]string{
			"OPENAI_API_KEY": "sk-proj-abcdef1234567890",
		},
		"base_urls": map[string]string{
			"OPENAI_API_KEY": "https://my-proxy.example.com/v1",
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Step 2: Verify base URL is returned in GET
	resp := getSettingsResponse(t)
	baseURLs, ok := resp["base_urls"].(map[string]interface{})
	if !ok {
		t.Fatal("base_urls not a map in response")
	}
	url, ok := baseURLs["OPENAI_API_KEY"].(string)
	if !ok || url != "https://my-proxy.example.com/v1" {
		t.Fatalf("expected base URL https://my-proxy.example.com/v1, got %v", baseURLs["OPENAI_API_KEY"])
	}

	// Step 3: Verify the API key is also present and masked
	apiKeys := resp["api_keys"].(map[string]interface{})
	if apiKeys["OPENAI_API_KEY"] != "****7890" {
		t.Fatalf("expected masked key ****7890, got %v", apiKeys["OPENAI_API_KEY"])
	}
}

func TestE2E_BaseURLPersistence_UsedInConnectionTest(t *testing.T) {
	setupE2EDB(t)

	// Custom base URL server returns success
	customServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"models":["custom-model"]}`))
	}))
	defer customServer.Close()

	// Default config points to a server that will fail (port 1)
	origConfig := providerConfigs["openai"]
	providerConfigs["openai"] = providerTestConfig{
		BaseURL:    "http://127.0.0.1:1", // will fail
		Path:       "/v1/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	}
	defer func() { providerConfigs["openai"] = origConfig }()

	// Test with custom base_url override — should succeed by reaching customServer
	rec := postTestProviderKey(t, map[string]interface{}{
		"provider": "openai",
		"api_key":  "test-key",
		"base_url": customServer.URL,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	resp := parseTestResponse(t, rec)
	if !resp.Success {
		t.Fatalf("expected success with custom base URL, got: %s (details: %s)", resp.Message, resp.Details)
	}
}

func TestE2E_BaseURLPersistence_DeletedWithAPIKey(t *testing.T) {
	setupE2EDB(t)

	// Save API key with base URL
	putSettings(t, map[string]interface{}{
		"api_keys": map[string]string{
			"OPENAI_API_KEY": "sk-proj-abcdef1234567890",
		},
		"base_urls": map[string]string{
			"OPENAI_API_KEY": "https://my-proxy.example.com/v1",
		},
	})

	// Verify both exist
	resp := getSettingsResponse(t)
	baseURLs := resp["base_urls"].(map[string]interface{})
	if _, ok := baseURLs["OPENAI_API_KEY"]; !ok {
		t.Fatal("base URL should exist before deletion")
	}

	// Delete the API key
	rec := putSettings(t, map[string]interface{}{
		"delete_api_keys": []string{"OPENAI_API_KEY"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify both API key and base URL are gone
	resp = getSettingsResponse(t)
	apiKeys := resp["api_keys"].(map[string]interface{})
	if _, ok := apiKeys["OPENAI_API_KEY"]; ok {
		t.Fatal("API key should be deleted")
	}
	baseURLs = resp["base_urls"].(map[string]interface{})
	if _, ok := baseURLs["OPENAI_API_KEY"]; ok {
		t.Fatal("base URL should be deleted when API key is deleted")
	}
}

func TestE2E_BaseURLPersistence_MultipleProviders(t *testing.T) {
	setupE2EDB(t)

	// Save multiple providers with different base URLs
	rec := putSettings(t, map[string]interface{}{
		"api_keys": map[string]string{
			"OPENAI_API_KEY":    "sk-proj-abcdef1234567890",
			"ANTHROPIC_API_KEY": "sk-ant-test1234567890",
		},
		"base_urls": map[string]string{
			"OPENAI_API_KEY":    "https://openai-proxy.example.com/v1",
			"ANTHROPIC_API_KEY": "https://anthropic-proxy.example.com/v1",
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	resp := getSettingsResponse(t)
	baseURLs := resp["base_urls"].(map[string]interface{})
	if baseURLs["OPENAI_API_KEY"] != "https://openai-proxy.example.com/v1" {
		t.Fatalf("expected OpenAI base URL, got %v", baseURLs["OPENAI_API_KEY"])
	}
	if baseURLs["ANTHROPIC_API_KEY"] != "https://anthropic-proxy.example.com/v1" {
		t.Fatalf("expected Anthropic base URL, got %v", baseURLs["ANTHROPIC_API_KEY"])
	}

	// Delete only one — the other should remain
	putSettings(t, map[string]interface{}{
		"delete_api_keys": []string{"OPENAI_API_KEY"},
	})

	resp = getSettingsResponse(t)
	baseURLs = resp["base_urls"].(map[string]interface{})
	if _, ok := baseURLs["OPENAI_API_KEY"]; ok {
		t.Fatal("OpenAI base URL should be deleted")
	}
	if baseURLs["ANTHROPIC_API_KEY"] != "https://anthropic-proxy.example.com/v1" {
		t.Fatal("Anthropic base URL should remain")
	}
}

// === E2E: Provider analytics end-to-end ===

func TestE2E_Analytics_ConnectionTestRecordsTelemetry(t *testing.T) {
	setupE2EDB(t)

	// Mock server: first call succeeds, second fails
	callCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"models":[]}`))
		} else {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid key"}`))
		}
	}))
	defer mockServer.Close()

	origConfig := providerConfigs["openai"]
	providerConfigs["openai"] = providerTestConfig{
		BaseURL:    mockServer.URL,
		Path:       "/v1/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	}
	defer func() { providerConfigs["openai"] = origConfig }()

	// Test 1: successful connection
	postTestProviderKey(t, testProviderKeyRequest{Provider: "openai", APIKey: "key1"})
	// Test 2: failed connection
	postTestProviderKey(t, testProviderKeyRequest{Provider: "openai", APIKey: "key2"})

	// Query analytics endpoint
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/providers", nil)
	rec := httptest.NewRecorder()
	GetProviderAnalytics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var providers map[string]database.ProviderStats
	if err := json.Unmarshal(result["providers"], &providers); err != nil {
		t.Fatalf("unmarshal providers: %v", err)
	}

	openai, ok := providers["openai"]
	if !ok {
		t.Fatal("expected openai stats in analytics")
	}
	if openai.TotalRequests != 2 {
		t.Fatalf("expected 2 total requests, got %d", openai.TotalRequests)
	}
	if openai.ErrorCount != 1 {
		t.Fatalf("expected 1 error, got %d", openai.ErrorCount)
	}
	if openai.ErrorRate < 0.4 || openai.ErrorRate > 0.6 {
		t.Fatalf("expected error rate ~0.5, got %f", openai.ErrorRate)
	}
}

func TestE2E_Analytics_MultipleProviders(t *testing.T) {
	setupE2EDB(t)

	// Insert telemetry for multiple providers
	now := time.Now()
	entries := []database.ProviderTelemetry{
		{Provider: "openai", StatusCode: 200, Latency: 100, IsError: false, CreatedAt: now},
		{Provider: "openai", StatusCode: 200, Latency: 200, IsError: false, CreatedAt: now},
		{Provider: "openai", StatusCode: 401, Latency: 50, IsError: true, ErrorMsg: "Invalid API key", CreatedAt: now},
		{Provider: "anthropic", StatusCode: 200, Latency: 150, IsError: false, CreatedAt: now},
		{Provider: "anthropic", StatusCode: 200, Latency: 250, IsError: false, CreatedAt: now},
		{Provider: "groq", StatusCode: 500, Latency: 30, IsError: true, ErrorMsg: "Server error", CreatedAt: now},
	}
	for _, e := range entries {
		if err := database.RecordTelemetry(&e); err != nil {
			t.Fatalf("record telemetry: %v", err)
		}
	}

	// Query analytics
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/providers", nil)
	rec := httptest.NewRecorder()
	GetProviderAnalytics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]json.RawMessage
	json.Unmarshal(rec.Body.Bytes(), &result)

	var providers map[string]database.ProviderStats
	json.Unmarshal(result["providers"], &providers)

	// Verify all three providers present
	if len(providers) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(providers))
	}

	// Verify openai stats
	openai := providers["openai"]
	if openai.TotalRequests != 3 {
		t.Fatalf("expected 3 openai requests, got %d", openai.TotalRequests)
	}
	if openai.ErrorCount != 1 {
		t.Fatalf("expected 1 openai error, got %d", openai.ErrorCount)
	}
	if openai.LastError != "Invalid API key" {
		t.Fatalf("expected last error 'Invalid API key', got '%s'", openai.LastError)
	}

	// Verify anthropic stats (no errors)
	anthropic := providers["anthropic"]
	if anthropic.TotalRequests != 2 {
		t.Fatalf("expected 2 anthropic requests, got %d", anthropic.TotalRequests)
	}
	if anthropic.ErrorCount != 0 {
		t.Fatalf("expected 0 anthropic errors, got %d", anthropic.ErrorCount)
	}
	if anthropic.AvgLatency < 190 || anthropic.AvgLatency > 210 {
		t.Fatalf("expected avg latency ~200 for anthropic, got %f", anthropic.AvgLatency)
	}

	// Verify groq stats (all errors)
	groq := providers["groq"]
	if groq.TotalRequests != 1 {
		t.Fatalf("expected 1 groq request, got %d", groq.TotalRequests)
	}
	if groq.ErrorRate < 0.9 {
		t.Fatalf("expected error rate ~1.0 for groq, got %f", groq.ErrorRate)
	}
}

// === E2E: Full flow — configure provider, test connection, check analytics ===

func TestE2E_FullFlow_ConfigureTestAnalytics(t *testing.T) {
	setupE2EDB(t)

	// Step 1: Configure a provider with API key and base URL
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "Bearer sk-proj-full-flow-key123" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"models":["gpt-4"]}`))
		} else {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid"}`))
		}
	}))
	defer mockServer.Close()

	origConfig := providerConfigs["openai"]
	providerConfigs["openai"] = providerTestConfig{
		BaseURL:    "http://127.0.0.1:1",
		Path:       "/v1/models",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Method:     "GET",
	}
	defer func() { providerConfigs["openai"] = origConfig }()

	// Save the API key and base URL
	rec := putSettings(t, map[string]interface{}{
		"api_keys": map[string]string{
			"OPENAI_API_KEY": "sk-proj-full-flow-key123",
		},
		"base_urls": map[string]string{
			"OPENAI_API_KEY": mockServer.URL,
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("save settings failed: %d", rec.Code)
	}

	// Step 2: Verify settings were persisted
	resp := getSettingsResponse(t)
	apiKeys := resp["api_keys"].(map[string]interface{})
	if apiKeys["OPENAI_API_KEY"] != "****y123" {
		t.Fatalf("expected masked key ****y123, got %v", apiKeys["OPENAI_API_KEY"])
	}
	baseURLs := resp["base_urls"].(map[string]interface{})
	if baseURLs["OPENAI_API_KEY"] != mockServer.URL {
		t.Fatalf("expected base URL %s, got %v", mockServer.URL, baseURLs["OPENAI_API_KEY"])
	}

	// Step 3: Test the connection using the custom base URL
	testRec := postTestProviderKey(t, map[string]interface{}{
		"provider": "openai",
		"api_key":  "sk-proj-full-flow-key123",
		"base_url": mockServer.URL,
	})
	testResp := parseTestResponse(t, testRec)
	if !testResp.Success {
		t.Fatalf("connection test should succeed, got: %s", testResp.Message)
	}

	// Step 4: Verify analytics reflect the connection test
	analyticsReq := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/providers", nil)
	analyticsRec := httptest.NewRecorder()
	GetProviderAnalytics(analyticsRec, analyticsReq)

	var analyticsResult map[string]json.RawMessage
	json.Unmarshal(analyticsRec.Body.Bytes(), &analyticsResult)

	var providers map[string]database.ProviderStats
	json.Unmarshal(analyticsResult["providers"], &providers)

	openai, ok := providers["openai"]
	if !ok {
		t.Fatal("expected openai in analytics after connection test")
	}
	if openai.TotalRequests < 1 {
		t.Fatalf("expected at least 1 request, got %d", openai.TotalRequests)
	}
	if openai.ErrorCount != 0 {
		t.Fatalf("expected 0 errors, got %d", openai.ErrorCount)
	}
}

// === E2E: Batch operations on backend ===

func TestE2E_BatchDelete_MultipleProviders(t *testing.T) {
	setupE2EDB(t)

	// Setup: add 3 providers with keys and base URLs
	putSettings(t, map[string]interface{}{
		"api_keys": map[string]string{
			"ANTHROPIC_API_KEY": "sk-ant-test1234567890",
			"OPENAI_API_KEY":   "sk-proj-abcdef1234567890",
			"GROQ_API_KEY":     "gsk_test1234567890abcdef",
		},
		"base_urls": map[string]string{
			"OPENAI_API_KEY": "https://proxy.example.com/v1",
		},
	})

	// Verify all 3 exist
	resp := getSettingsResponse(t)
	apiKeys := resp["api_keys"].(map[string]interface{})
	if len(apiKeys) != 3 {
		t.Fatalf("expected 3 api keys, got %d", len(apiKeys))
	}

	// Batch delete all 3
	rec := putSettings(t, map[string]interface{}{
		"delete_api_keys": []string{
			"ANTHROPIC_API_KEY",
			"OPENAI_API_KEY",
			"GROQ_API_KEY",
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify all are gone
	resp = getSettingsResponse(t)
	apiKeys = resp["api_keys"].(map[string]interface{})
	if len(apiKeys) != 0 {
		t.Fatalf("expected 0 api keys after batch delete, got %d: %v", len(apiKeys), apiKeys)
	}

	// Base URL should also be gone
	baseURLs := resp["base_urls"].(map[string]interface{})
	if len(baseURLs) != 0 {
		t.Fatalf("expected 0 base_urls after batch delete, got %d", len(baseURLs))
	}
}

// === E2E: Export keys format validation ===

func TestE2E_ExportKeysFormat(t *testing.T) {
	setupE2EDB(t)

	// Setup providers
	putSettings(t, map[string]interface{}{
		"api_keys": map[string]string{
			"ANTHROPIC_API_KEY": "sk-ant-test1234567890",
			"OPENAI_API_KEY":   "sk-proj-abcdef1234567890",
		},
	})

	// Get settings and verify the masked format is correct for export
	resp := getSettingsResponse(t)
	apiKeys := resp["api_keys"].(map[string]interface{})

	// Verify masked key format (****XXXX)
	anthropicKey := apiKeys["ANTHROPIC_API_KEY"].(string)
	if anthropicKey != "****7890" {
		t.Fatalf("expected ****7890, got %s", anthropicKey)
	}
	openaiKey := apiKeys["OPENAI_API_KEY"].(string)
	if openaiKey != "****7890" {
		t.Fatalf("expected ****7890, got %s", openaiKey)
	}

	// Verify keys are available by name for export mapping
	for _, expected := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		if _, ok := apiKeys[expected]; !ok {
			t.Fatalf("expected key %s in api_keys for export", expected)
		}
	}
}

// === E2E: Analytics with telemetry cleanup ===

func TestE2E_Analytics_OldDataExcluded(t *testing.T) {
	setupE2EDB(t)

	// Insert old data (8 days ago)
	oldTime := time.Now().AddDate(0, 0, -8)
	database.RecordTelemetry(&database.ProviderTelemetry{
		Provider: "openai", StatusCode: 200, Latency: 100, IsError: false, CreatedAt: oldTime,
	})

	// Insert recent data
	database.RecordTelemetry(&database.ProviderTelemetry{
		Provider: "anthropic", StatusCode: 200, Latency: 150, IsError: false, CreatedAt: time.Now(),
	})

	// Analytics should only return anthropic
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/providers", nil)
	rec := httptest.NewRecorder()
	GetProviderAnalytics(rec, req)

	var result map[string]json.RawMessage
	json.Unmarshal(rec.Body.Bytes(), &result)

	var providers map[string]database.ProviderStats
	json.Unmarshal(result["providers"], &providers)

	if _, ok := providers["openai"]; ok {
		t.Fatal("old openai data should not appear in 7-day analytics")
	}
	if _, ok := providers["anthropic"]; !ok {
		t.Fatal("recent anthropic data should appear in analytics")
	}

	// Now cleanup and verify
	cutoff := time.Now().AddDate(0, 0, -7)
	if err := database.CleanupOldTelemetry(cutoff); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	var count int64
	database.DB.Model(&database.ProviderTelemetry{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 entry after cleanup, got %d", count)
	}
}
