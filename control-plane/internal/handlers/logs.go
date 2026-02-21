package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshlogs"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
	"github.com/go-chi/chi/v5"
)

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

	// Parse log type; default to openclaw
	logType := sshlogs.LogType(r.URL.Query().Get("log_type"))
	if logType == "" {
		logType = sshlogs.LogTypeOpenClaw
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

	// Parse custom log paths from instance config
	var customPaths map[string]string
	if inst.LogPaths != "" && inst.LogPaths != "{}" {
		if err := json.Unmarshal([]byte(inst.LogPaths), &customPaths); err != nil {
			log.Printf("Failed to parse LogPaths for instance %s: %v", inst.Name, err)
			// Continue with default paths
		}
	}

	// Resolve log file path
	logPath, ok := sshlogs.ResolveLogPath(logType, customPaths)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Unknown log type: %s", logType))
		return
	}

	// Get SSH client for the instance
	sm := sshtunnel.GetSSHManager()
	if sm == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	sshClient, err := sm.GetClient(inst.Name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("No SSH connection for instance: %v", err))
		return
	}

	ch, err := sshlogs.StreamLogs(r.Context(), sshClient, logPath, tail, follow)
	if err != nil {
		log.Printf("Failed to stream logs for %s: %v", inst.Name, err)
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
