package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gluk-w/claworc/llm-proxy/internal/database"
	"github.com/gluk-w/claworc/llm-proxy/internal/proxy"
	"github.com/go-chi/chi/v5"
)

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// RegisterToken creates a new instance token.
func RegisterToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InstanceName string `json:"instance_name"`
		Token        string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.InstanceName == "" || body.Token == "" {
		writeError(w, http.StatusBadRequest, "instance_name and token are required")
		return
	}

	// Upsert: if instance already has a token, update it
	var existing database.InstanceToken
	result := database.DB.Where("instance_name = ?", body.InstanceName).First(&existing)
	if result.Error == nil {
		database.DB.Model(&existing).Updates(map[string]interface{}{
			"token":   body.Token,
			"enabled": true,
		})
		proxy.InvalidateAllTokenCache()
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
		return
	}

	token := database.InstanceToken{
		InstanceName: body.InstanceName,
		Token:        body.Token,
		Enabled:      true,
	}
	if err := database.DB.Create(&token).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create token")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

// RevokeToken deletes an instance token.
func RevokeToken(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	result := database.DB.Where("instance_name = ?", name).Delete(&database.InstanceToken{})
	if result.RowsAffected == 0 {
		writeError(w, http.StatusNotFound, "Token not found")
		return
	}
	proxy.InvalidateAllTokenCache()
	w.WriteHeader(http.StatusNoContent)
}

// DisableToken disables a token without deleting it.
func DisableToken(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	result := database.DB.Model(&database.InstanceToken{}).Where("instance_name = ?", name).Update("enabled", false)
	if result.RowsAffected == 0 {
		writeError(w, http.StatusNotFound, "Token not found")
		return
	}
	proxy.InvalidateAllTokenCache()
	writeJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
}

// EnableToken re-enables a token.
func EnableToken(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	result := database.DB.Model(&database.InstanceToken{}).Where("instance_name = ?", name).Update("enabled", true)
	if result.RowsAffected == 0 {
		writeError(w, http.StatusNotFound, "Token not found")
		return
	}
	proxy.InvalidateAllTokenCache()
	writeJSON(w, http.StatusOK, map[string]string{"status": "enabled"})
}

// SyncKeys bulk syncs API keys from the control plane.
func SyncKeys(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Keys []struct {
			Provider string `json:"provider"`
			Scope    string `json:"scope"` // "global" or instance name
			Key      string `json:"key"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	for _, k := range body.Keys {
		if k.Key == "" {
			database.DB.Where("provider_name = ? AND scope = ?", k.Provider, k.Scope).Delete(&database.ProviderKey{})
			continue
		}

		var existing database.ProviderKey
		result := database.DB.Where("provider_name = ? AND scope = ?", k.Provider, k.Scope).First(&existing)
		if result.Error == nil {
			database.DB.Model(&existing).Update("key_value", k.Key)
		} else {
			database.DB.Create(&database.ProviderKey{
				ProviderName: k.Provider,
				Scope:        k.Scope,
				KeyValue:     k.Key,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "synced"})
}

// GetUsage returns aggregate usage data.
func GetUsage(w http.ResponseWriter, r *http.Request) {
	since := r.URL.Query().Get("since")
	until := r.URL.Query().Get("until")
	groupBy := r.URL.Query().Get("group_by")

	query := database.DB.Model(&database.UsageRecord{})

	if since != "" {
		if t, err := time.Parse("2006-01-02", since); err == nil {
			query = query.Where("created_at >= ?", t)
		}
	}
	if until != "" {
		if t, err := time.Parse("2006-01-02", until); err == nil {
			query = query.Where("created_at < ?", t.AddDate(0, 0, 1))
		}
	}

	type usageSummary struct {
		Group            string `json:"group"`
		Requests         int64  `json:"requests"`
		InputTokens      int64  `json:"input_tokens"`
		OutputTokens     int64  `json:"output_tokens"`
		EstimatedCostUSD string `json:"estimated_cost_usd"`
		CostMicro        int64  `json:"-"`
	}

	var results []usageSummary

	selectFields := "COUNT(*) as requests, COALESCE(SUM(input_tokens),0) as input_tokens, COALESCE(SUM(output_tokens),0) as output_tokens, COALESCE(SUM(estimated_cost_micro),0) as cost_micro"

	switch groupBy {
	case "instance":
		query.Select("instance_name as `group`, " + selectFields).Group("instance_name").Order("cost_micro DESC").Scan(&results)
	case "provider":
		query.Select("provider as `group`, " + selectFields).Group("provider").Order("cost_micro DESC").Scan(&results)
	case "model":
		query.Select("model as `group`, " + selectFields).Group("model").Order("cost_micro DESC").Scan(&results)
	case "day":
		query.Select("DATE(created_at) as `group`, " + selectFields).Group("DATE(created_at)").Order("`group` DESC").Scan(&results)
	default:
		var total usageSummary
		total.Group = "total"
		query.Select(selectFields).Scan(&total)
		results = []usageSummary{total}
	}

	for i := range results {
		results[i].EstimatedCostUSD = fmt.Sprintf("$%.6f", float64(results[i].CostMicro)/1_000_000.0)
	}

	writeJSON(w, http.StatusOK, results)
}

// GetInstanceUsage returns per-instance usage data.
func GetInstanceUsage(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	since := r.URL.Query().Get("since")
	until := r.URL.Query().Get("until")

	query := database.DB.Model(&database.UsageRecord{}).Where("instance_name = ?", name)

	if since != "" {
		if t, err := time.Parse("2006-01-02", since); err == nil {
			query = query.Where("created_at >= ?", t)
		}
	}
	if until != "" {
		if t, err := time.Parse("2006-01-02", until); err == nil {
			query = query.Where("created_at < ?", t.AddDate(0, 0, 1))
		}
	}

	type usageSummary struct {
		Provider         string `json:"provider"`
		Model            string `json:"model"`
		Requests         int64  `json:"requests"`
		InputTokens      int64  `json:"input_tokens"`
		OutputTokens     int64  `json:"output_tokens"`
		EstimatedCostUSD string `json:"estimated_cost_usd"`
		CostMicro        int64  `json:"-"`
	}

	var results []usageSummary
	query.Select("provider, model, COUNT(*) as requests, COALESCE(SUM(input_tokens),0) as input_tokens, COALESCE(SUM(output_tokens),0) as output_tokens, COALESCE(SUM(estimated_cost_micro),0) as cost_micro").
		Group("provider, model").
		Order("cost_micro DESC").
		Scan(&results)

	for i := range results {
		results[i].EstimatedCostUSD = fmt.Sprintf("$%.6f", float64(results[i].CostMicro)/1_000_000.0)
	}

	writeJSON(w, http.StatusOK, results)
}

// GetLimits returns budget and rate limits for an instance.
func GetLimits(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	var budget database.BudgetLimit
	database.DB.Where("instance_name = ?", name).First(&budget)

	var rateLimits []database.RateLimit
	database.DB.Where("instance_name = ?", name).Find(&rateLimits)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"budget":      budget,
		"rate_limits": rateLimits,
	})
}

// SetLimits sets budget and rate limits for an instance.
func SetLimits(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	var body struct {
		Budget *struct {
			LimitMicro     int64   `json:"limit_micro"`
			PeriodType     string  `json:"period_type"`
			AlertThreshold float64 `json:"alert_threshold"`
			HardLimit      bool    `json:"hard_limit"`
		} `json:"budget"`
		RateLimits []struct {
			Provider          string `json:"provider"`
			RequestsPerMinute int    `json:"requests_per_minute"`
			TokensPerMinute   int    `json:"tokens_per_minute"`
		} `json:"rate_limits"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if body.Budget != nil {
		periodType := body.Budget.PeriodType
		if periodType == "" {
			periodType = "monthly"
		}
		alertThreshold := body.Budget.AlertThreshold
		if alertThreshold == 0 {
			alertThreshold = 0.8
		}

		var existing database.BudgetLimit
		result := database.DB.Where("instance_name = ?", name).First(&existing)
		if result.Error == nil {
			database.DB.Model(&existing).Updates(map[string]interface{}{
				"limit_micro":     body.Budget.LimitMicro,
				"period_type":     periodType,
				"alert_threshold": alertThreshold,
				"hard_limit":      body.Budget.HardLimit,
			})
		} else {
			database.DB.Create(&database.BudgetLimit{
				InstanceName:   name,
				LimitMicro:     body.Budget.LimitMicro,
				PeriodType:     periodType,
				AlertThreshold: alertThreshold,
				HardLimit:      body.Budget.HardLimit,
			})
		}
		proxy.InvalidateBudgetCache(name)
	}

	if body.RateLimits != nil {
		database.DB.Where("instance_name = ?", name).Delete(&database.RateLimit{})
		for _, rl := range body.RateLimits {
			provider := rl.Provider
			if provider == "" {
				provider = "*"
			}
			database.DB.Create(&database.RateLimit{
				InstanceName:      name,
				Provider:          provider,
				RequestsPerMinute: rl.RequestsPerMinute,
				TokensPerMinute:   rl.TokensPerMinute,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// HealthCheck returns proxy health status.
func HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "healthy",
	})
}
