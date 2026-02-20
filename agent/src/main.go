package main

import (
	"log"

	"github.com/gluk-w/claworc/agent/config"
	"github.com/gluk-w/claworc/agent/server"
	"github.com/gluk-w/claworc/agent/tunnel"
)

func main() {
	cfg := config.Load()

	// Start the mTLS tunnel listener in a background goroutine.
	go func() {
		log.Printf("claworc-agent-proxy tunnel on %s", cfg.TunnelAddr)
		if err := tunnel.ListenTunnel(cfg); err != nil {
			log.Fatalf("tunnel listener failed: %v", err)
		}
	}()

	srv := server.New(cfg)

	log.Printf("claworc-agent-proxy listening on %s (gateway → %s)", cfg.ListenAddr, cfg.GatewayAddr)
	log.Fatal(srv.ListenAndServe())
}
