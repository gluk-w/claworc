//go:build !linux || !cgo

package neko

import (
	"errors"
	"net/http"
)

// EmbedOptions configures the embedded Neko server.
// On non-Linux platforms this is a stub — Neko requires X11 and GStreamer.
type EmbedOptions struct {
	Display      string
	ScreenSize   string
	Bind         string
	StaticFiles  string
	PathPrefix   string
	UserPassword string
	AdminPassword string
}

// EmbeddedServer is a stub for non-Linux platforms.
type EmbeddedServer struct{}

// NewEmbeddedServer returns an error on non-Linux platforms.
func NewEmbeddedServer(_ EmbedOptions) (*EmbeddedServer, error) {
	return nil, errors.New("neko: not supported on this platform (requires Linux with X11 and GStreamer)")
}

// Handler returns a handler that reports the platform limitation.
func (s *EmbeddedServer) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "neko: not available on this platform", http.StatusServiceUnavailable)
	})
}

// Start returns an error on non-Linux platforms.
func (s *EmbeddedServer) Start() error {
	return errors.New("neko: not supported on this platform")
}

// Stop is a no-op on non-Linux platforms.
func (s *EmbeddedServer) Stop() error {
	return nil
}
