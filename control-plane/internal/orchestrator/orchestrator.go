package orchestrator

import (
	"context"
	"net/http"
)

// ContainerOrchestrator thin abstraction providing generic primitives for instance management.
type ContainerOrchestrator interface {
	Initialize(ctx context.Context) error
	IsAvailable(ctx context.Context) bool
	BackendName() string

	// Lifecycle
	CreateInstance(ctx context.Context, params CreateParams) error
	DeleteInstance(ctx context.Context, name string) error
	StartInstance(ctx context.Context, name string) error
	StopInstance(ctx context.Context, name string) error
	RestartInstance(ctx context.Context, name string) error
	GetInstanceStatus(ctx context.Context, name string) (string, error)

	// Config
	UpdateInstanceConfig(ctx context.Context, name string, configJSON string) error

	// Clone
	CloneVolumes(ctx context.Context, srcName, dstName string) error

	// Exec
	ExecInInstance(ctx context.Context, name string, cmd []string) (stdout string, stderr string, exitCode int, err error)

	// URLs
	GetGatewayWSURL(ctx context.Context, name string) (string, error)
	GetAgentTunnelAddr(ctx context.Context, name string) (string, error)

	// GetHTTPTransport returns a custom transport for reaching service URLs,
	// or nil if the default transport is sufficient (e.g. in-cluster).
	GetHTTPTransport() http.RoundTripper
}

type CreateParams struct {
	Name            string
	CPURequest      string
	CPULimit        string
	MemoryRequest   string
	MemoryLimit     string
	StorageHomebrew string
	StorageClawd    string
	StorageChrome   string
	ContainerImage  string
	VNCResolution   string
	EnvVars         map[string]string
	AgentTLSCert    string // PEM-encoded agent TLS certificate
	AgentTLSKey     string // PEM-encoded agent TLS private key (plaintext)
	ControlPlaneCA  string // PEM-encoded control-plane client certificate (for agent mTLS verification)
}
