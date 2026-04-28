package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gluk-w/claworc/control-plane/internal/analytics"
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
	"default_agent_image",
	"default_browser_image",
	"default_browser_provider",
	"default_browser_idle_minutes",
	"default_browser_ready_seconds",
	"default_browser_storage",
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
	"analytics_consent",
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
		if key == "analytics_consent" {
			v := raw[key]
			if v == "" {
				v = analytics.ConsentUnset
			}
			result[key] = v
			continue
		}
		result[key] = raw[key]
	}

	// Read-only: surface installation_id (auto-generated on first GET) so the
	// settings UI can show the user the random ID we report alongside events.
	id, _ := analytics.GetOrCreateInstallationID()
	result["installation_id"] = id

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

	// Global env vars — decrypt and surface as plaintext. Settings is an
	// admin-only surface; masking offers no real confidentiality here and the
	// edit flow needs the live value to diff against.
	result["default_env_vars"] = EnvVarsForResponse(raw["default_env_vars"])

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

	// Handle env_vars_set / env_vars_unset (PATCH-style for the encrypted map).
	// envVarsChanged is true only when the resulting plaintext map actually
	// differs from what was stored — a no-op request (e.g. re-setting the same
	// value, or an empty set/unset pair) skips the save and skips the restart
	// that would otherwise cascade to every running instance.
	envSet, envUnset, err := parseEnvVarsDelta(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	envVarsChanged := false
	if len(envSet) > 0 || len(envUnset) > 0 {
		existing, _ := database.GetSetting("default_env_vars")
		updated, changed, err := ApplyEnvVarsDelta(existing, envSet, envUnset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to update env vars: "+err.Error())
			return
		}
		if changed {
			if err := database.SetSetting("default_env_vars", updated); err != nil {
				writeError(w, http.StatusInternalServerError, "Failed to save env vars")
				return
			}
			envVarsChanged = true
			analytics.Track(r.Context(), analytics.EventGlobalEnvVarsEdited, map[string]any{
				"total_env_vars": len(decodeEncryptedEnvVarsJSON(updated)),
			})
		}
	}

	// Handle remaining plain settings
	for key, val := range raw {
		if key == "default_models" || key == "brave_api_key" || key == "env_vars_set" || key == "env_vars_unset" {
			continue
		}
		// installation_id is read-only; never accept it on update.
		if key == "installation_id" {
			continue
		}
		if strVal, ok := val.(string); ok {
			if key == "analytics_consent" {
				if strVal != analytics.ConsentOptIn && strVal != analytics.ConsentOptOut {
					// Reject "unset" or unknown values — once shown the modal,
					// users can only flip between in and out.
					continue
				}
				prev := analytics.GetConsent()
				// Send the opt_out event BEFORE persisting so Track()'s consent
				// gate doesn't short-circuit it. Then store the new state.
				if strVal == analytics.ConsentOptOut && prev == analytics.ConsentOptIn {
					analytics.TrackForceOptOut()
				}
				database.SetSetting(key, strVal)
				continue
			}
			database.SetSetting(key, strVal)
		}
	}

	// Auto-restart every currently-running instance so the new global env vars
	// take effect. The container only injects env vars on (re)create, so without
	// this the DB and the live containers silently diverge.
	var restartingInstances []restartTarget
	if envVarsChanged {
		var running []database.Instance
		database.DB.Where("status = ?", "running").Find(&running)
		for i := range running {
			restartInstanceAsync(running[i], callerID(r))
			restartingInstances = append(restartingInstances, restartTarget{
				ID:          running[i].ID,
				Name:        running[i].Name,
				DisplayName: running[i].DisplayName,
			})
		}
	}

	resp := settingsToResponse(getAllSettings())
	if len(restartingInstances) > 0 {
		resp["restarting_instances"] = restartingInstances
	}
	writeJSON(w, http.StatusOK, resp)
}

type restartTarget struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// parseEnvVarsDelta extracts env_vars_set (map[string]string) and
// env_vars_unset ([]string) from the raw JSON body. Missing keys → empty
// results. Malformed values → error.
func parseEnvVarsDelta(raw map[string]interface{}) (map[string]string, []string, error) {
	set := map[string]string{}
	if v, ok := raw["env_vars_set"]; ok && v != nil {
		m, ok := v.(map[string]interface{})
		if !ok {
			return nil, nil, fmt.Errorf("env_vars_set must be an object")
		}
		for k, val := range m {
			s, ok := val.(string)
			if !ok {
				return nil, nil, fmt.Errorf("env_vars_set[%s] must be a string", k)
			}
			set[k] = s
		}
	}
	var unset []string
	if v, ok := raw["env_vars_unset"]; ok && v != nil {
		arr, ok := v.([]interface{})
		if !ok {
			return nil, nil, fmt.Errorf("env_vars_unset must be an array")
		}
		for _, item := range arr {
			s, ok := item.(string)
			if !ok {
				return nil, nil, fmt.Errorf("env_vars_unset items must be strings")
			}
			unset = append(unset, s)
		}
	}
	return set, unset, nil
}
