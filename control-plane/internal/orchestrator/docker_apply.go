package orchestrator

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/docker/go-units"
)

// Apply creates or updates the workload described by spec. Volumes that don't
// exist are created; an existing container with the same name is stopped and
// removed before recreation so the new spec takes effect (Docker has no native
// equivalent to a Deployment rolling update).
//
// Init containers in spec.InitContainers run as post-start exec hooks. Docker
// has no built-in init-container concept; the practical effect is the same for
// our use case (fixing volume ownership) since volumes are mounted before the
// command runs.
func (d *DockerOrchestrator) Apply(ctx context.Context, spec WorkloadSpec) error {
	if spec.Pull != PullNever {
		if err := d.ensureImage(ctx, spec.Image); err != nil {
			return err
		}
	}

	if err := d.applyVolumesDocker(ctx, spec.Volumes); err != nil {
		return err
	}

	// Stop + remove any existing container with the same name so the new spec
	// rolls out cleanly. Volumes are separate objects and survive.
	if _, err := d.client.ContainerInspect(ctx, spec.Name); err == nil {
		timeout := 30
		_ = d.client.ContainerStop(ctx, spec.Name, container.StopOptions{Timeout: &timeout})
		_ = d.client.ContainerRemove(ctx, spec.Name, container.RemoveOptions{Force: true})
	} else if !dockerclient.IsErrNotFound(err) {
		return fmt.Errorf("inspect existing container %s: %w", spec.Name, err)
	}

	containerCfg, hostCfg, netCfg := d.buildContainerConfig(spec)
	resp, err := d.client.ContainerCreate(ctx, containerCfg, hostCfg, netCfg, nil, spec.Name)
	if err != nil {
		return fmt.Errorf("create container %s: %w", spec.Name, err)
	}
	if err := d.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container %s: %w", spec.Name, err)
	}

	for _, ic := range spec.InitContainers {
		if err := d.runPostStartExec(ctx, resp.ID, ic); err != nil {
			log.Printf("init container %q on %s: %v", ic.Name, spec.Name, err)
		}
	}
	return nil
}

// DeleteWorkload removes the container and any non-shared named volumes from
// spec.Volumes. Shared volumes are preserved.
func (d *DockerOrchestrator) DeleteWorkload(ctx context.Context, spec WorkloadSpec) error {
	timeout := 30
	if err := d.client.ContainerStop(ctx, spec.Name, container.StopOptions{Timeout: &timeout}); err != nil && !dockerclient.IsErrNotFound(err) {
		log.Printf("stop container %s: %v", spec.Name, err)
	}
	if err := d.client.ContainerRemove(ctx, spec.Name, container.RemoveOptions{Force: true}); err != nil && !dockerclient.IsErrNotFound(err) {
		log.Printf("remove container %s: %v", spec.Name, err)
	}

	for _, vol := range spec.Volumes {
		if vol.Shared {
			continue
		}
		if err := d.client.VolumeRemove(ctx, vol.Name, true); err != nil {
			log.Printf("remove volume %s: %v", vol.Name, err)
		}
	}
	return nil
}

// EnsureSSHAccess writes publicKey into the named workload's authorized_keys.
// Reuses the existing helper that drives ExecInInstance.
func (d *DockerOrchestrator) EnsureSSHAccess(ctx context.Context, name string, publicKey string) error {
	return configureSSHAccess(ctx, d.ExecInInstance, name, publicKey)
}

// WorkloadSSHAddress returns the address the control plane should dial to SSH
// into the workload. Mirrors the heuristic in the existing GetSSHAddress:
// container IP on the claworc bridge when the control plane runs inside Docker,
// otherwise the published host port on loopback.
func (d *DockerOrchestrator) WorkloadSSHAddress(ctx context.Context, name string) (string, int, error) {
	inspect, err := d.client.ContainerInspect(ctx, name)
	if err != nil {
		return "", 0, fmt.Errorf("inspect container %s: %w", name, err)
	}

	if _, err := os.Stat("/.dockerenv"); err == nil {
		if ep, ok := inspect.NetworkSettings.Networks[networkName]; ok && ep.IPAddress != "" {
			return ep.IPAddress, 22, nil
		}
	}
	if bindings, ok := inspect.NetworkSettings.Ports["22/tcp"]; ok && len(bindings) > 0 {
		port := 0
		fmt.Sscanf(bindings[0].HostPort, "%d", &port)
		if port > 0 {
			return "127.0.0.1", port, nil
		}
	}
	if ep, ok := inspect.NetworkSettings.Networks[networkName]; ok && ep.IPAddress != "" {
		return ep.IPAddress, 22, nil
	}
	return "", 0, fmt.Errorf("cannot determine SSH address for %s", name)
}

// --- internals ---

func (d *DockerOrchestrator) applyVolumesDocker(ctx context.Context, vols []VolumeMount) error {
	seen := map[string]bool{}
	for _, vol := range vols {
		if seen[vol.Name] {
			continue
		}
		seen[vol.Name] = true
		labels := map[string]string{"managed-by": labelManagedBy}
		if vol.Shared {
			labels["type"] = "shared-folder"
		}
		// VolumeCreate is idempotent on Docker — it returns the existing
		// volume if one with the same name already exists.
		if _, err := d.client.VolumeCreate(ctx, volume.CreateOptions{Name: vol.Name, Labels: labels}); err != nil {
			return fmt.Errorf("create volume %s: %w", vol.Name, err)
		}
	}
	return nil
}

func (d *DockerOrchestrator) buildContainerConfig(spec WorkloadSpec) (*container.Config, *container.HostConfig, *network.NetworkingConfig) {
	env := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	mounts := make([]mount.Mount, 0, len(spec.Volumes)+len(spec.EmptyDirs))
	for _, vol := range spec.Volumes {
		m := mount.Mount{
			Type:     mount.TypeVolume,
			Source:   vol.Name,
			Target:   vol.MountPath,
			ReadOnly: vol.ReadOnly,
		}
		if vol.SubPath != "" {
			m.VolumeOptions = &mount.VolumeOptions{Subpath: vol.SubPath}
		}
		mounts = append(mounts, m)
	}
	// EmptyDirs map to tmpfs mounts so they share pod-lifetime semantics with
	// K8s's EmptyDir{Medium: Memory}. Disk-backed EmptyDirs aren't used today;
	// when needed, switch on ed.Medium and create a named volume scoped to the
	// container lifetime instead.
	for _, ed := range spec.EmptyDirs {
		opts := map[string]string{}
		if ed.SizeLimit != "" {
			if size, err := units.RAMInBytes(strings.ReplaceAll(ed.SizeLimit, "i", "")); err == nil {
				opts["size"] = fmt.Sprintf("%d", size)
			}
		}
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeTmpfs,
			Target: ed.MountPath,
			TmpfsOptions: &mount.TmpfsOptions{
				SizeBytes: tmpfsSize(ed.SizeLimit),
			},
		})
	}

	exposed := nat.PortSet{}
	bindings := nat.PortMap{}
	for _, p := range spec.Ports {
		key := nat.Port(fmt.Sprintf("%d/tcp", p.ContainerPort))
		exposed[key] = struct{}{}
		// Only port 22 needs publishing; CDP/VNC stay loopback-only inside the
		// container per the SSH-tunnel design. Publishing 22 lets the host
		// reach SSH when the control plane runs outside Docker.
		if p.ContainerPort == 22 {
			bindings[key] = []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}}
		}
	}

	var nanoCPUs, memLimit int64
	if spec.Resources.CPULimit != "" {
		nanoCPUs = parseCPUToNanoCPUs(spec.Resources.CPULimit)
	}
	if spec.Resources.MemoryLimit != "" {
		memLimit = parseMemoryToBytes(spec.Resources.MemoryLimit)
	}

	shmSize, _ := units.RAMInBytes("2g")

	cc := &container.Config{
		Image:        spec.Image,
		Hostname:     spec.Hostname,
		Cmd:          spec.Command,
		Env:          env,
		Labels:       mergeLabels(spec.Labels, map[string]string{"managed-by": labelManagedBy, "instance": spec.Name}),
		ExposedPorts: exposed,
	}
	if probe := spec.Probes.Liveness; probe != nil {
		cc.Healthcheck = &container.HealthConfig{
			Test:          []string{"CMD-SHELL", fmt.Sprintf("bash -c '>/dev/tcp/127.0.0.1/%d'", probe.Port)},
			Interval:      probe.Period,
			Timeout:       10 * time.Second,
			Retries:       3,
			StartInterval: probe.InitialDelay,
		}
	}

	hc := &container.HostConfig{
		Privileged: spec.Security.Privileged,
		Mounts:     mounts,
		ShmSize:    shmSize,
		Resources: container.Resources{
			NanoCPUs: nanoCPUs,
			Memory:   memLimit,
		},
		PortBindings:  bindings,
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			networkName: {},
		},
	}
	return cc, hc, netCfg
}

func (d *DockerOrchestrator) runPostStartExec(ctx context.Context, containerID string, ic InitContainerSpec) error {
	cmd := ic.Command
	if len(cmd) == 0 {
		return nil
	}
	resp, err := d.client.ContainerExecCreate(ctx, containerID, container.ExecOptions{Cmd: cmd})
	if err != nil {
		return fmt.Errorf("exec create %s: %w", ic.Name, err)
	}
	if err := d.client.ContainerExecStart(ctx, resp.ID, container.ExecStartOptions{}); err != nil {
		return fmt.Errorf("exec start %s: %w", ic.Name, err)
	}
	return nil
}

func tmpfsSize(s string) int64 {
	if s == "" {
		return 0
	}
	if n, err := units.RAMInBytes(strings.ReplaceAll(s, "i", "")); err == nil {
		return n
	}
	return 0
}
