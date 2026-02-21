package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/crypto"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/logutil"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
	"github.com/gluk-w/claworc/control-plane/internal/sshfiles"
	"github.com/gluk-w/claworc/control-plane/internal/sshkeys"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
	"github.com/go-chi/chi/v5"
)

type modelsConfig struct {
	Disabled []string `json:"disabled"`
	Extra    []string `json:"extra"`
}

type instanceCreateRequest struct {
	DisplayName     string            `json:"display_name"`
	CPURequest      string            `json:"cpu_request"`
	CPULimit        string            `json:"cpu_limit"`
	MemoryRequest   string            `json:"memory_request"`
	MemoryLimit     string            `json:"memory_limit"`
	StorageHomebrew string            `json:"storage_homebrew"`
	StorageClawd    string            `json:"storage_clawd"`
	StorageChrome   string            `json:"storage_chrome"`
	BraveAPIKey     *string           `json:"brave_api_key"`
	APIKeys         map[string]string `json:"api_keys"`
	Models          *modelsConfig     `json:"models"`
	DefaultModel    string            `json:"default_model"`
	ContainerImage  *string           `json:"container_image"`
	VNCResolution   *string           `json:"vnc_resolution"`
}

type modelsResponse struct {
	Effective        []string `json:"effective"`
	DisabledDefaults []string `json:"disabled_defaults"`
	Extra            []string `json:"extra"`
}

type instanceResponse struct {
	ID                    uint            `json:"id"`
	Name                  string          `json:"name"`
	DisplayName           string          `json:"display_name"`
	Status                string          `json:"status"`
	CPURequest            string          `json:"cpu_request"`
	CPULimit              string          `json:"cpu_limit"`
	MemoryRequest         string          `json:"memory_request"`
	MemoryLimit           string          `json:"memory_limit"`
	StorageHomebrew       string          `json:"storage_homebrew"`
	StorageClawd          string          `json:"storage_clawd"`
	StorageChrome         string          `json:"storage_chrome"`
	HasBraveOverride      bool            `json:"has_brave_override"`
	APIKeyOverrides       []string        `json:"api_key_overrides"`
	Models                *modelsResponse `json:"models"`
	DefaultModel          string          `json:"default_model"`
	ContainerImage        *string         `json:"container_image"`
	HasImageOverride      bool            `json:"has_image_override"`
	VNCResolution         *string         `json:"vnc_resolution"`
	HasResolutionOverride bool            `json:"has_resolution_override"`
	ControlURL            string          `json:"control_url"`
	GatewayToken          string          `json:"gateway_token"`
	AllowedSourceIPs      string          `json:"allowed_source_ips"`
	SortOrder             int             `json:"sort_order"`
	CreatedAt             string          `json:"created_at"`
	UpdatedAt             string          `json:"updated_at"`
}

func generateName(displayName string) string {
	name := strings.ToLower(displayName)
	name = regexp.MustCompile(`[\s_]+`).ReplaceAllString(name, "-")
	name = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(name, "")
	name = strings.Trim(name, "-")
	name = "bot-" + name
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func formatTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

func getInstanceAPIKeyNames(instanceID uint) []string {
	var keys []database.InstanceAPIKey
	database.DB.Where("instance_id = ?", instanceID).Find(&keys)
	names := make([]string, 0, len(keys))
	for _, k := range keys {
		names = append(names, k.KeyName)
	}
	return names
}

func parseModelsConfig(raw string) modelsConfig {
	var mc modelsConfig
	if raw != "" {
		json.Unmarshal([]byte(raw), &mc)
	}
	if mc.Disabled == nil {
		mc.Disabled = []string{}
	}
	if mc.Extra == nil {
		mc.Extra = []string{}
	}
	return mc
}

func computeEffectiveModels(mc modelsConfig) []string {
	// Get global default models
	defaultModelsJSON, _ := database.GetSetting("default_models")
	var defaults []string
	if defaultModelsJSON != "" {
		json.Unmarshal([]byte(defaultModelsJSON), &defaults)
	}

	disabledSet := make(map[string]bool)
	for _, d := range mc.Disabled {
		disabledSet[d] = true
	}

	var effective []string
	for _, m := range defaults {
		if !disabledSet[m] {
			effective = append(effective, m)
		}
	}
	effective = append(effective, mc.Extra...)
	if effective == nil {
		effective = []string{}
	}
	return effective
}

// resolveInstanceModelsAndKeys builds the effective model list and collects all
// decrypted API keys (global + instance overrides) for pushing to the running instance.
func resolveInstanceModelsAndKeys(inst database.Instance) ([]string, map[string]string) {
	mc := parseModelsConfig(inst.ModelsConfig)
	effective := computeEffectiveModels(mc)

	// Collect API keys: start with global, overlay instance overrides
	apiKeys := make(map[string]string)

	// Global keys from settings (api_key:* prefix)
	var settings []database.Setting
	database.DB.Where("key LIKE ?", "api_key:%").Find(&settings)
	for _, s := range settings {
		keyName := strings.TrimPrefix(s.Key, "api_key:")
		if s.Value != "" {
			decrypted, err := crypto.Decrypt(s.Value)
			if err == nil {
				apiKeys[keyName] = decrypted
			}
		}
	}

	// Instance-level overrides (take precedence)
	var instKeys []database.InstanceAPIKey
	database.DB.Where("instance_id = ?", inst.ID).Find(&instKeys)
	for _, k := range instKeys {
		decrypted, err := crypto.Decrypt(k.KeyValue)
		if err == nil {
			apiKeys[k.KeyName] = decrypted
		}
	}

	// Remove keys for disabled providers
	disabledSet := make(map[string]bool)
	for _, d := range mc.Disabled {
		disabledSet[d] = true
	}
	for keyName := range apiKeys {
		if disabledSet[keyName] {
			delete(apiKeys, keyName)
		}
	}

	// Also include Brave key if set
	if inst.BraveAPIKey != "" {
		decrypted, err := crypto.Decrypt(inst.BraveAPIKey)
		if err == nil {
			apiKeys["BRAVE_API_KEY"] = decrypted
		}
	} else {
		globalBrave, _ := database.GetSetting("brave_api_key")
		if globalBrave != "" {
			decrypted, err := crypto.Decrypt(globalBrave)
			if err == nil {
				apiKeys["BRAVE_API_KEY"] = decrypted
			}
		}
	}

	return effective, apiKeys
}

func instanceToResponse(inst database.Instance, status string) instanceResponse {
	var containerImage *string
	if inst.ContainerImage != "" {
		containerImage = &inst.ContainerImage
	}
	var vncResolution *string
	if inst.VNCResolution != "" {
		vncResolution = &inst.VNCResolution
	}
	var gatewayToken string
	if inst.GatewayToken != "" {
		gatewayToken, _ = crypto.Decrypt(inst.GatewayToken)
	}

	apiKeyOverrides := getInstanceAPIKeyNames(inst.ID)

	mc := parseModelsConfig(inst.ModelsConfig)
	effective := computeEffectiveModels(mc)

	return instanceResponse{
		ID:                    inst.ID,
		Name:                  inst.Name,
		DisplayName:           inst.DisplayName,
		Status:                status,
		CPURequest:            inst.CPURequest,
		CPULimit:              inst.CPULimit,
		MemoryRequest:         inst.MemoryRequest,
		MemoryLimit:           inst.MemoryLimit,
		StorageHomebrew:       inst.StorageHomebrew,
		StorageClawd:          inst.StorageClawd,
		StorageChrome:         inst.StorageChrome,
		HasBraveOverride:      inst.BraveAPIKey != "",
		APIKeyOverrides:       apiKeyOverrides,
		Models:                &modelsResponse{Effective: effective, DisabledDefaults: mc.Disabled, Extra: mc.Extra},
		DefaultModel:          inst.DefaultModel,
		ContainerImage:        containerImage,
		HasImageOverride:      inst.ContainerImage != "",
		VNCResolution:         vncResolution,
		HasResolutionOverride: inst.VNCResolution != "",
		ControlURL:            fmt.Sprintf("/api/v1/instances/%d/control/", inst.ID),
		GatewayToken:          gatewayToken,
		AllowedSourceIPs:      inst.AllowedSourceIPs,
		SortOrder:             inst.SortOrder,
		CreatedAt:             formatTimestamp(inst.CreatedAt),
		UpdatedAt:             formatTimestamp(inst.UpdatedAt),
	}
}

func resolveStatus(inst *database.Instance, orchStatus string) string {
	if inst.Status == "stopping" {
		if orchStatus == "stopped" {
			database.DB.Model(inst).Updates(map[string]interface{}{
				"status":     "stopped",
				"updated_at": time.Now().UTC(),
			})
			return "stopped"
		}
		return "stopping"
	}

	if inst.Status == "error" && orchStatus == "stopped" {
		return "failed"
	}

	if inst.Status != "restarting" {
		return orchStatus
	}

	if orchStatus != "running" {
		return "restarting"
	}

	if !inst.UpdatedAt.IsZero() {
		if time.Since(inst.UpdatedAt) < 15*time.Second {
			return "restarting"
		}
	}

	database.DB.Model(inst).Updates(map[string]interface{}{
		"status":     "running",
		"updated_at": time.Now().UTC(),
	})
	return "running"
}

func getEffectiveImage(inst database.Instance) string {
	if inst.ContainerImage != "" {
		return inst.ContainerImage
	}
	val, err := database.GetSetting("default_container_image")
	if err == nil && val != "" {
		return val
	}
	return ""
}

func getEffectiveResolution(inst database.Instance) string {
	if inst.VNCResolution != "" {
		return inst.VNCResolution
	}
	val, err := database.GetSetting("default_vnc_resolution")
	if err == nil && val != "" {
		return val
	}
	return "1920x1080"
}

func ListInstances(w http.ResponseWriter, r *http.Request) {
	var instances []database.Instance
	user := middleware.GetUser(r)

	query := database.DB.Order("sort_order ASC, id ASC")
	if user != nil && user.Role != "admin" {
		// Non-admin users only see assigned instances
		assignedIDs, err := database.GetUserInstances(user.ID)
		if err != nil || len(assignedIDs) == 0 {
			writeJSON(w, http.StatusOK, []instanceResponse{})
			return
		}
		query = query.Where("id IN ?", assignedIDs)
	}

	if err := query.Find(&instances).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list instances")
		return
	}

	orch := orchestrator.Get()
	responses := make([]instanceResponse, 0, len(instances))
	for i := range instances {
		orchStatus := "stopped"
		if orch != nil {
			s, _ := orch.GetInstanceStatus(r.Context(), instances[i].Name)
			orchStatus = s
		}
		status := resolveStatus(&instances[i], orchStatus)
		responses = append(responses, instanceToResponse(instances[i], status))
	}

	writeJSON(w, http.StatusOK, responses)
}

func saveInstanceAPIKeys(instanceID uint, apiKeys map[string]string) error {
	for keyName, keyValue := range apiKeys {
		if keyValue == "" {
			// Delete the key
			database.DB.Where("instance_id = ? AND key_name = ?", instanceID, keyName).Delete(&database.InstanceAPIKey{})
			continue
		}
		encrypted, err := crypto.Encrypt(keyValue)
		if err != nil {
			return fmt.Errorf("encrypt key %s: %w", keyName, err)
		}
		var existing database.InstanceAPIKey
		result := database.DB.Where("instance_id = ? AND key_name = ?", instanceID, keyName).First(&existing)
		if result.Error != nil {
			// Create new
			if err := database.DB.Create(&database.InstanceAPIKey{
				InstanceID: instanceID,
				KeyName:    keyName,
				KeyValue:   encrypted,
			}).Error; err != nil {
				return err
			}
		} else {
			// Update existing
			database.DB.Model(&existing).Update("key_value", encrypted)
		}
	}
	return nil
}

func CreateInstance(w http.ResponseWriter, r *http.Request) {
	var body instanceCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if body.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "display_name is required")
		return
	}

	// Set defaults
	if body.CPURequest == "" {
		body.CPURequest = "500m"
	}
	if body.CPULimit == "" {
		body.CPULimit = "2000m"
	}
	if body.MemoryRequest == "" {
		body.MemoryRequest = "1Gi"
	}
	if body.MemoryLimit == "" {
		body.MemoryLimit = "4Gi"
	}
	if body.StorageHomebrew == "" {
		body.StorageHomebrew = "10Gi"
	}
	if body.StorageClawd == "" {
		body.StorageClawd = "5Gi"
	}
	if body.StorageChrome == "" {
		body.StorageChrome = "5Gi"
	}

	name := generateName(body.DisplayName)

	// Check uniqueness
	var count int64
	database.DB.Model(&database.Instance{}).Where("name = ?", name).Count(&count)
	if count > 0 {
		writeError(w, http.StatusConflict, fmt.Sprintf("Instance name '%s' already exists", name))
		return
	}

	// Encrypt Brave API key (stays as fixed field)
	var encBraveKey string
	if body.BraveAPIKey != nil && *body.BraveAPIKey != "" {
		var err error
		encBraveKey, err = crypto.Encrypt(*body.BraveAPIKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to encrypt API key")
			return
		}
	}

	// Generate gateway token
	gatewayTokenPlain := generateToken()
	encGatewayToken, err := crypto.Encrypt(gatewayTokenPlain)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to encrypt gateway token")
		return
	}

	var containerImage string
	if body.ContainerImage != nil {
		containerImage = *body.ContainerImage
	}
	var vncResolution string
	if body.VNCResolution != nil {
		vncResolution = *body.VNCResolution
	}

	// Serialize models config
	var modelsConfigJSON string
	if body.Models != nil {
		if body.Models.Disabled == nil {
			body.Models.Disabled = []string{}
		}
		if body.Models.Extra == nil {
			body.Models.Extra = []string{}
		}
		b, _ := json.Marshal(body.Models)
		modelsConfigJSON = string(b)
	} else {
		modelsConfigJSON = "{}"
	}

	// Generate SSH key pair for the instance
	sshPubKey, sshPrivKey, err := sshkeys.GenerateKeyPair()
	if err != nil {
		log.Printf("Failed to generate SSH key pair for %s: %v", logutil.SanitizeForLog(name), err)
		writeError(w, http.StatusInternalServerError, "Failed to generate SSH keys")
		return
	}

	sshKeyPath, err := sshkeys.SavePrivateKey(name, sshPrivKey)
	if err != nil {
		log.Printf("Failed to save SSH private key for %s: %v", logutil.SanitizeForLog(name), err)
		writeError(w, http.StatusInternalServerError, "Failed to save SSH key")
		return
	}

	sshAuthorizedKey, err := sshkeys.FormatPublicKeyForAuthorizedKeys(sshPubKey)
	if err != nil {
		log.Printf("Failed to format SSH public key for %s: %v", logutil.SanitizeForLog(name), err)
		writeError(w, http.StatusInternalServerError, "Failed to format SSH key")
		return
	}

	sshFingerprint, err := sshkeys.GetPublicKeyFingerprint(sshPubKey)
	if err != nil {
		log.Printf("Failed to compute SSH key fingerprint for %s: %v", logutil.SanitizeForLog(name), err)
		writeError(w, http.StatusInternalServerError, "Failed to compute SSH key fingerprint")
		return
	}

	// Compute next sort_order
	var maxSortOrder int
	database.DB.Model(&database.Instance{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxSortOrder)

	inst := database.Instance{
		Name:              name,
		DisplayName:       body.DisplayName,
		Status:            "creating",
		CPURequest:        body.CPURequest,
		CPULimit:          body.CPULimit,
		MemoryRequest:     body.MemoryRequest,
		MemoryLimit:       body.MemoryLimit,
		StorageHomebrew:   body.StorageHomebrew,
		StorageClawd:      body.StorageClawd,
		StorageChrome:     body.StorageChrome,
		BraveAPIKey:       encBraveKey,
		ContainerImage:    containerImage,
		VNCResolution:     vncResolution,
		GatewayToken:      encGatewayToken,
		ModelsConfig:      modelsConfigJSON,
		DefaultModel:      body.DefaultModel,
		SSHPublicKey:      string(sshPubKey),
		SSHPrivateKeyPath: sshKeyPath,
		SSHKeyFingerprint: sshFingerprint,
		SSHPort:           22,
		SortOrder:         maxSortOrder + 1,
	}

	if err := database.DB.Create(&inst).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create instance")
		return
	}

	// Save API keys to the new table
	allAPIKeys := make(map[string]string)
	for k, v := range body.APIKeys {
		allAPIKeys[k] = v
	}
	if len(allAPIKeys) > 0 {
		if err := saveInstanceAPIKeys(inst.ID, allAPIKeys); err != nil {
			log.Printf("Failed to save API keys for instance %d: %v", inst.ID, err)
		}
	}

	effectiveImage := getEffectiveImage(inst)
	effectiveResolution := getEffectiveResolution(inst)

	// Launch container creation asynchronously (image pull can take minutes)
	go func() {
		ctx := context.Background()
		orch := orchestrator.Get()
		if orch == nil {
			database.DB.Model(&inst).Update("status", "error")
			return
		}

		envVars := map[string]string{}
		if gatewayTokenPlain != "" {
			envVars["OPENCLAW_GATEWAY_TOKEN"] = gatewayTokenPlain
		}

		err := orch.CreateInstance(ctx, orchestrator.CreateParams{
			Name:            name,
			CPURequest:      body.CPURequest,
			CPULimit:        body.CPULimit,
			MemoryRequest:   body.MemoryRequest,
			MemoryLimit:     body.MemoryLimit,
			StorageHomebrew: body.StorageHomebrew,
			StorageClawd:    body.StorageClawd,
			StorageChrome:   body.StorageChrome,
			ContainerImage:  effectiveImage,
			VNCResolution:   effectiveResolution,
			EnvVars:         envVars,
			SSHPublicKey:    sshAuthorizedKey,
		})
		if err != nil {
			log.Printf("Failed to create container resources for %s: %v", logutil.SanitizeForLog(name), err)
			database.DB.Model(&inst).Update("status", "error")
			return
		}
		database.DB.Model(&inst).Updates(map[string]interface{}{
			"status":     "running",
			"updated_at": time.Now().UTC(),
		})

		// Push models and API keys to the instance (waits for container ready)
		database.DB.First(&inst, inst.ID)
		models, resolvedKeys := resolveInstanceModelsAndKeys(inst)
		if ops, ok := orch.(config.InstanceOps); ok {
			config.ConfigureInstance(ctx, ops, name, models, resolvedKeys)
		}
	}()

	writeJSON(w, http.StatusCreated, instanceToResponse(inst, "creating"))
}

func GetInstance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	orchStatus := "stopped"
	if orch := orchestrator.Get(); orch != nil {
		orchStatus, _ = orch.GetInstanceStatus(r.Context(), inst.Name)
	}
	status := resolveStatus(&inst, orchStatus)
	writeJSON(w, http.StatusOK, instanceToResponse(inst, status))
}

type instanceUpdateRequest struct {
	APIKeys      map[string]*string `json:"api_keys"` // null value = delete
	BraveAPIKey  *string            `json:"brave_api_key"`
	Models       *modelsConfig      `json:"models"`
	DefaultModel *string            `json:"default_model"`
}

func UpdateInstance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	var body instanceUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Update API keys
	if body.APIKeys != nil {
		for keyName, keyVal := range body.APIKeys {
			if keyVal == nil || *keyVal == "" {
				// Delete
				database.DB.Where("instance_id = ? AND key_name = ?", inst.ID, keyName).Delete(&database.InstanceAPIKey{})
			} else {
				encrypted, err := crypto.Encrypt(*keyVal)
				if err != nil {
					writeError(w, http.StatusInternalServerError, "Failed to encrypt API key")
					return
				}
				var existing database.InstanceAPIKey
				result := database.DB.Where("instance_id = ? AND key_name = ?", inst.ID, keyName).First(&existing)
				if result.Error != nil {
					database.DB.Create(&database.InstanceAPIKey{
						InstanceID: inst.ID,
						KeyName:    keyName,
						KeyValue:   encrypted,
					})
				} else {
					database.DB.Model(&existing).Update("key_value", encrypted)
				}
			}
		}
	}

	// Update Brave API key
	if body.BraveAPIKey != nil {
		if *body.BraveAPIKey != "" {
			encrypted, err := crypto.Encrypt(*body.BraveAPIKey)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "Failed to encrypt API key")
				return
			}
			database.DB.Model(&inst).Update("brave_api_key", encrypted)
		} else {
			database.DB.Model(&inst).Update("brave_api_key", "")
		}
	}

	// Update default model
	if body.DefaultModel != nil {
		database.DB.Model(&inst).Update("default_model", *body.DefaultModel)
	}

	// Update models config
	if body.Models != nil {
		if body.Models.Disabled == nil {
			body.Models.Disabled = []string{}
		}
		if body.Models.Extra == nil {
			body.Models.Extra = []string{}
		}
		b, _ := json.Marshal(body.Models)
		database.DB.Model(&inst).Update("models_config", string(b))
	}

	// Re-fetch
	database.DB.First(&inst, inst.ID)

	// Push updated config to the running instance
	orch := orchestrator.Get()
	orchStatus := "stopped"
	if orch != nil {
		orchStatus, _ = orch.GetInstanceStatus(r.Context(), inst.Name)
	}
	if orch != nil && orchStatus == "running" {
		models, resolvedKeys := resolveInstanceModelsAndKeys(inst)
		if ops, ok := orch.(config.InstanceOps); ok {
			go config.ConfigureInstance(context.Background(), ops, inst.Name, models, resolvedKeys)
		}
	}

	status := resolveStatus(&inst, orchStatus)
	writeJSON(w, http.StatusOK, instanceToResponse(inst, status))
}

func DeleteInstance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if orch := orchestrator.Get(); orch != nil {
		if err := orch.DeleteInstance(r.Context(), inst.Name); err != nil {
			log.Printf("Failed to delete container resources for %s – proceeding with DB cleanup: %v", logutil.SanitizeForLog(inst.Name), err)
		}
	}

	// Clean up SSH private key file
	if inst.SSHPrivateKeyPath != "" {
		if err := sshkeys.DeletePrivateKey(inst.SSHPrivateKeyPath); err != nil {
			log.Printf("Failed to delete SSH private key for %s: %v", logutil.SanitizeForLog(inst.Name), err)
		}
	}

	// Delete associated API keys
	database.DB.Where("instance_id = ?", inst.ID).Delete(&database.InstanceAPIKey{})
	database.DB.Delete(&inst)
	w.WriteHeader(http.StatusNoContent)
}

func StartInstance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	if orch := orchestrator.Get(); orch != nil {
		if err := orch.StartInstance(r.Context(), inst.Name); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to start instance: %v", err))
			return
		}
	}

	database.DB.Model(&inst).Updates(map[string]interface{}{
		"status":     "running",
		"updated_at": time.Now().UTC(),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "running"})
}

func StopInstance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	if orch := orchestrator.Get(); orch != nil {
		if err := orch.StopInstance(r.Context(), inst.Name); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to stop instance: %v", err))
			return
		}
	}

	database.DB.Model(&inst).Updates(map[string]interface{}{
		"status":     "stopping",
		"updated_at": time.Now().UTC(),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

func RestartInstance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	if orch := orchestrator.Get(); orch != nil {
		if err := orch.RestartInstance(r.Context(), inst.Name); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to restart instance: %v", err))
			return
		}
	}

	database.DB.Model(&inst).Updates(map[string]interface{}{
		"status":     "restarting",
		"updated_at": time.Now().UTC(),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarting"})
}

func GetInstanceConfig(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	sm := sshtunnel.GetSSHManager()
	if sm == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	sshClient, err := sm.GetClient(inst.Name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Instance must be running to read config")
		return
	}

	content, err := sshfiles.ReadFile(sshClient, orchestrator.PathOpenClawConfig)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Instance must be running to read config")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"config": string(content)})
}

func UpdateInstanceConfig(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var body struct {
		Config string `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate JSON
	if !json.Valid([]byte(body.Config)) {
		writeError(w, http.StatusBadRequest, "Invalid JSON in config")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	orch := orchestrator.Get()
	if orch == nil {
		writeError(w, http.StatusServiceUnavailable, "No orchestrator available")
		return
	}

	if err := orch.UpdateInstanceConfig(r.Context(), inst.Name, body.Config); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to update config: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"config":    body.Config,
		"restarted": true,
	})
}

func CloneInstance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var src database.Instance
	if err := database.DB.First(&src, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	// Generate clone display name and K8s-safe name
	cloneDisplayName := src.DisplayName + " (Copy)"
	cloneName := generateName(cloneDisplayName)

	// Ensure name uniqueness
	var count int64
	database.DB.Model(&database.Instance{}).Where("name = ?", cloneName).Count(&count)
	if count > 0 {
		suffix := hex.EncodeToString(func() []byte { b := make([]byte, 3); rand.Read(b); return b }())
		cloneName = cloneName + "-" + suffix
		if len(cloneName) > 63 {
			cloneName = cloneName[:63]
		}
	}

	// Generate new gateway token
	gatewayTokenPlain := generateToken()
	encGatewayToken, err := crypto.Encrypt(gatewayTokenPlain)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to encrypt gateway token")
		return
	}

	// Generate SSH key pair for the clone
	clonePubKey, clonePrivKey, err := sshkeys.GenerateKeyPair()
	if err != nil {
		log.Printf("Failed to generate SSH key pair for clone %s: %v", logutil.SanitizeForLog(cloneName), err)
		writeError(w, http.StatusInternalServerError, "Failed to generate SSH keys")
		return
	}

	cloneKeyPath, err := sshkeys.SavePrivateKey(cloneName, clonePrivKey)
	if err != nil {
		log.Printf("Failed to save SSH private key for clone %s: %v", logutil.SanitizeForLog(cloneName), err)
		writeError(w, http.StatusInternalServerError, "Failed to save SSH key")
		return
	}

	cloneAuthorizedKey, err := sshkeys.FormatPublicKeyForAuthorizedKeys(clonePubKey)
	if err != nil {
		log.Printf("Failed to format SSH public key for clone %s: %v", logutil.SanitizeForLog(cloneName), err)
		writeError(w, http.StatusInternalServerError, "Failed to format SSH key")
		return
	}

	cloneFingerprint, err := sshkeys.GetPublicKeyFingerprint(clonePubKey)
	if err != nil {
		log.Printf("Failed to compute SSH key fingerprint for clone %s: %v", logutil.SanitizeForLog(cloneName), err)
		writeError(w, http.StatusInternalServerError, "Failed to compute SSH key fingerprint")
		return
	}

	// Compute next sort_order
	var maxSortOrder int
	database.DB.Model(&database.Instance{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxSortOrder)

	inst := database.Instance{
		Name:              cloneName,
		DisplayName:       cloneDisplayName,
		Status:            "creating",
		CPURequest:        src.CPURequest,
		CPULimit:          src.CPULimit,
		MemoryRequest:     src.MemoryRequest,
		MemoryLimit:       src.MemoryLimit,
		StorageHomebrew:   src.StorageHomebrew,
		StorageClawd:      src.StorageClawd,
		StorageChrome:     src.StorageChrome,
		BraveAPIKey:       src.BraveAPIKey,
		ContainerImage:    src.ContainerImage,
		VNCResolution:     src.VNCResolution,
		GatewayToken:      encGatewayToken,
		ModelsConfig:      src.ModelsConfig,
		DefaultModel:      src.DefaultModel,
		SSHPublicKey:      string(clonePubKey),
		SSHPrivateKeyPath: cloneKeyPath,
		SSHKeyFingerprint: cloneFingerprint,
		SSHPort:           22,
		SortOrder:         maxSortOrder + 1,
	}

	if err := database.DB.Create(&inst).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create cloned instance")
		return
	}

	// Copy API keys from source instance
	var srcKeys []database.InstanceAPIKey
	database.DB.Where("instance_id = ?", src.ID).Find(&srcKeys)
	for _, k := range srcKeys {
		database.DB.Create(&database.InstanceAPIKey{
			InstanceID: inst.ID,
			KeyName:    k.KeyName,
			KeyValue:   k.KeyValue,
		})
	}

	// Run the full clone operation asynchronously
	go func() {
		ctx := context.Background()
		orch := orchestrator.Get()
		if orch == nil {
			database.DB.Model(&inst).Update("status", "error")
			return
		}

		effectiveImage := getEffectiveImage(inst)
		effectiveResolution := getEffectiveResolution(inst)

		envVars := map[string]string{}
		if gatewayTokenPlain != "" {
			envVars["OPENCLAW_GATEWAY_TOKEN"] = gatewayTokenPlain
		}

		// Create container/deployment with empty volumes
		err := orch.CreateInstance(ctx, orchestrator.CreateParams{
			Name:            cloneName,
			CPURequest:      inst.CPURequest,
			CPULimit:        inst.CPULimit,
			MemoryRequest:   inst.MemoryRequest,
			MemoryLimit:     inst.MemoryLimit,
			StorageHomebrew: inst.StorageHomebrew,
			StorageClawd:    inst.StorageClawd,
			StorageChrome:   inst.StorageChrome,
			ContainerImage:  effectiveImage,
			VNCResolution:   effectiveResolution,
			EnvVars:         envVars,
			SSHPublicKey:    cloneAuthorizedKey,
		})
		if err != nil {
			log.Printf("Failed to create container for clone %s: %v", cloneName, err)
			database.DB.Model(&inst).Update("status", "error")
			return
		}

		// Clone volume data from source
		if err := orch.CloneVolumes(ctx, src.Name, cloneName); err != nil {
			log.Printf("Failed to clone volumes from %s to %s: %v", src.Name, cloneName, err)
			// Continue anyway – instance is created, just without cloned data
		}

		database.DB.Model(&inst).Updates(map[string]interface{}{
			"status":     "running",
			"updated_at": time.Now().UTC(),
		})

		// Push models and API keys to the running instance
		// Re-fetch to get latest state
		database.DB.First(&inst, inst.ID)
		models, resolvedKeys := resolveInstanceModelsAndKeys(inst)
		if ops, ok := orch.(config.InstanceOps); ok {
			config.ConfigureInstance(ctx, ops, cloneName, models, resolvedKeys)
		}
	}()

	writeJSON(w, http.StatusCreated, instanceToResponse(inst, "creating"))
}

type tunnelInfo struct {
	Service    string `json:"service"`
	Type       string `json:"type"`
	LocalPort  int    `json:"local_port"`
	RemotePort int    `json:"remote_port"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	LastCheck  string `json:"last_check,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

func GetTunnelStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	tm := sshtunnel.GetTunnelManager()
	if tm == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"tunnels": []tunnelInfo{}})
		return
	}

	tunnels := tm.GetTunnels(inst.Name)
	infos := make([]tunnelInfo, 0, len(tunnels))
	for _, t := range tunnels {
		status := "active"
		if t.IsClosed() {
			status = "closed"
		}

		info := tunnelInfo{
			Service:    string(t.Config.Service),
			Type:       string(t.Config.Type),
			LocalPort:  t.LocalPort,
			RemotePort: t.Config.RemotePort,
			Status:     status,
			StartedAt:  formatTimestamp(t.StartedAt),
		}

		lastCheck, lastErr := t.LastCheck()
		if !lastCheck.IsZero() {
			info.LastCheck = formatTimestamp(lastCheck)
		}
		if lastErr != nil {
			info.LastError = lastErr.Error()
		}

		infos = append(infos, info)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"tunnels": infos})
}

type sshTestResponse struct {
	Success      bool            `json:"success"`
	LatencyMs    int64           `json:"latency_ms"`
	TunnelStatus []sshTunnelTest `json:"tunnel_status"`
	CommandTest  bool            `json:"command_test"`
	Error        string          `json:"error,omitempty"`
}

type sshTunnelTest struct {
	Service string `json:"service"`
	Healthy bool   `json:"healthy"`
	Error   string `json:"error,omitempty"`
}

func SSHConnectionTest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	orch := orchestrator.Get()
	if orch == nil {
		writeError(w, http.StatusServiceUnavailable, "No orchestrator available")
		return
	}

	sm := sshtunnel.GetSSHManager()
	if sm == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	if inst.SSHPrivateKeyPath == "" {
		writeError(w, http.StatusBadRequest, "Instance has no SSH key configured")
		return
	}

	// Verify SSH key fingerprint before connecting
	if inst.SSHPublicKey != "" && inst.SSHKeyFingerprint != "" {
		if verifyErr := sshkeys.VerifyFingerprint([]byte(inst.SSHPublicKey), inst.SSHKeyFingerprint); verifyErr != nil {
			log.Printf("[ssh-test] WARNING: SSH key fingerprint mismatch for %s: %v", logutil.SanitizeForLog(inst.Name), verifyErr)
			if u := middleware.GetUser(r); u != nil {
				sshaudit.LogFingerprintMismatch(inst.ID, inst.Name, u.Username, verifyErr.Error())
			}
			writeJSON(w, http.StatusOK, sshTestResponse{
				Success:      false,
				TunnelStatus: []sshTunnelTest{},
				Error:        fmt.Sprintf("SSH key integrity check failed: %v", verifyErr),
			})
			return
		}
	}

	// Check source IP restriction before connecting
	sourceIP := sshaudit.ExtractSourceIP(r)
	if inst.AllowedSourceIPs != "" {
		if ipErr := sshmanager.CheckIPAllowed(sourceIP, inst.AllowedSourceIPs); ipErr != nil {
			var userName string
			if u := middleware.GetUser(r); u != nil {
				userName = u.Username
			}
			sshaudit.LogIPRestricted(inst.ID, inst.Name, userName, sourceIP, ipErr.Error())
			writeJSON(w, http.StatusOK, sshTestResponse{
				Success:      false,
				TunnelStatus: []sshTunnelTest{},
				Error:        fmt.Sprintf("Source IP restriction: %v", ipErr),
			})
			return
		}
	}

	host, port, err := orch.GetInstanceSSHEndpoint(r.Context(), inst.Name)
	if err != nil {
		writeJSON(w, http.StatusOK, sshTestResponse{
			Success:      false,
			TunnelStatus: []sshTunnelTest{},
			Error:        fmt.Sprintf("Failed to get SSH endpoint: %v", err),
		})
		return
	}

	start := time.Now()

	var userName string
	if u := middleware.GetUser(r); u != nil {
		userName = u.Username
	}

	client, err := sm.Connect(r.Context(), inst.Name, host, port, inst.SSHPrivateKeyPath)
	if err != nil {
		latency := time.Since(start).Milliseconds()
		sshaudit.LogConnectionFailed(inst.ID, inst.Name, userName, err.Error())
		writeJSON(w, http.StatusOK, sshTestResponse{
			Success:      false,
			LatencyMs:    latency,
			TunnelStatus: []sshTunnelTest{},
			Error:        fmt.Sprintf("SSH connection failed: %v", err),
		})
		return
	}

	sshaudit.LogConnection(inst.ID, inst.Name, userName, sourceIP)

	session, err := client.NewSession()
	if err != nil {
		latency := time.Since(start).Milliseconds()
		writeJSON(w, http.StatusOK, sshTestResponse{
			Success:      false,
			LatencyMs:    latency,
			TunnelStatus: []sshTunnelTest{},
			Error:        fmt.Sprintf("Failed to create SSH session: %v", err),
		})
		return
	}
	defer session.Close()

	_, cmdErr := session.CombinedOutput("echo 'SSH test successful'")
	latency := time.Since(start).Milliseconds()

	commandTest := cmdErr == nil

	// Collect tunnel status
	tunnelStatuses := []sshTunnelTest{}
	if tm := sshtunnel.GetTunnelManager(); tm != nil {
		tunnelMetrics := tm.GetTunnelMetrics(inst.Name)
		for _, m := range tunnelMetrics {
			ts := sshTunnelTest{
				Service: string(m.Service),
				Healthy: m.Healthy,
			}
			if m.LastError != "" {
				ts.Error = m.LastError
			}
			tunnelStatuses = append(tunnelStatuses, ts)
		}
	}

	if cmdErr != nil {
		writeJSON(w, http.StatusOK, sshTestResponse{
			Success:      false,
			LatencyMs:    latency,
			TunnelStatus: tunnelStatuses,
			CommandTest:  commandTest,
			Error:        fmt.Sprintf("Command execution failed: %v", cmdErr),
		})
		return
	}

	writeJSON(w, http.StatusOK, sshTestResponse{
		Success:      true,
		LatencyMs:    latency,
		TunnelStatus: tunnelStatuses,
		CommandTest:  commandTest,
	})
}

func ReorderInstances(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrderedIDs []uint `json:"ordered_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if len(body.OrderedIDs) == 0 {
		writeError(w, http.StatusBadRequest, "ordered_ids is required")
		return
	}

	tx := database.DB.Begin()
	for i, id := range body.OrderedIDs {
		if err := tx.Model(&database.Instance{}).Where("id = ?", id).Update("sort_order", i+1).Error; err != nil {
			tx.Rollback()
			writeError(w, http.StatusInternalServerError, "Failed to reorder instances")
			return
		}
	}
	tx.Commit()
	w.WriteHeader(http.StatusNoContent)
}

// --- SSH Connection Status ---

type sshHealthMetrics struct {
	ConnectedAt      string `json:"connected_at"`
	LastHealthCheck  string `json:"last_health_check"`
	UptimeSeconds    int64  `json:"uptime_seconds"`
	SuccessfulChecks int64  `json:"successful_checks"`
	FailedChecks     int64  `json:"failed_checks"`
	Healthy          bool   `json:"healthy"`
}

type sshTunnelStatus struct {
	Service             string `json:"service"`
	LocalPort           int    `json:"local_port"`
	RemotePort          int    `json:"remote_port"`
	CreatedAt           string `json:"created_at"`
	LastCheck           string `json:"last_check,omitempty"`
	LastSuccessfulCheck string `json:"last_successful_check,omitempty"`
	LastError           string `json:"last_error,omitempty"`
	BytesTransferred    int64  `json:"bytes_transferred"`
	Healthy             bool   `json:"healthy"`
}

type sshStateEvent struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Timestamp string `json:"timestamp"`
}

type sshStatusResponse struct {
	ConnectionState string            `json:"connection_state"`
	Health          *sshHealthMetrics `json:"health"`
	Tunnels         []sshTunnelStatus `json:"tunnels"`
	RecentEvents    []sshStateEvent   `json:"recent_events"`
}

// GetSSHStatus returns the SSH connection status for an instance, including
// connection state, health metrics, active tunnels, and recent state changes.
func GetSSHStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	sm := sshtunnel.GetSSHManager()
	tm := sshtunnel.GetTunnelManager()

	resp := sshStatusResponse{
		ConnectionState: string(sshmanager.StateDisconnected),
		Tunnels:         []sshTunnelStatus{},
		RecentEvents:    []sshStateEvent{},
	}

	if sm != nil {
		resp.ConnectionState = string(sm.GetConnectionState(inst.Name))

		if metrics := sm.GetMetrics(inst.Name); metrics != nil {
			resp.Health = &sshHealthMetrics{
				ConnectedAt:      formatTimestamp(metrics.ConnectedAt),
				LastHealthCheck:  formatTimestamp(metrics.LastHealthCheck),
				UptimeSeconds:    int64(metrics.Uptime().Seconds()),
				SuccessfulChecks: metrics.SuccessfulChecks,
				FailedChecks:     metrics.FailedChecks,
				Healthy:          metrics.Healthy,
			}
		}

		transitions := sm.GetStateTransitions(inst.Name)
		count := len(transitions)
		start := 0
		if count > 10 {
			start = count - 10
		}
		for _, t := range transitions[start:] {
			resp.RecentEvents = append(resp.RecentEvents, sshStateEvent{
				From:      string(t.From),
				To:        string(t.To),
				Timestamp: formatTimestamp(t.Timestamp),
			})
		}
	}

	if tm != nil {
		tunnelMetrics := tm.GetTunnelMetrics(inst.Name)
		for _, m := range tunnelMetrics {
			resp.Tunnels = append(resp.Tunnels, sshTunnelStatus{
				Service:             string(m.Service),
				LocalPort:           m.LocalPort,
				RemotePort:          m.RemotePort,
				CreatedAt:           formatTimestamp(m.CreatedAt),
				LastCheck:           formatTimestamp(m.LastCheck),
				LastSuccessfulCheck: formatTimestamp(m.LastSuccessfulCheck),
				LastError:           m.LastError,
				BytesTransferred:    m.BytesTransferred,
				Healthy:             m.Healthy,
			})
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// sshEventResponse represents a single connection event in the API response.
type sshEventResponse struct {
	InstanceName string `json:"instance_name"`
	Type         string `json:"type"`
	Details      string `json:"details"`
	Timestamp    string `json:"timestamp"`
}

// sshEventsResponse wraps the list of connection events.
type sshEventsResponse struct {
	Events []sshEventResponse `json:"events"`
}

// GetSSHEvents returns the SSH connection event history for an instance.
// Events include connections, disconnections, health check failures,
// reconnection attempts and outcomes. Useful for debugging connection issues.
// Supports ?limit=N query parameter (default: 50, max: 100).
func GetSSHEvents(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
			if limit > 100 {
				limit = 100
			}
		}
	}

	sm := sshtunnel.GetSSHManager()

	resp := sshEventsResponse{
		Events: []sshEventResponse{},
	}

	if sm != nil {
		events := sm.GetRecentEvents(inst.Name, limit)
		for _, e := range events {
			resp.Events = append(resp.Events, sshEventResponse{
				InstanceName: e.InstanceName,
				Type:         string(e.Type),
				Details:      e.Details,
				Timestamp:    formatTimestamp(e.Timestamp),
			})
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- SSH Reconnect ---

type sshReconnectResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// SSHReconnect forces a reconnection of the SSH connection for an instance.
func SSHReconnect(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	orch := orchestrator.Get()
	if orch == nil {
		writeError(w, http.StatusServiceUnavailable, "No orchestrator available")
		return
	}

	sm := sshtunnel.GetSSHManager()
	if sm == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	if inst.SSHPrivateKeyPath == "" {
		writeJSON(w, http.StatusOK, sshReconnectResponse{
			Success: false,
			Message: "Instance has no SSH key configured",
		})
		return
	}

	// Verify SSH key fingerprint before reconnecting
	if inst.SSHPublicKey != "" && inst.SSHKeyFingerprint != "" {
		if err := sshkeys.VerifyFingerprint([]byte(inst.SSHPublicKey), inst.SSHKeyFingerprint); err != nil {
			log.Printf("[ssh-reconnect] WARNING: SSH key fingerprint mismatch for %s: %v", logutil.SanitizeForLog(inst.Name), err)
			username := ""
			if u := middleware.GetUser(r); u != nil {
				username = u.Username
			}
			sshaudit.LogFingerprintMismatch(inst.ID, inst.Name, username, err.Error())
			writeJSON(w, http.StatusOK, sshReconnectResponse{
				Success: false,
				Message: fmt.Sprintf("SSH key integrity check failed: %v", err),
			})
			return
		}
	}

	// Check source IP restriction before reconnecting
	sourceIP := sshaudit.ExtractSourceIP(r)
	if inst.AllowedSourceIPs != "" {
		if ipErr := sshmanager.CheckIPAllowed(sourceIP, inst.AllowedSourceIPs); ipErr != nil {
			username := ""
			if u := middleware.GetUser(r); u != nil {
				username = u.Username
			}
			sshaudit.LogIPRestricted(inst.ID, inst.Name, username, sourceIP, ipErr.Error())
			writeJSON(w, http.StatusOK, sshReconnectResponse{
				Success: false,
				Message: fmt.Sprintf("Source IP restriction: %v", ipErr),
			})
			return
		}
	}

	// Close existing connection if any
	if sm.HasClient(inst.Name) {
		_ = sm.Close(inst.Name)
	}

	host, port, err := orch.GetInstanceSSHEndpoint(r.Context(), inst.Name)
	if err != nil {
		writeJSON(w, http.StatusOK, sshReconnectResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to get SSH endpoint: %v", err),
		})
		return
	}

	_, err = sm.Connect(r.Context(), inst.Name, host, port, inst.SSHPrivateKeyPath)
	if err != nil {
		writeJSON(w, http.StatusOK, sshReconnectResponse{
			Success: false,
			Message: fmt.Sprintf("Reconnection failed: %v", err),
		})
		return
	}

	// Restart tunnels for the instance
	if tm := sshtunnel.GetTunnelManager(); tm != nil {
		tm.StopTunnelsForInstance(inst.Name)
		if tunnelErr := tm.StartTunnelsForInstance(r.Context(), inst.Name); tunnelErr != nil {
			log.Printf("[ssh-reconnect] tunnels failed after reconnect for %s: %v", logutil.SanitizeForLog(inst.Name), tunnelErr)
		}
	}

	writeJSON(w, http.StatusOK, sshReconnectResponse{
		Success: true,
		Message: "SSH connection re-established successfully",
	})
}

// --- SSH Fingerprint ---

type sshFingerprintResponse struct {
	Fingerprint string `json:"fingerprint"`
	Algorithm   string `json:"algorithm"`
	Verified    bool   `json:"verified"`
}

// GetSSHFingerprint returns the SSH public key fingerprint for an instance.
// It also verifies the computed fingerprint against the stored expected value.
func GetSSHFingerprint(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	if inst.SSHPublicKey == "" {
		writeError(w, http.StatusBadRequest, "Instance has no SSH key configured")
		return
	}

	fingerprint, err := sshkeys.GetPublicKeyFingerprint([]byte(inst.SSHPublicKey))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to parse SSH public key")
		return
	}

	algorithm, _ := sshkeys.GetPublicKeyAlgorithm([]byte(inst.SSHPublicKey))

	// Verify fingerprint matches stored expected value
	verified := true
	if inst.SSHKeyFingerprint != "" && inst.SSHKeyFingerprint != fingerprint {
		verified = false
		log.Printf("[ssh-fingerprint] WARNING: fingerprint mismatch for instance %d: stored=%s computed=%s",
			inst.ID, inst.SSHKeyFingerprint, fingerprint)
	}

	writeJSON(w, http.StatusOK, sshFingerprintResponse{
		Fingerprint: fingerprint,
		Algorithm:   algorithm,
		Verified:    verified,
	})
}

// --- Global SSH Status ---

// globalSSHInstanceStatus represents SSH status for a single instance in the global dashboard.
type globalSSHInstanceStatus struct {
	InstanceID      uint              `json:"instance_id"`
	InstanceName    string            `json:"instance_name"`
	DisplayName     string            `json:"display_name"`
	InstanceStatus  string            `json:"instance_status"`
	ConnectionState string            `json:"connection_state"`
	Health          *sshHealthMetrics `json:"health"`
	TunnelCount     int               `json:"tunnel_count"`
	HealthyTunnels  int               `json:"healthy_tunnels"`
}

// globalSSHStatusResponse wraps the list plus summary stats.
type globalSSHStatusResponse struct {
	Instances    []globalSSHInstanceStatus `json:"instances"`
	TotalCount   int                      `json:"total_count"`
	Connected    int                      `json:"connected"`
	Reconnecting int                      `json:"reconnecting"`
	Failed       int                      `json:"failed"`
	Disconnected int                      `json:"disconnected"`
}

// GetGlobalSSHStatus returns an overview of SSH connection status across all instances
// the current user has access to. Used by the global SSH dashboard.
func GetGlobalSSHStatus(w http.ResponseWriter, r *http.Request) {
	var instances []database.Instance
	user := middleware.GetUser(r)

	query := database.DB.Order("sort_order ASC, id ASC")
	if user != nil && user.Role != "admin" {
		assignedIDs, err := database.GetUserInstances(user.ID)
		if err != nil || len(assignedIDs) == 0 {
			writeJSON(w, http.StatusOK, globalSSHStatusResponse{
				Instances: []globalSSHInstanceStatus{},
			})
			return
		}
		query = query.Where("id IN ?", assignedIDs)
	}

	if err := query.Find(&instances).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list instances")
		return
	}

	orch := orchestrator.Get()
	sm := sshtunnel.GetSSHManager()
	tm := sshtunnel.GetTunnelManager()

	resp := globalSSHStatusResponse{
		Instances: make([]globalSSHInstanceStatus, 0, len(instances)),
	}

	for i := range instances {
		inst := &instances[i]

		orchStatus := "stopped"
		if orch != nil {
			s, _ := orch.GetInstanceStatus(r.Context(), inst.Name)
			orchStatus = s
		}
		status := resolveStatus(inst, orchStatus)

		entry := globalSSHInstanceStatus{
			InstanceID:      inst.ID,
			InstanceName:    inst.Name,
			DisplayName:     inst.DisplayName,
			InstanceStatus:  status,
			ConnectionState: string(sshmanager.StateDisconnected),
		}

		if sm != nil {
			entry.ConnectionState = string(sm.GetConnectionState(inst.Name))

			if metrics := sm.GetMetrics(inst.Name); metrics != nil {
				entry.Health = &sshHealthMetrics{
					ConnectedAt:      formatTimestamp(metrics.ConnectedAt),
					LastHealthCheck:  formatTimestamp(metrics.LastHealthCheck),
					UptimeSeconds:    int64(metrics.Uptime().Seconds()),
					SuccessfulChecks: metrics.SuccessfulChecks,
					FailedChecks:     metrics.FailedChecks,
					Healthy:          metrics.Healthy,
				}
			}
		}

		if tm != nil {
			tunnelMetrics := tm.GetTunnelMetrics(inst.Name)
			entry.TunnelCount = len(tunnelMetrics)
			for _, m := range tunnelMetrics {
				if m.Healthy {
					entry.HealthyTunnels++
				}
			}
		}

		// Accumulate stats
		switch entry.ConnectionState {
		case "connected":
			resp.Connected++
		case "reconnecting":
			resp.Reconnecting++
		case "failed":
			resp.Failed++
		default:
			resp.Disconnected++
		}

		resp.Instances = append(resp.Instances, entry)
	}

	resp.TotalCount = len(resp.Instances)
	writeJSON(w, http.StatusOK, resp)
}

// --- SSH Key Rotation ---

type sshRotateResponse struct {
	Success     bool   `json:"success"`
	Fingerprint string `json:"fingerprint"`
	RotatedAt   string `json:"rotated_at"`
	Message     string `json:"message,omitempty"`
}

// RotateSSHKey rotates the SSH key pair for an instance. It generates a new key,
// appends it to the agent's authorized_keys, verifies the new key works, then
// removes the old key. Requires admin access.
func RotateSSHKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	orch := orchestrator.Get()
	if orch == nil {
		writeError(w, http.StatusServiceUnavailable, "No orchestrator available")
		return
	}

	sm := sshtunnel.GetSSHManager()
	if sm == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	if inst.SSHPublicKey == "" || inst.SSHPrivateKeyPath == "" {
		writeError(w, http.StatusBadRequest, "Instance has no SSH key configured")
		return
	}

	// Verify current SSH key fingerprint before rotation
	if inst.SSHKeyFingerprint != "" {
		if err := sshkeys.VerifyFingerprint([]byte(inst.SSHPublicKey), inst.SSHKeyFingerprint); err != nil {
			log.Printf("[ssh-rotate] WARNING: SSH key fingerprint mismatch for %s: %v", logutil.SanitizeForLog(inst.Name), err)
			if u := middleware.GetUser(r); u != nil {
				sshaudit.LogFingerprintMismatch(inst.ID, inst.Name, u.Username, err.Error())
			}
		}
	}

	// Get the existing SSH client for this instance
	sshClient, err := sm.GetClient(inst.Name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Instance must have an active SSH connection for key rotation")
		return
	}

	// Get the SSH endpoint for testing the new key
	host, port, err := orch.GetInstanceSSHEndpoint(r.Context(), inst.Name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("Failed to get SSH endpoint: %v", err))
		return
	}

	// Perform the key rotation
	newPubKey, newKeyPath, result, err := sshkeys.RotateKeyPair(
		sshClient,
		inst.Name,
		inst.SSHPublicKey,
		host,
		port,
	)
	if err != nil {
		log.Printf("[ssh-rotate] key rotation failed for %s: %v", logutil.SanitizeForLog(inst.Name), err)
		writeJSON(w, http.StatusOK, sshRotateResponse{
			Success: false,
			Message: fmt.Sprintf("Key rotation failed: %v", err),
		})
		return
	}

	// Update the database with new key info
	now := result.RotatedAt
	if err := database.DB.Model(&inst).Updates(map[string]interface{}{
		"ssh_public_key":       string(newPubKey),
		"ssh_private_key_path": newKeyPath,
		"ssh_key_fingerprint":  result.NewFingerprint,
		"last_key_rotation":    &now,
	}).Error; err != nil {
		log.Printf("[ssh-rotate] failed to update database for %s: %v", logutil.SanitizeForLog(inst.Name), err)
		writeError(w, http.StatusInternalServerError, "Key rotation succeeded but failed to update database")
		return
	}

	// Reconnect SSH with the new key
	if sm.HasClient(inst.Name) {
		_ = sm.Close(inst.Name)
	}
	_, reconnErr := sm.Connect(r.Context(), inst.Name, host, port, newKeyPath)
	if reconnErr != nil {
		log.Printf("[ssh-rotate] reconnection with new key failed for %s: %v", logutil.SanitizeForLog(inst.Name), reconnErr)
	}

	// Restart tunnels after reconnection
	if tm := sshtunnel.GetTunnelManager(); tm != nil {
		tm.StopTunnelsForInstance(inst.Name)
		if tunnelErr := tm.StartTunnelsForInstance(r.Context(), inst.Name); tunnelErr != nil {
			log.Printf("[ssh-rotate] tunnel restart failed after rotation for %s: %v", logutil.SanitizeForLog(inst.Name), tunnelErr)
		}
	}

	// Clean up old private key file
	if inst.SSHPrivateKeyPath != newKeyPath {
		if delErr := sshkeys.DeletePrivateKey(inst.SSHPrivateKeyPath); delErr != nil {
			log.Printf("[ssh-rotate] failed to delete old key file for %s: %v", logutil.SanitizeForLog(inst.Name), delErr)
		}
	}

	log.Printf("[ssh-rotate] key rotation completed for %s (fingerprint: %s)", logutil.SanitizeForLog(inst.Name), result.NewFingerprint)

	if u := middleware.GetUser(r); u != nil {
		sshaudit.LogKeyRotation(inst.ID, inst.Name, u.Username, result.NewFingerprint)
	}

	writeJSON(w, http.StatusOK, sshRotateResponse{
		Success:     true,
		Fingerprint: result.NewFingerprint,
		RotatedAt:   formatTimestamp(now),
		Message:     "SSH key rotation completed successfully",
	})
}

// --- SSH Metrics ---

type sshUptimeBucket struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type sshHealthRate struct {
	InstanceName string  `json:"instance_name"`
	DisplayName  string  `json:"display_name"`
	SuccessRate  float64 `json:"success_rate"`
	TotalChecks  int64   `json:"total_checks"`
}

type sshReconnectionCount struct {
	InstanceName string `json:"instance_name"`
	DisplayName  string `json:"display_name"`
	Count        int    `json:"count"`
}

type sshMetricsResponse struct {
	UptimeBuckets      []sshUptimeBucket      `json:"uptime_buckets"`
	HealthRates        []sshHealthRate         `json:"health_rates"`
	ReconnectionCounts []sshReconnectionCount  `json:"reconnection_counts"`
}

// GetSSHMetrics returns aggregated SSH metrics for visualization.
// Includes uptime distribution, health check success rates, and reconnection
// counts across all instances the current user can access.
func GetSSHMetrics(w http.ResponseWriter, r *http.Request) {
	var instances []database.Instance
	user := middleware.GetUser(r)

	query := database.DB.Order("sort_order ASC, id ASC")
	if user != nil && user.Role != "admin" {
		assignedIDs, err := database.GetUserInstances(user.ID)
		if err != nil || len(assignedIDs) == 0 {
			writeJSON(w, http.StatusOK, sshMetricsResponse{
				UptimeBuckets:      []sshUptimeBucket{},
				HealthRates:        []sshHealthRate{},
				ReconnectionCounts: []sshReconnectionCount{},
			})
			return
		}
		query = query.Where("id IN ?", assignedIDs)
	}

	if err := query.Find(&instances).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list instances")
		return
	}

	sm := sshtunnel.GetSSHManager()

	// Build name -> display name map for accessible instances
	nameToDisplay := make(map[string]string, len(instances))
	for i := range instances {
		nameToDisplay[instances[i].Name] = instances[i].DisplayName
	}

	// Uptime distribution buckets
	type bucket struct {
		label    string
		maxSecs  int64 // exclusive upper bound, -1 = unlimited
	}
	bucketDefs := []bucket{
		{"< 1h", 3600},
		{"1–6h", 21600},
		{"6–24h", 86400},
		{"1–7d", 604800},
		{"> 7d", -1},
	}
	uptimeCounts := make([]int, len(bucketDefs))

	healthRates := make([]sshHealthRate, 0)
	reconnCounts := make([]sshReconnectionCount, 0)

	// Get reconnection event counts
	var reconnMap map[string]int
	if sm != nil {
		reconnMap = sm.GetEventCountsByType(sshmanager.EventReconnecting)
	}

	for i := range instances {
		inst := &instances[i]
		if sm == nil {
			continue
		}

		metrics := sm.GetMetrics(inst.Name)
		if metrics == nil {
			continue
		}

		// Uptime distribution
		uptimeSecs := int64(metrics.Uptime().Seconds())
		for bi, bd := range bucketDefs {
			if bd.maxSecs == -1 || uptimeSecs < bd.maxSecs {
				uptimeCounts[bi]++
				break
			}
		}

		// Health check success rate
		total := metrics.SuccessfulChecks + metrics.FailedChecks
		if total > 0 {
			healthRates = append(healthRates, sshHealthRate{
				InstanceName: inst.Name,
				DisplayName:  inst.DisplayName,
				SuccessRate:  float64(metrics.SuccessfulChecks) / float64(total),
				TotalChecks:  total,
			})
		}
	}

	// Reconnection counts (only for accessible instances)
	for name, count := range reconnMap {
		if displayName, ok := nameToDisplay[name]; ok {
			reconnCounts = append(reconnCounts, sshReconnectionCount{
				InstanceName: name,
				DisplayName:  displayName,
				Count:        count,
			})
		}
	}

	uptimeBuckets := make([]sshUptimeBucket, len(bucketDefs))
	for i, bd := range bucketDefs {
		uptimeBuckets[i] = sshUptimeBucket{Label: bd.label, Count: uptimeCounts[i]}
	}

	writeJSON(w, http.StatusOK, sshMetricsResponse{
		UptimeBuckets:      uptimeBuckets,
		HealthRates:        healthRates,
		ReconnectionCounts: reconnCounts,
	})
}
