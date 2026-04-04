package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/llmgateway"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
	"github.com/go-chi/chi/v5"
)

// ---------------------------------------------------------------------------
// Provider catalog proxy (claworc.com/providers API, 1-hour in-process cache)
// ---------------------------------------------------------------------------

const catalogBaseURL = "https://claworc.com/providers"

type catalogCacheEntry struct {
	body      []byte
	expiresAt time.Time
}

var (
	catalogCacheMu    sync.RWMutex
	catalogCache      = map[string]*catalogCacheEntry{}
	catalogHTTPClient = &http.Client{Timeout: 10 * time.Second}

	// providerProbeClient is used for user-provided URLs and includes SSRF
	// protection that rejects connections to private/loopback addresses.
	providerProbeClient = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: ssrfSafeDialContext,
		},
	}
)

// ssrfSafeDialContext resolves the target host and rejects connections to
// private, loopback, and link-local IP addresses to prevent SSRF attacks.
func ssrfSafeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address: %w", err)
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS lookup failed: %w", err)
	}

	for _, ip := range ips {
		if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() || ip.IP.IsUnspecified() {
			return nil, fmt.Errorf("connections to private/internal networks are not allowed")
		}
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
}

func proxyCatalog(w http.ResponseWriter, path string) {
	catalogCacheMu.RLock()
	entry := catalogCache[path]
	catalogCacheMu.RUnlock()

	if entry == nil || time.Now().After(entry.expiresAt) {
		resp, err := catalogHTTPClient.Get(catalogBaseURL + path)
		if err != nil {
			log.Printf("catalog proxy: fetch %s: %v", utils.SanitizeForLog(path), err)
			http.Error(w, `{"error":"catalog unavailable"}`, http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, `{"error":"catalog read error"}`, http.StatusBadGateway)
			return
		}
		if resp.StatusCode != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			w.Write(body)
			return
		}
		newEntry := &catalogCacheEntry{body: body, expiresAt: time.Now().Add(time.Hour)}
		catalogCacheMu.Lock()
		catalogCache[path] = newEntry
		catalogCacheMu.Unlock()
		entry = newEntry
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(entry.body)
}

// GetCatalogProviders proxies GET /providers/ from the catalog API.
func GetCatalogProviders(w http.ResponseWriter, r *http.Request) {
	proxyCatalog(w, "/")
}

// GetCatalogProviderDetail derives a single provider from the cached root catalog.
func GetCatalogProviderDetail(w http.ResponseWriter, r *http.Request) {
	key := strings.ToLower(chi.URLParam(r, "key"))
	entry, err := getCatalogEntryByKey(key)
	if err != nil {
		http.Error(w, `{"error":"catalog unavailable"}`, http.StatusBadGateway)
		return
	}
	if entry == nil {
		writeError(w, http.StatusNotFound, "Provider not found in catalog")
		return
	}
	body, _ := json.Marshal(entry)
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// getCatalogEntryByKey looks up a provider by key from the cached root catalog.
// Returns nil, nil if the key is not found. Returns nil, err on fetch failure.
func getCatalogEntryByKey(key string) (*catalogRootEntry, error) {
	entries, err := ensureRootCatalog()
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Name == key {
			return &e, nil
		}
	}
	return nil, nil
}

// ensureRootCatalog returns parsed root catalog entries, using cache if valid.
func ensureRootCatalog() ([]catalogRootEntry, error) {
	catalogCacheMu.RLock()
	entry := catalogCache["/"]
	catalogCacheMu.RUnlock()

	if entry == nil || time.Now().After(entry.expiresAt) {
		// Fetch and cache
		return getCatalogRoot()
	}
	var entries []catalogRootEntry
	if err := json.Unmarshal(entry.body, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// catalogRootModel is one model entry from the catalog root response.
type catalogRootModel struct {
	ModelID         string  `json:"model_id"`
	ModelName       string  `json:"model_name"`
	Reasoning       bool    `json:"reasoning"`
	Vision          bool    `json:"vision"`
	ContextWindow   *int    `json:"context_window"`
	MaxTokens       *int    `json:"max_tokens"`
	InputCost       float64 `json:"input_cost"`
	OutputCost      float64 `json:"output_cost"`
	CachedReadCost  float64 `json:"cached_read_cost"`
	CachedWriteCost float64 `json:"cached_write_cost"`
	Tag             string  `json:"tag"`
	Description     string  `json:"description"`
}

// catalogRootEntry is one provider entry from the catalog root response.
type catalogRootEntry struct {
	Name      string             `json:"name"`
	Label     string             `json:"label"`
	IconKey   string             `json:"icon_key"`
	APIFormat string             `json:"api_format"`
	BaseURL   string             `json:"base_url"`
	Models    []catalogRootModel `json:"models"`
}

// catalogModelToProviderModel converts a catalogRootModel to a database.ProviderModel.
func catalogModelToProviderModel(m catalogRootModel) database.ProviderModel {
	pm := database.ProviderModel{
		ID:            m.ModelID,
		Name:          m.ModelName,
		Reasoning:     m.Reasoning,
		ContextWindow: m.ContextWindow,
		MaxTokens:     m.MaxTokens,
	}
	if m.InputCost > 0 || m.OutputCost > 0 || m.CachedReadCost > 0 || m.CachedWriteCost > 0 {
		pm.Cost = &database.ProviderModelCost{
			Input:      m.InputCost,
			Output:     m.OutputCost,
			CacheRead:  m.CachedReadCost,
			CacheWrite: m.CachedWriteCost,
		}
	}
	return pm
}

// getCatalogRoot force-refreshes the "/" cache entry, fetches the full catalog root,
// stores raw bytes in the cache, and returns the parsed entries.
func getCatalogRoot() ([]catalogRootEntry, error) {
	catalogCacheMu.Lock()
	delete(catalogCache, "/")
	catalogCacheMu.Unlock()

	resp, err := catalogHTTPClient.Get(catalogBaseURL + "/")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog returned status %d", resp.StatusCode)
	}
	entry := &catalogCacheEntry{body: body, expiresAt: time.Now().Add(time.Hour)}
	catalogCacheMu.Lock()
	catalogCache["/"] = entry
	catalogCacheMu.Unlock()

	var entries []catalogRootEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// getCatalogModels returns ProviderModel entries for a catalog provider,
// derived from the cached root catalog. Returns nil on error.
var getCatalogModels = func(catalogKey string) []database.ProviderModel {
	if catalogKey == "" {
		return nil
	}
	entry, err := getCatalogEntryByKey(strings.ToLower(catalogKey))
	if err != nil || entry == nil {
		if err != nil {
			log.Printf("getCatalogModels: %s: %v", utils.SanitizeForLog(catalogKey), err)
		}
		return nil
	}
	result := make([]database.ProviderModel, len(entry.Models))
	for i, m := range entry.Models {
		result[i] = catalogModelToProviderModel(m)
	}
	return result
}

var providerKeyRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]*[a-z0-9]$|^[a-z0-9]$`)

type providerRequest struct {
	Key        string                   `json:"key"`
	Provider   string                   `json:"provider"` // catalog provider key, optional
	Name       string                   `json:"name"`
	BaseURL    string                   `json:"base_url"`
	APIType    string                   `json:"api_type"`
	Models     []database.ProviderModel `json:"models"`
	APIKey     string                   `json:"api_key"`
	InstanceID *uint                    `json:"instance_id,omitempty"` // non-nil = instance-specific provider
}

type providerResp struct {
	ID           uint                     `json:"id"`
	Key          string                   `json:"key"`
	InstanceID   *uint                    `json:"instance_id,omitempty"`
	Provider     string                   `json:"provider"`
	Name         string                   `json:"name"`
	BaseURL      string                   `json:"base_url"`
	APIType      string                   `json:"api_type"`
	MaskedAPIKey string                   `json:"masked_api_key"`
	Models       []database.ProviderModel `json:"models"`
	CreatedAt    string                   `json:"created_at"`
	UpdatedAt    string                   `json:"updated_at"`
}

func toProviderResp(p database.LLMProvider) providerResp {
	var masked string
	if p.APIKey != "" {
		if decrypted, err := utils.Decrypt(p.APIKey); err == nil && decrypted != "" {
			masked = utils.Mask(decrypted)
		}
	}
	return providerResp{
		ID:           p.ID,
		Key:          p.Key,
		InstanceID:   p.InstanceID,
		Provider:     p.Provider,
		Name:         p.Name,
		BaseURL:      p.BaseURL,
		APIType:      p.APIType,
		MaskedAPIKey: masked,
		Models:       database.ParseProviderModels(p.Models),
		CreatedAt:    formatTimestamp(p.CreatedAt),
		UpdatedAt:    formatTimestamp(p.UpdatedAt),
	}
}

func ListProviders(w http.ResponseWriter, r *http.Request) {
	var providers []database.LLMProvider
	if err := database.DB.Where("instance_id IS NULL").Order("id ASC").Find(&providers).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list providers")
		return
	}
	result := make([]providerResp, len(providers))
	for i, p := range providers {
		result[i] = toProviderResp(p)
	}
	writeJSON(w, http.StatusOK, result)
}

func ListInstanceProviders(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}
	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}
	var providers []database.LLMProvider
	if err := database.DB.Where("instance_id = ?", id).Order("id ASC").Find(&providers).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list instance providers")
		return
	}
	result := make([]providerResp, len(providers))
	for i, p := range providers {
		result[i] = toProviderResp(p)
	}
	writeJSON(w, http.StatusOK, result)
}

func CreateProvider(w http.ResponseWriter, r *http.Request) {
	var body providerRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.Key == "" || body.Name == "" || body.BaseURL == "" {
		writeError(w, http.StatusBadRequest, "key, name, and base_url are required")
		return
	}
	if !providerKeyRegex.MatchString(body.Key) {
		writeError(w, http.StatusBadRequest, "key must be lowercase alphanumeric with hyphens (e.g. anthropic, my-ollama)")
		return
	}
	if body.Provider != "" && strings.TrimSpace(body.APIKey) == "" {
		writeError(w, http.StatusBadRequest, "api_key is required for catalog providers")
		return
	}

	apiType := body.APIType
	if apiType == "" {
		apiType = "openai-completions"
	}
	modelsJSON := []byte("[]")
	if body.Models != nil {
		modelsJSON, _ = json.Marshal(body.Models)
	} else if body.Provider != "" {
		// Auto-fetch models from catalog for catalog providers
		if catalogModels := getCatalogModels(body.Provider); catalogModels != nil {
			modelsJSON, _ = json.Marshal(catalogModels)
		}
	}
	p := database.LLMProvider{
		Key:      body.Key,
		Provider: body.Provider,
		Name:     body.Name,
		BaseURL:  body.BaseURL,
		APIType:  apiType,
		Models:   string(modelsJSON),
	}
	if apiKey := strings.TrimSpace(body.APIKey); apiKey != "" {
		encrypted, err := utils.Encrypt(apiKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to encrypt API key")
			return
		}
		p.APIKey = encrypted
	}
	if body.InstanceID != nil {
		// Instance-specific provider — check access
		if !middleware.CanAccessInstance(r, *body.InstanceID) {
			writeError(w, http.StatusForbidden, "Access denied")
			return
		}
		var inst database.Instance
		if err := database.DB.First(&inst, *body.InstanceID).Error; err != nil {
			writeError(w, http.StatusBadRequest, "Instance not found")
			return
		}
		p.InstanceID = body.InstanceID
	}
	if err := database.DB.Create(&p).Error; err != nil {
		writeError(w, http.StatusConflict, "Provider key already exists")
		return
	}

	// For instance-specific providers, ensure gateway keys and reconfigure
	if p.InstanceID != nil {
		var inst database.Instance
		if database.DB.First(&inst, *p.InstanceID).Error == nil {
			enabledIDs := parseEnabledProviders(inst.EnabledProviders)
			allIDs := allProviderIDsForInstance(inst.ID, enabledIDs)
			if err := llmgateway.EnsureKeysForInstance(inst.ID, allIDs); err != nil {
				log.Printf("Failed to ensure gateway keys for instance %d after provider create: %s", inst.ID, utils.SanitizeForLog(err.Error()))
			}
			reconfigureInstanceAsync(inst.ID)
		}
	}

	writeJSON(w, http.StatusCreated, toProviderResp(p))
}

func UpdateProvider(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid provider ID")
		return
	}

	var p database.LLMProvider
	if err := database.DB.First(&p, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Provider not found")
		return
	}

	// Access control for instance-specific providers
	if p.InstanceID != nil {
		if !middleware.CanAccessInstance(r, *p.InstanceID) {
			writeError(w, http.StatusForbidden, "Access denied")
			return
		}
	}

	var body providerRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if body.Name != "" {
		p.Name = body.Name
	}
	if body.BaseURL != "" {
		p.BaseURL = body.BaseURL
	}
	if body.APIType != "" {
		p.APIType = body.APIType
	}
	if body.Models != nil {
		modelsJSON, _ := json.Marshal(body.Models)
		p.Models = string(modelsJSON)
	}
	if apiKey := strings.TrimSpace(body.APIKey); apiKey != "" {
		encrypted, err := utils.Encrypt(apiKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to encrypt API key")
			return
		}
		p.APIKey = encrypted
	}
	// Key is immutable once created
	if err := database.DB.Save(&p).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update provider")
		return
	}

	pushProviderUpdateToInstances(uint(id))
	writeJSON(w, http.StatusOK, toProviderResp(p))
}

func pushProviderUpdateToInstances(providerID uint) {
	orch := orchestrator.Get()
	if orch == nil {
		return
	}

	// Check if this is an instance-specific provider
	var provider database.LLMProvider
	if err := database.DB.First(&provider, providerID).Error; err != nil {
		return
	}
	if provider.InstanceID != nil {
		// Instance-specific: only reconfigure the owning instance
		reconfigureInstanceAsync(*provider.InstanceID)
		return
	}

	// Global provider: reconfigure all instances that have it enabled
	var instances []database.Instance
	database.DB.Find(&instances)
	for _, inst := range instances {
		ids := parseEnabledProviders(inst.EnabledProviders)
		enabled := false
		for _, id := range ids {
			if id == providerID {
				enabled = true
				break
			}
		}
		if !enabled {
			continue
		}
		status, err := orch.GetInstanceStatus(context.Background(), inst.Name)
		if err != nil || status != "running" {
			continue
		}
		allIDs := allProviderIDsForInstance(inst.ID, ids)
		llmgateway.EnsureKeysForInstance(inst.ID, allIDs)
		database.DB.First(&inst, inst.ID)
		models := resolveInstanceModels(inst)
		gatewayProviders := resolveGatewayProviders(inst)
		instID := inst.ID
		instName := inst.Name
		go func() {
			bgCtx := context.Background()
			sshClient, err := SSHMgr.WaitForSSH(bgCtx, instID, 30*time.Second)
			if err != nil {
				log.Printf("Failed to get SSH connection for instance %d during provider update: %s", instID, utils.SanitizeForLog(err.Error()))
				return
			}
			ConfigureInstance(
				bgCtx, orch, sshproxy.NewSSHInstance(sshClient), instName,
				models, gatewayProviders,
				config.Cfg.LLMGatewayPort,
			)
		}()
	}
}

// reconfigureInstanceAsync triggers a background reconfiguration of an instance's
// models and gateway providers via SSH.
func reconfigureInstanceAsync(instID uint) {
	orch := orchestrator.Get()
	if orch == nil {
		return
	}
	var inst database.Instance
	if err := database.DB.First(&inst, instID).Error; err != nil {
		return
	}
	status, err := orch.GetInstanceStatus(context.Background(), inst.Name)
	if err != nil || status != "running" {
		return
	}
	enabledIDs := parseEnabledProviders(inst.EnabledProviders)
	allIDs := allProviderIDsForInstance(inst.ID, enabledIDs)
	llmgateway.EnsureKeysForInstance(inst.ID, allIDs)
	database.DB.First(&inst, inst.ID)
	models := resolveInstanceModels(inst)
	gatewayProviders := resolveGatewayProviders(inst)
	instName := inst.Name
	safeID := inst.ID
	go func() {
		bgCtx := context.Background()
		sshClient, err := SSHMgr.WaitForSSH(bgCtx, safeID, 30*time.Second)
		if err != nil {
			log.Printf("Failed to get SSH connection for instance %d during reconfigure: %s", safeID, utils.SanitizeForLog(err.Error()))
			return
		}
		ConfigureInstance(
			bgCtx, orch, sshproxy.NewSSHInstance(sshClient), instName,
			models, gatewayProviders,
			config.Cfg.LLMGatewayPort,
		)
	}()
}

func SyncProviderModels(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid provider ID")
		return
	}

	var p database.LLMProvider
	if err := database.DB.First(&p, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Provider not found")
		return
	}
	if p.Provider == "" {
		writeError(w, http.StatusBadRequest, "Custom providers have no catalog to sync from")
		return
	}

	// Force-refresh the root catalog cache
	catalogCacheMu.Lock()
	delete(catalogCache, "/")
	catalogCacheMu.Unlock()

	log.Printf("Syncing models for provider %d (%s)", p.ID, p.Provider)
	models := getCatalogModels(p.Provider)
	if models == nil {
		log.Printf("Failed to fetch catalog models for provider %d (%s)", p.ID, p.Provider)
		writeError(w, http.StatusBadGateway, "Failed to fetch catalog models")
		return
	}

	modelsJSON, _ := json.Marshal(models)
	p.Models = string(modelsJSON)
	if err := database.DB.Save(&p).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update provider models")
		return
	}
	log.Printf("Synced %d models for provider %d (%s)", len(models), p.ID, p.Provider)
	writeJSON(w, http.StatusOK, toProviderResp(p))
}

type syncProviderChange struct {
	Old string `json:"old"`
	New string `json:"new"`
}

type syncProviderResult struct {
	ID      uint                          `json:"id"`
	Key     string                        `json:"key"`
	Catalog string                        `json:"catalog"`
	Skipped bool                          `json:"skipped"`
	Updated bool                          `json:"updated"`
	Changes map[string]syncProviderChange `json:"changes,omitempty"`
}

type syncAllResp struct {
	Catalog []catalogRootEntry   `json:"catalog"`
	Results []syncProviderResult `json:"results"`
}

func SyncAllProviderModels(w http.ResponseWriter, r *http.Request) {
	catalogEntries, err := getCatalogRoot()
	if err != nil {
		log.Printf("SyncAllProviderModels: fetch catalog root: %v", err)
		writeError(w, http.StatusBadGateway, "Failed to fetch provider catalog")
		return
	}

	catalogByKey := make(map[string]catalogRootEntry, len(catalogEntries))
	for _, e := range catalogEntries {
		catalogByKey[e.Name] = e
	}

	var providers []database.LLMProvider
	database.DB.Order("id ASC").Find(&providers)

	results := make([]syncProviderResult, 0, len(providers))
	for _, p := range providers {
		res := syncProviderResult{ID: p.ID, Key: p.Key, Catalog: p.Provider}
		if p.Provider == "" {
			res.Skipped = true
			results = append(results, res)
			continue
		}
		catEntry, found := catalogByKey[p.Provider]
		if !found {
			res.Skipped = true
			results = append(results, res)
			continue
		}

		changes := map[string]syncProviderChange{}

		if p.Name != catEntry.Label {
			changes["name"] = syncProviderChange{Old: p.Name, New: catEntry.Label}
			p.Name = catEntry.Label
		}
		if p.BaseURL != catEntry.BaseURL {
			changes["base_url"] = syncProviderChange{Old: p.BaseURL, New: catEntry.BaseURL}
			p.BaseURL = catEntry.BaseURL
		}
		if p.APIType != catEntry.APIFormat {
			changes["api_type"] = syncProviderChange{Old: p.APIType, New: catEntry.APIFormat}
			p.APIType = catEntry.APIFormat
		}

		// Convert catalog models and compare serialized JSON
		newModels := make([]database.ProviderModel, len(catEntry.Models))
		for i, m := range catEntry.Models {
			newModels[i] = catalogModelToProviderModel(m)
		}
		newModelsJSON, _ := json.Marshal(newModels)
		if string(newModelsJSON) != p.Models {
			changes["models"] = syncProviderChange{Old: p.Models, New: string(newModelsJSON)}
			p.Models = string(newModelsJSON)
		}

		if len(changes) > 0 {
			database.DB.Save(&p)
			log.Printf("SyncAllProviderModels: updated provider %d (%s): %v", p.ID, p.Provider, changes)
			res.Updated = true
			res.Changes = changes
		}
		results = append(results, res)
	}

	writeJSON(w, http.StatusOK, syncAllResp{Catalog: catalogEntries, Results: results})
}

func DeleteProvider(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid provider ID")
		return
	}

	var p database.LLMProvider
	if err := database.DB.First(&p, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Provider not found")
		return
	}

	// Access control for instance-specific providers
	if p.InstanceID != nil {
		if !middleware.CanAccessInstance(r, *p.InstanceID) {
			writeError(w, http.StatusForbidden, "Access denied")
			return
		}
	}

	ownerInstanceID := p.InstanceID

	// Cascade-delete gateway keys (API key is on the provider row itself)
	database.DB.Where("provider_id = ?", id).Delete(&database.LLMGatewayKey{})
	database.DB.Delete(&p)

	// Reconfigure owning instance if this was instance-specific
	if ownerInstanceID != nil {
		reconfigureInstanceAsync(*ownerInstanceID)
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Usage stats aggregation
// ---------------------------------------------------------------------------

type InstanceUsageStat struct {
	InstanceID          uint    `json:"instance_id"`
	InstanceName        string  `json:"instance_name"`
	InstanceDisplayName string  `json:"instance_display_name"`
	TotalRequests       int     `json:"total_requests"`
	InputTokens         int64   `json:"input_tokens"`
	CachedInputTokens   int64   `json:"cached_input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CostUSD             float64 `json:"cost_usd"`
}

type ProviderUsageStat struct {
	ProviderID        uint    `json:"provider_id"`
	ProviderKey       string  `json:"provider_key"`
	ProviderName      string  `json:"provider_name"`
	TotalRequests     int     `json:"total_requests"`
	InputTokens       int64   `json:"input_tokens"`
	CachedInputTokens int64   `json:"cached_input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CostUSD           float64 `json:"cost_usd"`
}

type ModelUsageStat struct {
	ModelID           string  `json:"model_id"`
	ProviderID        uint    `json:"provider_id"`
	ProviderKey       string  `json:"provider_key"`
	TotalRequests     int     `json:"total_requests"`
	InputTokens       int64   `json:"input_tokens"`
	CachedInputTokens int64   `json:"cached_input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CostUSD           float64 `json:"cost_usd"`
}

type UsageTimePoint struct {
	Date              string  `json:"date"`
	TotalRequests     int     `json:"total_requests"`
	InputTokens       int64   `json:"input_tokens"`
	CachedInputTokens int64   `json:"cached_input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CostUSD           float64 `json:"cost_usd"`
}

type UsageTotals struct {
	TotalRequests     int     `json:"total_requests"`
	InputTokens       int64   `json:"input_tokens"`
	CachedInputTokens int64   `json:"cached_input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CostUSD           float64 `json:"cost_usd"`
}

type UsageInstanceInfo struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

type UsageProviderInfo struct {
	ID   uint   `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

type UsageStatsResponse struct {
	ByInstance  []InstanceUsageStat `json:"by_instance"`
	ByProvider  []ProviderUsageStat `json:"by_provider"`
	ByModel     []ModelUsageStat    `json:"by_model"`
	TimeSeries  []UsageTimePoint    `json:"time_series"`
	Total       UsageTotals         `json:"total"`
	Instances   []UsageInstanceInfo `json:"instances"`
	Providers   []UsageProviderInfo `json:"providers"`
	Granularity string              `json:"granularity"`
}

func GetUsageStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now().UTC()
	startDate := now.AddDate(0, 0, -30).Format("2006-01-02")
	endDate := now.Format("2006-01-02")
	if v := q.Get("start_date"); v != "" {
		startDate = v
	}
	if v := q.Get("end_date"); v != "" {
		endDate = v
	}
	// Determine time-series granularity based on date range
	startParsed, _ := time.Parse("2006-01-02", startDate)
	endParsed, _ := time.Parse("2006-01-02", endDate)
	daysDiff := int(endParsed.Sub(startParsed).Hours() / 24)

	var tsGroupExpr, granularity string
	switch {
	case daysDiff == 0:
		tsGroupExpr = "strftime('%Y-%m-%dT%H:%M', requested_at)"
		granularity = "minute"
	case daysDiff < 7:
		tsGroupExpr = "strftime('%Y-%m-%dT%H', requested_at)"
		granularity = "hour"
	default:
		tsGroupExpr = "strftime('%Y-%m-%d', requested_at)"
		granularity = "day"
	}

	// Build optional filters
	var instanceFilter *uint
	var providerFilter *uint
	if v := q.Get("instance_id"); v != "" {
		if id, err := strconv.ParseUint(v, 10, 32); err == nil {
			uid := uint(id)
			instanceFilter = &uid
		}
	}
	if v := q.Get("provider_id"); v != "" {
		if id, err := strconv.ParseUint(v, 10, 32); err == nil {
			uid := uint(id)
			providerFilter = &uid
		}
	}

	// Use DATE() to compare only the date part, making filtering format-agnostic
	// regardless of how GORM/SQLite stores the time.Time value.
	baseWhere := "DATE(requested_at) >= ? AND DATE(requested_at) <= ?"
	baseArgs := []interface{}{startDate, endDate}
	if instanceFilter != nil {
		baseWhere += " AND instance_id = ?"
		baseArgs = append(baseArgs, *instanceFilter)
	}
	if providerFilter != nil {
		baseWhere += " AND provider_id = ?"
		baseArgs = append(baseArgs, *providerFilter)
	}

	// by_instance
	type instRow struct {
		InstanceID        uint
		TotalRequests     int
		InputTokens       int64
		CachedInputTokens int64
		OutputTokens      int64
		CostUSD           float64
	}
	var instRows []instRow
	database.LogsDB.Raw(
		"SELECT instance_id, COUNT(*) total_requests, SUM(input_tokens) input_tokens, SUM(cached_input_tokens) cached_input_tokens, SUM(output_tokens) output_tokens, SUM(cost_usd) cost_usd FROM llm_request_logs WHERE "+baseWhere+" GROUP BY instance_id ORDER BY cost_usd DESC",
		baseArgs...,
	).Scan(&instRows)

	// by_provider
	type provRow struct {
		ProviderID        uint
		TotalRequests     int
		InputTokens       int64
		CachedInputTokens int64
		OutputTokens      int64
		CostUSD           float64
	}
	var provRows []provRow
	database.LogsDB.Raw(
		"SELECT provider_id, COUNT(*) total_requests, SUM(input_tokens) input_tokens, SUM(cached_input_tokens) cached_input_tokens, SUM(output_tokens) output_tokens, SUM(cost_usd) cost_usd FROM llm_request_logs WHERE "+baseWhere+" GROUP BY provider_id ORDER BY cost_usd DESC",
		baseArgs...,
	).Scan(&provRows)

	// by_model
	type modelRow struct {
		ModelID           string
		ProviderID        uint
		TotalRequests     int
		InputTokens       int64
		CachedInputTokens int64
		OutputTokens      int64
		CostUSD           float64
	}
	var modelRows []modelRow
	database.LogsDB.Raw(
		"SELECT model_id, provider_id, COUNT(*) total_requests, SUM(input_tokens) input_tokens, SUM(cached_input_tokens) cached_input_tokens, SUM(output_tokens) output_tokens, SUM(cost_usd) cost_usd FROM llm_request_logs WHERE "+baseWhere+" GROUP BY model_id, provider_id ORDER BY cost_usd DESC",
		baseArgs...,
	).Scan(&modelRows)

	// time_series
	type tsRow struct {
		Date              string
		TotalRequests     int
		InputTokens       int64
		CachedInputTokens int64
		OutputTokens      int64
		CostUSD           float64
	}
	var tsRows []tsRow
	database.LogsDB.Raw(
		"SELECT "+tsGroupExpr+" date, COUNT(*) total_requests, SUM(input_tokens) input_tokens, SUM(cached_input_tokens) cached_input_tokens, SUM(output_tokens) output_tokens, SUM(cost_usd) cost_usd FROM llm_request_logs WHERE "+baseWhere+" GROUP BY "+tsGroupExpr+" ORDER BY date ASC",
		baseArgs...,
	).Scan(&tsRows)

	// Load instance name map from main DB
	var instances []database.Instance
	database.DB.Select("id, name, display_name").Find(&instances)
	type instInfo struct{ Name, DisplayName string }
	instInfoMap := map[uint]instInfo{}
	for _, inst := range instances {
		instInfoMap[inst.ID] = instInfo{Name: inst.Name, DisplayName: inst.DisplayName}
	}

	// Load provider key/name map from main DB
	var providers []database.LLMProvider
	database.DB.Select("id, key, name").Find(&providers)
	provInfoMap := map[uint]struct{ Key, Name string }{}
	for _, p := range providers {
		provInfoMap[p.ID] = struct{ Key, Name string }{p.Key, p.Name}
	}

	// Build response
	resp := UsageStatsResponse{
		ByInstance: make([]InstanceUsageStat, len(instRows)),
		ByProvider: make([]ProviderUsageStat, len(provRows)),
		ByModel:    make([]ModelUsageStat, len(modelRows)),
		TimeSeries: make([]UsageTimePoint, len(tsRows)),
		Instances:  make([]UsageInstanceInfo, len(instances)),
		Providers:  make([]UsageProviderInfo, len(providers)),
	}

	for i, row := range instRows {
		ii := instInfoMap[row.InstanceID]
		resp.ByInstance[i] = InstanceUsageStat{
			InstanceID:          row.InstanceID,
			InstanceName:        ii.Name,
			InstanceDisplayName: ii.DisplayName,
			TotalRequests:       row.TotalRequests,
			InputTokens:         row.InputTokens,
			CachedInputTokens:   row.CachedInputTokens,
			OutputTokens:        row.OutputTokens,
			CostUSD:             row.CostUSD,
		}
		resp.Total.TotalRequests += row.TotalRequests
		resp.Total.InputTokens += row.InputTokens
		resp.Total.CachedInputTokens += row.CachedInputTokens
		resp.Total.OutputTokens += row.OutputTokens
		resp.Total.CostUSD += row.CostUSD
	}

	for i, row := range provRows {
		info := provInfoMap[row.ProviderID]
		resp.ByProvider[i] = ProviderUsageStat{
			ProviderID:        row.ProviderID,
			ProviderKey:       info.Key,
			ProviderName:      info.Name,
			TotalRequests:     row.TotalRequests,
			InputTokens:       row.InputTokens,
			CachedInputTokens: row.CachedInputTokens,
			OutputTokens:      row.OutputTokens,
			CostUSD:           row.CostUSD,
		}
	}

	for i, row := range modelRows {
		info := provInfoMap[row.ProviderID]
		resp.ByModel[i] = ModelUsageStat{
			ModelID:           row.ModelID,
			ProviderID:        row.ProviderID,
			ProviderKey:       info.Key,
			TotalRequests:     row.TotalRequests,
			InputTokens:       row.InputTokens,
			CachedInputTokens: row.CachedInputTokens,
			OutputTokens:      row.OutputTokens,
			CostUSD:           row.CostUSD,
		}
	}

	for i, row := range tsRows {
		resp.TimeSeries[i] = UsageTimePoint{
			Date:              row.Date,
			TotalRequests:     row.TotalRequests,
			InputTokens:       row.InputTokens,
			CachedInputTokens: row.CachedInputTokens,
			OutputTokens:      row.OutputTokens,
			CostUSD:           row.CostUSD,
		}
	}

	for i, inst := range instances {
		resp.Instances[i] = UsageInstanceInfo{ID: inst.ID, Name: inst.Name, DisplayName: inst.DisplayName}
	}
	for i, p := range providers {
		resp.Providers[i] = UsageProviderInfo{ID: p.ID, Key: p.Key, Name: p.Name}
	}

	resp.Granularity = granularity
	writeJSON(w, http.StatusOK, resp)
}

// probeProviderURL makes a validated HTTP GET request to a provider URL and returns
// the status code. The URL is validated and reconstructed from parsed components
// to ensure only http(s) schemes with valid hosts are used.
func probeProviderURL(ctx context.Context, baseURL, pathSuffix, apiType, apiKey string) (statusCode int, respBody string, err error) {
	safeURL, urlErr := utils.ValidateExternalURL(baseURL, pathSuffix)
	if urlErr != nil {
		return 0, "", urlErr
	}

	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, safeURL, nil)
	if reqErr != nil {
		return 0, "", reqErr
	}

	// Set auth and probe headers via API type abstraction
	at := llmgateway.GetAPIType(apiType)
	at.SetAuthHeader(req, apiKey)
	at.ProbeHeaders(req)

	resp, doErr := providerProbeClient.Do(req)
	if doErr != nil {
		return 0, "", doErr
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return resp.StatusCode, strings.TrimSpace(string(body)), nil
}

// TestProviderKey validates an API key by making a lightweight probe request
// to the provider's API. No saved provider is required — credentials are passed inline.
func TestProviderKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
		APIType string `json:"api_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.BaseURL == "" || body.APIKey == "" || body.APIType == "" {
		writeError(w, http.StatusBadRequest, "base_url, api_key, and api_type are required")
		return
	}

	at := llmgateway.GetAPIType(body.APIType)
	probePath := strings.TrimPrefix(at.ProbeURL(body.BaseURL), strings.TrimRight(body.BaseURL, "/"))
  
	statusCode, respBody, err := probeProviderURL(r.Context(), body.BaseURL, probePath, body.APIType, body.APIKey)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": "invalid URL or connection failed"})
		return
	}

	ok := statusCode >= 200 && statusCode < 300
	result := map[string]interface{}{"ok": ok, "status": statusCode}
	if !ok {
		result["error"] = fmt.Sprintf("HTTP %d: %s", statusCode, respBody)
	}
	writeJSON(w, http.StatusOK, result)
}

func ResetUsageLogs(w http.ResponseWriter, r *http.Request) {
	database.LogsDB.Exec("DELETE FROM llm_request_logs")
	w.WriteHeader(http.StatusNoContent)
}

type usageLogResponse struct {
	ID                uint    `json:"id"`
	InstanceID        uint    `json:"instance_id"`
	ProviderID        uint    `json:"provider_id"`
	ProviderKey       string  `json:"provider_key"`
	ModelID           string  `json:"model_id"`
	InputTokens       int     `json:"input_tokens"`
	OutputTokens      int     `json:"output_tokens"`
	CachedInputTokens int     `json:"cached_input_tokens"`
	CostUSD           float64 `json:"cost_usd"`
	StatusCode        int     `json:"status_code"`
	LatencyMs         int64   `json:"latency_ms"`
	ErrorMessage      string  `json:"error_message,omitempty"`
	RequestedAt       string  `json:"requested_at"`
}

func GetUsageLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 100
	offset := 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	query := database.LogsDB.Order("requested_at DESC").Limit(limit).Offset(offset)
	if v := q.Get("instance_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			query = query.Where("instance_id = ?", id)
		}
	}
	if v := q.Get("provider_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			query = query.Where("provider_id = ?", id)
		}
	}

	var logs []database.LLMRequestLog
	if err := query.Find(&logs).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch usage logs")
		return
	}

	// Load provider keys for enrichment (from main DB)
	providerKeys := map[uint]string{}
	var providers []database.LLMProvider
	database.DB.Find(&providers)
	for _, p := range providers {
		providerKeys[p.ID] = p.Key
	}

	result := make([]usageLogResponse, len(logs))
	for i, l := range logs {
		result[i] = usageLogResponse{
			ID:                l.ID,
			InstanceID:        l.InstanceID,
			ProviderID:        l.ProviderID,
			ProviderKey:       providerKeys[l.ProviderID],
			ModelID:           l.ModelID,
			InputTokens:       l.InputTokens,
			OutputTokens:      l.OutputTokens,
			CachedInputTokens: l.CachedInputTokens,
			CostUSD:           l.CostUSD,
			StatusCode:        l.StatusCode,
			LatencyMs:         l.LatencyMs,
			ErrorMessage:      l.ErrorMessage,
			RequestedAt:       formatTimestamp(l.RequestedAt),
		}
	}
	writeJSON(w, http.StatusOK, result)
}
