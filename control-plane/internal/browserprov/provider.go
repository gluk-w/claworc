// Package browserprov defines the on-demand browser session abstraction used
// by the control plane to spawn, reach, and reap a per-instance browser
// (CDP + VNC) on whichever provider the operator has configured. Local
// (Kubernetes/Docker) and SaaS (Cloudflare/Browserless/...) implementations
// share this single interface.
package browserprov

import (
	"context"
	"errors"
	"io"
	"net"
	"time"
)

// Status mirrors the lifecycle column on the browser_sessions table.
type Status string

const (
	StatusStopped  Status = "stopped"
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusError    Status = "error"
)

// Capabilities advertises optional features. Bridge / handler code reads these
// to gate UI and feature flags.
type Capabilities struct {
	SupportsVNC               bool
	SupportsPersistentProfile bool
	SupportsHeadful           bool
}

// SessionParams configures a session at spawn time. Optional fields fall back
// to provider defaults.
type SessionParams struct {
	Image         string
	StorageSize   string
	VNCResolution string
	UserAgent     string
	Timezone      string
	EnvVars       map[string]string
}

// Session is the provider's view of a running browser. It mirrors the
// database row but omits provider-specific opaque fields — those are set on
// the row by the bridge after EnsureSession returns.
type Session struct {
	InstanceID  uint
	Provider    string
	Status      Status
	Image       string
	PodName     string // K8s deployment / Docker container; empty for SaaS
	ProviderRef string // opaque per-provider session id
	StartedAt   time.Time
	LastUsedAt  time.Time
}

// ErrNotSupported is returned by providers that don't implement an optional
// capability (e.g. SaaS providers that don't expose VNC).
var ErrNotSupported = errors.New("browserprov: not supported by this provider")

// Provider is the contract every browser back-end satisfies. The interface is
// intentionally small: lifecycle, status, and two byte-stream dial methods
// (CDP, VNC). For non-byte-stream providers (Cloudflare's per-session
// WebSocket), DialCDP returns an io.ReadWriteCloser that internally translates
// HTTP /json/* and ws:// upgrade traffic.
type Provider interface {
	Name() string
	Capabilities() Capabilities

	EnsureSession(ctx context.Context, instanceID uint, params SessionParams) (*Session, error)
	StopSession(ctx context.Context, instanceID uint) error
	DeleteSession(ctx context.Context, instanceID uint) error

	// DialCDP returns a byte-stream connection to the browser's CDP HTTP/WS
	// endpoint as it expects to be reached at http://127.0.0.1:9222. Callers
	// are responsible for closing the returned conn.
	DialCDP(ctx context.Context, instanceID uint) (io.ReadWriteCloser, error)

	// DialVNC returns a byte-stream connection to the browser's noVNC
	// websocket bridge, or ErrNotSupported when the provider has no VNC.
	DialVNC(ctx context.Context, instanceID uint) (io.ReadWriteCloser, error)

	// VNCDialer returns a DialContext-compatible function bound to the noVNC
	// port (3000) inside the browser pod. Each invocation opens a fresh SSH
	// channel; callers pass it as http.Transport.DialContext to route HTTP
	// and WebSocket traffic through the same SSH session that DialVNC uses.
	// Returns ErrNotSupported for providers without VNC.
	VNCDialer(ctx context.Context, instanceID uint) (func(ctx context.Context, network, addr string) (net.Conn, error), error)

	// TestConnection runs a one-shot SSH command against the browser pod's
	// sshd to prove end-to-end connectivity (key provisioned, sshd up,
	// network reachable). Returns the command output. Used by the SSH
	// Troubleshoot popup. Returns ErrNotSupported for providers without an
	// addressable SSH endpoint (SaaS).
	TestConnection(ctx context.Context, instanceID uint) (output string, err error)

	// Reconnect closes any cached SSH client/state for the browser pod so
	// the next dial re-establishes a fresh session and re-uploads the
	// public key. Returns ErrNotSupported for providers that don't manage
	// a connection.
	Reconnect(ctx context.Context, instanceID uint) error

	SessionStatus(ctx context.Context, instanceID uint) (Status, error)
}
