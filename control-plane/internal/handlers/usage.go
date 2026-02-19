package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/llmproxy"
	"github.com/go-chi/chi/v5"
)

// GetUsage returns aggregate usage from the proxy.
func GetUsage(w http.ResponseWriter, r *http.Request) {
	if !config.Cfg.ProxyEnabled {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}

	since := r.URL.Query().Get("since")
	until := r.URL.Query().Get("until")
	groupBy := r.URL.Query().Get("group_by")

	results, err := llmproxy.GetUsage(since, until, groupBy)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Failed to fetch usage from proxy")
		return
	}

	writeJSON(w, http.StatusOK, results)
}

// GetInstanceUsage returns per-instance usage from the proxy.
func GetInstanceUsage(w http.ResponseWriter, r *http.Request) {
	if !config.Cfg.ProxyEnabled {
		writeJSON(w, http.StatusOK, []interface{}{})
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

	since := r.URL.Query().Get("since")
	until := r.URL.Query().Get("until")

	results, err := llmproxy.GetInstanceUsage(inst.Name, since, until)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Failed to fetch instance usage from proxy")
		return
	}

	writeJSON(w, http.StatusOK, results)
}

// SetInstanceBudget sets budget limits for an instance via the proxy.
func SetInstanceBudget(w http.ResponseWriter, r *http.Request) {
	if !config.Cfg.ProxyEnabled {
		writeError(w, http.StatusServiceUnavailable, "Proxy not enabled")
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

	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Forward to proxy — wrapping in the expected format
	var budgetReq struct {
		LimitMicro     int64   `json:"limit_micro"`
		PeriodType     string  `json:"period_type"`
		AlertThreshold float64 `json:"alert_threshold"`
		HardLimit      bool    `json:"hard_limit"`
	}
	if err := json.Unmarshal(body, &budgetReq); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := llmproxy.SetLimits(inst.Name, map[string]interface{}{
		"budget": budgetReq,
	}); err != nil {
		writeError(w, http.StatusBadGateway, "Failed to set budget at proxy")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// SetInstanceRateLimit sets rate limits for an instance via the proxy.
func SetInstanceRateLimit(w http.ResponseWriter, r *http.Request) {
	if !config.Cfg.ProxyEnabled {
		writeError(w, http.StatusServiceUnavailable, "Proxy not enabled")
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

	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	var rateLimitReq []struct {
		Provider          string `json:"provider"`
		RequestsPerMinute int    `json:"requests_per_minute"`
		TokensPerMinute   int    `json:"tokens_per_minute"`
	}
	if err := json.Unmarshal(body, &rateLimitReq); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := llmproxy.SetLimits(inst.Name, map[string]interface{}{
		"rate_limits": rateLimitReq,
	}); err != nil {
		writeError(w, http.StatusBadGateway, "Failed to set rate limit at proxy")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// GetInstanceLimits returns limits for an instance from the proxy.
func GetInstanceLimits(w http.ResponseWriter, r *http.Request) {
	if !config.Cfg.ProxyEnabled {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"budget":      nil,
			"rate_limits": []interface{}{},
		})
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

	limits, err := llmproxy.GetLimits(inst.Name)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Failed to fetch limits from proxy")
		return
	}

	writeJSON(w, http.StatusOK, limits)
}

// GetProxyStatus returns whether the proxy is enabled.
func GetProxyStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"proxy_enabled": config.Cfg.ProxyEnabled,
	})
}
