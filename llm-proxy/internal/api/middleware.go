package api

import (
	"net/http"
	"strings"

	"github.com/gluk-w/claworc/llm-proxy/internal/config"
)

// AdminAuth middleware validates the shared admin secret.
func AdminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if config.Cfg.AdminSecret == "" {
			http.Error(w, `{"error":"not_configured","message":"Admin secret not configured"}`, http.StatusServiceUnavailable)
			return
		}

		auth := r.Header.Get("Authorization")
		token := strings.TrimPrefix(auth, "Bearer ")
		if token == "" || token == auth {
			http.Error(w, `{"error":"unauthorized","message":"Missing admin token"}`, http.StatusUnauthorized)
			return
		}

		if token != config.Cfg.AdminSecret {
			http.Error(w, `{"error":"forbidden","message":"Invalid admin token"}`, http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}
