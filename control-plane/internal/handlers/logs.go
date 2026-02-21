package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshlogs"
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

	logType := sshlogs.LogType(r.URL.Query().Get("type"))
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

	orch := orchestrator.Get()
	if orch == nil {
		writeError(w, http.StatusServiceUnavailable, "No orchestrator available")
		return
	}

	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	// Parse custom log paths from instance if set
	var customPaths map[sshlogs.LogType]string
	if inst.LogPaths != "" {
		if err := json.Unmarshal([]byte(inst.LogPaths), &customPaths); err != nil {
			log.Printf("Invalid log_paths JSON for instance %d: %v", inst.ID, err)
		}
	}

	logPath := sshlogs.ResolveLogPath(logType, customPaths)
	if logPath == "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Unknown log type: %s", logType))
		return
	}

	client, err := SSHMgr.EnsureConnected(r.Context(), inst.ID, orch)
	if err != nil {
		log.Printf("Failed to get SSH connection for instance %d: %v", inst.ID, err)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("SSH connection failed: %v", err))
		return
	}

	ch, err := sshlogs.StreamLogs(r.Context(), client, logPath, tail, follow)
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
