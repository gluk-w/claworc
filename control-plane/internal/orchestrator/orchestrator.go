package orchestrator

import (
	"context"
	"io"

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
	RestartInstance(ctx context.Context, name string, params CreateParams) error
	GetInstanceStatus(ctx context.Context, name string) (string, error)
	GetInstanceImageInfo(ctx context.Context, name string) (string, error)

	// Config
	UpdateInstanceConfig(ctx context.Context, name string, configJSON string) error

	// Resources
	UpdateResources(ctx context.Context, name string, params UpdateResourcesParams) error
	GetContainerStats(ctx context.Context, name string) (*ContainerStats, error)

	// Image
	UpdateImage(ctx context.Context, name string, params CreateParams) error

	// Clone
	CloneVolumes(ctx context.Context, srcName, dstName string) error

	// SSH
	ConfigureSSHAccess(ctx context.Context, instanceID uint, publicKey string) error
	GetSSHAddress(ctx context.Context, instanceID uint) (host string, port int, err error)

	// Exec
	ExecInInstance(ctx context.Context, name string, cmd []string) (stdout string, stderr string, exitCode int, err error)

	// StreamExecInInstance runs a command and streams stdout to the provided writer.
	// Used for large outputs like tar archives that cannot be buffered in memory.
	StreamExecInInstance(ctx context.Context, name string, cmd []string, stdout io.Writer) (stderr string, exitCode int, err error)

	// DeleteSharedVolume removes the backing volume/PVC for a shared folder.
	DeleteSharedVolume(ctx context.Context, folderID uint) error
}

// SharedFolderMount describes a shared volume to mount into a container.
type SharedFolderMount struct {
	VolumeID  uint   // SharedFolder.ID, used to derive volume name
	MountPath string // Container mount path
}

type CreateParams struct {
	Name               string
	CPURequest         string
	CPULimit           string
	MemoryRequest      string
	MemoryLimit        string
	StorageHomebrew    string
	StorageHome        string
	ContainerImage     string
	VNCResolution      string
	Timezone           string
	UserAgent          string
	EnvVars            map[string]string
	OnProgress         func(string)
	SharedFolderMounts []SharedFolderMount
}

type UpdateResourcesParams struct {
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
}

type ContainerStats struct {
	CPUUsageMillicores int64   `json:"cpu_usage_millicores"`
	CPUUsagePercent    float64 `json:"cpu_usage_percent"`  // percentage of CPU limit
	MemoryUsageBytes   int64   `json:"memory_usage_bytes"`
	MemoryLimitBytes   int64   `json:"memory_limit_bytes"` // from container runtime
}

// FileEntry is a type alias for sshproxy.FileEntry, kept for backward compatibility.
type FileEntry = sshproxy.FileEntry
