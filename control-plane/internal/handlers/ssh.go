package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/go-chi/chi/v5"
)

// SSHMgr is set from main.go during init.
var SSHMgr *sshproxy.SSHManager

// TunnelMgr is set from main.go during init.
var TunnelMgr *sshproxy.TunnelManager

// SSHConnectionTest tests SSH connectivity to an instance by establishing a
// connection (or reusing an existing one) and executing a simple command.
func SSHConnectionTest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	orch := orchestrator.Get()
	if orch == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status":     "error",
			"output":     "",
			"latency_ms": 0,
			"error":      "No orchestrator available",
		})
		return
	}

	if SSHMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status":     "error",
			"output":     "",
			"latency_ms": 0,
			"error":      "SSH manager not initialized",
		})
		return
	}

	start := time.Now()

	client, err := SSHMgr.EnsureConnected(r.Context(), inst.ID, orch)
	if err != nil {
		latency := time.Since(start).Milliseconds()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":     "error",
			"output":     "",
			"latency_ms": latency,
			"error":      err.Error(),
		})
		return
	}

	session, err := client.NewSession()
	if err != nil {
		latency := time.Since(start).Milliseconds()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":     "error",
			"output":     "",
			"latency_ms": latency,
			"error":      "Failed to create SSH session: " + err.Error(),
		})
		return
	}
	defer session.Close()

	output, err := session.CombinedOutput("echo \"SSH test successful\"")
	latency := time.Since(start).Milliseconds()

	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":     "error",
			"output":     string(output),
			"latency_ms": latency,
			"error":      "Command execution failed: " + err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "ok",
		"output":     string(output),
		"latency_ms": latency,
		"error":      nil,
	})
}

// GetTunnelStatus returns the active SSH tunnels for an instance.
func GetTunnelStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	if TunnelMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"tunnels": []interface{}{},
			"error":   "Tunnel manager not initialized",
		})
		return
	}

	tunnels := TunnelMgr.GetTunnelsForInstance(inst.ID)

	type tunnelResponse struct {
		Label      string `json:"label"`
		Type       string `json:"type"`
		LocalPort  int    `json:"local_port"`
		RemotePort int    `json:"remote_port"`
		Status     string `json:"status"`
		Error      string `json:"error,omitempty"`
		LastCheck  string `json:"last_check"`
	}

	resp := make([]tunnelResponse, len(tunnels))
	for i, t := range tunnels {
		resp[i] = tunnelResponse{
			Label:      t.Label,
			Type:       string(t.Config.Type),
			LocalPort:  t.LocalPort,
			RemotePort: t.Config.RemotePort,
			Status:     t.Status,
			Error:      t.Error,
			LastCheck:  t.LastCheck.UTC().Format(time.RFC3339),
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tunnels": resp,
	})
}
