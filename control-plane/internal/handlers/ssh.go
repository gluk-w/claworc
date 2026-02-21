package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/go-chi/chi/v5"
)

// SSHMgr is set from main.go during init.
var SSHMgr *sshmanager.SSHManager

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

	client, err := SSHMgr.EnsureConnected(r.Context(), inst.Name, orch)
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
