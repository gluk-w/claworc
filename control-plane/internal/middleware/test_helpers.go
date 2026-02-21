package middleware

import (
	"context"
	"net/http"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// WithUserForTest attaches a User to the request context for testing.
func WithUserForTest(r *http.Request, user *database.User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userContextKey, user))
}
