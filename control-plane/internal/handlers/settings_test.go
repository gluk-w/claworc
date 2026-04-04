package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

func setupSettingsTest(t *testing.T) {
	t.Helper()
	setupTestDB(t)
	// Seed defaults so settings exist
	for _, key := range plainSettings {
		database.SetSetting(key, "")
	}
	database.SetSetting("default_models", "[]")
}

func TestGetSettings_ReturnsPlainSettings(t *testing.T) {
	setupSettingsTest(t)
	database.SetSetting("default_container_image", "glukw/openclaw-vnc-chromium:latest")
	database.SetSetting("default_cpu_request", "500m")

	w := httptest.NewRecorder()
	GetSettings(w, httptest.NewRequest("GET", "/api/v1/settings", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)

	if body["default_container_image"] != "glukw/openclaw-vnc-chromium:latest" {
		t.Errorf("container_image = %v", body["default_container_image"])
	}
	if body["default_cpu_request"] != "500m" {
		t.Errorf("cpu_request = %v", body["default_cpu_request"])
	}
}

func TestGetSettings_DefaultModelsAsArray(t *testing.T) {
	setupSettingsTest(t)
	database.SetSetting("default_models", `["gpt-4","claude-3"]`)

	w := httptest.NewRecorder()
	GetSettings(w, httptest.NewRequest("GET", "/api/v1/settings", nil))

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	models, ok := body["default_models"].([]interface{})
	if !ok {
		t.Fatalf("default_models not an array: %T", body["default_models"])
	}
	if len(models) != 2 {
		t.Errorf("models len = %d, want 2", len(models))
	}
}

func TestGetSettings_BraveKeyMasked(t *testing.T) {
	setupSettingsTest(t)
	encrypted, _ := utils.Encrypt("sk-real-api-key-12345")
	database.SetSetting("brave_api_key", encrypted)

	w := httptest.NewRecorder()
	GetSettings(w, httptest.NewRequest("GET", "/api/v1/settings", nil))

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	val, _ := body["brave_api_key"].(string)
	if val == "sk-real-api-key-12345" {
		t.Error("brave_api_key returned in plain text")
	}
	if !strings.HasPrefix(val, "****") {
		t.Errorf("brave_api_key not masked: %q", val)
	}
}

func TestGetSettings_BraveKeyEmpty(t *testing.T) {
	setupSettingsTest(t)

	w := httptest.NewRecorder()
	GetSettings(w, httptest.NewRequest("GET", "/api/v1/settings", nil))

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["brave_api_key"] != "" {
		t.Errorf("empty brave_api_key = %v, want empty", body["brave_api_key"])
	}
}

func TestUpdateSettings_PlainSetting(t *testing.T) {
	setupSettingsTest(t)

	w := httptest.NewRecorder()
	UpdateSettings(w, postJSON("/api/v1/settings", map[string]string{
		"default_cpu_request": "1000m",
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	val, _ := database.GetSetting("default_cpu_request")
	if val != "1000m" {
		t.Errorf("setting = %q, want 1000m", val)
	}
}

func TestUpdateSettings_BraveKey_EncryptsAndMasks(t *testing.T) {
	setupSettingsTest(t)

	w := httptest.NewRecorder()
	UpdateSettings(w, postJSON("/api/v1/settings", map[string]string{
		"brave_api_key": "sk-new-key-value",
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// Verify stored value is encrypted
	stored, _ := database.GetSetting("brave_api_key")
	if stored == "sk-new-key-value" {
		t.Error("brave_api_key stored in plain text")
	}
	if stored == "" {
		t.Error("brave_api_key not stored")
	}

	// Verify can decrypt
	decrypted, err := utils.Decrypt(stored)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decrypted != "sk-new-key-value" {
		t.Errorf("decrypted = %q, want sk-new-key-value", decrypted)
	}

	// Verify response is masked
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	val, _ := body["brave_api_key"].(string)
	if !strings.HasPrefix(val, "****") {
		t.Errorf("response not masked: %q", val)
	}
}

func TestUpdateSettings_BraveKey_ClearEmpty(t *testing.T) {
	setupSettingsTest(t)
	encrypted, _ := utils.Encrypt("old-key")
	database.SetSetting("brave_api_key", encrypted)

	w := httptest.NewRecorder()
	UpdateSettings(w, postJSON("/api/v1/settings", map[string]string{
		"brave_api_key": "",
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	val, _ := database.GetSetting("brave_api_key")
	if val != "" {
		t.Errorf("brave_api_key should be empty after clear, got %q", val)
	}
}

func TestUpdateSettings_DefaultModels(t *testing.T) {
	setupSettingsTest(t)

	body := `{"default_models":["model-a","model-b"]}`
	req := httptest.NewRequest("POST", "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	UpdateSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	val, _ := database.GetSetting("default_models")
	if val != `["model-a","model-b"]` {
		t.Errorf("default_models = %q", val)
	}
}
