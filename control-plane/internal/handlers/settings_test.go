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

// TestUpdateSettings_EnvVars_RestartsRunningInstances verifies that env var
// changes trigger restart of every running instance and that the response
// reports each one. The orchestrator is not initialized in this test, so
// restartInstanceAsync no-ops internally — but the response listing is still
// built from the DB query and is what we assert on.
func TestUpdateSettings_EnvVars_RestartsRunningInstances(t *testing.T) {
	setupSettingsTest(t)

	// Two running + one stopped instance — only the running ones should appear
	// in the restart list.
	database.DB.Create(&database.Instance{Name: "bot-alpha", DisplayName: "Alpha", Status: "running"})
	database.DB.Create(&database.Instance{Name: "bot-beta", DisplayName: "Beta", Status: "running"})
	database.DB.Create(&database.Instance{Name: "bot-gamma", DisplayName: "Gamma", Status: "stopped"})

	body := `{"env_vars_set":{"MYVAR":"hello"}}`
	req := httptest.NewRequest("POST", "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	UpdateSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}

	restarting, ok := resp["restarting_instances"].([]interface{})
	if !ok {
		t.Fatalf("restarting_instances missing or wrong type: %T %v", resp["restarting_instances"], resp["restarting_instances"])
	}
	if len(restarting) != 2 {
		t.Fatalf("restarting_instances len = %d, want 2 (running only)", len(restarting))
	}
	names := map[string]bool{}
	for _, item := range restarting {
		m, _ := item.(map[string]interface{})
		if name, ok := m["name"].(string); ok {
			names[name] = true
		}
	}
	if !names["bot-alpha"] || !names["bot-beta"] {
		t.Errorf("expected bot-alpha and bot-beta, got %v", names)
	}
	if names["bot-gamma"] {
		t.Error("stopped instance should not be in the restart list")
	}

	// The env var should also have been persisted.
	stored, _ := database.GetSetting("default_env_vars")
	if !strings.Contains(stored, "MYVAR") {
		t.Errorf("MYVAR not saved: %q", stored)
	}
}

// TestUpdateSettings_EnvVars_NoChangeMeansNoRestart guards the condition:
// non-env-var settings edits must not trigger restart.
func TestUpdateSettings_EnvVars_NoChangeMeansNoRestart(t *testing.T) {
	setupSettingsTest(t)
	database.DB.Create(&database.Instance{Name: "bot-alpha", DisplayName: "Alpha", Status: "running"})

	body := `{"default_cpu_request":"1000m"}`
	req := httptest.NewRequest("POST", "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	UpdateSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["restarting_instances"]; ok {
		t.Errorf("restarting_instances should be absent when no env vars changed: %v", resp["restarting_instances"])
	}
}

// TestUpdateSettings_EnvVars_NoOpSetSkipsRestart covers a realistic flow:
// the admin enters edit mode, saves without changes, or re-saves the same
// value. Backend decrypts the existing map, sees no diff, skips restart.
func TestUpdateSettings_EnvVars_NoOpSetSkipsRestart(t *testing.T) {
	setupSettingsTest(t)
	// Seed MYVAR with the same plaintext that the request is about to submit.
	initial, err := UpsertEncryptedEnvVarsJSON("{}", map[string]string{"MYVAR": "unchanged"}, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	database.SetSetting("default_env_vars", initial)
	database.DB.Create(&database.Instance{Name: "bot-alpha", DisplayName: "Alpha", Status: "running"})

	body := `{"env_vars_set":{"MYVAR":"unchanged"}}`
	req := httptest.NewRequest("POST", "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	UpdateSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["restarting_instances"]; ok {
		t.Errorf("no-op save must not restart anyone: %v", resp["restarting_instances"])
	}
}
