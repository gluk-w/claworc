package handlers

import (
	"net/http"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// GetProviderAnalytics handles GET /api/v1/analytics/providers.
// Returns usage stats per provider for the last 7 days.
func GetProviderAnalytics(w http.ResponseWriter, r *http.Request) {
	since := time.Now().AddDate(0, 0, -7)

	stats, err := database.GetProviderStats(since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to retrieve analytics")
		return
	}

	// Build a map keyed by provider for easy frontend consumption
	result := make(map[string]interface{})
	byProvider := make(map[string]database.ProviderStats)
	for _, s := range stats {
		byProvider[s.Provider] = s
	}
	result["providers"] = byProvider
	result["period_days"] = 7
	result["since"] = since.UTC().Format(time.RFC3339)

	writeJSON(w, http.StatusOK, result)
}
