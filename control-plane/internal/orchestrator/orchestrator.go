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
	// CloneVolume copies a single named volume (PVC on K8s, named volume on
	// Docker) from src to dst. Used by feature packages (e.g. browserprov)
	// that own their own data volumes outside the agent's main set.
	CloneVolume(ctx context.Context, srcVolName, dstVolName string) error

	// VolumeNameFor returns the canonical persistent-volume name the backend
	// uses for a (workloadName, suffix) pair. Lets callers reference volumes
	// owned by other workloads (e.g. browserprov mounting the agent's home
	// volume) without hardcoding per-runtime naming conventions.
	VolumeNameFor(workloadName, suffix string) string

	// SSH
	ConfigureSSHAccess(ctx context.Context, instanceID uint, publicKey string) error
	GetSSHAddress(ctx context.Context, instanceID uint) (host string, port int, err error)

	// Workload (generic, name-scoped). These are the primitives feature
	// packages use to spin up a container without the orchestrator knowing
	// what they're for. Apply creates or rolls a workload from a WorkloadSpec.
	// DeleteWorkload removes the container/Deployment plus any non-shared
	// volumes from the spec. EnsureSSHAccess installs publicKey into the
	// workload's authorized_keys. WorkloadSSHAddress returns the (host, port)
	// the control plane should dial to reach the workload's sshd.
	Apply(ctx context.Context, spec WorkloadSpec) error
	DeleteWorkload(ctx context.Context, spec WorkloadSpec) error
	EnsureSSHAccess(ctx context.Context, name, publicKey string) error
	WorkloadSSHAddress(ctx context.Context, name string) (host string, port int, err error)

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
	CPUUsagePercent    float64 `json:"cpu_usage_percent"` // percentage of CPU limit
	MemoryUsageBytes   int64   `json:"memory_usage_bytes"`
	MemoryLimitBytes   int64   `json:"memory_limit_bytes"` // from container runtime
}

// FileEntry is a type alias for sshproxy.FileEntry, kept for backward compatibility.
type FileEntry = sshproxy.FileEntry
