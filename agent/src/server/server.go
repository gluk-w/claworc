package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gluk-w/claworc/agent/config"
	"github.com/gluk-w/claworc/agent/proxy"
)

// New creates an *http.Server with all routes wired up.
func New(cfg config.Config) *http.Server {
	mux := http.NewServeMux()

	gw := proxy.GatewayHandler(cfg.GatewayAddr)
	mux.Handle("/gateway/", gw)
	mux.Handle("/websocket/", gw)

	mux.HandleFunc("/health", healthHandler)

	return &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": "claworc-agent-proxy",
	})
}
