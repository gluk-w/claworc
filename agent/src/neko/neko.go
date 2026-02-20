// Package neko provides the Neko VNC/streaming integration for the agent proxy.
//
// This package wraps the vendored Neko server (third_party/neko-server) and
// exposes it as an embeddable component within the claworc agent proxy.
// Neko requires Linux with X11 and GStreamer, so the full implementation is
// only compiled on Linux (neko_linux.go). On other platforms, stub types are
// provided (neko_stub.go) to allow compilation — New() returns an error.
package neko

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gluk-w/claworc/agent/config"
)

// NekoServer wraps the embedded Neko VNC/streaming server for the agent proxy.
type NekoServer struct {
	embedded *EmbeddedServer
}

// New creates a NekoServer configured to capture the Chromium instance
// running on display :0 (or $DISPLAY). Resolution is read from VNC_RESOLUTION
// (default "1920x1080"). Neko's own authentication is disabled; the
// proxy/tunnel layer handles auth.
func New(_ *config.Config) (*NekoServer, error) {
	opts := buildEmbedOptions()

	srv, err := NewEmbeddedServer(opts)
	if err != nil {
		return nil, err
	}

	return &NekoServer{embedded: srv}, nil
}

// buildEmbedOptions reads environment variables and returns the EmbedOptions
// for the embedded Neko server.
func buildEmbedOptions() EmbedOptions {
	display := os.Getenv("DISPLAY")
	if display == "" {
		display = ":0"
	}

	resolution := os.Getenv("VNC_RESOLUTION")
	if resolution == "" {
		resolution = "1920x1080"
	}

	// Append default frame rate if not specified.
	screenSize := resolution
	if !strings.Contains(resolution, "@") {
		screenSize = resolution + "@30"
	}

	return EmbedOptions{
		Display:    display,
		ScreenSize: screenSize,
		PathPrefix: "/",
		// Auth is handled by the proxy/tunnel layer, not Neko.
		// The embedded server defaults to "neko"/"admin" passwords
		// which are never exposed externally.
	}
}

// Handler returns the Neko HTTP handler for WebSocket (/ws) and static files (/).
// If the server is not initialized, a 503 Service Unavailable handler is returned.
func (n *NekoServer) Handler() http.Handler {
	if n == nil || n.embedded == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "neko server not initialized", http.StatusServiceUnavailable)
		})
	}
	return n.embedded.Handler()
}

// Start initializes desktop capture and WebRTC streaming.
func (n *NekoServer) Start(ctx context.Context) error {
	if n == nil || n.embedded == nil {
		return errors.New("neko server not initialized")
	}
	log.Println("neko: starting embedded server")
	return n.embedded.Start()
}

// Stop gracefully shuts down the Neko server.
func (n *NekoServer) Stop() error {
	if n == nil || n.embedded == nil {
		return nil
	}
	log.Println("neko: stopping embedded server")
	return n.embedded.Stop()
}
