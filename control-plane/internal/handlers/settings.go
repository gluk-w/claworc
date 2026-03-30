package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
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
	"default_storage_home",
	"default_timezone",
	"default_user_agent",
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
			decrypted, err := utils.Decrypt(val)
			if err != nil {
				result[key] = ""
			} else {
				result[key] = utils.Mask(decrypted)
			}
		} else {
			result[key] = ""
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

	// Handle brave_api_key (fixed encrypted)
	if v, ok := raw["brave_api_key"]; ok {
		if strVal, ok := v.(string); ok {
			if strVal != "" {
				encrypted, err := utils.Encrypt(strVal)
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

	// Handle remaining plain settings
	for key, val := range raw {
		if key == "default_models" || key == "brave_api_key" {
			continue
		}
		if strVal, ok := val.(string); ok {
			database.SetSetting(key, strVal)
		}
	}

	allRaw := getAllSettings()
	writeJSON(w, http.StatusOK, settingsToResponse(allRaw))
}
