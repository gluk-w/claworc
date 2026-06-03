package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// webhookKeyDTO is the API representation of a WebhookApiKey row. The raw
// token is decrypted on read so the admin UI can render and copy it; the
// stored column is always Fernet-encrypted. Match the pattern used for
// other admin-only key endpoints (e.g. LLMProvider.APIKey).
type webhookKeyDTO struct {
	ID         uint    `json:"id"`
	Key        string  `json:"key"`
	Label      string  `json:"label"`
	IsPrivate  bool    `json:"is_private"`
	LastUsedAt *string `json:"last_used_at,omitempty"`
	CreatedAt  string  `json:"created_at"`
}

func keyToDTO(k database.WebhookApiKey) webhookKeyDTO {
	raw, _ := utils.Decrypt(k.Key)
	dto := webhookKeyDTO{
		ID:        k.ID,
		Key:       raw,
		Label:     k.Label,
		IsPrivate: k.IsPrivate,
		CreatedAt: k.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if k.LastUsedAt != nil {
		s := k.LastUsedAt.Format("2006-01-02T15:04:05Z07:00")
		dto.LastUsedAt = &s
	}
	return dto
}

// generateRawWebhookToken returns a 128-char hex token (64 bytes of
// crypto/rand) with no recognizable prefix.
func generateRawWebhookToken() (string, error) {
	buf := make([]byte, 64)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// resolveInstanceForWebhookAdmin loads the instance referenced by the URL
// id parameter and enforces the per-instance settings permission model
// (admin bypass, team manager allowed). Returns the instance and writes
// an error response on failure.
func resolveInstanceForWebhookAdmin(w http.ResponseWriter, r *http.Request) (*database.Instance, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return nil, false
	}
	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return nil, false
	}
	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return nil, false
	}
	return &inst, true
}

// ensureInstanceUUID is a safety net for the rare case where an Instance
// row predates both the migration backfill and the BeforeCreate hook
// (e.g. raw INSERTs in tests). Migration 00007 covers the common case.
func ensureInstanceUUID(inst *database.Instance) error {
	if inst.UUID != "" {
		return nil
	}
	newUUID := uuid.New().String()
	if err := database.DB.Model(inst).Update("uuid", newUUID).Error; err != nil {
		return err
	}
	inst.UUID = newUUID
	return nil
}

// webhookURLs returns the (public, private) trigger URLs for an instance.
//
// The public URL is intentionally just a relative path — the browser
// resolves it against its current origin, so the same row is correct
// under the Vite dev proxy, behind any reverse proxy, and on the bare
// control-plane port.
//
// The private URL is the loopback gateway address as each instance sees
// it. Tunnel setup pins the agent-side remote port to
// config.Cfg.InternalProxyPort (see internal/sshproxy/tunnel.go), so this
// is what other agents reach via 127.0.0.1.
func webhookURLs(instUUID string) (publicURL, privateURL string) {
	publicURL = fmt.Sprintf("/webhooks/%s", instUUID)
	privateURL = fmt.Sprintf("http://127.0.0.1:%d/webhooks/%s", config.Cfg.InternalProxyPort, instUUID)
	return
}

// GET /api/v1/instances/{id}/webhook
func GetInstanceWebhook(w http.ResponseWriter, r *http.Request) {
	inst, ok := resolveInstanceForWebhookAdmin(w, r)
	if !ok {
		return
	}
	if err := ensureInstanceUUID(inst); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to assign instance UUID")
		return
	}

	var keys []database.WebhookApiKey
	database.DB.Where("instance_id = ?", inst.ID).Order("id").Find(&keys)
	dtos := make([]webhookKeyDTO, 0, len(keys))
	for _, k := range keys {
		dtos = append(dtos, keyToDTO(k))
	}

	var logs []database.WebhookLog
	database.DB.Where("instance_id = ?", inst.ID).Order("created_at DESC").Limit(50).Find(&logs)

	publicURL, privateURL := webhookURLs(inst.UUID)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"instance_uuid": inst.UUID,
		"public_url":    publicURL,
		"private_url":   privateURL,
		"keys":          dtos,
		"recent_logs":   logs,
	})
}

// POST /api/v1/instances/{id}/webhook/keys
func CreateInstanceWebhookKey(w http.ResponseWriter, r *http.Request) {
	inst, ok := resolveInstanceForWebhookAdmin(w, r)
	if !ok {
		return
	}
	if err := ensureInstanceUUID(inst); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to assign instance UUID")
		return
	}

	var body struct {
		Label     string `json:"label"`
		IsPrivate bool   `json:"is_private"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	raw, err := generateRawWebhookToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}
	enc, err := utils.Encrypt(raw)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to encrypt token")
		return
	}
	row := database.WebhookApiKey{
		InstanceID: inst.ID,
		Key:        enc,
		Label:      body.Label,
		IsPrivate:  body.IsPrivate,
	}
	if err := database.DB.Create(&row).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save key")
		return
	}
	writeJSON(w, http.StatusOK, keyToDTO(row))
}

// POST /api/v1/instances/{id}/webhook/keys/{keyId}/regenerate
func RegenerateInstanceWebhookKey(w http.ResponseWriter, r *http.Request) {
	inst, ok := resolveInstanceForWebhookAdmin(w, r)
	if !ok {
		return
	}
	keyID, err := strconv.Atoi(chi.URLParam(r, "keyId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid key ID")
		return
	}
	var key database.WebhookApiKey
	if err := database.DB.Where("id = ? AND instance_id = ?", keyID, inst.ID).First(&key).Error; err != nil {
		writeError(w, http.StatusNotFound, "Key not found")
		return
	}

	var body struct {
		Label     *string `json:"label,omitempty"`
		IsPrivate *bool   `json:"is_private,omitempty"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	raw, err := generateRawWebhookToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}
	enc, err := utils.Encrypt(raw)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to encrypt token")
		return
	}
	updates := map[string]interface{}{"key": enc, "last_used_at": nil}
	if body.Label != nil {
		updates["label"] = *body.Label
	}
	if body.IsPrivate != nil {
		updates["is_private"] = *body.IsPrivate
	}
	if err := database.DB.Model(&key).Updates(updates).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update key")
		return
	}
	// Reload for fresh values.
	database.DB.First(&key, key.ID)
	writeJSON(w, http.StatusOK, keyToDTO(key))
}

// PATCH /api/v1/instances/{id}/webhook/keys/{keyId}
func UpdateInstanceWebhookKey(w http.ResponseWriter, r *http.Request) {
	inst, ok := resolveInstanceForWebhookAdmin(w, r)
	if !ok {
		return
	}
	keyID, err := strconv.Atoi(chi.URLParam(r, "keyId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid key ID")
		return
	}
	var key database.WebhookApiKey
	if err := database.DB.Where("id = ? AND instance_id = ?", keyID, inst.ID).First(&key).Error; err != nil {
		writeError(w, http.StatusNotFound, "Key not found")
		return
	}
	var body struct {
		Label     *string `json:"label,omitempty"`
		IsPrivate *bool   `json:"is_private,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	updates := map[string]interface{}{}
	if body.Label != nil {
		updates["label"] = *body.Label
	}
	if body.IsPrivate != nil {
		updates["is_private"] = *body.IsPrivate
	}
	if len(updates) > 0 {
		if err := database.DB.Model(&key).Updates(updates).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to update key")
			return
		}
		database.DB.First(&key, key.ID)
	}
	writeJSON(w, http.StatusOK, keyToDTO(key))
}

// DELETE /api/v1/instances/{id}/webhook/keys/{keyId}
func DeleteInstanceWebhookKey(w http.ResponseWriter, r *http.Request) {
	inst, ok := resolveInstanceForWebhookAdmin(w, r)
	if !ok {
		return
	}
	keyID, err := strconv.Atoi(chi.URLParam(r, "keyId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid key ID")
		return
	}
	if err := database.DB.Where("id = ? AND instance_id = ?", keyID, inst.ID).
		Delete(&database.WebhookApiKey{}).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete key")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// GET /api/v1/instances/{id}/webhook/logs
func ListInstanceWebhookLogs(w http.ResponseWriter, r *http.Request) {
	inst, ok := resolveInstanceForWebhookAdmin(w, r)
	if !ok {
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	q := database.DB.Where("instance_id = ?", inst.ID)
	if sn := r.URL.Query().Get("session_name"); sn != "" {
		q = q.Where("session_name = ?", sn)
	}
	var logs []database.WebhookLog
	q.Order("created_at DESC").Limit(limit).Find(&logs)
	writeJSON(w, http.StatusOK, logs)
}
