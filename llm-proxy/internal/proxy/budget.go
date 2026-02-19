package proxy

import (
	"net/http"
	"sync"
	"time"

	"github.com/gluk-w/claworc/llm-proxy/internal/database"
)

type budgetEntry struct {
	SpentMicro int64
	LimitMicro int64
	HardLimit  bool
	PeriodType string
	FetchedAt  time.Time
}

var budgetCache sync.Map

const budgetCacheTTL = 10 * time.Second

func getBudgetInfo(instanceName string) *budgetEntry {
	if cached, ok := budgetCache.Load(instanceName); ok {
		be := cached.(*budgetEntry)
		if time.Since(be.FetchedAt) < budgetCacheTTL {
			return be
		}
	}

	var limit database.BudgetLimit
	if err := database.DB.Where("instance_name = ?", instanceName).First(&limit).Error; err != nil {
		return nil
	}

	if limit.LimitMicro == 0 {
		return nil
	}

	// Calculate period start
	now := time.Now().UTC()
	var periodStart time.Time
	switch limit.PeriodType {
	case "daily":
		periodStart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	default: // monthly
		periodStart = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}

	var spent int64
	database.DB.Model(&database.UsageRecord{}).
		Where("instance_name = ? AND created_at >= ?", instanceName, periodStart).
		Select("COALESCE(SUM(estimated_cost_micro), 0)").
		Scan(&spent)

	entry := &budgetEntry{
		SpentMicro: spent,
		LimitMicro: limit.LimitMicro,
		HardLimit:  limit.HardLimit,
		PeriodType: limit.PeriodType,
		FetchedAt:  time.Now(),
	}
	budgetCache.Store(instanceName, entry)
	return entry
}

// BudgetMiddleware checks if the instance has exceeded its budget.
func BudgetMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		instanceName := GetInstanceName(r.Context())
		if instanceName == "" {
			next.ServeHTTP(w, r)
			return
		}

		entry := getBudgetInfo(instanceName)
		if entry != nil && entry.HardLimit && entry.SpentMicro >= entry.LimitMicro {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"budget_exceeded","message":"Budget limit has been reached"}`))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// InvalidateBudgetCache removes a budget entry from the cache.
func InvalidateBudgetCache(instanceName string) {
	budgetCache.Delete(instanceName)
}
