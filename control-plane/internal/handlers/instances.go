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
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/analytics"
	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/llmgateway"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/taskmanager"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
	"github.com/go-chi/chi/v5"
)

// startInstanceTask registers a long-running instance operation with the
// TaskManager so it appears in the SSE task stream and toast UI. These
// tasks are not user-cancellable (OnCancel nil per scope decision —
// aborting mid-K8s call is too risky). If TaskMgr is nil (test scaffolding
// without a wired manager), the work falls back to a plain goroutine.
//
// The goroutine bodies still update the instance DB row themselves; the
// task return value is informational for toast UX. We infer "failed" from
// the in-memory status message ("Failed: ...") so the toast turns red.
func startInstanceTask(taskType taskmanager.TaskType, instanceID, userID uint, displayName, title string, work func(ctx context.Context)) {
	startInstanceTaskWithMessage(taskType, instanceID, userID, displayName, title, "", work)
}

// startInstanceTaskWithMessage is like startInstanceTask but also seeds the
// task's `message` (toast description) before Run executes. The toast
// surfaces the description immediately instead of waiting for the first
// h.UpdateMessage from inside the work func.
func startInstanceTaskWithMessage(taskType taskmanager.TaskType, instanceID, userID uint, displayName, title, message string, work func(ctx context.Context)) {
	startInstanceTaskFull(taskType, instanceID, userID, displayName, title, message, nil, work)
}

// startInstanceTaskFull is the most flexible variant: also accepts an
// OnCancel cleanup callback. A non-nil OnCancel marks the task as
// user-cancellable on the SSE/REST surface.
func startInstanceTaskFull(taskType taskmanager.TaskType, instanceID, userID uint, displayName, title, message string, onCancel taskmanager.OnCancel, work func(ctx context.Context)) string {
	if TaskMgr == nil {
		go work(context.Background())
		return ""
	}
	return TaskMgr.Start(taskmanager.StartOpts{
		Type:         taskType,
		InstanceID:   instanceID,
		UserID:       userID,
		ResourceName: displayName,
		Title:        title,
		OnCancel:     onCancel,
		Run: func(ctx context.Context, h *taskmanager.Handle) error {
			if message != "" {
				h.UpdateMessage(message)
			}
			work(ctx)
			if msg := getStatusMessage(instanceID); strings.HasPrefix(msg, "Failed") {
				return fmt.Errorf("%s", msg)
			}
			return nil
		},
	})
}

// callerID returns the authenticated caller's user ID, or 0 if no user is
// in context. Used to stamp UserID onto tasks for visibility filtering.
func callerID(r *http.Request) uint {
	if u := middleware.GetUser(r); u != nil {
		return u.ID
	}
	return 0
}

// In-memory status messages for instance creation progress.
var statusMessages sync.Map

func setStatusMessage(id uint, msg string) { statusMessages.Store(id, msg) }
func clearStatusMessage(id uint)           { statusMessages.Delete(id) }
func getStatusMessage(id uint) string {
	if v, ok := statusMessages.Load(id); ok {
		return v.(string)
	}
	return ""
}

type modelsConfig struct {
	Disabled []string `json:"disabled"`
	Extra    []string `json:"extra"`
}

type instanceCreateRequest struct {
	DisplayName        string            `json:"display_name"`
	CPURequest         string            `json:"cpu_request"`
	CPULimit           string            `json:"cpu_limit"`
	MemoryRequest      string            `json:"memory_request"`
	MemoryLimit        string            `json:"memory_limit"`
	StorageHomebrew    string            `json:"storage_homebrew"`
	StorageHome        string            `json:"storage_home"`
	BraveAPIKey        *string           `json:"brave_api_key"`
	Models             *modelsConfig     `json:"models"`
	DefaultModel       string            `json:"default_model"`
	ContainerImage     *string           `json:"container_image"`
	VNCResolution      *string           `json:"vnc_resolution"`
	Timezone           *string           `json:"timezone"`
	UserAgent          *string           `json:"user_agent"`
	EnabledProviders   []uint            `json:"enabled_providers"`
	EnvVarsSet         map[string]string `json:"env_vars_set"`
	BrowserProvider    *string           `json:"browser_provider"`
	BrowserImage       *string           `json:"browser_image"`
	BrowserIdleMinutes *int              `json:"browser_idle_minutes"`
	BrowserStorage     *string           `json:"browser_storage"`
}

type modelsResponse struct {
	Effective        []string `json:"effective"`
	DisabledDefaults []string `json:"disabled_defaults"`
	Extra            []string `json:"extra"`
}

type instanceResponse struct {
	ID                    uint              `json:"id"`
	Name                  string            `json:"name"`
	DisplayName           string            `json:"display_name"`
	Status                string            `json:"status"`
	CPURequest            string            `json:"cpu_request"`
	CPULimit              string            `json:"cpu_limit"`
	MemoryRequest         string            `json:"memory_request"`
	MemoryLimit           string            `json:"memory_limit"`
	StorageHomebrew       string            `json:"storage_homebrew"`
	StorageHome           string            `json:"storage_home"`
	HasBraveOverride      bool              `json:"has_brave_override"`
	Models                *modelsResponse   `json:"models"`
	DefaultModel          string            `json:"default_model"`
	ContainerImage        *string           `json:"container_image"`
	HasImageOverride      bool              `json:"has_image_override"`
	VNCResolution         *string           `json:"vnc_resolution"`
	HasResolutionOverride bool              `json:"has_resolution_override"`
	Timezone              *string           `json:"timezone"`
	HasTimezoneOverride   bool              `json:"has_timezone_override"`
	UserAgent             *string           `json:"user_agent"`
	HasUserAgentOverride  bool              `json:"has_user_agent_override"`
	EnvVars               map[string]string `json:"env_vars"`
	HasEnvOverride        bool              `json:"has_env_override"`
	RequiresRestart       bool              `json:"requires_restart,omitempty"`
	Restarting            bool              `json:"restarting,omitempty"`
	LiveImageInfo         *string           `json:"live_image_info,omitempty"`
	StatusMessage         string            `json:"status_message,omitempty"`
	AllowedSourceIPs      string            `json:"allowed_source_ips"`
	EnabledProviders      []uint            `json:"enabled_providers"`
	InstanceProviders     []providerResp    `json:"instance_providers"`
	ControlURL            string            `json:"control_url"`
	GatewayToken          string            `json:"gateway_token"`
	SortOrder             int               `json:"sort_order"`
	CreatedAt             string            `json:"created_at"`
	UpdatedAt             string            `json:"updated_at"`
	IsLegacyEmbedded      bool              `json:"is_legacy_embedded"`
	BrowserProvider       string            `json:"browser_provider,omitempty"`
	BrowserImage          string            `json:"browser_image,omitempty"`
	BrowserIdleMinutes    *int              `json:"browser_idle_minutes,omitempty"`
	BrowserStorage        string            `json:"browser_storage,omitempty"`
	BrowserActive         bool              `json:"browser_active"`
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

// GatewayProvider holds the virtual auth key, API type, and models for a gateway provider.
type GatewayProvider struct {
	Key        string
	APIType    string
	Models     []database.ProviderModel
	CatalogKey string // non-empty for catalog-backed providers (e.g. "openai", "anthropic")
}

// openclawProviderCfg is the JSON shape expected by OpenClaw's models.providers config.
type openclawProviderCfg struct {
	BaseURL string                   `json:"baseUrl"`
	API     string                   `json:"api"`
	APIKey  string                   `json:"apiKey"`
	Models  []database.ProviderModel `json:"models"`
}

// buildOpenClawProvidersJSON builds the models.providers JSON for OpenClaw config.
// It filters catalog providers to only the selected models.
func buildOpenClawProvidersJSON(models []string, gatewayProviders map[string]GatewayProvider, gatewayPort int) (string, error) {
	if len(gatewayProviders) == 0 || gatewayPort <= 0 {
		return "", nil
	}

	effectiveSet := make(map[string]struct{}, len(models))
	for _, m := range models {
		effectiveSet[m] = struct{}{}
	}

	providers := make(map[string]openclawProviderCfg, len(gatewayProviders))
	for providerKey, gp := range gatewayProviders {
		apiType := gp.APIType
		if apiType == "" {
			apiType = "openai-completions"
		}
		var gpModels []database.ProviderModel
		if gp.CatalogKey != "" {
			var allModels []database.ProviderModel
			if len(gp.Models) > 0 {
				allModels = gp.Models
			} else {
				allModels = getCatalogModels(gp.CatalogKey)
			}
			for _, m := range allModels {
				if _, ok := effectiveSet[providerKey+"/"+m.ID]; ok {
					gpModels = append(gpModels, m)
				}
			}
		} else if len(gp.Models) > 0 {
			gpModels = gp.Models
		}
		if gpModels == nil {
			gpModels = []database.ProviderModel{}
		}
		providers[providerKey] = openclawProviderCfg{
			BaseURL: fmt.Sprintf("http://127.0.0.1:%d", gatewayPort),
			API:     apiType,
			APIKey:  gp.Key,
			Models:  gpModels,
		}
	}

	b, err := json.Marshal(providers)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// resolveGatewayProviders builds the providerKey→GatewayProvider map for an instance's enabled
// providers (both global and instance-specific). Each entry includes the virtual auth key,
// API type, and stored model list.
func resolveGatewayProviders(inst database.Instance) map[string]GatewayProvider {
	enabledIDs := parseEnabledProviders(inst.EnabledProviders)
	gatewayKeys := llmgateway.GetInstanceGatewayKeys(inst.ID)

	var providers []database.LLMProvider
	if len(enabledIDs) > 0 {
		database.DB.Where("id IN ?", enabledIDs).Find(&providers)
	}

	// Also load instance-specific providers
	var instProviders []database.LLMProvider
	database.DB.Where("instance_id = ?", inst.ID).Find(&instProviders)
	providers = append(providers, instProviders...)

	if len(providers) == 0 {
		return nil
	}

	result := make(map[string]GatewayProvider, len(providers))
	for _, p := range providers {
		gk, ok := gatewayKeys[p.ID]
		if !ok {
			continue
		}
		result[p.Key] = GatewayProvider{
			Key:        gk,
			APIType:    p.APIType,
			Models:     database.ParseProviderModels(p.Models),
			CatalogKey: p.Provider,
		}
	}
	return result
}

// resolveInstanceModels builds the effective model list for pushing to the running instance.
// If DefaultModel is set and present in the list, it is moved to the front so it becomes the primary model.
func resolveInstanceModels(inst database.Instance) []string {
	mc := parseModelsConfig(inst.ModelsConfig)
	models := computeEffectiveModels(mc)

	if inst.DefaultModel != "" {
		for i, m := range models {
			if m == inst.DefaultModel {
				models = append([]string{m}, append(models[:i:i], models[i+1:]...)...)
				break
			}
		}
	}
	return models
}

func parseEnabledProviders(raw string) []uint {
	if raw == "" || raw == "[]" {
		return []uint{}
	}
	var ids []uint
	json.Unmarshal([]byte(raw), &ids)
	if ids == nil {
		return []uint{}
	}
	return ids
}

// allProviderIDsForInstance returns the union of global enabled provider IDs
// and instance-specific provider IDs.
func allProviderIDsForInstance(instID uint, globalEnabledIDs []uint) []uint {
	var instProviders []database.LLMProvider
	database.DB.Where("instance_id = ?", instID).Select("id").Find(&instProviders)
	all := append([]uint{}, globalEnabledIDs...)
	for _, p := range instProviders {
		all = append(all, p.ID)
	}
	return all
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
	var timezone *string
	if inst.Timezone != "" {
		timezone = &inst.Timezone
	}
	var userAgent *string
	if inst.UserAgent != "" {
		userAgent = &inst.UserAgent
	}
	var gatewayToken string
	if inst.GatewayToken != "" {
		gatewayToken, _ = utils.Decrypt(inst.GatewayToken)
	}

	enabledProviders := parseEnabledProviders(inst.EnabledProviders)

	// Fetch instance-specific providers
	var instProviders []database.LLMProvider
	database.DB.Where("instance_id = ?", inst.ID).Order("id ASC").Find(&instProviders)
	instProviderResps := make([]providerResp, len(instProviders))
	for i, p := range instProviders {
		instProviderResps[i] = toProviderResp(p)
	}

	mc := parseModelsConfig(inst.ModelsConfig)
	effective := computeEffectiveModels(mc)

	envVarsPlain := EnvVarsForResponse(inst.EnvVars)

	return instanceResponse{
		ID:                    inst.ID,
		Name:                  inst.Name,
		DisplayName:           inst.DisplayName,
		Status:                status,
		StatusMessage:         getStatusMessage(inst.ID),
		CPURequest:            inst.CPURequest,
		CPULimit:              inst.CPULimit,
		MemoryRequest:         inst.MemoryRequest,
		MemoryLimit:           inst.MemoryLimit,
		StorageHomebrew:       inst.StorageHomebrew,
		StorageHome:           inst.StorageHome,
		HasBraveOverride:      inst.BraveAPIKey != "",
		Models:                &modelsResponse{Effective: effective, DisabledDefaults: mc.Disabled, Extra: mc.Extra},
		DefaultModel:          inst.DefaultModel,
		ContainerImage:        containerImage,
		HasImageOverride:      inst.ContainerImage != "",
		VNCResolution:         vncResolution,
		HasResolutionOverride: inst.VNCResolution != "",
		Timezone:              timezone,
		HasTimezoneOverride:   inst.Timezone != "",
		UserAgent:             userAgent,
		HasUserAgentOverride:  inst.UserAgent != "",
		EnvVars:               envVarsPlain,
		HasEnvOverride:        len(envVarsPlain) > 0,
		AllowedSourceIPs:      inst.AllowedSourceIPs,
		EnabledProviders:      enabledProviders,
		InstanceProviders:     instProviderResps,
		ControlURL:            fmt.Sprintf("/openclaw/%d/", inst.ID),
		GatewayToken:          gatewayToken,
		SortOrder:             inst.SortOrder,
		CreatedAt:             formatTimestamp(inst.CreatedAt),
		UpdatedAt:             formatTimestamp(inst.UpdatedAt),
		IsLegacyEmbedded:      database.IsLegacyEmbedded(getEffectiveImage(inst)),
		BrowserProvider:       inst.BrowserProvider,
		BrowserImage:          inst.BrowserImage,
		BrowserIdleMinutes:    inst.BrowserIdleMinutes,
		BrowserStorage:        inst.BrowserStorage,
		BrowserActive:         inst.BrowserActive,
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

	if inst.Status == "creating" {
		return "creating"
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

func getEffectiveTimezone(inst database.Instance) string {
	if inst.Timezone != "" {
		return inst.Timezone
	}
	val, err := database.GetSetting("default_timezone")
	if err == nil && val != "" {
		return val
	}
	return "America/New_York"
}

func getEffectiveUserAgent(inst database.Instance) string {
	if inst.UserAgent != "" {
		return inst.UserAgent
	}
	val, err := database.GetSetting("default_user_agent")
	if err == nil && val != "" {
		return val
	}
	return ""
}

// restartInstanceAsync restarts a running instance in the background,
// rebuilding its container with current config and shared folder mounts.
// Safe to call for stopped instances (no-op if status is not "running").
func restartInstanceAsync(inst database.Instance, userID uint) {
	restartInstanceAsyncWithToast(inst, userID,
		fmt.Sprintf("Restarting instance %s", inst.DisplayName), "")
}

// restartInstanceAsyncWithToast is like restartInstanceAsync but lets the
// caller customize the toast title and description. Used by flows that
// trigger a restart as a side effect (e.g. shared-folder mount changes).
func restartInstanceAsyncWithToast(inst database.Instance, userID uint, title, message string) {
	if inst.Status != "running" {
		return
	}
	orch := orchestrator.Get()
	if orch == nil {
		return
	}

	// Stop SSH tunnels; they will be recreated by the background manager
	if SSHMgr != nil {
		SSHMgr.CancelReconnection(inst.ID)
	}
	if TunnelMgr != nil {
		if err := TunnelMgr.StopTunnelsForInstance(inst.ID); err != nil {
			log.Printf("Failed to stop tunnels for instance %d: %v", inst.ID, err)
		}
	}

	database.DB.Model(&inst).Updates(map[string]interface{}{
		"status":     "restarting",
		"updated_at": time.Now().UTC(),
	})

	startInstanceTaskWithMessage(taskmanager.TaskInstanceRestart, inst.ID, userID, inst.DisplayName,
		title, message,
		func(ctx context.Context) {
			params := buildCreateParams(inst)
			if err := orch.RestartInstance(ctx, inst.Name, params); err != nil {
				log.Printf("Failed to restart instance %d: %v", inst.ID, err)
				setStatusMessage(inst.ID, fmt.Sprintf("Failed: %v", err))
				database.DB.Model(&database.Instance{}).Where("id = ?", inst.ID).Updates(map[string]interface{}{
					"status":     "error",
					"updated_at": time.Now().UTC(),
				})
			}
		})
}

// buildCreateParams constructs orchestrator.CreateParams from a database Instance.
func buildCreateParams(inst database.Instance) orchestrator.CreateParams {
	envVars := map[string]string{}

	// User-defined env vars (global defaults overridden by per-instance values)
	MergeUserEnvVars(envVars, LoadGlobalEnvVars(), LoadInstanceEnvVars(inst))

	// System env vars — applied last so they cannot be shadowed
	if inst.GatewayToken != "" {
		if plain, err := utils.Decrypt(inst.GatewayToken); err == nil {
			envVars["OPENCLAW_GATEWAY_TOKEN"] = plain
		}
	}
	envVars["CLAWORC_INSTANCE_ID"] = fmt.Sprintf("%d", inst.ID)

	return orchestrator.CreateParams{
		Name:               inst.Name,
		CPURequest:         inst.CPURequest,
		CPULimit:           inst.CPULimit,
		MemoryRequest:      inst.MemoryRequest,
		MemoryLimit:        inst.MemoryLimit,
		StorageHomebrew:    inst.StorageHomebrew,
		StorageHome:        inst.StorageHome,
		ContainerImage:     getEffectiveImage(inst),
		VNCResolution:      getEffectiveResolution(inst),
		Timezone:           getEffectiveTimezone(inst),
		UserAgent:          getEffectiveUserAgent(inst),
		EnvVars:            envVars,
		SharedFolderMounts: getSharedFolderMounts(inst.ID),
	}
}

func getSharedFolderMounts(instanceID uint) []orchestrator.SharedFolderMount {
	folders, err := database.GetSharedFoldersForInstance(instanceID)
	if err != nil {
		log.Printf("Failed to load shared folder mounts for instance %d: %v", instanceID, err)
		return nil
	}
	var mounts []orchestrator.SharedFolderMount
	for _, sf := range folders {
		mounts = append(mounts, orchestrator.SharedFolderMount{
			VolumeID:  sf.ID,
			MountPath: sf.MountPath,
		})
	}
	return mounts
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

	// Set defaults: prefer the configured global default for each resource,
	// falling back to a hardcoded value only if the setting is missing.
	resolveDefault := func(field *string, settingKey, fallback string) {
		if *field != "" {
			return
		}
		if v, err := database.GetSetting(settingKey); err == nil && v != "" {
			*field = v
			return
		}
		*field = fallback
	}
	resolveDefault(&body.CPURequest, "default_cpu_request", "500m")
	resolveDefault(&body.CPULimit, "default_cpu_limit", "2000m")
	resolveDefault(&body.MemoryRequest, "default_memory_request", "1Gi")
	resolveDefault(&body.MemoryLimit, "default_memory_limit", "4Gi")
	resolveDefault(&body.StorageHomebrew, "default_storage_homebrew", "10Gi")
	resolveDefault(&body.StorageHome, "default_storage_home", "10Gi")

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
		encBraveKey, err = utils.Encrypt(*body.BraveAPIKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to encrypt API key")
			return
		}
	}

	// Generate gateway token
	gatewayTokenPlain := generateToken()
	encGatewayToken, err := utils.Encrypt(gatewayTokenPlain)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to encrypt gateway token")
		return
	}

	var containerImage string
	if body.ContainerImage != nil {
		containerImage = *body.ContainerImage
	}
	// Populate the new slim agent image as the default for newly-created
	// instances. Existing rows with empty ContainerImage keep resolving via
	// default_container_image (which seeds to the legacy combined image), but
	// any instance created from now on opts into the on-demand browser-pod
	// layout unless the caller passes an explicit container_image override.
	if containerImage == "" {
		if def, err := database.GetSetting("default_agent_image"); err == nil && def != "" {
			containerImage = def
		}
	}
	var vncResolution string
	if body.VNCResolution != nil {
		vncResolution = *body.VNCResolution
	}
	var timezone string
	if body.Timezone != nil {
		timezone = *body.Timezone
	}
	var userAgent string
	if body.UserAgent != nil {
		userAgent = *body.UserAgent
	}
	var browserProvider, browserImage, browserStorage string
	var browserIdleMinutes *int
	if body.BrowserProvider != nil {
		browserProvider = *body.BrowserProvider
	}
	if body.BrowserImage != nil {
		browserImage = *body.BrowserImage
	}
	if body.BrowserStorage != nil {
		browserStorage = *body.BrowserStorage
	}
	if body.BrowserIdleMinutes != nil {
		browserIdleMinutes = body.BrowserIdleMinutes
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

	// Serialize enabled providers
	enabledProviders := body.EnabledProviders
	if enabledProviders == nil {
		enabledProviders = []uint{}
	}
	enabledProvidersJSON, _ := json.Marshal(enabledProviders)

	// Serialize (and encrypt) user-supplied env vars
	envVarsJSON := "{}"
	if len(body.EnvVarsSet) > 0 {
		encoded, err := UpsertEncryptedEnvVarsJSON("{}", body.EnvVarsSet, nil)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		envVarsJSON = encoded
	}

	// Compute next sort_order
	var maxSortOrder int
	database.DB.Model(&database.Instance{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxSortOrder)

	inst := database.Instance{
		Name:               name,
		DisplayName:        body.DisplayName,
		Status:             "creating",
		CPURequest:         body.CPURequest,
		CPULimit:           body.CPULimit,
		MemoryRequest:      body.MemoryRequest,
		MemoryLimit:        body.MemoryLimit,
		StorageHomebrew:    body.StorageHomebrew,
		StorageHome:        body.StorageHome,
		BraveAPIKey:        encBraveKey,
		ContainerImage:     containerImage,
		VNCResolution:      vncResolution,
		Timezone:           timezone,
		UserAgent:          userAgent,
		GatewayToken:       encGatewayToken,
		ModelsConfig:       modelsConfigJSON,
		DefaultModel:       body.DefaultModel,
		EnabledProviders:   string(enabledProvidersJSON),
		EnvVars:            envVarsJSON,
		SortOrder:          maxSortOrder + 1,
		BrowserProvider:    browserProvider,
		BrowserImage:       browserImage,
		BrowserIdleMinutes: browserIdleMinutes,
		BrowserStorage:     browserStorage,
	}

	if err := database.DB.Create(&inst).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create instance")
		return
	}

	// Auto-assign the new instance to the creator if they are a non-admin user.
	// Admins implicitly access all instances and don't need a UserInstance row.
	if caller := middleware.GetUser(r); caller != nil && caller.Role != "admin" {
		if err := database.DB.Create(&database.UserInstance{UserID: caller.ID, InstanceID: inst.ID}).Error; err != nil {
			log.Printf("Failed to auto-assign instance %d to creator %d: %s", inst.ID, caller.ID, utils.SanitizeForLog(err.Error()))
		}
	}

	effectiveImage := getEffectiveImage(inst)
	effectiveResolution := getEffectiveResolution(inst)
	effectiveTimezone := getEffectiveTimezone(inst)
	effectiveUserAgent := getEffectiveUserAgent(inst)

	// Pre-create virtual keys so we can pass initial config to the container.
	// This eliminates the race where messages arrive before providers are configured.
	allIDs := allProviderIDsForInstance(inst.ID, enabledProviders)
	if err := llmgateway.EnsureKeysForInstance(inst.ID, allIDs); err != nil {
		log.Printf("Failed to ensure LLM gateway keys for instance %d: %s", inst.ID, utils.SanitizeForLog(err.Error()))
	}
	models := resolveInstanceModels(inst)
	gatewayProviders := resolveGatewayProviders(inst)

	// Build initial OpenClaw config env vars so the gateway starts with providers already configured
	initialModelsJSON := ""
	if len(models) > 0 {
		modelConfig := map[string]interface{}{"primary": models[0]}
		if len(models) > 1 {
			modelConfig["fallbacks"] = models[1:]
		} else {
			modelConfig["fallbacks"] = []string{}
		}
		if b, err := json.Marshal(modelConfig); err == nil {
			initialModelsJSON = string(b)
		}
	}
	initialProvidersJSON, _ := buildOpenClawProvidersJSON(models, gatewayProviders, config.Cfg.LLMGatewayPort)

	// Launch container creation asynchronously (image pull can take minutes)
	startInstanceTask(taskmanager.TaskInstanceCreate, inst.ID, callerID(r), inst.DisplayName,
		fmt.Sprintf("Creating instance %s", inst.DisplayName),
		func(ctx context.Context) {
			orch := orchestrator.Get()
			if orch == nil {
				setStatusMessage(inst.ID, "Failed: no orchestrator available")
				database.DB.Model(&inst).Update("status", "error")
				return
			}

			envVars := map[string]string{}

			// User-defined env vars (global defaults overridden by per-instance values).
			// Applied first so reserved system names below can never be shadowed.
			MergeUserEnvVars(envVars, LoadGlobalEnvVars(), LoadInstanceEnvVars(inst))

			// System env vars — reserved, always win over user values
			if gatewayTokenPlain != "" {
				envVars["OPENCLAW_GATEWAY_TOKEN"] = gatewayTokenPlain
			}
			envVars["CLAWORC_INSTANCE_ID"] = fmt.Sprintf("%d", inst.ID)
			if initialModelsJSON != "" {
				envVars["OPENCLAW_INITIAL_MODELS"] = initialModelsJSON
			}
			if initialProvidersJSON != "" {
				envVars["OPENCLAW_INITIAL_PROVIDERS"] = initialProvidersJSON
			}

			err := orch.CreateInstance(ctx, orchestrator.CreateParams{
				Name:            name,
				CPURequest:      body.CPURequest,
				CPULimit:        body.CPULimit,
				MemoryRequest:   body.MemoryRequest,
				MemoryLimit:     body.MemoryLimit,
				StorageHomebrew: body.StorageHomebrew,
				StorageHome:     body.StorageHome,
				ContainerImage:  effectiveImage,
				VNCResolution:   effectiveResolution,
				Timezone:        effectiveTimezone,
				UserAgent:       effectiveUserAgent,
				EnvVars:         envVars,
				OnProgress:      func(msg string) { setStatusMessage(inst.ID, msg) },
			})
			if err != nil {
				log.Printf("Failed to create container resources for %s: %s", utils.SanitizeForLog(name), utils.SanitizeForLog(err.Error()))
				setStatusMessage(inst.ID, fmt.Sprintf("Failed: %v", err))
				database.DB.Model(&inst).Update("status", "error")
				return
			}
			clearStatusMessage(inst.ID)
			database.DB.Model(&inst).Updates(map[string]interface{}{
				"status":     "running",
				"updated_at": time.Now().UTC(),
			})

			// Reconcile models and providers via SSH (handles any config that couldn't
			// be passed via env vars, and restarts the gateway for a clean state)
			database.DB.First(&inst, inst.ID)
			sshClient, err := SSHMgr.WaitForSSH(ctx, inst.ID, 120*time.Second)
			if err != nil {
				log.Printf("Failed to get SSH connection for instance %d during configure: %v", inst.ID, err)
				return
			}
			ConfigureInstance(ctx, orch, sshproxy.NewSSHInstance(sshClient), inst.Name, models, gatewayProviders, config.Cfg.LLMGatewayPort)
		})

	var totalInstances int64
	database.DB.Model(&database.Instance{}).Count(&totalInstances)
	providerAliases := make([]string, 0, len(gatewayProviders))
	for alias := range gatewayProviders {
		providerAliases = append(providerAliases, alias)
	}
	primaryModel := ""
	primaryProvider := ""
	if len(models) > 0 {
		primaryModel = models[0]
		if len(providerAliases) > 0 {
			primaryProvider = providerAliases[0]
		}
	}
	analytics.Track(r.Context(), analytics.EventInstanceCreated, map[string]any{
		"total_instances":  totalInstances,
		"instance_id":      inst.ID,
		"provider_aliases": providerAliases,
		"cpu_limit":        inst.CPULimit,
		"memory_limit":     inst.MemoryLimit,
		"model":            primaryModel,
		"provider_name":    primaryProvider,
	})

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

	orch := orchestrator.Get()
	orchStatus := "stopped"
	if orch != nil {
		orchStatus, _ = orch.GetInstanceStatus(r.Context(), inst.Name)
	}
	status := resolveStatus(&inst, orchStatus)
	resp := instanceToResponse(inst, status)
	if orch != nil {
		if info, err := orch.GetInstanceImageInfo(r.Context(), inst.Name); err == nil && info != "" {
			resp.LiveImageInfo = &info
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type instanceUpdateRequest struct {
	BraveAPIKey        *string           `json:"brave_api_key"`
	Models             *modelsConfig     `json:"models"`
	DefaultModel       *string           `json:"default_model"`
	Timezone           *string           `json:"timezone"`
	UserAgent          *string           `json:"user_agent"`
	AllowedSourceIPs   *string           `json:"allowed_source_ips"` // admin only: comma-separated IPs/CIDRs
	EnabledProviders   *[]uint           `json:"enabled_providers"`  // admin only: LLM gateway provider IDs
	DisplayName        *string           `json:"display_name"`       // admin only
	CPURequest         *string           `json:"cpu_request"`        // admin only
	CPULimit           *string           `json:"cpu_limit"`          // admin only
	MemoryRequest      *string           `json:"memory_request"`     // admin only
	MemoryLimit        *string           `json:"memory_limit"`       // admin only
	VNCResolution      *string           `json:"vnc_resolution"`     // admin only
	EnvVarsSet         map[string]string `json:"env_vars_set"`
	EnvVarsUnset       []string          `json:"env_vars_unset"`
	BrowserProvider    *string           `json:"browser_provider"`     // non-legacy only
	BrowserImage       *string           `json:"browser_image"`        // non-legacy only
	BrowserIdleMinutes *int              `json:"browser_idle_minutes"` // non-legacy only; null = global default
	BrowserStorage     *string           `json:"browser_storage"`      // non-legacy only
}

var (
	cpuRegex        = regexp.MustCompile(`^(\d+m|\d+(\.\d+)?)$`)
	memoryRegex     = regexp.MustCompile(`^\d+(Ki|Mi|Gi)$`)
	resolutionRegex = regexp.MustCompile(`^\d+x\d+$`)
)

func cpuToMillicores(s string) int64 {
	if strings.HasSuffix(s, "m") {
		n, _ := strconv.ParseInt(s[:len(s)-1], 10, 64)
		return n
	}
	f, _ := strconv.ParseFloat(s, 64)
	return int64(f * 1000)
}

func memoryToBytes(s string) int64 {
	unitMap := map[string]int64{"Ki": 1024, "Mi": 1024 * 1024, "Gi": 1024 * 1024 * 1024}
	for suffix, multiplier := range unitMap {
		if strings.HasSuffix(s, suffix) {
			n, _ := strconv.ParseInt(s[:len(s)-len(suffix)], 10, 64)
			return n * multiplier
		}
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
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

	// Update Brave API key
	if body.BraveAPIKey != nil {
		if *body.BraveAPIKey != "" {
			encrypted, err := utils.Encrypt(*body.BraveAPIKey)
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

	// Update timezone
	if body.Timezone != nil {
		database.DB.Model(&inst).Update("timezone", *body.Timezone)
	}

	// Update user agent
	if body.UserAgent != nil {
		database.DB.Model(&inst).Update("user_agent", *body.UserAgent)
	}

	// Browser-pod settings (only meaningful for non-legacy instances).
	if body.BrowserProvider != nil {
		database.DB.Model(&inst).Update("browser_provider", *body.BrowserProvider)
	}
	if body.BrowserImage != nil {
		database.DB.Model(&inst).Update("browser_image", *body.BrowserImage)
	}
	if body.BrowserStorage != nil {
		database.DB.Model(&inst).Update("browser_storage", *body.BrowserStorage)
	}
	if body.BrowserIdleMinutes != nil {
		database.DB.Model(&inst).Update("browser_idle_minutes", body.BrowserIdleMinutes)
	}

	// Update allowed source IPs (admin only)
	if body.AllowedSourceIPs != nil {
		user := middleware.GetUser(r)
		if user == nil || user.Role != "admin" {
			writeError(w, http.StatusForbidden, "Only admins can configure source IP restrictions")
			return
		}
		// Validate the IP list before saving
		if *body.AllowedSourceIPs != "" {
			if _, err := sshproxy.ParseIPRestrictions(*body.AllowedSourceIPs); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid source IP restriction: %v", err))
				return
			}
		}
		database.DB.Model(&inst).Update("allowed_source_ips", *body.AllowedSourceIPs)
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

	// Update enabled providers (admin only)
	if body.EnabledProviders != nil {
		user := middleware.GetUser(r)
		if user == nil || user.Role != "admin" {
			writeError(w, http.StatusForbidden, "Only admins can configure LLM gateway providers")
			return
		}
		b, _ := json.Marshal(*body.EnabledProviders)
		database.DB.Model(&inst).Update("enabled_providers", string(b))
		allIDs := allProviderIDsForInstance(inst.ID, *body.EnabledProviders)
		if err := llmgateway.EnsureKeysForInstance(inst.ID, allIDs); err != nil {
			log.Printf("Failed to ensure LLM gateway keys for instance %d: %s", inst.ID, utils.SanitizeForLog(err.Error()))
		}
	}

	// Update display name (admin only)
	if body.DisplayName != nil {
		user := middleware.GetUser(r)
		if user == nil || user.Role != "admin" {
			writeError(w, http.StatusForbidden, "Only admins can rename instances")
			return
		}
		trimmed := strings.TrimSpace(*body.DisplayName)
		if trimmed == "" {
			writeError(w, http.StatusBadRequest, "Display name cannot be empty")
			return
		}
		database.DB.Model(&inst).Update("display_name", trimmed)
	}

	// Update CPU/memory resources (admin only)
	resourcesChanged := false
	if body.CPURequest != nil || body.CPULimit != nil || body.MemoryRequest != nil || body.MemoryLimit != nil {
		user := middleware.GetUser(r)
		if user == nil || user.Role != "admin" {
			writeError(w, http.StatusForbidden, "Only admins can change resource limits")
			return
		}

		cpuReq := inst.CPURequest
		cpuLim := inst.CPULimit
		memReq := inst.MemoryRequest
		memLim := inst.MemoryLimit

		if body.CPURequest != nil {
			if !cpuRegex.MatchString(*body.CPURequest) {
				writeError(w, http.StatusBadRequest, "Invalid CPU request format (e.g., 500m or 0.5)")
				return
			}
			cpuReq = *body.CPURequest
		}
		if body.CPULimit != nil {
			if !cpuRegex.MatchString(*body.CPULimit) {
				writeError(w, http.StatusBadRequest, "Invalid CPU limit format (e.g., 2000m or 2)")
				return
			}
			cpuLim = *body.CPULimit
		}
		if body.MemoryRequest != nil {
			if !memoryRegex.MatchString(*body.MemoryRequest) {
				writeError(w, http.StatusBadRequest, "Invalid memory request format (e.g., 1Gi or 512Mi)")
				return
			}
			memReq = *body.MemoryRequest
		}
		if body.MemoryLimit != nil {
			if !memoryRegex.MatchString(*body.MemoryLimit) {
				writeError(w, http.StatusBadRequest, "Invalid memory limit format (e.g., 4Gi or 2048Mi)")
				return
			}
			memLim = *body.MemoryLimit
		}

		if cpuToMillicores(cpuReq) > cpuToMillicores(cpuLim) {
			writeError(w, http.StatusBadRequest, "CPU request cannot exceed CPU limit")
			return
		}
		if memoryToBytes(memReq) > memoryToBytes(memLim) {
			writeError(w, http.StatusBadRequest, "Memory request cannot exceed memory limit")
			return
		}

		database.DB.Model(&inst).Updates(map[string]interface{}{
			"cpu_request":    cpuReq,
			"cpu_limit":      cpuLim,
			"memory_request": memReq,
			"memory_limit":   memLim,
		})
		resourcesChanged = true
	}

	// Update VNC resolution (admin only)
	if body.VNCResolution != nil {
		user := middleware.GetUser(r)
		if user == nil || user.Role != "admin" {
			writeError(w, http.StatusForbidden, "Only admins can change VNC resolution")
			return
		}
		if *body.VNCResolution != "" && !resolutionRegex.MatchString(*body.VNCResolution) {
			writeError(w, http.StatusBadRequest, "Invalid resolution format (e.g., 1920x1080)")
			return
		}
		database.DB.Model(&inst).Update("vnc_resolution", *body.VNCResolution)
	}

	// Update env vars (PATCH-style: set+unset). Only write and restart when the
	// plaintext set actually changed — a no-op request (e.g. the user clicked
	// Save without modifying anything) skips both.
	envVarsChanged := false
	if len(body.EnvVarsSet) > 0 || len(body.EnvVarsUnset) > 0 {
		updated, changed, err := ApplyEnvVarsDelta(inst.EnvVars, body.EnvVarsSet, body.EnvVarsUnset)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if changed {
			if err := database.DB.Model(&inst).Update("env_vars", updated).Error; err != nil {
				writeError(w, http.StatusInternalServerError, "Failed to save env vars")
				return
			}
			envVarsChanged = true
			localCount := len(decodeEncryptedEnvVarsJSON(updated))
			globalRaw, _ := database.GetSetting("default_env_vars")
			analytics.Track(r.Context(), analytics.EventInstanceEnvVarsEdited, map[string]any{
				"instance_id":     inst.ID,
				"local_env_vars":  localCount,
				"global_env_vars": len(decodeEncryptedEnvVarsJSON(globalRaw)),
			})
		}
	}

	// Re-fetch
	database.DB.First(&inst, inst.ID)

	// Push updated config to the running instance
	orch := orchestrator.Get()
	orchStatus := "stopped"
	if orch != nil {
		orchStatus, _ = orch.GetInstanceStatus(r.Context(), inst.Name)
	}

	// Apply resource changes to running container
	if resourcesChanged && orch != nil && orchStatus == "running" {
		if err := orch.UpdateResources(r.Context(), inst.Name, orchestrator.UpdateResourcesParams{
			CPURequest:    inst.CPURequest,
			CPULimit:      inst.CPULimit,
			MemoryRequest: inst.MemoryRequest,
			MemoryLimit:   inst.MemoryLimit,
		}); err != nil {
			log.Printf("Failed to update resources for instance %d: %v", inst.ID, err)
		}
	}
	if orch != nil && orchStatus == "running" {
		models := resolveInstanceModels(inst)
		gatewayProviders := resolveGatewayProviders(inst)
		instID := inst.ID
		instName := inst.Name
		go func() {
			bgCtx := context.Background()
			sshClient, err := SSHMgr.WaitForSSH(bgCtx, instID, 30*time.Second)
			if err != nil {
				log.Printf("Failed to get SSH connection for instance %d during configure: %v", instID, err)
				return
			}
			ConfigureInstance(bgCtx, orch, sshproxy.NewSSHInstance(sshClient), instName, models, gatewayProviders, config.Cfg.LLMGatewayPort)
		}()
	}

	status := resolveStatus(&inst, orchStatus)
	// Auto-restart so the new env vars are injected into the container.
	// buildCreateParams (evaluated inside restartInstanceAsync's goroutine)
	// re-reads EnvVars from the DB, so it picks up what we just wrote.
	restarting := false
	if envVarsChanged && status == "running" {
		restartInstanceAsync(inst, callerID(r))
		restarting = true
		status = "restarting"
	}

	resp := instanceToResponse(inst, status)
	if envVarsChanged && (restarting || status == "running") {
		resp.RequiresRestart = true
	}
	resp.Restarting = restarting
	if orch != nil {
		if info, err := orch.GetInstanceImageInfo(r.Context(), inst.Name); err == nil && info != "" {
			resp.LiveImageInfo = &info
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func GetInstanceStats(w http.ResponseWriter, r *http.Request) {
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

	stats, err := orch.GetContainerStats(r.Context(), inst.Name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Stats unavailable")
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

func UpdateInstanceImage(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "Only admins can update instance images")
		return
	}

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

	orchStatus, _ := orch.GetInstanceStatus(r.Context(), inst.Name)
	if orchStatus != "running" {
		writeError(w, http.StatusBadRequest, "Instance must be running to update image")
		return
	}

	effectiveImage := getEffectiveImage(inst)
	if effectiveImage == "" {
		writeError(w, http.StatusBadRequest, "No container image configured")
		return
	}
	if strings.Contains(effectiveImage, "@sha256:") {
		writeError(w, http.StatusBadRequest, "Cannot update a digest-pinned image; use a tag-based image instead")
		return
	}

	// Set status to restarting
	database.DB.Model(&inst).Updates(map[string]interface{}{
		"status":     "restarting",
		"updated_at": time.Now().UTC(),
	})

	// Stop SSH tunnels before update; they will be recreated by the background manager
	if SSHMgr != nil {
		SSHMgr.CancelReconnection(inst.ID)
	}
	if TunnelMgr != nil {
		if err := TunnelMgr.StopTunnelsForInstance(inst.ID); err != nil {
			log.Printf("Failed to stop tunnels for instance %d: %v", inst.ID, err)
		}
	}

	effectiveResolution := getEffectiveResolution(inst)
	effectiveTimezone := getEffectiveTimezone(inst)
	effectiveUserAgent := getEffectiveUserAgent(inst)

	// Decrypt gateway token for env vars
	envVars := map[string]string{}
	if inst.GatewayToken != "" {
		if plain, err := utils.Decrypt(inst.GatewayToken); err == nil {
			envVars["OPENCLAW_GATEWAY_TOKEN"] = plain
		}
	}
	envVars["CLAWORC_INSTANCE_ID"] = fmt.Sprintf("%d", inst.ID)

	instID := inst.ID
	instName := inst.Name
	startInstanceTask(taskmanager.TaskInstanceImageUpdate, inst.ID, callerID(r), inst.DisplayName,
		fmt.Sprintf("Updating image for %s", inst.DisplayName),
		func(ctx context.Context) {
			err := orch.UpdateImage(ctx, instName, orchestrator.CreateParams{
				Name:               instName,
				CPURequest:         inst.CPURequest,
				CPULimit:           inst.CPULimit,
				MemoryRequest:      inst.MemoryRequest,
				MemoryLimit:        inst.MemoryLimit,
				ContainerImage:     effectiveImage,
				VNCResolution:      effectiveResolution,
				Timezone:           effectiveTimezone,
				UserAgent:          effectiveUserAgent,
				EnvVars:            envVars,
				SharedFolderMounts: getSharedFolderMounts(instID),
			})
			if err != nil {
				log.Printf("Failed to update image for instance %d: %v", instID, err)
				finalStatus := "error"
				if liveStatus, lerr := orch.GetInstanceStatus(ctx, instName); lerr == nil && liveStatus == "running" {
					log.Printf("Instance %d pod is still running after UpdateImage failure; keeping status=running so tunnels are reconciled", instID)
					finalStatus = "running"
				}
				database.DB.Model(&database.Instance{}).Where("id = ?", instID).Updates(map[string]interface{}{
					"status":         finalStatus,
					"status_message": fmt.Sprintf("Image update failed: %v", err),
					"updated_at":     time.Now().UTC(),
				})
				return
			}
			log.Printf("Image updated successfully for instance %d", instID)
			database.DB.Model(&database.Instance{}).Where("id = ?", instID).Updates(map[string]interface{}{
				"status":     "running",
				"updated_at": time.Now().UTC(),
			})
		})

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
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

	// Stop SSH tunnels and close connection before deleting
	if SSHMgr != nil {
		SSHMgr.CancelReconnection(inst.ID)
	}
	if TunnelMgr != nil {
		if err := TunnelMgr.StopTunnelsForInstance(inst.ID); err != nil {
			log.Printf("Failed to stop tunnels for instance %d: %v", inst.ID, err)
		}
	}

	if orch := orchestrator.Get(); orch != nil {
		// Tear down the on-demand browser pod first so its container/volume
		// don't outlive the agent. Best-effort: a failure here shouldn't
		// block the agent cleanup or DB delete.
		if err := orch.DeleteBrowserPod(r.Context(), inst.ID); err != nil {
			log.Printf("Failed to delete browser pod for %s: %v", utils.SanitizeForLog(inst.Name), err)
		}
		if err := orch.DeleteInstance(r.Context(), inst.Name); err != nil {
			log.Printf("Failed to delete container resources for %s – proceeding with DB cleanup: %v", utils.SanitizeForLog(inst.Name), err)
		}
	}
	if err := database.DeleteBrowserSession(inst.ID); err != nil {
		log.Printf("Failed to delete browser session row for %s: %v", utils.SanitizeForLog(inst.Name), err)
	}

	// Delete instance-specific providers (API key is on the provider row)
	database.DB.Where("instance_id = ?", inst.ID).Delete(&database.LLMProvider{})

	// Delete associated gateway keys
	database.DB.Where("instance_id = ?", inst.ID).Delete(&database.LLMGatewayKey{})
	database.DB.Delete(&inst)
	var remaining int64
	database.DB.Model(&database.Instance{}).Count(&remaining)
	analytics.Track(r.Context(), analytics.EventInstanceDeleted, map[string]any{
		"remaining_instances": remaining,
	})
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

	// Stop SSH tunnels and close connection for this instance
	if SSHMgr != nil {
		SSHMgr.CancelReconnection(inst.ID)
	}
	if TunnelMgr != nil {
		if err := TunnelMgr.StopTunnelsForInstance(inst.ID); err != nil {
			log.Printf("Failed to stop tunnels for instance %d: %v", inst.ID, err)
		}
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

	// Stop SSH tunnels and close connection before restart; they will be recreated by the background manager
	if SSHMgr != nil {
		SSHMgr.CancelReconnection(inst.ID)
	}
	if TunnelMgr != nil {
		if err := TunnelMgr.StopTunnelsForInstance(inst.ID); err != nil {
			log.Printf("Failed to stop tunnels for instance %d: %v", inst.ID, err)
		}
	}

	if orch := orchestrator.Get(); orch != nil {
		// buildCreateParams is the single source of truth for CreateParams —
		// it merges global + per-instance user env vars before applying the
		// reserved system vars. Duplicating the param struct inline here used
		// to drop user env vars on every manual restart.
		params := buildCreateParams(inst)

		if err := orch.RestartInstance(r.Context(), inst.Name, params); err != nil {
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

	orch := orchestrator.Get()
	if orch == nil {
		writeError(w, http.StatusServiceUnavailable, "No orchestrator available")
		return
	}

	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	client, err := SSHMgr.EnsureConnectedWithIPCheck(r.Context(), inst.ID, orch, inst.AllowedSourceIPs)
	if err != nil {
		log.Printf("Failed to get SSH connection for instance %d: %v", inst.ID, err)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("SSH connection failed: %v", err))
		return
	}

	content, err := sshproxy.ReadFile(client, orchestrator.PathOpenClawConfig)
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

	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	orch := orchestrator.Get()
	if orch == nil {
		writeError(w, http.StatusServiceUnavailable, "No orchestrator available")
		return
	}

	client, err := SSHMgr.EnsureConnectedWithIPCheck(r.Context(), inst.ID, orch, inst.AllowedSourceIPs)
	if err != nil {
		log.Printf("Failed to get SSH connection for instance %d: %v", inst.ID, err)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("SSH connection failed: %v", err))
		return
	}

	if err := sshproxy.WriteFile(client, orchestrator.PathOpenClawConfig, []byte(body.Config)); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to write config: %v", err))
		return
	}

	instanceConn := sshproxy.NewSSHInstance(client)
	if _, stderr, code, err := instanceConn.ExecOpenclaw(r.Context(), "gateway", "stop"); err != nil || code != 0 {
		log.Printf("Failed to restart gateway for instance %d: %v %s", inst.ID, err, stderr)
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
	encGatewayToken, err := utils.Encrypt(gatewayTokenPlain)
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
		StorageHome:     src.StorageHome,
		BraveAPIKey:     src.BraveAPIKey,
		ContainerImage:  src.ContainerImage,
		VNCResolution:   src.VNCResolution,
		Timezone:        src.Timezone,
		UserAgent:       src.UserAgent,
		GatewayToken:    encGatewayToken,
		ModelsConfig:    src.ModelsConfig,
		DefaultModel:    src.DefaultModel,
		SortOrder:       maxSortOrder + 1,
		// Carry over on-demand browser config so the clone behaves like the
		// original (same image override, idle timeout, storage size, and
		// pane-visibility default).
		BrowserProvider:    src.BrowserProvider,
		BrowserImage:       src.BrowserImage,
		BrowserIdleMinutes: src.BrowserIdleMinutes,
		BrowserStorage:     src.BrowserStorage,
		BrowserActive:      src.BrowserActive,
	}

	if err := database.DB.Create(&inst).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create cloned instance")
		return
	}
	// GORM's `default:true` on BrowserActive overrides the explicit `false`
	// passed in via Create (false is the Go zero value, so the column is
	// omitted from the INSERT and the DB-level default kicks in). Patch it
	// after Create so a clone of a pane-hidden instance stays pane-hidden.
	if !src.BrowserActive {
		database.DB.Model(&inst).Update("browser_active", false)
		inst.BrowserActive = false
	}

	// Run the full clone operation asynchronously. The task is cancellable:
	// hitting POST /api/v1/tasks/{id}/cancel cancels the Run context (so
	// in-flight orchestrator calls abort) and then runs the OnCancel
	// callback, which tears down whatever was created and removes the DB
	// row. Cleanup is idempotent and best-effort.
	startInstanceTaskFull(taskmanager.TaskInstanceClone, inst.ID, callerID(r), inst.DisplayName,
		"Cloning instance",
		fmt.Sprintf("Cloning %s to %s", src.DisplayName, inst.DisplayName),
		cloneOnCancel(inst.ID, cloneName),
		func(ctx context.Context) {
			orch := orchestrator.Get()
			if orch == nil {
				setStatusMessage(inst.ID, "Failed: no orchestrator available")
				database.DB.Model(&inst).Update("status", "error")
				return
			}

			effectiveImage := getEffectiveImage(inst)
			effectiveResolution := getEffectiveResolution(inst)
			effectiveTimezone := getEffectiveTimezone(inst)
			effectiveUserAgent := getEffectiveUserAgent(inst)

			envVars := map[string]string{}
			if gatewayTokenPlain != "" {
				envVars["OPENCLAW_GATEWAY_TOKEN"] = gatewayTokenPlain
			}
			envVars["CLAWORC_INSTANCE_ID"] = fmt.Sprintf("%d", inst.ID)

			// Create container/deployment with empty volumes
			err := orch.CreateInstance(ctx, orchestrator.CreateParams{
				Name:               cloneName,
				CPURequest:         inst.CPURequest,
				CPULimit:           inst.CPULimit,
				MemoryRequest:      inst.MemoryRequest,
				MemoryLimit:        inst.MemoryLimit,
				StorageHomebrew:    inst.StorageHomebrew,
				StorageHome:        inst.StorageHome,
				ContainerImage:     effectiveImage,
				VNCResolution:      effectiveResolution,
				Timezone:           effectiveTimezone,
				UserAgent:          effectiveUserAgent,
				EnvVars:            envVars,
				OnProgress:         func(msg string) { setStatusMessage(inst.ID, msg) },
				SharedFolderMounts: getSharedFolderMounts(inst.ID),
			})
			if err != nil {
				log.Printf("Failed to create container for clone %s: %v", cloneName, err)
				setStatusMessage(inst.ID, fmt.Sprintf("Failed: %v", err))
				database.DB.Model(&inst).Update("status", "error")
				return
			}

			// Clone volume data from source
			setStatusMessage(inst.ID, "Cloning volumes...")
			if err := orch.CloneVolumes(ctx, src.Name, cloneName); err != nil {
				log.Printf("Failed to clone volumes from %s to %s: %v", src.Name, cloneName, err)
				// Continue anyway – instance is created, just without cloned data
			}
			// Clone the on-demand browser profile volume too, so Chrome cookies,
			// sessions, and persisted state follow the clone. Best-effort: if the
			// source never launched a browser there's nothing to copy.
			if err := orch.CloneBrowserVolume(ctx, src.Name, cloneName); err != nil {
				log.Printf("Failed to clone browser volume from %s to %s: %v", src.Name, cloneName, err)
			}

			clearStatusMessage(inst.ID)
			database.DB.Model(&inst).Updates(map[string]interface{}{
				"status":     "running",
				"updated_at": time.Now().UTC(),
			})

			// Push models and API keys to the running instance
			// Re-fetch to get latest state
			database.DB.First(&inst, inst.ID)
			// Don't carry over gateway keys from source — the clone gets its own instance ID
			models := resolveInstanceModels(inst)
			sshClient, err := SSHMgr.WaitForSSH(ctx, inst.ID, 120*time.Second)
			if err != nil {
				log.Printf("Failed to get SSH connection for clone %d during configure: %v", inst.ID, err)
				return
			}
			ConfigureInstance(ctx, orch, sshproxy.NewSSHInstance(sshClient), cloneName, models, nil, config.Cfg.LLMGatewayPort)
		})

	writeJSON(w, http.StatusCreated, instanceToResponse(inst, "creating"))
}

// cloneOnCancel returns the cleanup callback for a clone task. It tears
// down whatever the in-flight Run may have created on the destination —
// container, volumes, browser pod, browser volume — and removes the
// destination DB row. Idempotent and best-effort: each step logs and
// continues so a partial failure can't trap the cleanup.
//
// IMPORTANT: this only touches the *destination*, never the source. The
// instanceID and cloneName captured in the closure are the destination's.
func cloneOnCancel(instanceID uint, cloneName string) taskmanager.OnCancel {
	return func(ctx context.Context) {
		setStatusMessage(instanceID, "Canceling clone...")
		if SSHMgr != nil {
			SSHMgr.CancelReconnection(instanceID)
		}
		if TunnelMgr != nil {
			if err := TunnelMgr.StopTunnelsForInstance(instanceID); err != nil {
				log.Printf("clone-cancel: stop tunnels for %d: %v", instanceID, err)
			}
		}
		if orch := orchestrator.Get(); orch != nil {
			if err := orch.DeleteBrowserPod(ctx, instanceID); err != nil {
				log.Printf("clone-cancel: delete browser pod for %s: %v", utils.SanitizeForLog(cloneName), err)
			}
			if err := orch.DeleteInstance(ctx, cloneName); err != nil {
				log.Printf("clone-cancel: delete container for %s: %v", utils.SanitizeForLog(cloneName), err)
			}
		}
		if err := database.DeleteBrowserSession(instanceID); err != nil {
			log.Printf("clone-cancel: delete browser session row for %d: %v", instanceID, err)
		}
		// Drop instance providers / gateway keys / instance row. Mirrors the
		// teardown in DeleteInstance so a canceled clone leaves no rows behind.
		database.DB.Where("instance_id = ?", instanceID).Delete(&database.LLMProvider{})
		database.DB.Where("instance_id = ?", instanceID).Delete(&database.LLMGatewayKey{})
		database.DB.Delete(&database.Instance{}, instanceID)
		clearStatusMessage(instanceID)
	}
}

// SetBrowserActive flips the per-instance "show browser pane" toggle. Stopping
// the browser pod when the user hides the pane is intentionally NOT done here
// — the dedicated /browser/stop handler covers that and lets the frontend
// surface task feedback.
func SetBrowserActive(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}
	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	var body struct {
		BrowserActive *bool `json:"browser_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.BrowserActive == nil {
		writeError(w, http.StatusBadRequest, "browser_active is required")
		return
	}

	if err := database.DB.Model(&database.Instance{}).
		Where("id = ?", id).
		Update("browser_active", *body.BrowserActive).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update instance")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"browser_active": *body.BrowserActive})
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

// ConfigureInstance sets the model configuration and gateway providers on a running instance
// via openclaw CLI over SSH through inst.
//
// gatewayProviders (optional) maps provider key → gateway auth key for configuring
// models.providers in OpenClaw to route through the internal LLM gateway.
// gatewayPort is the port the LLM gateway listens on (typically 40001).
func ConfigureInstance(ctx context.Context, ops orchestrator.ContainerOrchestrator, inst sshproxy.Instance, name string, models []string, gatewayProviders map[string]GatewayProvider, gatewayPort int) {
	if len(models) == 0 && len(gatewayProviders) == 0 {
		return
	}

	// Wait for instance to become running
	if !waitForRunning(ctx, ops, name, 120*time.Second) {
		log.Printf("Timed out waiting for %s to start; models not configured", utils.SanitizeForLog(name))
		return
	}

	// Set model config via openclaw config set
	if len(models) > 0 {
		modelConfig := map[string]interface{}{
			"primary": models[0],
		}
		if len(models) > 1 {
			modelConfig["fallbacks"] = models[1:]
		} else {
			modelConfig["fallbacks"] = []string{}
		}
		modelJSON, err := json.Marshal(modelConfig)
		if err != nil {
			log.Printf("Error marshaling model config for %s: %v", utils.SanitizeForLog(name), err)
			return
		}
		_, stderr, code, err := inst.ExecOpenclaw(ctx, "config", "set", "agents.defaults.model", string(modelJSON), "--json")
		if err != nil {
			log.Printf("Error setting model config for %s: %v", utils.SanitizeForLog(name), err)
			return
		}
		if code != 0 {
			log.Printf("Failed to set model config for %s: %s", utils.SanitizeForLog(name), utils.SanitizeForLog(stderr))
			// continue — providers must still be configured even if model config failed
		}

		// Set models allowlist to restrict the UI dropdown to only configured models
		modelsMap := make(map[string]interface{}, len(models))
		for _, m := range models {
			modelsMap[m] = map[string]interface{}{}
		}
		modelsMapJSON, err := json.Marshal(modelsMap)
		if err != nil {
			log.Printf("Error marshaling models allowlist for %s: %v", utils.SanitizeForLog(name), err)
		} else {
			_, stderr, code, err := inst.ExecOpenclaw(ctx, "config", "set", "agents.defaults.models", string(modelsMapJSON), "--json")
			if err != nil {
				log.Printf("Error setting models allowlist for %s: %v", utils.SanitizeForLog(name), err)
			} else if code != 0 {
				log.Printf("Failed to set models allowlist for %s: %s", utils.SanitizeForLog(name), utils.SanitizeForLog(stderr))
			}
		}
	}

	// Set gateway providers via openclaw CLI.
	if len(gatewayProviders) > 0 && gatewayPort > 0 {
		providersJSON, err := buildOpenClawProvidersJSON(models, gatewayProviders, gatewayPort)
		if err != nil {
			log.Printf("Error marshaling gateway providers for %s: %v", utils.SanitizeForLog(name), err)
		} else if providersJSON != "" {
			stdout, stderr, code, err := inst.ExecOpenclaw(ctx, "config", "set", "models.providers", providersJSON, "--json")
			if err != nil {
				log.Printf("Error setting gateway providers for %s: %v", utils.SanitizeForLog(name), err)
			} else if code != 0 {
				log.Printf("Failed to set gateway providers for %s: stdout=%q stderr=%q",
					utils.SanitizeForLog(name), utils.SanitizeForLog(stdout), utils.SanitizeForLog(stderr))
			}
		}
	}

	// Restart gateway so it picks up new env vars and config
	stdout, stderr, code, err := inst.ExecOpenclaw(ctx, "gateway", "stop")
	if err != nil {
		log.Printf("Error restarting gateway for %s: %v", utils.SanitizeForLog(name), err)
		return
	}
	if code != 0 {
		log.Printf("Failed to restart gateway for %s: stdout=%q stderr=%q", utils.SanitizeForLog(name), utils.SanitizeForLog(stdout), utils.SanitizeForLog(stderr))
		return
	}
	log.Printf("Models and providers configured for %s", utils.SanitizeForLog(name))
}

func waitForRunning(ctx context.Context, ops orchestrator.ContainerOrchestrator, name string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := ops.GetInstanceStatus(ctx, name)
		if err == nil && status == "running" {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
	return false
}
