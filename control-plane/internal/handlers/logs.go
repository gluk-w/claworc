package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/go-chi/chi/v5"
)

// StreamCreationLogs streams real-time pod creation events via SSE.
//
// Creation logs capture Kubernetes pod events (scheduling, image pulls),
// container status transitions (waiting, running), and early container
// stdout once the pod is ready. This gives users visibility into what
// happens between requesting an instance and the instance becoming usable.
//
// These logs are separate from runtime logs because they come from
// different data sources (K8s Event API and pod status vs. container
// stdout) and are inherently ephemeral — they are not persisted and
// exist only for the duration of the SSE stream.
//
// Expected lifecycle:
//   - The stream is opened while the instance status is "creating".
//   - The orchestrator polls pod events and container status on a ticker.
//   - Once all containers are ready, the last N container log lines are
//     emitted and the stream closes.
//   - If the instance is already running/stopped/failed/error, a single
//     informational message is sent and the stream closes immediately.
//   - A 10-minute timeout in the orchestrator guards against stuck pods.
func StreamCreationLogs(w http.ResponseWriter, r *http.Request) {
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

	// Short-circuit for instances that are not in creation phase
	switch inst.Status {
	case "running", "stopped", "failed", "error":
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "Streaming not supported")
			return
		}
		flusher.Flush()

		fmt.Fprintf(w, "data: Instance is not in creation phase. Switch to Runtime logs or restart the instance to see creation logs.\n\n")
		flusher.Flush()
		return
	}

	orch := orchestrator.Get()
	if orch == nil {
		writeError(w, http.StatusServiceUnavailable, "No orchestrator available")
		return
	}

	ch, err := orch.StreamCreationLogs(r.Context(), inst.Name)
	if err != nil {
		log.Printf("Failed to stream creation logs for %s: %v", inst.Name, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to stream creation logs: %v", err))
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

	ch, err := orch.StreamInstanceLogs(r.Context(), inst.Name, tail, follow)
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
