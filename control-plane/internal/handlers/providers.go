package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/llmgateway"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
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
)

func proxyCatalog(w http.ResponseWriter, path string) {
	catalogCacheMu.RLock()
	entry := catalogCache[path]
	catalogCacheMu.RUnlock()

	if entry == nil || time.Now().After(entry.expiresAt) {
		resp, err := catalogHTTPClient.Get(catalogBaseURL + path)
		if err != nil {
			log.Printf("catalog proxy: fetch %s: %v", path, err)
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

// GetCatalogProviderDetail proxies GET /providers/{key}/ from the catalog API.
func GetCatalogProviderDetail(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	proxyCatalog(w, "/"+key+"/")
}

// getCatalogProviderModels returns ProviderModel entries for a catalog provider,
// using the in-process cache when available and fetching otherwise.
// Returns nil on error.
var getCatalogModels = func(catalogKey string) []database.ProviderModel {
	if catalogKey == "" {
		return nil
	}
	path := "/" + catalogKey + "/"

	catalogCacheMu.RLock()
	entry := catalogCache[path]
	catalogCacheMu.RUnlock()

	if entry == nil || time.Now().After(entry.expiresAt) {
		resp, err := catalogHTTPClient.Get(catalogBaseURL + path)
		if err != nil {
			log.Printf("getCatalogModels: fetch %s: %v", catalogKey, err)
			return nil
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != http.StatusOK {
			return nil
		}
		entry = &catalogCacheEntry{body: body, expiresAt: time.Now().Add(time.Hour)}
		catalogCacheMu.Lock()
		catalogCache[path] = entry
		catalogCacheMu.Unlock()
	}

	var detail struct {
		Models []struct {
			ModelID       string `json:"model_id"`
			ModelName     string `json:"model_name"`
			Reasoning     bool   `json:"reasoning"`
			ContextWindow *int   `json:"context_window"`
			MaxTokens     *int   `json:"max_tokens"`
		} `json:"models"`
	}
	if err := json.Unmarshal(entry.body, &detail); err != nil {
		return nil
	}
	result := make([]database.ProviderModel, len(detail.Models))
	for i, m := range detail.Models {
		result[i] = database.ProviderModel{
			ID:            m.ModelID,
			Name:          m.ModelName,
			Reasoning:     m.Reasoning,
			ContextWindow: m.ContextWindow,
			MaxTokens:     m.MaxTokens,
		}
	}
	return result
}

var providerKeyRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]*[a-z0-9]$|^[a-z0-9]$`)

type providerRequest struct {
	Key      string                   `json:"key"`
	Provider string                   `json:"provider"` // catalog provider key, optional
	Name     string                   `json:"name"`
	BaseURL  string                   `json:"base_url"`
	APIType  string                   `json:"api_type"`
	Models   []database.ProviderModel `json:"models"`
}

type providerResp struct {
	ID        uint                     `json:"id"`
	Key       string                   `json:"key"`
	Provider  string                   `json:"provider"`
	Name      string                   `json:"name"`
	BaseURL   string                   `json:"base_url"`
	APIType   string                   `json:"api_type"`
	Models    []database.ProviderModel `json:"models"`
	CreatedAt string                   `json:"created_at"`
	UpdatedAt string                   `json:"updated_at"`
}

func toProviderResp(p database.LLMProvider) providerResp {
	return providerResp{
		ID:        p.ID,
		Key:       p.Key,
		Provider:  p.Provider,
		Name:      p.Name,
		BaseURL:   p.BaseURL,
		APIType:   p.APIType,
		Models:    database.ParseProviderModels(p.Models),
		CreatedAt: formatTimestamp(p.CreatedAt),
		UpdatedAt: formatTimestamp(p.UpdatedAt),
	}
}

func ListProviders(w http.ResponseWriter, r *http.Request) {
	var providers []database.LLMProvider
	if err := database.DB.Order("id ASC").Find(&providers).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list providers")
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

	apiType := body.APIType
	if apiType == "" {
		apiType = "openai-completions"
	}
	modelsJSON := []byte("[]")
	if body.Models != nil {
		modelsJSON, _ = json.Marshal(body.Models)
	}
	p := database.LLMProvider{
		Key:      body.Key,
		Provider: body.Provider,
		Name:     body.Name,
		BaseURL:  body.BaseURL,
		APIType:  apiType,
		Models:   string(modelsJSON),
	}
	if err := database.DB.Create(&p).Error; err != nil {
		writeError(w, http.StatusConflict, "Provider key already exists")
		return
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
		llmgateway.EnsureKeysForInstance(inst.ID, ids)
		database.DB.First(&inst, inst.ID)
		models := resolveInstanceModels(inst)
		gatewayProviders := resolveGatewayProviders(inst)
		instID := inst.ID
		instName := inst.Name
		go func() {
			bgCtx := context.Background()
			sshClient, err := SSHMgr.WaitForSSH(bgCtx, instID, 30*time.Second)
			if err != nil {
				log.Printf("Failed to get SSH connection for instance %d during provider update: %v", instID, err)
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

	// Force-refresh the catalog cache by clearing the entry first
	path := "/" + p.Provider + "/"
	catalogCacheMu.Lock()
	delete(catalogCache, path)
	catalogCacheMu.Unlock()

	models := getCatalogModels(p.Provider)
	if models == nil {
		writeError(w, http.StatusBadGateway, "Failed to fetch catalog models")
		return
	}

	modelsJSON, _ := json.Marshal(models)
	p.Models = string(modelsJSON)
	if err := database.DB.Save(&p).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update provider models")
		return
	}
	pushProviderUpdateToInstances(uint(id))
	writeJSON(w, http.StatusOK, toProviderResp(p))
}

func SyncAllProviderModels(w http.ResponseWriter, r *http.Request) {
	var providers []database.LLMProvider
	database.DB.Order("id ASC").Find(&providers)

	var result []providerResp
	for _, p := range providers {
		if p.Provider == "" {
			result = append(result, toProviderResp(p))
			continue
		}
		path := "/" + p.Provider + "/"
		catalogCacheMu.Lock()
		delete(catalogCache, path)
		catalogCacheMu.Unlock()

		models := getCatalogModels(p.Provider)
		if models == nil {
			result = append(result, toProviderResp(p))
			continue
		}
		modelsJSON, _ := json.Marshal(models)
		p.Models = string(modelsJSON)
		if err := database.DB.Save(&p).Error; err == nil {
			pushProviderUpdateToInstances(p.ID)
		}
		result = append(result, toProviderResp(p))
	}
	if result == nil {
		result = []providerResp{}
	}
	writeJSON(w, http.StatusOK, result)
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

	// Cascade-delete all gateway keys for this provider
	database.DB.Where("provider_id = ?", id).Delete(&database.LLMGatewayKey{})
	database.DB.Delete(&p)
	w.WriteHeader(http.StatusNoContent)
}

type usageLogResponse struct {
	ID           uint   `json:"id"`
	InstanceID   uint   `json:"instance_id"`
	ProviderID   uint   `json:"provider_id"`
	ProviderKey  string `json:"provider_key"`
	ModelID      string `json:"model_id"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	StatusCode   int    `json:"status_code"`
	LatencyMs    int64  `json:"latency_ms"`
	ErrorMessage string `json:"error_message,omitempty"`
	RequestedAt  string `json:"requested_at"`
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

	query := database.DB.Order("requested_at DESC").Limit(limit).Offset(offset)
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

	// Load provider keys for enrichment
	providerKeys := map[uint]string{}
	var providers []database.LLMProvider
	database.DB.Find(&providers)
	for _, p := range providers {
		providerKeys[p.ID] = p.Key
	}

	result := make([]usageLogResponse, len(logs))
	for i, l := range logs {
		result[i] = usageLogResponse{
			ID:           l.ID,
			InstanceID:   l.InstanceID,
			ProviderID:   l.ProviderID,
			ProviderKey:  providerKeys[l.ProviderID],
			ModelID:      l.ModelID,
			InputTokens:  l.InputTokens,
			OutputTokens: l.OutputTokens,
			StatusCode:   l.StatusCode,
			LatencyMs:    l.LatencyMs,
			ErrorMessage: l.ErrorMessage,
			RequestedAt:  formatTimestamp(l.RequestedAt),
		}
	}
	writeJSON(w, http.StatusOK, result)
}
