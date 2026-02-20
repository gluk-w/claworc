package handlers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/tunnel"
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

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	tc := tunnel.Manager.Get(inst.ID)
	if tc == nil {
		writeError(w, http.StatusServiceUnavailable, "No tunnel available")
		return
	}

	ctx := r.Context()

	stream, err := tc.OpenChannel(ctx, tunnel.ChannelLogs)
	if err != nil {
		log.Printf("Failed to open logs tunnel stream for %s: %v", inst.Name, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to stream logs: %v", err))
		return
	}
	defer stream.Close()

	// Send the logs header.
	header := struct {
		Tail   int  `json:"tail"`
		Follow bool `json:"follow"`
	}{Tail: tail, Follow: follow}
	if err := json.NewEncoder(stream).Encode(header); err != nil {
		log.Printf("Failed to write logs header for %s: %v", inst.Name, err)
		writeError(w, http.StatusInternalServerError, "Failed to initialize log stream")
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

	scanner := bufio.NewScanner(stream)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		fmt.Fprintf(w, "data: %s\n\n", scanner.Text())
		flusher.Flush()
	}
}
