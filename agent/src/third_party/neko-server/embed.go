// Package neko provides an embeddable Neko server for use as a library.
//
// Neko's core implementation lives in internal/ packages which Go prevents
// external modules from importing. This facade file, being part of the same
// module, bridges that gap by exposing the constructors and types needed to
// embed Neko into the claworc agent proxy.
//
// Key internal packages exposed through this facade:
//
//   - internal/config   — Server, Desktop, Capture, WebRTC, Session, Member, Plugins configs
//   - internal/http     — HttpManagerCtx (the HTTP server + chi router)
//   - internal/api      — ApiManagerCtx (REST API routes)
//   - internal/session  — SessionManagerCtx (session lifecycle)
//   - internal/member   — MemberManagerCtx (authentication/authorization)
//   - internal/desktop  — DesktopManagerCtx (X11 desktop capture)
//   - internal/capture  — CaptureManagerCtx (GStreamer pipelines)
//   - internal/webrtc   — WebRTCManagerCtx (WebRTC streaming)
//   - internal/websocket — WebSocketManagerCtx (WebSocket control channel)
//
// Usage from the agent proxy:
//
//	import neko "github.com/m1k1o/neko/server"
//	srv, err := neko.NewEmbeddedServer(neko.EmbedOptions{...})
//	handler := srv.Handler()
package neko

import (
	"fmt"
	"net/http"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"

	"github.com/m1k1o/neko/server/internal/api"
	"github.com/m1k1o/neko/server/internal/capture"
	"github.com/m1k1o/neko/server/internal/config"
	"github.com/m1k1o/neko/server/internal/desktop"
	internalhttp "github.com/m1k1o/neko/server/internal/http"
	"github.com/m1k1o/neko/server/internal/member"
	"github.com/m1k1o/neko/server/internal/session"
	"github.com/m1k1o/neko/server/internal/webrtc"
	"github.com/m1k1o/neko/server/internal/websocket"
)

// EmbedOptions configures the embedded Neko server for library usage.
type EmbedOptions struct {
	// Display is the X11 display to capture (e.g. ":0"). Defaults to $DISPLAY.
	Display string

	// ScreenSize in "WIDTHxHEIGHT@RATE" format (e.g. "1920x1080@30").
	// Defaults to "1920x1080@30".
	ScreenSize string

	// Bind address for Neko's internal HTTP server. When embedding, this is
	// typically unused because the handler is mounted on the parent router.
	// Defaults to "127.0.0.1:0" (random port, not exposed externally).
	Bind string

	// StaticFiles path to Neko client files. Empty disables static serving
	// (the parent app serves the client files if needed).
	StaticFiles string

	// PathPrefix for Neko's HTTP routes. Defaults to "/".
	PathPrefix string

	// UserPassword for the default "user" role. Empty disables password auth.
	UserPassword string

	// AdminPassword for the default "admin" role. Empty disables admin.
	AdminPassword string
}

// EmbeddedServer wraps a fully-wired Neko server that can be used as an
// http.Handler within another Go application.
type EmbeddedServer struct {
	logger zerolog.Logger

	desktopMgr  *desktop.DesktopManagerCtx
	captureMgr  *capture.CaptureManagerCtx
	webRTCMgr   *webrtc.WebRTCManagerCtx
	sessionMgr  *session.SessionManagerCtx
	memberMgr   *member.MemberManagerCtx
	webSocketMgr *websocket.WebSocketManagerCtx
	apiMgr      *api.ApiManagerCtx
	httpMgr     *internalhttp.HttpManagerCtx

	configs struct {
		Desktop config.Desktop
		Capture config.Capture
		WebRTC  config.WebRTC
		Member  config.Member
		Session config.Session
		Server  config.Server
	}
}

// NewEmbeddedServer creates a new Neko server configured for embedded use.
// The server is not started — call Start() to begin desktop capture and
// WebRTC streaming, then use Handler() to get the HTTP handler.
func NewEmbeddedServer(opts EmbedOptions) (*EmbeddedServer, error) {
	logger := log.With().Str("module", "neko-embedded").Logger()

	// Apply defaults.
	if opts.ScreenSize == "" {
		opts.ScreenSize = "1920x1080@30"
	}
	if opts.Bind == "" {
		opts.Bind = "127.0.0.1:0"
	}
	if opts.PathPrefix == "" {
		opts.PathPrefix = "/"
	}

	// Configure via Viper (Neko's config reads from Viper).
	v := viper.GetViper()
	v.Set("desktop.screen", opts.ScreenSize)
	if opts.Display != "" {
		v.Set("desktop.display", opts.Display)
	}
	v.Set("server.bind", opts.Bind)
	v.Set("server.static", opts.StaticFiles)
	v.Set("server.path_prefix", opts.PathPrefix)
	v.Set("server.cors", []string{"*"})
	v.Set("server.metrics", false)
	v.Set("server.pprof", false)

	// Disable authentication for embedded mode (proxy handles auth).
	v.Set("member.provider", "multiuser")
	if opts.UserPassword != "" {
		v.Set("member.multiuser.user_password", opts.UserPassword)
	} else {
		v.Set("member.multiuser.user_password", "neko")
	}
	if opts.AdminPassword != "" {
		v.Set("member.multiuser.admin_password", opts.AdminPassword)
	} else {
		v.Set("member.multiuser.admin_password", "admin")
	}

	// Session config: disable auth requirements for embedded use.
	v.Set("session.private_mode", false)
	v.Set("session.implicit_hosting", true)

	srv := &EmbeddedServer{
		logger: logger,
	}

	// Set configurations from Viper.
	srv.configs.Desktop.Set()
	srv.configs.Capture.Set()
	srv.configs.WebRTC.Set()
	srv.configs.Member.Set()
	srv.configs.Session.Set()
	srv.configs.Server.Set()

	return srv, nil
}

// Start initializes all Neko managers and begins desktop capture.
// Call this before accessing Handler().
func (s *EmbeddedServer) Start() error {
	s.logger.Info().Msg("starting embedded neko server")

	s.sessionMgr = session.New(&s.configs.Session)

	s.memberMgr = member.New(s.sessionMgr, &s.configs.Member)
	if err := s.memberMgr.Connect(); err != nil {
		return fmt.Errorf("neko member manager: %w", err)
	}

	s.desktopMgr = desktop.New(&s.configs.Desktop)
	s.desktopMgr.Start()

	s.captureMgr = capture.New(s.desktopMgr, &s.configs.Capture)
	s.captureMgr.Start()

	s.webRTCMgr = webrtc.New(s.desktopMgr, s.captureMgr, &s.configs.WebRTC)
	s.webRTCMgr.Start()

	s.webSocketMgr = websocket.New(
		s.sessionMgr,
		s.desktopMgr,
		s.captureMgr,
		s.webRTCMgr,
	)
	s.webSocketMgr.Start()

	s.apiMgr = api.New(
		s.sessionMgr,
		s.memberMgr,
		s.desktopMgr,
		s.captureMgr,
	)

	s.httpMgr = internalhttp.New(
		s.webSocketMgr,
		s.apiMgr,
		&s.configs.Server,
	)

	s.logger.Info().Msg("embedded neko server ready")
	return nil
}

// Handler returns Neko's chi-based HTTP handler. This includes all API
// endpoints, WebSocket upgrade, static files, and health check routes.
// Mount this on your parent router (e.g. under /neko/).
func (s *EmbeddedServer) Handler() http.Handler {
	if s.httpMgr == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "neko server not started", http.StatusServiceUnavailable)
		})
	}
	return s.httpMgr.Handler()
}

// Stop gracefully shuts down all Neko managers.
func (s *EmbeddedServer) Stop() error {
	s.logger.Info().Msg("stopping embedded neko server")

	var firstErr error
	setErr := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if s.httpMgr != nil {
		setErr(s.httpMgr.Shutdown())
	}
	if s.webSocketMgr != nil {
		setErr(s.webSocketMgr.Shutdown())
	}
	if s.webRTCMgr != nil {
		setErr(s.webRTCMgr.Shutdown())
	}
	if s.captureMgr != nil {
		setErr(s.captureMgr.Shutdown())
	}
	if s.desktopMgr != nil {
		setErr(s.desktopMgr.Shutdown())
	}
	if s.memberMgr != nil {
		setErr(s.memberMgr.Disconnect())
	}

	s.logger.Info().Msg("embedded neko server stopped")
	return firstErr
}
