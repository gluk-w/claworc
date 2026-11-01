package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

// BrowserAuthnOps is the slice of browserauthn.Manager the handlers use.
// Wired in main.go; nil when no orchestrator (no on-demand browser) exists.
type BrowserAuthnOps interface {
	Reload(ctx context.Context, instanceID uint) error
	Stop(instanceID uint)
	RemoveCredential(ctx context.Context, instanceID uint, credentialID string)
}

// BrowserAuthn is the virtual WebAuthn authenticator manager.
var BrowserAuthn BrowserAuthnOps

type passkeyResponse struct {
	ID              uint       `json:"id"`
	Authenticator   string     `json:"authenticator"`
	RpID            string     `json:"rp_id"`
	UserName        string     `json:"user_name"`
	UserDisplayName string     `json:"user_display_name"`
	IsResident      bool       `json:"is_resident"`
	SignCount       uint32     `json:"sign_count"`
	CreatedAt       time.Time  `json:"created_at"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
}

// ListInstancePasskeys returns the passkeys stored by the instance's virtual
// authenticator. Key material is never included.
func ListInstancePasskeys(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}
	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}
	rows, err := database.ListBrowserPasskeys(uint(id))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list passkeys")
		return
	}
	out := make([]passkeyResponse, 0, len(rows))
	for _, p := range rows {
		out = append(out, passkeyResponse{
			ID:              p.ID,
			Authenticator:   p.Authenticator,
			RpID:            p.RpID,
			UserName:        p.UserName,
			UserDisplayName: p.UserDisplayName,
			IsResident:      p.IsResident,
			SignCount:       p.SignCount,
			CreatedAt:       p.CreatedAt,
			LastUsedAt:      p.LastUsedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// DeleteInstancePasskey removes a stored passkey and evicts it from every
// open tab of the running browser.
func DeleteInstancePasskey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}
	pid, err := strconv.Atoi(chi.URLParam(r, "passkeyId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid passkey ID")
		return
	}
	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}
	p, err := database.GetBrowserPasskey(uint(id), uint(pid))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeError(w, http.StatusNotFound, "Passkey not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load passkey")
		return
	}
	if err := database.DeleteBrowserPasskey(uint(id), uint(pid)); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete passkey")
		return
	}
	if BrowserAuthn != nil {
		BrowserAuthn.RemoveCredential(r.Context(), uint(id), p.CredentialID)
	}
	w.WriteHeader(http.StatusNoContent)
}
