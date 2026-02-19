package proxy

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gluk-w/claworc/llm-proxy/internal/database"
)

// tokenCache caches instance tokens for fast lookup.
// Key: token string, Value: cachedToken
var tokenCache sync.Map

type cachedToken struct {
	InstanceName string
	Enabled      bool
	CachedAt     time.Time
}

const tokenCacheTTL = 30 * time.Second

func lookupToken(token string) (string, bool) {
	if cached, ok := tokenCache.Load(token); ok {
		ct := cached.(cachedToken)
		if time.Since(ct.CachedAt) < tokenCacheTTL {
			if !ct.Enabled {
				return "", false
			}
			return ct.InstanceName, true
		}
		tokenCache.Delete(token)
	}

	var it database.InstanceToken
	if err := database.DB.Where("token = ?", token).First(&it).Error; err != nil {
		return "", false
	}

	tokenCache.Store(token, cachedToken{
		InstanceName: it.InstanceName,
		Enabled:      it.Enabled,
		CachedAt:     time.Now(),
	})

	if !it.Enabled {
		return "", false
	}
	return it.InstanceName, true
}

// InvalidateTokenCache removes a token from the cache.
func InvalidateTokenCache(token string) {
	tokenCache.Delete(token)
}

// InvalidateAllTokenCache clears the entire token cache.
func InvalidateAllTokenCache() {
	tokenCache.Range(func(key, _ any) bool {
		tokenCache.Delete(key)
		return true
	})
}

// AuthMiddleware validates the proxy token and injects the instance name into the request context.
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			http.Error(w, `{"error":"missing_token","message":"API key is required"}`, http.StatusUnauthorized)
			return
		}

		instanceName, ok := lookupToken(token)
		if !ok {
			http.Error(w, `{"error":"invalid_token","message":"Invalid or disabled API key"}`, http.StatusUnauthorized)
			return
		}

		r = r.WithContext(withInstanceName(r.Context(), instanceName))
		next.ServeHTTP(w, r)
	})
}

func extractToken(r *http.Request) string {
	// Try Authorization header (Bearer token)
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}

	// Try x-api-key header (Anthropic style)
	if key := r.Header.Get("x-api-key"); key != "" {
		return key
	}

	// Try x-goog-api-key header (Google style)
	if key := r.Header.Get("x-goog-api-key"); key != "" {
		return key
	}

	return ""
}
