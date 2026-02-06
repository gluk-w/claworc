package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/glukw/claworc/internal/auth"
	"github.com/glukw/claworc/internal/database"
	"github.com/glukw/claworc/internal/middleware"
	"github.com/go-webauthn/webauthn/protocol"
)

// SessionStore is set from main.go during init.
var SessionStore *auth.SessionStore

func setSessionCookie(w http.ResponseWriter, r *http.Request, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(auth.SessionDuration.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if body.Username == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "Username and password are required")
		return
	}

	user, err := database.GetUserByUsername(body.Username)
	if err != nil || !auth.CheckPassword(body.Password, user.PasswordHash) {
		writeError(w, http.StatusUnauthorized, "Invalid username or password")
		return
	}

	sessionID, err := SessionStore.Create(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create session")
		return
	}

	setSessionCookie(w, r, sessionID)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":       user.ID,
		"username": user.Username,
		"role":     user.Role,
	})
}

func Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(auth.SessionCookie)
	if err == nil {
		SessionStore.Delete(cookie.Value)
	}
	clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func GetCurrentUser(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":       user.ID,
		"username": user.Username,
		"role":     user.Role,
	})
}

func SetupRequired(w http.ResponseWriter, r *http.Request) {
	count, err := database.UserCount()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"setup_required": count == 0})
}

func SetupCreateAdmin(w http.ResponseWriter, r *http.Request) {
	count, err := database.UserCount()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Database error")
		return
	}
	if count > 0 {
		writeError(w, http.StatusConflict, "Setup already completed")
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if body.Username == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "Username and password are required")
		return
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to hash password")
		return
	}

	user := &database.User{
		Username:     body.Username,
		PasswordHash: hash,
		Role:         "admin",
	}
	if err := database.CreateUser(user); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create admin user")
		return
	}

	sessionID, err := SessionStore.Create(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create session")
		return
	}

	setSessionCookie(w, r, sessionID)
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":       user.ID,
		"username": user.Username,
		"role":     user.Role,
	})
}

// WebAuthn handlers

func WebAuthnRegisterBegin(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	wau, err := auth.LoadWebAuthnUser(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load user")
		return
	}

	options, session, err := auth.WebAuthn.BeginRegistration(wau,
		func(cco *protocol.PublicKeyCredentialCreationOptions) {
			cco.AuthenticatorSelection.ResidentKey = protocol.ResidentKeyRequirementPreferred
			cco.AuthenticatorSelection.UserVerification = protocol.VerificationPreferred
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("WebAuthn error: %v", err))
		return
	}

	auth.StoreChallenge(user.ID, session)
	writeJSON(w, http.StatusOK, options)
}

func WebAuthnRegisterFinish(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	wau, err := auth.LoadWebAuthnUser(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load user")
		return
	}

	session, ok := auth.GetChallenge(user.ID)
	if !ok {
		writeError(w, http.StatusBadRequest, "No pending registration challenge")
		return
	}

	cred, err := auth.WebAuthn.FinishRegistration(wau, *session, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Registration failed: %v", err))
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		name = fmt.Sprintf("Passkey %s", time.Now().Format("2006-01-02"))
	}

	if err := auth.SaveCredential(user.ID, name, cred); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save credential")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func WebAuthnLoginBegin(w http.ResponseWriter, r *http.Request) {
	options, session, err := auth.WebAuthn.BeginDiscoverableLogin(
		func(opts *protocol.PublicKeyCredentialRequestOptions) {
			opts.UserVerification = protocol.VerificationPreferred
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("WebAuthn error: %v", err))
		return
	}

	// Store with userID=0 for discoverable login
	auth.StoreChallenge(0, session)
	writeJSON(w, http.StatusOK, options)
}

func WebAuthnLoginFinish(w http.ResponseWriter, r *http.Request) {
	session, ok := auth.GetChallenge(0)
	if !ok {
		writeError(w, http.StatusBadRequest, "No pending login challenge")
		return
	}

	cred, err := auth.WebAuthn.FinishDiscoverableLogin(
		auth.DiscoverableLogin,
		*session,
		r,
	)
	if err != nil {
		writeError(w, http.StatusUnauthorized, fmt.Sprintf("Login failed: %v", err))
		return
	}

	// Update sign count
	database.UpdateCredentialSignCount(string(cred.ID), cred.Authenticator.SignCount)

	// Find the user from the credential
	var dbCreds []database.WebAuthnCredential
	database.DB.Where("id = ?", string(cred.ID)).Find(&dbCreds)
	if len(dbCreds) == 0 {
		writeError(w, http.StatusUnauthorized, "Credential not found")
		return
	}

	user, err := database.GetUserByID(dbCreds[0].UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "User not found")
		return
	}

	sessionID, err := SessionStore.Create(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create session")
		return
	}

	setSessionCookie(w, r, sessionID)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":       user.ID,
		"username": user.Username,
		"role":     user.Role,
	})
}

func ListWebAuthnCredentials(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	creds, err := database.GetWebAuthnCredentials(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list credentials")
		return
	}

	type credResponse struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		CreatedAt string `json:"created_at"`
	}
	result := make([]credResponse, 0, len(creds))
	for _, c := range creds {
		result = append(result, credResponse{
			ID:        c.ID,
			Name:      c.Name,
			CreatedAt: formatTimestamp(c.CreatedAt),
		})
	}

	writeJSON(w, http.StatusOK, result)
}

func DeleteWebAuthnCredential(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	// Get credId from URL path - chi doesn't decode this automatically
	credID := r.PathValue("credId")
	if credID == "" {
		// Fallback: extract from path manually
		path := r.URL.Path
		parts := splitPath(path)
		if len(parts) > 0 {
			credID = parts[len(parts)-1]
		}
	}

	if credID == "" {
		writeError(w, http.StatusBadRequest, "Credential ID required")
		return
	}

	if err := database.DeleteWebAuthnCredential(credID, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete credential")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func splitPath(path string) []string {
	var parts []string
	for _, p := range split(path, '/') {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func split(s string, sep byte) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			if i > start {
				result = append(result, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}
