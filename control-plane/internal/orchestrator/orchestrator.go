package orchestrator

import (
	"context"
)

// NOTE: Log streaming has been migrated to SSH (see internal/sshlogs).
// The StreamInstanceLogs method was removed from this interface.

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

	// Clone
	CloneVolumes(ctx context.Context, srcName, dstName string) error

	// SSH
	GetInstanceSSHEndpoint(ctx context.Context, name string) (host string, port int, err error)
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
