package orchestrator

import (
	"context"
	"io"
	"net/http"
)

// ContainerOrchestrator thin abstraction providing generic primitives (exec, read/write files)
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

	// Logs
	StreamInstanceLogs(ctx context.Context, name string, tail int, follow bool) (<-chan string, error)

	// Clone
	CloneVolumes(ctx context.Context, srcName, dstName string) error

	// Exec & Files
	ExecInInstance(ctx context.Context, name string, cmd []string) (stdout string, stderr string, exitCode int, err error)
	ExecInteractive(ctx context.Context, name string, cmd []string) (*ExecSession, error)
	ListDirectory(ctx context.Context, name string, path string) ([]FileEntry, error)
	ReadFile(ctx context.Context, name string, path string) ([]byte, error)
	CreateFile(ctx context.Context, name string, path string, content string) error
	CreateDirectory(ctx context.Context, name string, path string) error
	WriteFile(ctx context.Context, name string, path string, data []byte) error

	// URLs
	GetGatewayWSURL(ctx context.Context, name string) (string, error)

	// GetHTTPTransport returns a custom transport for reaching service URLs,
	// or nil if the default transport is sufficient (e.g. in-cluster).
	GetHTTPTransport() http.RoundTripper
}

// ExecSession represents an interactive exec session with stdin/stdout and resize support.
type ExecSession struct {
	Stdin  io.WriteCloser
	Stdout io.Reader
	Resize func(cols, rows uint16) error
	Close  func() error
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
}

type FileEntry struct {
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Size        *string `json:"size"`
	Permissions string  `json:"permissions"`
}
