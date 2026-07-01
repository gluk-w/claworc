package middleware

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/auth"
	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// lastLoginThrottle bounds how often the CF auth path writes last_login_at, so
// a busy client doesn't trigger a DB write on every request.
const lastLoginThrottle = 5 * time.Minute

// touchLastLogin records the user's last login, throttled to avoid a write per
// request. Best-effort: errors are ignored (auth already succeeded).
func touchLastLogin(user *database.User) {
	if user.LastLoginAt != nil && time.Since(*user.LastLoginAt) < lastLoginThrottle {
		return
	}
	_ = database.TouchUserLastLogin(user.ID)
}

type contextKey string

const userContextKey contextKey = "user"

// CFAccessHeader is the request header Cloudflare Access injects carrying the
// signed identity JWT.
const CFAccessHeader = "Cf-Access-Jwt-Assertion"

// CFVerifier validates a Cloudflare Access JWT and returns the authenticated
// email. *cfaccess.Verifier satisfies this; the interface keeps RequireAuth
// testable without a live JWKS endpoint.
type CFVerifier interface {
	Verify(ctx context.Context, token string) (string, error)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// RequireAuth authenticates requests. Precedence: the AuthDisabled dev bypass,
// then Cloudflare Access (when enabled), then the session cookie. cf may be nil
// when Cloudflare Access is disabled.
func RequireAuth(store *auth.SessionStore, cf CFVerifier) func(http.Handler) http.Handler {
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

			// Cloudflare Access (Zero Trust). When enabled, identity comes from
			// the cryptographically verified JWT only — never the plaintext
			// email header — and is matched to an existing user. Verified per
			// request (stateless); no Claworc session cookie is involved.
			if config.Cfg.CFAccessEnabled && cf != nil {
				token := r.Header.Get(CFAccessHeader)
				if token == "" {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Authentication required"})
					return
				}
				email, err := cf.Verify(r.Context(), token)
				if err != nil {
					log.Printf("Cloudflare Access JWT verification failed: %v", err)
					writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Authentication required"})
					return
				}
				user, err := database.GetUserByEmail(email)
				if err != nil {
					log.Printf("Cloudflare Access: no Claworc account for verified email %q", email)
					writeJSON(w, http.StatusForbidden, map[string]string{"detail": "No Claworc account for this identity"})
					return
				}
				touchLastLogin(user)
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
