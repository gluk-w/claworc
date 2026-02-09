package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/glukw/claworc/internal/crypto"
	"github.com/glukw/claworc/internal/database"
)

// fixedEncryptedSettings are non-LLM keys stored as fixed setting entries.
var fixedEncryptedSettings = map[string]bool{
	"brave_api_key": true,
}

// plainSettings are returned as-is (not encrypted).
var plainSettings = []string{
	"default_container_image",
	"default_vnc_resolution",
	"default_cpu_request",
	"default_cpu_limit",
	"default_memory_request",
	"default_memory_limit",
	"default_storage_homebrew",
	"default_storage_clawd",
	"default_storage_chrome",
	"default_models",
}

func getAllSettings() map[string]string {
	var settings []database.Setting
	database.DB.Find(&settings)
	result := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
	}
	return result
}

func settingsToResponse(raw map[string]string) map[string]interface{} {
	result := make(map[string]interface{})

	// Plain settings
	for _, key := range plainSettings {
		if key == "default_models" {
			var models []string
			if err := json.Unmarshal([]byte(raw[key]), &models); err != nil || raw[key] == "" {
				models = []string{}
			}
			result[key] = models
			continue
		}
		result[key] = raw[key]
	}

	// Fixed encrypted settings (brave_api_key)
	for key := range fixedEncryptedSettings {
		val := raw[key]
		if val != "" {
			decrypted, err := crypto.Decrypt(val)
			if err != nil {
				result[key] = ""
			} else {
				result[key] = crypto.Mask(decrypted)
			}
		} else {
			result[key] = ""
		}
	}

	// Dynamic LLM API keys (api_key:* prefix)
	apiKeys := make(map[string]string)
	for k, v := range raw {
		if strings.HasPrefix(k, "api_key:") {
			keyName := strings.TrimPrefix(k, "api_key:")
			if v != "" {
				decrypted, err := crypto.Decrypt(v)
				if err != nil {
					apiKeys[keyName] = ""
				} else {
					apiKeys[keyName] = crypto.Mask(decrypted)
				}
			}
		}
	}
	result["api_keys"] = apiKeys

	// Backward compat: also emit old fields from migrated data
	for _, old := range []struct{ oldKey, newKey string }{
		{"anthropic_api_key", "api_key:ANTHROPIC_API_KEY"},
		{"openai_api_key", "api_key:OPENAI_API_KEY"},
	} {
		if _, exists := result[old.oldKey]; !exists {
			val := raw[old.newKey]
			if val != "" {
				decrypted, err := crypto.Decrypt(val)
				if err != nil {
					result[old.oldKey] = ""
				} else {
					result[old.oldKey] = crypto.Mask(decrypted)
				}
			} else {
				result[old.oldKey] = ""
			}
		}
	}

	return result
}

func GetSettings(w http.ResponseWriter, r *http.Request) {
	raw := getAllSettings()
	writeJSON(w, http.StatusOK, settingsToResponse(raw))
}

type settingsUpdateRequest struct {
	DefaultModels *json.RawMessage       `json:"default_models,omitempty"`
	APIKeys       map[string]string      `json:"api_keys,omitempty"`
	DeleteAPIKeys []string               `json:"delete_api_keys,omitempty"`
	BraveAPIKey   *string                `json:"brave_api_key,omitempty"`
	Plain         map[string]interface{} `json:"-"` // remaining plain fields
}

func UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var raw map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Handle default_models
	if v, ok := raw["default_models"]; ok {
		b, err := json.Marshal(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid default_models")
			return
		}
		database.SetSetting("default_models", string(b))
	}

	// Handle dynamic LLM api_keys
	if v, ok := raw["api_keys"]; ok {
		keysMap, ok := v.(map[string]interface{})
		if !ok {
			writeError(w, http.StatusBadRequest, "api_keys must be an object")
			return
		}
		for keyName, keyVal := range keysMap {
			strVal, ok := keyVal.(string)
			if !ok {
				continue
			}
			settingKey := "api_key:" + keyName
			if strVal != "" {
				encrypted, err := crypto.Encrypt(strVal)
				if err != nil {
					writeError(w, http.StatusInternalServerError, "Failed to encrypt API key")
					return
				}
				database.SetSetting(settingKey, encrypted)
			} else {
				database.SetSetting(settingKey, "")
			}
		}
	}

	// Handle delete_api_keys
	if v, ok := raw["delete_api_keys"]; ok {
		arr, ok := v.([]interface{})
		if ok {
			for _, item := range arr {
				if keyName, ok := item.(string); ok {
					database.DeleteSetting("api_key:" + keyName)
				}
			}
		}
	}

	// Handle brave_api_key (fixed encrypted)
	if v, ok := raw["brave_api_key"]; ok {
		if strVal, ok := v.(string); ok {
			if strVal != "" {
				encrypted, err := crypto.Encrypt(strVal)
				if err != nil {
					writeError(w, http.StatusInternalServerError, "Failed to encrypt API key")
					return
				}
				database.SetSetting("brave_api_key", encrypted)
			} else {
				database.SetSetting("brave_api_key", "")
			}
		}
	}

	// Handle old-format anthropic_api_key / openai_api_key (backward compat)
	for _, old := range []struct{ oldKey, newKey string }{
		{"anthropic_api_key", "api_key:ANTHROPIC_API_KEY"},
		{"openai_api_key", "api_key:OPENAI_API_KEY"},
	} {
		if v, ok := raw[old.oldKey]; ok {
			if strVal, ok := v.(string); ok && strVal != "" {
				encrypted, err := crypto.Encrypt(strVal)
				if err != nil {
					writeError(w, http.StatusInternalServerError, "Failed to encrypt API key")
					return
				}
				database.SetSetting(old.newKey, encrypted)
			}
		}
	}

	// Handle remaining plain settings
	for key, val := range raw {
		if key == "default_models" || key == "api_keys" || key == "delete_api_keys" || key == "brave_api_key" || key == "anthropic_api_key" || key == "openai_api_key" {
			continue
		}
		if strVal, ok := val.(string); ok {
			database.SetSetting(key, strVal)
		}
	}

	allRaw := getAllSettings()
	writeJSON(w, http.StatusOK, settingsToResponse(allRaw))
}
