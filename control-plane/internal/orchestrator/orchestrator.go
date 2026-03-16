package orchestrator

import (
	"context"

	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
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
	RecreateInstance(ctx context.Context, name string, newImage string, onProgress func(string)) error
	GetInstanceStatus(ctx context.Context, name string) (string, error)
	GetInstanceImageInfo(ctx context.Context, name string) (string, error)

	// Config
	UpdateInstanceConfig(ctx context.Context, name string, configJSON string) error

	// Clone
	CloneVolumes(ctx context.Context, srcName, dstName string) error

	// SSH
	ConfigureSSHAccess(ctx context.Context, instanceID uint, publicKey string) error
	GetSSHAddress(ctx context.Context, instanceID uint) (host string, port int, err error)

	// Exec
	ExecInInstance(ctx context.Context, name string, cmd []string) (stdout string, stderr string, exitCode int, err error)
}

type CreateParams struct {
	Name            string
	CPURequest      string
	CPULimit        string
	MemoryRequest   string
	MemoryLimit     string
	StorageHomebrew string
	StorageHome     string
	ContainerImage  string
	VNCResolution   string
	Timezone        string
	UserAgent       string
	EnvVars         map[string]string
	OnProgress      func(string)
}

// FileEntry is a type alias for sshproxy.FileEntry, kept for backward compatibility.
type FileEntry = sshproxy.FileEntry
