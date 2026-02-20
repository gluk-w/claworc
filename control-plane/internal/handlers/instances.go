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
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/tunnel"
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
	TunnelConnected       bool            `json:"tunnel_connected"`
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

// isTunnelConnected reports whether the tunnel for the given instance is
// connected and the yamux session is still alive.
func isTunnelConnected(instanceID uint) bool {
	if tunnel.Manager == nil {
		return false
	}
	tc := tunnel.Manager.Get(instanceID)
	return tc != nil && !tc.IsClosed()
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
		TunnelConnected:       isTunnelConnected(inst.ID),
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

	// Compute next sort_order
	var maxSortOrder int
	database.DB.Model(&database.Instance{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxSortOrder)

	inst := database.Instance{
		Name:            name,
		DisplayName:     body.DisplayName,
		Status:          "creating",
		CPURequest:      body.CPURequest,
		CPULimit:        body.CPULimit,
		MemoryRequest:   body.MemoryRequest,
		MemoryLimit:     body.MemoryLimit,
		StorageHomebrew: body.StorageHomebrew,
		StorageClawd:    body.StorageClawd,
		StorageChrome:   body.StorageChrome,
		BraveAPIKey:     encBraveKey,
		ContainerImage:  containerImage,
		VNCResolution:   vncResolution,
		GatewayToken:    encGatewayToken,
		ModelsConfig:    modelsConfigJSON,
		DefaultModel:    body.DefaultModel,
		SortOrder:       maxSortOrder + 1,
	}

	if err := database.DB.Create(&inst).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create instance")
		return
	}

	// Generate agent TLS certificate pair
	var agentCertPEM, agentKeyPEM string
	certPEM, keyPEM, err := crypto.GenerateAgentCertPair(name)
	if err != nil {
		log.Printf("Failed to generate agent cert for %s: %v", name, err)
	} else {
		agentCertPEM = certPEM
		agentKeyPEM = keyPEM
		encKeyPEM, err := crypto.Encrypt(keyPEM)
		if err != nil {
			log.Printf("Failed to encrypt agent cert key for %s: %v", name, err)
		} else {
			database.DB.Model(&inst).Updates(map[string]interface{}{
				"agent_cert":     certPEM,
				"agent_cert_key": encKeyPEM,
			})
		}
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
			AgentTLSCert:    agentCertPEM,
			AgentTLSKey:     agentKeyPEM,
		})
		if err != nil {
			log.Printf("Failed to create container resources for %s: %v", name, err)
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
		config.ConfigureInstance(ctx, orch, name, models, resolvedKeys)

		// Connect tunnel to the newly running instance
		startTunnelForInstance(&inst)
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
		go config.ConfigureInstance(context.Background(), orch, inst.Name, models, resolvedKeys)
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

	// Disconnect tunnel and cancel reconnect loop
	stopTunnelForInstance(inst.ID)

	if orch := orchestrator.Get(); orch != nil {
		if err := orch.DeleteInstance(r.Context(), inst.Name); err != nil {
			log.Printf("Failed to delete container resources for %s – proceeding with DB cleanup: %v", inst.Name, err)
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

	// Connect tunnel in background (agent may need time to fully start)
	instCopy := inst
	go startTunnelForInstance(&instCopy)

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

	// Disconnect tunnel and cancel reconnect loop before stopping
	stopTunnelForInstance(inst.ID)

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

	// Disconnect existing tunnel; start reconnect loop for when agent comes back
	stopTunnelForInstance(inst.ID)
	instCopy := inst
	go startTunnelForInstance(&instCopy)

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

	orch := orchestrator.Get()
	if orch == nil {
		writeError(w, http.StatusServiceUnavailable, "No orchestrator available")
		return
	}

	stdout, _, code, err := orch.ExecInInstance(r.Context(), inst.Name, []string{"cat", orchestrator.PathOpenClawConfig})
	if err != nil || code != 0 {
		writeError(w, http.StatusServiceUnavailable, "Instance must be running to read config")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"config": stdout})
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

	// Compute next sort_order
	var maxSortOrder int
	database.DB.Model(&database.Instance{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxSortOrder)

	inst := database.Instance{
		Name:            cloneName,
		DisplayName:     cloneDisplayName,
		Status:          "creating",
		CPURequest:      src.CPURequest,
		CPULimit:        src.CPULimit,
		MemoryRequest:   src.MemoryRequest,
		MemoryLimit:     src.MemoryLimit,
		StorageHomebrew: src.StorageHomebrew,
		StorageClawd:    src.StorageClawd,
		StorageChrome:   src.StorageChrome,
		BraveAPIKey:     src.BraveAPIKey,
		ContainerImage:  src.ContainerImage,
		VNCResolution:   src.VNCResolution,
		GatewayToken:    encGatewayToken,
		ModelsConfig:    src.ModelsConfig,
		DefaultModel:    src.DefaultModel,
		SortOrder:       maxSortOrder + 1,
	}

	if err := database.DB.Create(&inst).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create cloned instance")
		return
	}

	// Generate agent TLS certificate pair for the clone
	var cloneCertPEM, cloneKeyPEM string
	certPEM, keyPEM, err := crypto.GenerateAgentCertPair(cloneName)
	if err != nil {
		log.Printf("Failed to generate agent cert for clone %s: %v", cloneName, err)
	} else {
		cloneCertPEM = certPEM
		cloneKeyPEM = keyPEM
		encKeyPEM, err := crypto.Encrypt(keyPEM)
		if err != nil {
			log.Printf("Failed to encrypt agent cert key for clone %s: %v", cloneName, err)
		} else {
			database.DB.Model(&inst).Updates(map[string]interface{}{
				"agent_cert":     certPEM,
				"agent_cert_key": encKeyPEM,
			})
		}
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
			AgentTLSCert:    cloneCertPEM,
			AgentTLSKey:     cloneKeyPEM,
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
		config.ConfigureInstance(ctx, orch, cloneName, models, resolvedKeys)

		// Connect tunnel to the newly running clone
		startTunnelForInstance(&inst)
	}()

	writeJSON(w, http.StatusCreated, instanceToResponse(inst, "creating"))
}

func TunnelStatus(w http.ResponseWriter, r *http.Request) {
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

	writeJSON(w, http.StatusOK, map[string]bool{
		"connected": isTunnelConnected(inst.ID),
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
