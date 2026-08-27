// user_ssh_keys.go manages per-user SSH public keys for the inbound SSH
// gateway. Keys can be generated server-side (the private key is returned
// exactly once and never stored) or uploaded by the user.

package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

type userSSHKeyResponse struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   string `json:"created_at"`
	LastUsedAt  string `json:"last_used_at,omitempty"`
}

func toUserSSHKeyResponse(k database.UserSSHKey) userSSHKeyResponse {
	resp := userSSHKeyResponse{
		ID:          k.ID,
		Name:        k.Name,
		Fingerprint: k.Fingerprint,
		CreatedAt:   formatTimestamp(k.CreatedAt),
	}
	if k.LastUsedAt != nil {
		resp.LastUsedAt = formatTimestamp(*k.LastUsedAt)
	}
	return resp
}

// GenerateUserSSHKey creates an ED25519 key pair for the current user,
// stores the public key, and returns the private key once.
func GenerateUserSSHKey(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&body) // body is optional

	pubKey, privKeyPEM, err := sshproxy.GenerateOpenSSHKeyPair()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate key pair")
		return
	}

	key, err := storeUserSSHKey(user.ID, body.Name, string(pubKey))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to store key")
		return
	}

	auditKeyEvent(user.Username, "generated", key)
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"key":         toUserSSHKeyResponse(*key),
		"private_key": string(privKeyPEM),
	})
}

// UploadUserSSHKey registers a user-provided public key.
func UploadUserSSHKey(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	var body struct {
		Name      string `json:"name"`
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.PublicKey) == "" {
		writeError(w, http.StatusBadRequest, "public_key is required")
		return
	}

	key, err := storeUserSSHKey(user.ID, body.Name, body.PublicKey)
	if err != nil {
		if errors.Is(err, errInvalidPublicKey) {
			writeError(w, http.StatusBadRequest, "Invalid public key (expected OpenSSH authorized_keys format)")
			return
		}
		if isDuplicateKeyError(err) {
			writeError(w, http.StatusConflict, "This key is already registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to store key")
		return
	}

	auditKeyEvent(user.Username, "uploaded", key)
	writeJSON(w, http.StatusCreated, map[string]interface{}{"key": toUserSSHKeyResponse(*key)})
}

var errInvalidPublicKey = errors.New("invalid public key")

func storeUserSSHKey(userID uint, name, publicKey string) (*database.UserSSHKey, error) {
	parsed, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(publicKey))
	if err != nil {
		return nil, errInvalidPublicKey
	}
	if name == "" {
		name = comment
	}
	key := &database.UserSSHKey{
		UserID:      userID,
		Name:        name,
		PublicKey:   strings.TrimSpace(string(ssh.MarshalAuthorizedKey(parsed))),
		Fingerprint: ssh.FingerprintSHA256(parsed),
	}
	if err := database.CreateUserSSHKey(key); err != nil {
		return nil, err
	}
	return key, nil
}

func isDuplicateKeyError(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate")
}

// ListUserSSHKeys returns the current user's registered SSH keys.
func ListUserSSHKeys(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	keys, err := database.ListUserSSHKeys(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list keys")
		return
	}
	result := make([]userSSHKeyResponse, 0, len(keys))
	for _, k := range keys {
		result = append(result, toUserSSHKeyResponse(k))
	}
	writeJSON(w, http.StatusOK, result)
}

// DeleteUserSSHKey revokes one of the current user's SSH keys.
func DeleteUserSSHKey(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	keyID, err := strconv.ParseUint(chi.URLParam(r, "keyId"), 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid key ID")
		return
	}

	if err := database.DeleteUserSSHKey(user.ID, uint(keyID)); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "Key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to delete key")
		return
	}

	if AuditLog != nil {
		AuditLog.Log(sshaudit.EventKeyRotation, 0, user.Username, fmt.Sprintf("gateway key %d revoked", keyID))
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetSSHGatewayInfo reports the gateway's connection parameters for the UI.
func GetSSHGatewayInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled": config.Cfg.SSHGatewayEnabled,
		"port":    config.Cfg.SSHGatewayPort,
		"host":    config.Cfg.SSHGatewayPublicHost,
	})
}

func auditKeyEvent(username, action string, key *database.UserSSHKey) {
	if AuditLog != nil {
		AuditLog.Log(sshaudit.EventKeyUpload, 0, username,
			fmt.Sprintf("gateway key %s: %s", action, key.Fingerprint))
	}
}
