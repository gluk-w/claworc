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
// Init containers in spec.InitContainers run BEFORE the main container starts,
// matching K8s init-container semantics: each init container is a one-shot
// helper that mounts the volumes named in its Mounts list, runs the command,
// exits, and only then is the main container created.
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

	for _, ic := range spec.InitContainers {
		if err := d.runInitContainer(ctx, ic); err != nil {
			return fmt.Errorf("init container %q for %s: %w", ic.Name, spec.Name, err)
		}
	}

	containerCfg, hostCfg, netCfg := d.buildContainerConfig(spec)
	resp, err := d.client.ContainerCreate(ctx, containerCfg, hostCfg, netCfg, nil, spec.Name)
	if err != nil {
		return fmt.Errorf("create container %s: %w", spec.Name, err)
	}
	if err := d.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container %s: %w", spec.Name, err)
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

// runInitContainer spawns a one-shot helper container that mounts the init
// spec's named volumes, runs the command, and waits for it to exit. Mirrors
// K8s init-container semantics for the limited cases we use (preparing
// SubPath directories, scrubbing host-scoped files on a freshly cloned
// volume). The image is pulled best-effort.
func (d *DockerOrchestrator) runInitContainer(ctx context.Context, ic InitContainerSpec) error {
	if len(ic.Command) == 0 {
		return nil
	}
	image := ic.Image
	if image == "" {
		image = "alpine:latest"
	}
	_ = d.ensureImage(ctx, image)

	mounts := make([]mount.Mount, 0, len(ic.Mounts))
	for _, m := range ic.Mounts {
		mt := mount.Mount{Type: mount.TypeVolume, Source: m.Name, Target: m.MountPath, ReadOnly: m.ReadOnly}
		if m.SubPath != "" {
			mt.VolumeOptions = &mount.VolumeOptions{Subpath: m.SubPath}
		}
		mounts = append(mounts, mt)
	}

	cfg := &container.Config{Image: image, Cmd: ic.Command}
	hostCfg := &container.HostConfig{Mounts: mounts}
	resp, err := d.client.ContainerCreate(ctx, cfg, hostCfg, nil, nil, "")
	if err != nil {
		return fmt.Errorf("create init container: %w", err)
	}
	defer d.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})

	if err := d.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start init container: %w", err)
	}
	statusCh, errCh := d.client.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("wait init container: %w", err)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			return fmt.Errorf("init container exited with status %d", status.StatusCode)
		}
	case <-ctx.Done():
		return ctx.Err()
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
