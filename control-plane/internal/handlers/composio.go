package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/internalproxy"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// composioClient builds a Composio REST client from the global (encrypted) API
// key setting, or returns false if no key is configured.
func composioClient() (*internalproxy.ComposioClient, bool) {
	enc, err := database.GetSetting("composio_api_key")
	if err != nil || enc == "" {
		return nil, false
	}
	key, err := utils.Decrypt(enc)
	if err != nil || key == "" {
		return nil, false
	}
	return internalproxy.NewComposioClient(key), true
}

// instanceForRequest loads the instance named by {id} and enforces access.
// Returns false (after writing the response) on any failure.
func instanceForRequest(w http.ResponseWriter, r *http.Request, mutate bool) (database.Instance, bool) {
	var inst database.Instance
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return inst, false
	}
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return inst, false
	}
	allowed := middleware.CanAccessInstance(r, inst.ID)
	if mutate {
		allowed = middleware.CanMutateInstance(r, inst.ID)
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "Access denied")
		return inst, false
	}
	return inst, true
}

// ListComposioToolkits returns the OAuth toolkits available to connect. The
// control plane calls Composio directly with the real key — this is the wizard
// catalog, not an instance-facing route.
func ListComposioToolkits(w http.ResponseWriter, r *http.Request) {
	client, ok := composioClient()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Composio API key is not configured")
		return
	}
	toolkits, err := client.ListOAuthToolkits(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "Failed to list Composio toolkits: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toolkits)
}

// ListConnections returns the instance's connections (public view).
func ListConnections(w http.ResponseWriter, r *http.Request) {
	inst, ok := instanceForRequest(w, r, false)
	if !ok {
		return
	}
	var conns []database.ComposioConnection
	database.DB.Where("instance_id = ?", inst.ID).Order("created_at DESC").Find(&conns)
	if conns == nil {
		conns = []database.ComposioConnection{}
	}
	writeJSON(w, http.StatusOK, conns)
}

type initiateConnectionRequest struct {
	ToolkitSlug string `json:"toolkit_slug"`
	ToolkitName string `json:"toolkit_name"`
	CallbackURL string `json:"callback_url"`
}

type initiateConnectionResponse struct {
	ConnectedAccountID string `json:"connected_account_id"`
	RedirectURL        string `json:"redirect_url"`
}

// InitiateConnection starts an OAuth connection for the instance and returns the
// hosted authorization URL the browser should open. No connection row is
// persisted here — the row is created only once the connection becomes ACTIVE
// (see ConfirmConnection), so abandoned flows never leave stale rows.
func InitiateConnection(w http.ResponseWriter, r *http.Request) {
	inst, ok := instanceForRequest(w, r, true)
	if !ok {
		return
	}
	var body initiateConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.ToolkitSlug == "" {
		writeError(w, http.StatusBadRequest, "toolkit_slug is required")
		return
	}
	client, ok := composioClient()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Composio API key is not configured")
		return
	}

	// Ensure the instance has a connection secret (injected into the container on
	// next start). For an already-running instance this is created here so a
	// restart will activate it.
	if _, _, err := internalproxy.EnsureConnectionSecret(inst.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to provision connection secret")
		return
	}

	authConfigID, err := ensureAuthConfig(r, client, body.ToolkitSlug)
	if err != nil {
		if internalproxy.ComposioErrorSlug(err) == "Auth_Config_DefaultAuthConfigNotFound" {
			writeError(w, http.StatusBadRequest,
				"Composio has no managed credentials for this service. You must set up a custom OAuth application.")
			return
		}
		writeError(w, http.StatusBadGateway, "Failed to prepare auth config: "+err.Error())
		return
	}

	userID := internalproxy.ComposioUserID(inst)
	connectedAccountID, redirectURL, err := client.InitiateConnection(r.Context(), userID, authConfigID, body.CallbackURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Failed to initiate connection: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, initiateConnectionResponse{
		ConnectedAccountID: connectedAccountID,
		RedirectURL:        redirectURL,
	})
}

type confirmConnectionRequest struct {
	ConnectedAccountID string `json:"connected_account_id"`
	ToolkitSlug        string `json:"toolkit_slug"`
	ToolkitName        string `json:"toolkit_name"`
}

type confirmConnectionResponse struct {
	Status     string                       `json:"status"`
	Connection *database.ComposioConnection `json:"connection,omitempty"`
}

// ConfirmConnection is called once the browser's OAuth callback fires. It asks
// Composio whether the account is authorized and, only if ACTIVE, persists the
// connection row. Until then nothing is written — a pending connection lives
// purely in the browser's memory.
func ConfirmConnection(w http.ResponseWriter, r *http.Request) {
	inst, ok := instanceForRequest(w, r, true)
	if !ok {
		return
	}
	var body confirmConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.ConnectedAccountID == "" {
		writeError(w, http.StatusBadRequest, "connected_account_id is required")
		return
	}
	client, ok := composioClient()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Composio API key is not configured")
		return
	}
	acct, err := client.GetConnectedAccount(r.Context(), body.ConnectedAccountID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Failed to confirm connection: "+err.Error())
		return
	}
	resp := confirmConnectionResponse{Status: acct.Status}
	if acct.Status == "ACTIVE" {
		resp.Connection = persistActiveConnection(inst.ID, body.ConnectedAccountID, body.ToolkitSlug, body.ToolkitName, acct.Label)
		// Generate and deploy the per-toolkit skill (best-effort, off the request
		// path so the wizard response isn't blocked by SSH/Composio calls).
		go deployConnectionSkill(inst, body.ToolkitSlug, body.ToolkitName)
	}
	writeJSON(w, http.StatusOK, resp)
}

// persistActiveConnection upserts the connection row for a now-ACTIVE account.
func persistActiveConnection(instanceID uint, connectedAccountID, toolkitSlug, toolkitName, label string) *database.ComposioConnection {
	var conn database.ComposioConnection
	err := database.DB.Where("instance_id = ? AND composio_connected_account_id = ?", instanceID, connectedAccountID).First(&conn).Error
	if err == nil {
		// Already recorded — refresh status/label.
		database.DB.Model(&database.ComposioConnection{}).Where("id = ?", conn.ID).
			Updates(map[string]any{"status": "ACTIVE", "account_label": label})
		conn.Status = "ACTIVE"
		conn.AccountLabel = label
		return &conn
	}
	name := toolkitName
	if name == "" {
		name = toolkitSlug
	}
	var authConfigID string
	var cached database.ComposioAuthConfig
	if database.DB.Where("toolkit_slug = ?", toolkitSlug).First(&cached).Error == nil {
		authConfigID = cached.AuthConfigID
	}
	conn = database.ComposioConnection{
		InstanceID:                 instanceID,
		ToolkitSlug:                toolkitSlug,
		Name:                       name,
		ComposioConnectedAccountID: connectedAccountID,
		AuthConfigID:               authConfigID,
		Status:                     "ACTIVE",
		AccountLabel:               label,
	}
	database.DB.Create(&conn)
	return &conn
}

// DeleteConnection removes a connection locally and best-effort at Composio.
func DeleteConnection(w http.ResponseWriter, r *http.Request) {
	inst, ok := instanceForRequest(w, r, true)
	if !ok {
		return
	}
	conn, ok := connectionForRequest(w, r, inst.ID)
	if !ok {
		return
	}
	if client, ok := composioClient(); ok && conn.ComposioConnectedAccountID != "" {
		if err := client.DeleteConnectedAccount(r.Context(), conn.ComposioConnectedAccountID); err != nil {
			// Best-effort: still remove locally so the UI isn't stuck.
			log.Printf("Failed to delete Composio connected account %s: %v", conn.ComposioConnectedAccountID, err)
		}
	}
	database.DB.Delete(&database.ComposioConnection{}, conn.ID)
	// Remove the deployed skill from the instance (best-effort). Only when no
	// other ACTIVE connection still uses the same toolkit.
	go removeConnectionSkill(inst.ID, conn.ToolkitSlug)
	w.WriteHeader(http.StatusNoContent)
}

// deployConnectionSkill builds the "claworc-<toolkit>" skill for an instance's
// connection and writes it into the instance over SSH. Best-effort: any failure
// (no Composio key, instance not SSH-connected, etc.) is logged and ignored.
func deployConnectionSkill(inst database.Instance, toolkitSlug, toolkitName string) {
	client, ok := composioClient()
	if !ok {
		return
	}
	secret, _, err := internalproxy.EnsureConnectionSecret(inst.ID)
	if err != nil {
		log.Printf("connection skill: ensure secret for instance %d: %v", inst.ID, err)
		return
	}
	skillName, files, err := internalproxy.GenerateConnectionSkill(context.Background(), client, toolkitSlug, toolkitName, secret)
	if err != nil {
		log.Printf("connection skill: generate for toolkit %s: %v", toolkitSlug, err)
		return
	}
	if result := deployToInstance(inst.ID, skillName, files); result.Status != "ok" {
		log.Printf("connection skill: deploy %s to instance %d: %s", skillName, inst.ID, result.Error)
	}
}

// removeConnectionSkill deletes a connection's skill directory from the instance.
// If another ACTIVE connection still uses the same toolkit the skill is kept.
// Best-effort: failures are logged and ignored.
func removeConnectionSkill(instanceID uint, toolkitSlug string) {
	var remaining int64
	database.DB.Model(&database.ComposioConnection{}).
		Where("instance_id = ? AND toolkit_slug = ? AND status = ?", instanceID, toolkitSlug, "ACTIVE").
		Count(&remaining)
	if remaining > 0 {
		return
	}
	client, ok := SSHMgr.GetConnection(instanceID)
	if !ok {
		return
	}
	skillDir := "/home/claworc/.openclaw/skills/" + internalproxy.ConnectionSkillName(toolkitSlug)
	if err := sshproxy.DeletePath(client, skillDir); err != nil {
		log.Printf("connection skill: remove %s from instance %d: %v", skillDir, instanceID, err)
	}
}

// deployActiveConnectionSkills (re)deploys the skills for all of an instance's
// ACTIVE connections. Called after an instance (re)connects over SSH so skills
// survive container recreation. Best-effort.
func deployActiveConnectionSkills(instanceID uint) {
	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return
	}
	var conns []database.ComposioConnection
	database.DB.Where("instance_id = ? AND status = ?", instanceID, "ACTIVE").Find(&conns)
	for _, conn := range conns {
		deployConnectionSkill(inst, conn.ToolkitSlug, conn.Name)
	}
}

// connectionForRequest loads the {connID} connection and verifies it belongs to
// the given instance.
func connectionForRequest(w http.ResponseWriter, r *http.Request, instanceID uint) (database.ComposioConnection, bool) {
	var conn database.ComposioConnection
	connID, err := strconv.Atoi(chi.URLParam(r, "connID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid connection ID")
		return conn, false
	}
	if err := database.DB.First(&conn, connID).Error; err != nil || conn.InstanceID != instanceID {
		writeError(w, http.StatusNotFound, "Connection not found")
		return conn, false
	}
	return conn, true
}

// ensureAuthConfig returns the cached Composio auth_config id for a toolkit,
// creating (and caching) one if none exists yet.
func ensureAuthConfig(r *http.Request, client *internalproxy.ComposioClient, toolkitSlug string) (string, error) {
	var cached database.ComposioAuthConfig
	if err := database.DB.Where("toolkit_slug = ?", toolkitSlug).First(&cached).Error; err == nil && cached.AuthConfigID != "" {
		return cached.AuthConfigID, nil
	}
	authConfigID, err := client.CreateAuthConfig(r.Context(), toolkitSlug)
	if err != nil {
		return "", err
	}
	database.DB.Save(&database.ComposioAuthConfig{ToolkitSlug: toolkitSlug, AuthConfigID: authConfigID})
	return authConfigID, nil
}
