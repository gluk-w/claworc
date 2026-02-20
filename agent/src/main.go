package main

import (
	"log"
	"net/http"
	"time"

	"github.com/gluk-w/claworc/agent/config"
	"github.com/gluk-w/claworc/agent/proxy"
)

func main() {
	cfg := config.Load()

	mux := http.NewServeMux()

	gw := proxy.GatewayHandler(cfg.GatewayAddr)
	mux.Handle("/gateway/", gw)
	mux.Handle("/websocket/", gw)

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("claworc-agent-proxy listening on %s (gateway → %s)", cfg.ListenAddr, cfg.GatewayAddr)
	log.Fatal(srv.ListenAndServe())
}
