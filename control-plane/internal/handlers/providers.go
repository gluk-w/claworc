package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/go-chi/chi/v5"
)

var providerKeyRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]*[a-z0-9]$|^[a-z0-9]$`)

type providerRequest struct {
	Key      string `json:"key"`
	Provider string `json:"provider"` // catalog provider key, optional
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
}

func ListProviders(w http.ResponseWriter, r *http.Request) {
	var providers []database.LLMProvider
	if err := database.DB.Order("id ASC").Find(&providers).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list providers")
		return
	}
	if providers == nil {
		providers = []database.LLMProvider{}
	}
	writeJSON(w, http.StatusOK, providers)
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

	p := database.LLMProvider{Key: body.Key, Provider: body.Provider, Name: body.Name, BaseURL: body.BaseURL}
	if err := database.DB.Create(&p).Error; err != nil {
		writeError(w, http.StatusConflict, "Provider key already exists")
		return
	}
	writeJSON(w, http.StatusCreated, p)
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
	// Key is immutable once created
	if err := database.DB.Save(&p).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update provider")
		return
	}
	writeJSON(w, http.StatusOK, p)
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
