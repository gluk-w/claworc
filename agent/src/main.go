package main

import (
	"context"
	"log"
	"net/http"

	"github.com/gluk-w/claworc/agent/config"
	"github.com/gluk-w/claworc/agent/neko"
	"github.com/gluk-w/claworc/agent/server"
	"github.com/gluk-w/claworc/agent/services"
	"github.com/gluk-w/claworc/agent/tunnel"
)

func main() {
	cfg := config.Load()

	// Initialise the embedded Neko VNC/streaming server.
	// On non-Linux platforms New() returns an error; the proxy continues
	// without Neko and the /neko/ route is simply not registered.
	var nekoHandler http.Handler
	nekoSrv, err := neko.New(&cfg)
	if err != nil {
		log.Printf("neko: disabled (%v)", err)
	} else {
		if err := nekoSrv.Start(context.Background()); err != nil {
			log.Printf("neko: failed to start (%v)", err)
		} else {
			nekoHandler = nekoSrv.Handler()
			defer nekoSrv.Stop()
		}
	}

	// Register tunnel channel handlers before starting the listener.
	if nekoHandler != nil {
		tunnel.RegisterChannel(tunnel.ChannelNeko, tunnel.HTTPChannelHandler(nekoHandler))
	}
	tunnel.RegisterChannel(tunnel.ChannelTerminal, services.HandleTerminalStream)
	tunnel.RegisterChannel(tunnel.ChannelFiles, services.HandleFilesStream)
	tunnel.RegisterChannel(tunnel.ChannelLogs, services.HandleLogsStream)

	// Start the mTLS tunnel listener in a background goroutine.
	go func() {
		log.Printf("claworc-agent-proxy tunnel on %s", cfg.TunnelAddr)
		if err := tunnel.ListenTunnel(cfg); err != nil {
			log.Fatalf("tunnel listener failed: %v", err)
		}
	}()

	srv := server.New(cfg, nekoHandler)

	log.Printf("claworc-agent-proxy listening on %s (gateway → %s)", cfg.ListenAddr, cfg.GatewayAddr)
	log.Fatal(srv.ListenAndServe())
}
