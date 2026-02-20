// Package neko provides the Neko VNC/streaming integration for the agent proxy.
//
// This package wraps the vendored Neko server (third_party/neko-server) and
// exposes it as an embeddable component within the claworc agent proxy.
//
// Neko requires Linux with X11 and GStreamer, so the full implementation is
// only compiled on Linux (see neko_linux.go). On other platforms, stub types
// are provided (see neko_stub.go) to allow compilation.
//
// Neko types and packages used from the vendored module:
//
//   - neko.EmbeddedServer: The main server wrapper (facade in embed.go)
//   - neko.EmbedOptions: Configuration for embedded mode
//   - neko.NewEmbeddedServer(): Constructor
//   - EmbeddedServer.Handler() http.Handler: The HTTP handler for mounting
//   - EmbeddedServer.Start() / Stop(): Lifecycle management
//
// Internal packages accessed via the facade (not importable directly):
//
//   - internal/config: Server, Desktop, Capture, WebRTC, Session, Member configs
//   - internal/http: HttpManagerCtx — chi router with API + WebSocket + static
//   - internal/api: ApiManagerCtx — REST endpoints (/api/login, /api/sessions, etc.)
//   - internal/session: SessionManagerCtx — session lifecycle
//   - internal/member: MemberManagerCtx — auth/authz with multiuser provider
//   - internal/desktop: DesktopManagerCtx — X11 display capture (requires X11+GStreamer)
//   - internal/capture: CaptureManagerCtx — GStreamer video/audio pipelines
//   - internal/webrtc: WebRTCManagerCtx — Pion WebRTC streaming
//   - internal/websocket: WebSocketManagerCtx — real-time control channel
package neko
