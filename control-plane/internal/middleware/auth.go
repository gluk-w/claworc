package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gluk-w/claworc/control-plane/internal/auth"
	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
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

// RequireInstanceCreator allows admins or users who manage at least one
// team. Per-team authorization (creating an instance in a specific team)
// is enforced inside the handler.
func RequireInstanceCreator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUser(r)
		if user == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Authentication required"})
			return
		}
		if user.Role == "admin" {
			next.ServeHTTP(w, r)
			return
		}
		managed, _ := database.UserManagedTeamIDs(user.ID)
		if len(managed) > 0 {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"detail": "Instance creation permission required"})
	})
}

// CanMutateInstance reports whether the user is allowed to start, stop,
// restart, delete or otherwise change the lifecycle of an instance. Admins
// always; otherwise the user must be a manager of the instance's team.
func CanMutateInstance(r *http.Request, instanceID uint) bool {
	user := GetUser(r)
	if user == nil {
		return false
	}
	if user.Role == "admin" {
		return true
	}
	inst, err := database.GetInstance(instanceID)
	if err != nil {
		return false
	}
	return database.IsTeamManager(user.ID, inst.TeamID)
}

// CanManageTeam reports whether the user can manage the given team:
// admins always; otherwise the user must hold the manager role on that team.
func CanManageTeam(r *http.Request, teamID uint) bool {
	user := GetUser(r)
	if user == nil {
		return false
	}
	if user.Role == "admin" {
		return true
	}
	return database.IsTeamManager(user.ID, teamID)
}

func GetUser(r *http.Request) *database.User {
	user, _ := r.Context().Value(userContextKey).(*database.User)
	return user
}

// WithUser returns a new context with the given user set. Useful for testing.
func WithUser(ctx context.Context, user *database.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

func CanAccessInstance(r *http.Request, instanceID uint) bool {
	user := GetUser(r)
	if user == nil {
		return false
	}
	if user.Role == "admin" {
		return true
	}
	// Look up the instance's team. Managers of that team have access to
	// all team instances. Regular team members still need an explicit
	// UserInstance grant.
	inst, err := database.GetInstance(instanceID)
	if err == nil {
		role := database.GetTeamRole(user.ID, inst.TeamID)
		if role == database.TeamRoleManager {
			return true
		}
		if role == database.TeamRoleUser && database.IsUserAssignedToInstance(user.ID, instanceID) {
			return true
		}
		return false
	}
	return database.IsUserAssignedToInstance(user.ID, instanceID)
}
