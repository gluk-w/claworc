package proxy

import (
	"net/http"
	"sync"
	"time"

	"github.com/gluk-w/claworc/llm-proxy/internal/database"
)

type rateLimitWindow struct {
	mu       sync.Mutex
	requests []time.Time
}

func (w *rateLimitWindow) count(window time.Duration) int {
	w.mu.Lock()
	defer w.mu.Unlock()

	cutoff := time.Now().Add(-window)
	// Remove expired entries
	i := 0
	for i < len(w.requests) && w.requests[i].Before(cutoff) {
		i++
	}
	w.requests = w.requests[i:]
	return len(w.requests)
}

func (w *rateLimitWindow) add() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.requests = append(w.requests, time.Now())
}

// key: "instance:provider"
var rateLimitWindows sync.Map

func getWindow(key string) *rateLimitWindow {
	val, _ := rateLimitWindows.LoadOrStore(key, &rateLimitWindow{})
	return val.(*rateLimitWindow)
}

// RateLimitMiddleware enforces per-instance rate limits.
func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		instanceName := GetInstanceName(r.Context())
		if instanceName == "" {
			next.ServeHTTP(w, r)
			return
		}

		provider := r.Context().Value(providerKey)
		providerName := ""
		if p, ok := provider.(string); ok {
			providerName = p
		}

		// Check rate limits for this instance
		var limits []database.RateLimit
		database.DB.Where("instance_name = ? AND (provider = ? OR provider = '*')", instanceName, providerName).Find(&limits)

		for _, limit := range limits {
			if limit.RequestsPerMinute > 0 {
				key := instanceName + ":" + limit.Provider
				window := getWindow(key)
				if window.count(time.Minute) >= limit.RequestsPerMinute {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Retry-After", "60")
					w.WriteHeader(http.StatusTooManyRequests)
					w.Write([]byte(`{"error":"rate_limit_exceeded","message":"Rate limit exceeded"}`))
					return
				}
			}
		}

		// Record the request
		for _, limit := range limits {
			if limit.RequestsPerMinute > 0 {
				key := instanceName + ":" + limit.Provider
				window := getWindow(key)
				window.add()
			}
		}

		next.ServeHTTP(w, r)
	})
}

type providerKeyType string

const providerKey providerKeyType = "provider"
