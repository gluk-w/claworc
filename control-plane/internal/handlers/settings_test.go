package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/crypto"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupTestDB creates a fresh in-memory SQLite database for each test.
func setupTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	database.DB = db
	if err := db.AutoMigrate(&database.Setting{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

// getSettingsResponse calls GetSettings and returns the parsed response.
func getSettingsResponse(t *testing.T) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	rec := httptest.NewRecorder()
	GetSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return result
}

// putSettings sends a PUT request to UpdateSettings and returns the recorder.
func putSettings(t *testing.T, payload interface{}) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	UpdateSettings(rec, req)
	return rec
}

// --- Tests ---

func TestUpdateSettings_APIKeys(t *testing.T) {
	setupTestDB(t)

	payload := map[string]interface{}{
		"api_keys": map[string]string{
			"ANTHROPIC_API_KEY": "sk-ant-test1234567890",
			"OPENAI_API_KEY":   "sk-proj-abcdef1234567890",
		},
	}

	rec := putSettings(t, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify keys are stored encrypted in the database
	var anthropicSetting database.Setting
	if err := database.DB.Where("key = ?", "api_key:ANTHROPIC_API_KEY").First(&anthropicSetting).Error; err != nil {
		t.Fatalf("fetch anthropic setting: %v", err)
	}
	// The stored value should NOT be the plaintext
	if anthropicSetting.Value == "sk-ant-test1234567890" {
		t.Fatal("API key stored in plaintext, expected encrypted")
	}
	// But it should decrypt back to the original
	decrypted, err := crypto.Decrypt(anthropicSetting.Value)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decrypted != "sk-ant-test1234567890" {
		t.Fatalf("expected sk-ant-test1234567890, got %s", decrypted)
	}

	// Verify GET returns masked keys
	resp := getSettingsResponse(t)
	apiKeys, ok := resp["api_keys"].(map[string]interface{})
	if !ok {
		t.Fatal("api_keys not a map in response")
	}
	maskedAnthropic, ok := apiKeys["ANTHROPIC_API_KEY"].(string)
	if !ok {
		t.Fatal("ANTHROPIC_API_KEY not in response")
	}
	if maskedAnthropic != "****7890" {
		t.Fatalf("expected ****7890, got %s", maskedAnthropic)
	}
	maskedOpenAI, ok := apiKeys["OPENAI_API_KEY"].(string)
	if !ok {
		t.Fatal("OPENAI_API_KEY not in response")
	}
	if maskedOpenAI != "****7890" {
		t.Fatalf("expected ****7890, got %s", maskedOpenAI)
	}
}

func TestUpdateSettings_DeleteAPIKeys(t *testing.T) {
	setupTestDB(t)

	// First, add a key
	putSettings(t, map[string]interface{}{
		"api_keys": map[string]string{
			"ANTHROPIC_API_KEY": "sk-ant-test1234567890",
			"OPENAI_API_KEY":   "sk-proj-abcdef1234567890",
		},
	})

	// Verify both keys exist
	resp := getSettingsResponse(t)
	apiKeys := resp["api_keys"].(map[string]interface{})
	if len(apiKeys) != 2 {
		t.Fatalf("expected 2 api_keys, got %d", len(apiKeys))
	}

	// Delete Anthropic key
	rec := putSettings(t, map[string]interface{}{
		"delete_api_keys": []string{"ANTHROPIC_API_KEY"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify only OpenAI remains
	resp = getSettingsResponse(t)
	apiKeys = resp["api_keys"].(map[string]interface{})
	if _, exists := apiKeys["ANTHROPIC_API_KEY"]; exists {
		t.Fatal("ANTHROPIC_API_KEY should have been deleted")
	}
	if _, exists := apiKeys["OPENAI_API_KEY"]; !exists {
		t.Fatal("OPENAI_API_KEY should still exist")
	}
}

func TestUpdateSettings_BraveAPIKey(t *testing.T) {
	setupTestDB(t)

	// Add Brave key (uses dedicated brave_api_key field, not api_keys)
	rec := putSettings(t, map[string]interface{}{
		"brave_api_key": "abcdefghijklmnopqrstuvwxyz123456",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify it's stored under "brave_api_key" key (not "api_key:BRAVE_API_KEY")
	var setting database.Setting
	if err := database.DB.Where("key = ?", "brave_api_key").First(&setting).Error; err != nil {
		t.Fatalf("brave_api_key not found in db: %v", err)
	}
	// Should be encrypted
	decrypted, err := crypto.Decrypt(setting.Value)
	if err != nil {
		t.Fatalf("decrypt brave key: %v", err)
	}
	if decrypted != "abcdefghijklmnopqrstuvwxyz123456" {
		t.Fatalf("expected original brave key, got %s", decrypted)
	}

	// Verify GET returns masked value
	resp := getSettingsResponse(t)
	maskedBrave, ok := resp["brave_api_key"].(string)
	if !ok {
		t.Fatal("brave_api_key not in response")
	}
	if maskedBrave != "****3456" {
		t.Fatalf("expected ****3456, got %s", maskedBrave)
	}

	// Brave should NOT appear in api_keys
	apiKeys := resp["api_keys"].(map[string]interface{})
	if _, exists := apiKeys["BRAVE_API_KEY"]; exists {
		t.Fatal("Brave should NOT be in api_keys map")
	}
}

func TestUpdateSettings_CombinedSaveAndDelete(t *testing.T) {
	setupTestDB(t)

	// Setup: add two keys
	putSettings(t, map[string]interface{}{
		"api_keys": map[string]string{
			"ANTHROPIC_API_KEY": "sk-ant-test1234567890",
			"OPENAI_API_KEY":   "sk-proj-abcdef1234567890",
		},
	})

	// Combine: add a new key + delete an existing one in the same request
	rec := putSettings(t, map[string]interface{}{
		"api_keys": map[string]string{
			"GROQ_API_KEY": "gsk_test1234567890abcdef",
		},
		"delete_api_keys": []string{"ANTHROPIC_API_KEY"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify state
	resp := getSettingsResponse(t)
	apiKeys := resp["api_keys"].(map[string]interface{})

	if _, exists := apiKeys["ANTHROPIC_API_KEY"]; exists {
		t.Fatal("ANTHROPIC_API_KEY should be deleted")
	}
	if _, exists := apiKeys["OPENAI_API_KEY"]; !exists {
		t.Fatal("OPENAI_API_KEY should still exist")
	}
	if _, exists := apiKeys["GROQ_API_KEY"]; !exists {
		t.Fatal("GROQ_API_KEY should have been added")
	}
}

func TestUpdateSettings_InvalidBody(t *testing.T) {
	setupTestDB(t)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	UpdateSettings(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestUpdateSettings_PersistenceAcrossRequests(t *testing.T) {
	setupTestDB(t)

	// Add key in first request
	putSettings(t, map[string]interface{}{
		"api_keys": map[string]string{
			"ANTHROPIC_API_KEY": "sk-ant-test1234567890",
		},
	})

	// Add another key in a second request
	putSettings(t, map[string]interface{}{
		"api_keys": map[string]string{
			"OPENAI_API_KEY": "sk-proj-abcdef1234567890",
		},
	})

	// Both keys should be present
	resp := getSettingsResponse(t)
	apiKeys := resp["api_keys"].(map[string]interface{})
	if len(apiKeys) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(apiKeys), apiKeys)
	}
}

func TestGetSettings_EmptyState(t *testing.T) {
	setupTestDB(t)

	resp := getSettingsResponse(t)

	// api_keys should be an empty map
	apiKeys, ok := resp["api_keys"].(map[string]interface{})
	if !ok {
		t.Fatal("api_keys not a map")
	}
	if len(apiKeys) != 0 {
		t.Fatalf("expected empty api_keys, got %v", apiKeys)
	}

	// brave_api_key should be empty string
	brave, ok := resp["brave_api_key"].(string)
	if !ok {
		t.Fatal("brave_api_key not a string")
	}
	if brave != "" {
		t.Fatalf("expected empty brave_api_key, got %s", brave)
	}
}

func TestUpdateSettings_BaseURLs(t *testing.T) {
	setupTestDB(t)

	// Save a base URL alongside an API key
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

	// Verify base URL is stored as plain text in the database
	var setting database.Setting
	if err := database.DB.Where("key = ?", "base_url:OPENAI_API_KEY").First(&setting).Error; err != nil {
		t.Fatalf("base_url setting not found in db: %v", err)
	}
	if setting.Value != "https://my-proxy.example.com/v1" {
		t.Fatalf("expected https://my-proxy.example.com/v1, got %s", setting.Value)
	}

	// Verify GET returns base URLs in the response
	resp := getSettingsResponse(t)
	baseURLs, ok := resp["base_urls"].(map[string]interface{})
	if !ok {
		t.Fatal("base_urls not a map in response")
	}
	url, ok := baseURLs["OPENAI_API_KEY"].(string)
	if !ok {
		t.Fatal("OPENAI_API_KEY not in base_urls response")
	}
	if url != "https://my-proxy.example.com/v1" {
		t.Fatalf("expected https://my-proxy.example.com/v1, got %s", url)
	}
}

func TestUpdateSettings_BaseURLDeletedWithAPIKey(t *testing.T) {
	setupTestDB(t)

	// Setup: add API key and base URL
	putSettings(t, map[string]interface{}{
		"api_keys": map[string]string{
			"OPENAI_API_KEY": "sk-proj-abcdef1234567890",
		},
		"base_urls": map[string]string{
			"OPENAI_API_KEY": "https://my-proxy.example.com/v1",
		},
	})

	// Delete the API key — base URL should also be removed
	rec := putSettings(t, map[string]interface{}{
		"delete_api_keys": []string{"OPENAI_API_KEY"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify base URL is gone
	resp := getSettingsResponse(t)
	baseURLs, ok := resp["base_urls"].(map[string]interface{})
	if !ok {
		t.Fatal("base_urls not a map in response")
	}
	if _, exists := baseURLs["OPENAI_API_KEY"]; exists {
		t.Fatal("base URL should have been deleted along with API key")
	}
}

func TestUpdateSettings_BaseURLClearedWithEmptyString(t *testing.T) {
	setupTestDB(t)

	// Setup: add API key and base URL
	putSettings(t, map[string]interface{}{
		"api_keys": map[string]string{
			"OPENAI_API_KEY": "sk-proj-abcdef1234567890",
		},
		"base_urls": map[string]string{
			"OPENAI_API_KEY": "https://my-proxy.example.com/v1",
		},
	})

	// Clear the base URL by sending empty string
	rec := putSettings(t, map[string]interface{}{
		"base_urls": map[string]string{
			"OPENAI_API_KEY": "",
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify base URL is gone but API key remains
	resp := getSettingsResponse(t)
	baseURLs := resp["base_urls"].(map[string]interface{})
	if _, exists := baseURLs["OPENAI_API_KEY"]; exists {
		t.Fatal("base URL should have been cleared")
	}
	apiKeys := resp["api_keys"].(map[string]interface{})
	if _, exists := apiKeys["OPENAI_API_KEY"]; !exists {
		t.Fatal("API key should still exist")
	}
}

func TestGetSettings_EmptyBaseURLs(t *testing.T) {
	setupTestDB(t)

	resp := getSettingsResponse(t)
	baseURLs, ok := resp["base_urls"].(map[string]interface{})
	if !ok {
		t.Fatal("base_urls not a map in response")
	}
	if len(baseURLs) != 0 {
		t.Fatalf("expected empty base_urls, got %v", baseURLs)
	}
}

func TestUpdateSettings_PayloadFormat(t *testing.T) {
	setupTestDB(t)

	// Simulate the exact payload format the frontend sends
	frontendPayload := map[string]interface{}{
		"api_keys": map[string]string{
			"ANTHROPIC_API_KEY": "sk-ant-test1234567890",
		},
		"brave_api_key":   "abcdefghijklmnopqrstuvwxyz123456",
		"delete_api_keys": []string{"OPENAI_API_KEY"},
	}

	rec := putSettings(t, frontendPayload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Parse the response and verify it has the expected structure
	var result map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &result)

	// Should have api_keys object
	if _, ok := result["api_keys"]; !ok {
		t.Fatal("response missing api_keys")
	}

	// Should have brave_api_key string
	if _, ok := result["brave_api_key"]; !ok {
		t.Fatal("response missing brave_api_key")
	}
}
