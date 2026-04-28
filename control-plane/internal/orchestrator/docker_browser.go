package orchestrator

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/go-connections/nat"
	"github.com/docker/go-units"
	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// dockerLookupInstanceName fetches the K8s-safe name from the DB for an
// instance ID. Mirrors KubernetesOrchestrator.lookupInstanceName.
func (d *DockerOrchestrator) lookupInstanceName(instanceID uint) (string, error) {
	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return "", fmt.Errorf("instance %d not found: %w", instanceID, err)
	}
	return inst.Name, nil
}

func dockerBrowserContainerName(instanceName string) string {
	return instanceName + "-browser"
}

func dockerBrowserVolumeName(instanceName string) string {
	return fmt.Sprintf("claworc-%s-browser", instanceName)
}

// EnsureBrowserPod creates (or starts) a browser sidecar container for the
// given instance. The container joins the shared claworc bridge so the
// control plane can reach it by container name, but no host ports are
// exposed.
func (d *DockerOrchestrator) EnsureBrowserPod(ctx context.Context, instanceID uint, params BrowserPodParams) (BrowserPodEndpoint, error) {
	name := params.Name
	if name == "" {
		var err error
		name, err = d.lookupInstanceName(instanceID)
		if err != nil {
			return BrowserPodEndpoint{}, err
		}
	}
	containerName := dockerBrowserContainerName(name)

	if err := d.ensureImage(ctx, params.Image); err != nil {
		return BrowserPodEndpoint{}, err
	}

	// Ensure browser-data volume.
	volName := dockerBrowserVolumeName(name)
	if _, err := d.client.VolumeCreate(ctx, volume.CreateOptions{
		Name:   volName,
		Labels: map[string]string{"managed-by": labelManagedBy, "instance": name, "claworc-role": "browser"},
	}); err != nil {
		// VolumeCreate is idempotent in practice; ignore name-collision errors.
		if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return BrowserPodEndpoint{}, fmt.Errorf("create browser volume %s: %w", volName, err)
		}
	}

	// If a container already exists, try to start it instead of recreating —
	// but only if it has the host port bindings we now require. Containers
	// created before port publishing was added (or with stale config) are
	// removed so the create path below can recreate them with PortBindings.
	if existing, err := d.client.ContainerInspect(ctx, containerName); err == nil {
		if browserContainerHasRequiredBindings(existing.HostConfig) {
			if !existing.State.Running {
				if err := d.client.ContainerStart(ctx, containerName, container.StartOptions{}); err != nil {
					return BrowserPodEndpoint{}, fmt.Errorf("start browser container: %w", err)
				}
			}
			return d.lookupBrowserEndpoint(ctx, containerName)
		}
		if err := d.client.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true}); err != nil {
			return BrowserPodEndpoint{}, fmt.Errorf("recreate browser container %s: %w", containerName, err)
		}
	}

	// Otherwise create it.
	envVars := []string{}
	if parts := strings.SplitN(params.VNCResolution, "x", 2); len(parts) == 2 {
		envVars = append(envVars, "DISPLAY_WIDTH="+parts[0], "DISPLAY_HEIGHT="+parts[1])
	}
	if params.Timezone != "" {
		envVars = append(envVars, "TZ="+params.Timezone)
	}
	if params.UserAgent != "" {
		envVars = append(envVars, "CHROMIUM_USER_AGENT="+params.UserAgent)
	}
	for k, v := range params.EnvVars {
		envVars = append(envVars, fmt.Sprintf("%s=%s", k, v))
	}

	shmSize, _ := units.RAMInBytes("2g")

	cfg := &container.Config{
		Image: params.Image,
		Env:   envVars,
		Labels: map[string]string{
			"managed-by":   labelManagedBy,
			"instance":     name,
			"claworc-role": "browser",
		},
		ExposedPorts: nat.PortSet{
			"9222/tcp": struct{}{},
			"3000/tcp": struct{}{},
		},
	}
	// Publish CDP/noVNC ports to 127.0.0.1 with auto-assigned host ports so the
	// control plane can reach the browser pod even when running natively
	// outside the claworc Docker bridge network (e.g. `make dev` on macOS).
	// Mount the agent's home volume into the browser container with a Subpath
	// of "Downloads" so files Chromium saves at /home/claworc/Downloads land
	// directly on the agent's home volume — visible to OpenClaw and the agent
	// terminal without copying. Docker requires the Subpath to exist before
	// mount; pre-create it (claworc:claworc) in case the agent hasn't run
	// init-setup yet.
	agentHomeVol := d.volumeName(name, "home")
	if err := d.runOnVolume(ctx, agentHomeVol, []string{"sh", "-c", "mkdir -p /vol/Downloads && chown 1000:1000 /vol/Downloads"}); err != nil {
		return BrowserPodEndpoint{}, fmt.Errorf("ensure Downloads subpath on %s: %w", agentHomeVol, err)
	}
	hostCfg := &container.HostConfig{
		Mounts: []mount.Mount{
			{Type: mount.TypeVolume, Source: volName, Target: "/home/claworc/chrome-data"},
			{
				Type:          mount.TypeVolume,
				Source:        agentHomeVol,
				Target:        "/home/claworc/Downloads",
				VolumeOptions: &mount.VolumeOptions{Subpath: "Downloads"},
			},
		},
		ShmSize: shmSize,
		PortBindings: nat.PortMap{
			"9222/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}},
			"3000/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}},
		},
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
	}
	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			networkName: {NetworkID: networkName},
		},
	}
	if _, err := d.client.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, containerName); err != nil {
		return BrowserPodEndpoint{}, fmt.Errorf("create browser container: %w", err)
	}
	if err := d.client.ContainerStart(ctx, containerName, container.StartOptions{}); err != nil {
		return BrowserPodEndpoint{}, fmt.Errorf("start browser container: %w", err)
	}
	return d.lookupBrowserEndpoint(ctx, containerName)
}

// lookupBrowserEndpoint returns the host-published CDP and noVNC ports for a
// running browser container. We publish on 127.0.0.1 with auto-assigned host
// ports so multiple instances don't clash; this resolves them from the live
// container state.
func (d *DockerOrchestrator) lookupBrowserEndpoint(ctx context.Context, containerName string) (BrowserPodEndpoint, error) {
	info, err := d.client.ContainerInspect(ctx, containerName)
	if err != nil {
		return BrowserPodEndpoint{}, fmt.Errorf("inspect browser container %s: %w", containerName, err)
	}
	cdp, err := publishedPort(info.NetworkSettings.Ports, "9222/tcp")
	if err != nil {
		return BrowserPodEndpoint{}, fmt.Errorf("browser %s: %w", containerName, err)
	}
	vnc, err := publishedPort(info.NetworkSettings.Ports, "3000/tcp")
	if err != nil {
		return BrowserPodEndpoint{}, fmt.Errorf("browser %s: %w", containerName, err)
	}
	return BrowserPodEndpoint{Host: "127.0.0.1", CDPPort: cdp, VNCPort: vnc}, nil
}

// CloneBrowserVolume copies the on-demand browser profile volume from src to
// dst. Skips silently if the source volume doesn't exist (browser never
// launched on src).
//
// Chromium's Singleton{Lock,Cookie,Socket} files are profile-and-host scoped:
// they're symlinks like "<hostname>-<pid>" plus a Unix socket under /tmp.
// When we copy them into a brand-new container with a different hostname,
// every Chromium launch aborts with "profile appears to be in use", trapping
// us in the svc-desktop respawn loop. We strip them here as part of the
// clone so the cloned profile boots cleanly the first time.
func (d *DockerOrchestrator) CloneBrowserVolume(ctx context.Context, srcInstanceName, dstInstanceName string) error {
	srcVol := dockerBrowserVolumeName(srcInstanceName)
	dstVol := dockerBrowserVolumeName(dstInstanceName)
	if _, err := d.client.VolumeInspect(ctx, srcVol); err != nil {
		return nil
	}
	if _, err := d.client.VolumeCreate(ctx, volume.CreateOptions{
		Name:   dstVol,
		Labels: map[string]string{"managed-by": labelManagedBy, "instance": dstInstanceName, "claworc-role": "browser"},
	}); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return fmt.Errorf("create dst browser volume %s: %w", dstVol, err)
	}
	if err := d.copyVolume(ctx, srcVol, dstVol); err != nil {
		return err
	}
	return d.runOnVolume(ctx, dstVol, []string{"sh", "-c", "rm -f /vol/SingletonLock /vol/SingletonCookie /vol/SingletonSocket"})
}

// runOnVolume launches a one-shot alpine container that mounts the given
// volume at /vol and runs cmd. Used by CloneBrowserVolume to scrub
// host-specific files from a freshly-copied profile.
func (d *DockerOrchestrator) runOnVolume(ctx context.Context, vol string, cmd []string) error {
	_ = d.ensureImage(ctx, "alpine:latest")

	containerCfg := &container.Config{Image: "alpine:latest", Cmd: cmd}
	hostCfg := &container.HostConfig{
		Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: vol, Target: "/vol"}},
	}
	resp, err := d.client.ContainerCreate(ctx, containerCfg, hostCfg, nil, nil, "")
	if err != nil {
		return fmt.Errorf("create scrub container: %w", err)
	}
	defer d.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})

	if err := d.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start scrub container: %w", err)
	}
	statusCh, errCh := d.client.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("wait for scrub container: %w", err)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			return fmt.Errorf("scrub container exited with status %d", status.StatusCode)
		}
	}
	return nil
}

// browserContainerHasRequiredBindings returns true if the container is
// configured to publish CDP and noVNC ports to the host. Containers created
// before this requirement landed lack these bindings and must be recreated.
func browserContainerHasRequiredBindings(hostCfg *container.HostConfig) bool {
	if hostCfg == nil {
		return false
	}
	for _, p := range []nat.Port{"9222/tcp", "3000/tcp"} {
		if len(hostCfg.PortBindings[p]) == 0 {
			return false
		}
	}
	return true
}

func publishedPort(ports nat.PortMap, key nat.Port) (int, error) {
	bindings := ports[key]
	for _, b := range bindings {
		if b.HostPort == "" {
			continue
		}
		p, err := strconv.Atoi(b.HostPort)
		if err == nil {
			return p, nil
		}
	}
	return 0, fmt.Errorf("port %s not published", key)
}

func (d *DockerOrchestrator) StopBrowserPod(ctx context.Context, instanceID uint) error {
	name, err := d.lookupInstanceName(instanceID)
	if err != nil {
		return err
	}
	timeout := 10
	if err := d.client.ContainerStop(ctx, dockerBrowserContainerName(name), container.StopOptions{Timeout: &timeout}); err != nil {
		// Tolerate "no such container" — pod already gone is success.
		if !strings.Contains(strings.ToLower(err.Error()), "no such") {
			return fmt.Errorf("stop browser container: %w", err)
		}
	}
	return nil
}

func (d *DockerOrchestrator) DeleteBrowserPod(ctx context.Context, instanceID uint) error {
	name, err := d.lookupInstanceName(instanceID)
	if err != nil {
		return err
	}
	containerName := dockerBrowserContainerName(name)
	if err := d.client.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true}); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "no such") {
			return fmt.Errorf("remove browser container: %w", err)
		}
	}
	if err := d.client.VolumeRemove(ctx, dockerBrowserVolumeName(name), true); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "no such") {
			return fmt.Errorf("remove browser volume: %w", err)
		}
	}
	return nil
}

func (d *DockerOrchestrator) GetBrowserPodStatus(ctx context.Context, instanceID uint) (string, error) {
	name, err := d.lookupInstanceName(instanceID)
	if err != nil {
		return "error", err
	}
	info, err := d.client.ContainerInspect(ctx, dockerBrowserContainerName(name))
	if err != nil {
		return "stopped", nil
	}
	if info.State == nil {
		return "stopped", nil
	}
	switch {
	case info.State.Running && info.State.Health != nil && info.State.Health.Status == "healthy":
		return "running", nil
	case info.State.Running:
		return "running", nil
	case info.State.Restarting:
		return "starting", nil
	case info.State.Status == "created":
		return "starting", nil
	case info.State.Dead || info.State.OOMKilled:
		return "error", nil
	default:
		return "stopped", nil
	}
}

func (d *DockerOrchestrator) GetBrowserPodEndpoint(ctx context.Context, instanceID uint) (BrowserPodEndpoint, error) {
	name, err := d.lookupInstanceName(instanceID)
	if err != nil {
		return BrowserPodEndpoint{}, err
	}
	return d.lookupBrowserEndpoint(ctx, dockerBrowserContainerName(name))
}
