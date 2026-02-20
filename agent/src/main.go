package main

import (
	"log"

	"github.com/gluk-w/claworc/agent/config"
	"github.com/gluk-w/claworc/agent/server"
)

func main() {
	cfg := config.Load()

	srv := server.New(cfg)

	log.Printf("claworc-agent-proxy listening on %s (gateway → %s)", cfg.ListenAddr, cfg.GatewayAddr)
	log.Fatal(srv.ListenAndServe())
}
