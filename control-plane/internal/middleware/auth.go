package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/glukw/claworc/internal/auth"
	"github.com/glukw/claworc/internal/config"
	"github.com/glukw/claworc/internal/database"
)

type contextKey string

const userContextKey contextKey = "user"

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func RequireAuth(store *auth.SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if config.Cfg.AuthDisabled {
				user, err := database.GetFirstAdmin()
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": "No admin user found"})
					return
				}
				ctx := context.WithValue(r.Context(), userContextKey, user)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			cookie, err := r.Cookie(auth.SessionCookie)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Authentication required"})
				return
			}

			userID, ok := store.Get(cookie.Value)
			if !ok {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Authentication required"})
				return
			}

			user, err := database.GetUserByID(userID)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Authentication required"})
				return
			}

			ctx := context.WithValue(r.Context(), userContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUser(r)
		if user == nil || user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"detail": "Admin access required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func GetUser(r *http.Request) *database.User {
	user, _ := r.Context().Value(userContextKey).(*database.User)
	return user
}

func CanAccessInstance(r *http.Request, instanceID uint) bool {
	user := GetUser(r)
	if user == nil {
		return false
	}
	if user.Role == "admin" {
		return true
	}
	return database.IsUserAssignedToInstance(user.ID, instanceID)
}
