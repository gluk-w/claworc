package orchestrator

import "time"

// WorkloadSpec describes a single named workload that the orchestrator should
// run. Backends translate it into Kubernetes resources (Deployment + PVC +
// Service + NetworkPolicy) or Docker primitives (container + named volumes +
// published ports). The orchestrator does not interpret the content — every
// workload-specific decision (which env vars, which probe ports, which
// affinity) is encoded here by the caller.
//
// The spec is the only payload `Apply` accepts. Domain layers (OpenClaw, the
// browser provider) build a WorkloadSpec from their own data and hand it to
// the orchestrator. The orchestrator stays workload-agnostic.
type WorkloadSpec struct {
	// Name is the K8s-safe identifier for the workload. It becomes the
	// Deployment / Service / NetworkPolicy name and the `app=` label.
	Name string

	Image     string
	Command   []string
	Env       map[string]string
	Resources ResourceParams

	// Volumes are persistent (PVC on K8s, named volume on Docker). Each entry
	// is created on first Apply and reused afterwards. Size is honoured only
	// on creation.
	Volumes []VolumeMount

	// EmptyDirs are ephemeral pod-lifetime volumes (tmpfs on Docker, EmptyDir
	// on K8s). Used for /dev/shm and similar.
	EmptyDirs []EmptyDirMount

	// Ports exposed by the container. On K8s these are also published via a
	// ClusterIP Service of the same name. On Docker they are published on
	// the claworc bridge network.
	Ports []PortSpec

	Probes   ProbeSpec
	Security SecurityOptions

	// Pull controls how the image is fetched on each rollout.
	Pull PullPolicy

	// Hostname sets the pod's hostname (K8s only; ignored by Docker).
	Hostname string

	// Affinity expresses optional co-location constraints. Used by the
	// browser workload to share an RWO PVC with its OpenClaw agent.
	Affinity *AffinitySpec

	// InitContainers run to completion before the main container starts.
	// Used for one-shot setup like fixing volume ownership.
	InitContainers []InitContainerSpec

	// Labels are applied to every resource generated for this workload
	// (Deployment, Pod template, Service, NetworkPolicy).
	Labels map[string]string
}

// VolumeMount describes a persistent volume mounted into the workload.
// On Kubernetes this materialises as a PVC; on Docker as a named volume.
type VolumeMount struct {
	// Name is the volume identifier. Both PVC name and volume name on Docker.
	// Names are namespaced per backend; callers are responsible for uniqueness
	// across workloads.
	Name string

	// Size is the requested storage capacity (e.g. "10Gi"). Honoured only
	// when the volume is first created; resize is not supported here.
	Size string

	// Shared indicates the volume is mounted by multiple workloads
	// concurrently. Maps to ReadWriteMany on K8s.
	Shared bool

	MountPath string
	SubPath   string // optional; mounts only a subdirectory of the volume
	ReadOnly  bool
}

// EmptyDirMount is a pod-lifetime ephemeral volume.
type EmptyDirMount struct {
	Name      string
	MountPath string
	// Medium is "" (default disk-backed) or "Memory" (tmpfs).
	Medium    string
	SizeLimit string // e.g. "2Gi"; optional
}

// PortSpec is a TCP port exposed by the workload.
type PortSpec struct {
	Name          string
	ContainerPort int
	// ServicePort defaults to ContainerPort if zero.
	ServicePort int
	// Protocol defaults to "TCP" if empty.
	Protocol string
}

// ProbeSpec carries optional health probes. nil sub-fields disable that probe.
type ProbeSpec struct {
	Readiness *TCPProbe
	Liveness  *TCPProbe
}

// TCPProbe is a TCP-socket probe; the only kind we currently use.
type TCPProbe struct {
	Port         int
	InitialDelay time.Duration
	Period       time.Duration
}

// SecurityOptions captures container-level security knobs that vary between
// agent (more permissive) and browser (locked down) workloads.
type SecurityOptions struct {
	Privileged               bool
	AllowPrivilegeEscalation bool
	DropCapabilities         []string
	AddCapabilities          []string
	// SeccompDefault selects the runtime's default seccomp profile.
	SeccompDefault bool
}

// ResourceParams carries CPU and memory requests / limits in K8s string form
// (e.g. "500m", "1Gi"). It is used both at workload creation time and by
// `UpdateResources` for live resizing. Mirrors UpdateResourcesParams during
// the orchestrator refactor; once the old API is gone this becomes the
// canonical type.
type ResourceParams struct {
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
}

// PullPolicy mirrors the K8s container imagePullPolicy values.
type PullPolicy string

const (
	PullAlways       PullPolicy = "Always"
	PullIfNotPresent PullPolicy = "IfNotPresent"
	PullNever        PullPolicy = "Never"
)

// AffinitySpec lets a workload be required to land on the same node as one
// of the named workloads. Used to satisfy RWO PVC sharing between the agent
// and its browser pod.
type AffinitySpec struct {
	RequiredCoLocation []string
}

// InitContainerSpec describes a one-shot container that runs to completion
// before the main container starts. Mounts entries reference Volumes from the
// parent WorkloadSpec by name; Size on those VolumeMount entries is ignored.
type InitContainerSpec struct {
	Name    string
	Image   string
	Command []string
	Mounts  []VolumeMount
}
