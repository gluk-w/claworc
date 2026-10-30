package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/go-chi/chi/v5"
)

// agentLogPath returns the primary log file the agent declares (shim meta
// log_files[0]), or "" when it declares none or cannot be probed.
func agentLogPath(ctx context.Context, instanceID uint) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := agentClientFor(ctx, instanceID)
	if err != nil {
		return ""
	}
	caps, err := client.Capabilities(ctx)
	if err != nil || len(caps.LogFiles) == 0 {
		return ""
	}
	return caps.LogFiles[0].Path
}

func StreamLogs(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	tail := 100
	if t := r.URL.Query().Get("tail"); t != "" {
		if v, err := strconv.Atoi(t); err == nil {
			tail = v
		}
	}

	follow := true
	if f := r.URL.Query().Get("follow"); f == "false" {
		follow = false
	}

	logType := sshproxy.LogType(r.URL.Query().Get("type"))
	// "openclaw" is the legacy name of the primary agent log.
	if logType == "" || logType == sshproxy.LogTypeOpenClaw {
		logType = sshproxy.LogTypeAgent
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
		WriteOrchestratorUnavailable(w)
		return
	}

	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	// Parse custom log paths from instance if set
	var customPaths map[sshproxy.LogType]string
	if inst.LogPaths != "" {
		if err := json.Unmarshal([]byte(inst.LogPaths), &customPaths); err != nil {
			log.Printf("Invalid log_paths JSON for instance %d: %v", inst.ID, err)
		}
	}

	var logPath string
	if logType == sshproxy.LogTypeAgent {
		logPath = sshproxy.ResolveAgentLogPath(agentLogPath(r.Context(), inst.ID), customPaths)
	} else {
		logPath = sshproxy.ResolveLogPath(logType, customPaths)
	}
	if logPath == "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Unknown log type: %s", logType))
		return
	}

	client, err := SSHMgr.EnsureConnectedWithIPCheck(r.Context(), inst.ID, orch, inst.AllowedSourceIPs)
	if err != nil {
		log.Printf("Failed to get SSH connection for instance %d: %v", inst.ID, err)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("SSH connection failed: %v", err))
		return
	}

	ch, err := sshproxy.StreamLogs(r.Context(), client, logPath, sshproxy.StreamOptions{
		Tail:   tail,
		Follow: follow,
	})
	if err != nil {
		log.Printf("Failed to stream logs for instance %d (%s): %v", inst.ID, logPath, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to stream logs: %v", err))
		return
	}

	// SSE response
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming not supported")
		return
	}

	// Flush headers immediately so the EventSource connection is established
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case line, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}
