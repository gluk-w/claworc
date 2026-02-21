package orchestrator

import (
	"context"
	"io"
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

	// Exec
	ExecInteractive(ctx context.Context, name string, cmd []string) (*ExecSession, error)

	// SSH
	GetInstanceSSHEndpoint(ctx context.Context, name string) (host string, port int, err error)
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
	SSHPublicKey    string // Public key to install in agent's authorized_keys
}

type FileEntry struct {
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Size        *string `json:"size"`
	Permissions string  `json:"permissions"`
}
