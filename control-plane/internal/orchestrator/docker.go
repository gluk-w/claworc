package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/docker/go-units"
	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

const (
	labelManagedBy = "claworc"
	networkName    = "claworc"
)

var volumeSuffixes = []string{"homebrew", "home"}

type DockerOrchestrator struct {
	client          *dockerclient.Client
	available       bool
	InstanceFactory sshproxy.InstanceFactory
}

func (d *DockerOrchestrator) Initialize(ctx context.Context) error {
	var opts []dockerclient.Opt
	opts = append(opts, dockerclient.FromEnv)
	opts = append(opts, dockerclient.WithAPIVersionNegotiation())
	if config.Cfg.DockerHost != "" {
		opts = append(opts, dockerclient.WithHost(config.Cfg.DockerHost))
	}

	var err error
	d.client, err = dockerclient.NewClientWithOpts(opts...)
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}

	_, err = d.client.Ping(ctx)
	if err != nil {
		return fmt.Errorf("docker ping: %w", err)
	}

	if err := d.ensureNetwork(ctx); err != nil {
		return fmt.Errorf("docker network: %w", err)
	}

	d.available = true
	log.Println("Docker daemon connected")
	return nil
}

func (d *DockerOrchestrator) ensureNetwork(ctx context.Context) error {
	_, err := d.client.NetworkInspect(ctx, networkName, network.InspectOptions{})
	if err == nil {
		return nil
	}
	_, err = d.client.NetworkCreate(ctx, networkName, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{"managed-by": labelManagedBy},
	})
	if err != nil {
		return fmt.Errorf("create network %s: %w", networkName, err)
	}
	log.Printf("Created Docker network: %s", networkName)
	return nil
}

func (d *DockerOrchestrator) IsAvailable(_ context.Context) bool {
	return d.available
}

func (d *DockerOrchestrator) BackendName() string {
	return "docker"
}

func (d *DockerOrchestrator) volumeName(name, suffix string) string {
	return fmt.Sprintf("claworc-%s-%s", name, suffix)
}

func parseCPUToNanoCPUs(cpuStr string) int64 {
	if strings.HasSuffix(cpuStr, "m") {
		val := cpuStr[:len(cpuStr)-1]
		var n int64
		fmt.Sscanf(val, "%d", &n)
		return n * 1_000_000
	}
	var f float64
	fmt.Sscanf(cpuStr, "%f", &f)
	return int64(f * 1_000_000_000)
}

func parseMemoryToBytes(memStr string) int64 {
	unitMap := map[string]int64{
		"Ki": 1024,
		"Mi": 1024 * 1024,
		"Gi": 1024 * 1024 * 1024,
		"Ti": 1024 * 1024 * 1024 * 1024,
		"K":  1000,
		"M":  1000 * 1000,
		"G":  1000 * 1000 * 1000,
		"T":  1000 * 1000 * 1000 * 1000,
	}
	for suffix, multiplier := range unitMap {
		if strings.HasSuffix(memStr, suffix) {
			val := memStr[:len(memStr)-len(suffix)]
			var n int64
			fmt.Sscanf(val, "%d", &n)
			return n * multiplier
		}
	}
	var n int64
	fmt.Sscanf(memStr, "%d", &n)
	return n
}

func (d *DockerOrchestrator) ensureImage(ctx context.Context, img string) error {
	// Check if image exists locally first
	_, _, err := d.client.ImageInspectWithRaw(ctx, img)
	if err == nil {
		log.Printf("Image %s found locally", img)
		return nil
	}

	// Image not found locally, try to pull
	log.Printf("Image %s not found locally, pulling...", img)
	reader, err := d.client.ImagePull(ctx, img, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %s: %w", img, err)
	}
	defer reader.Close()
	io.Copy(io.Discard, reader)
	log.Printf("Image %s pulled successfully", img)
	return nil
}

func (d *DockerOrchestrator) CreateInstance(ctx context.Context, params CreateParams) error {
	progress := params.OnProgress
	if progress == nil {
		progress = func(string) {}
	}

	progress("Pulling image...")
	if err := d.ensureImage(ctx, params.ContainerImage); err != nil {
		return err
	}

	// Create volumes
	progress("Creating volumes...")
	for _, suffix := range volumeSuffixes {
		volName := d.volumeName(params.Name, suffix)
		_, err := d.client.VolumeCreate(ctx, volume.CreateOptions{
			Name:   volName,
			Labels: map[string]string{"managed-by": labelManagedBy, "instance": params.Name},
		})
		if err != nil {
			log.Printf("Volume %s may already exist: %v", utils.SanitizeForLog(volName), err)
		}
	}

	// Create shared folder volumes
	for _, sfm := range params.SharedFolderMounts {
		volName := fmt.Sprintf("claworc-shared-%d", sfm.VolumeID)
		_, err := d.client.VolumeCreate(ctx, volume.CreateOptions{
			Name:   volName,
			Labels: map[string]string{"managed-by": labelManagedBy, "type": "shared-folder"},
		})
		if err != nil {
			log.Printf("Shared volume %s may already exist: %v", volName, err)
		}
	}

	progress("Creating container...")
	return d.createContainer(ctx, params)
}

func (d *DockerOrchestrator) CloneVolumes(ctx context.Context, srcName, dstName string) error {
	// Stop destination container while we copy data into its volumes
	timeout := 30
	d.client.ContainerStop(ctx, dstName, container.StopOptions{Timeout: &timeout})

	for _, suffix := range volumeSuffixes {
		srcVol := d.volumeName(srcName, suffix)
		dstVol := d.volumeName(dstName, suffix)
		if err := d.copyVolume(ctx, srcVol, dstVol); err != nil {
			// Best-effort: restart destination even on error
			d.client.ContainerStart(ctx, dstName, container.StartOptions{})
			return fmt.Errorf("copy volume %s: %w", suffix, err)
		}
	}

	return d.client.ContainerStart(ctx, dstName, container.StartOptions{})
}

func (d *DockerOrchestrator) copyVolume(ctx context.Context, srcVol, dstVol string) error {
	_ = d.ensureImage(ctx, "alpine:latest")

	containerCfg := &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sh", "-c", "cp -a /src/. /dst/"},
	}
	hostCfg := &container.HostConfig{
		Mounts: []mount.Mount{
			{Type: mount.TypeVolume, Source: srcVol, Target: "/src", ReadOnly: true},
			{Type: mount.TypeVolume, Source: dstVol, Target: "/dst"},
		},
	}

	resp, err := d.client.ContainerCreate(ctx, containerCfg, hostCfg, nil, nil, "")
	if err != nil {
		return fmt.Errorf("create copy container: %w", err)
	}
	defer d.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})

	if err := d.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start copy container: %w", err)
	}

	statusCh, errCh := d.client.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("wait for copy container: %w", err)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			return fmt.Errorf("copy failed with exit code %d", status.StatusCode)
		}
	}
	return nil
}

func (d *DockerOrchestrator) DeleteInstance(ctx context.Context, name string) error {
	// Remove container
	err := d.client.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
	if err != nil && !dockerclient.IsErrNotFound(err) {
		log.Printf("Remove container %s: %v", utils.SanitizeForLog(name), err)
	}

	// Remove volumes
	for _, suffix := range volumeSuffixes {
		volName := d.volumeName(name, suffix)
		if err := d.client.VolumeRemove(ctx, volName, true); err != nil && !dockerclient.IsErrNotFound(err) {
			log.Printf("Remove volume %s: %v", utils.SanitizeForLog(volName), err)
		}
	}
	return nil
}

func (d *DockerOrchestrator) DeleteSharedVolume(ctx context.Context, folderID uint) error {
	volName := fmt.Sprintf("claworc-shared-%d", folderID)
	if err := d.client.VolumeRemove(ctx, volName, true); err != nil && !dockerclient.IsErrNotFound(err) {
		return fmt.Errorf("remove shared volume %s: %w", volName, err)
	}
	return nil
}

func (d *DockerOrchestrator) StartInstance(ctx context.Context, name string) error {
	return d.client.ContainerStart(ctx, name, container.StartOptions{})
}

func (d *DockerOrchestrator) StopInstance(ctx context.Context, name string) error {
	timeout := 30
	return d.client.ContainerStop(ctx, name, container.StopOptions{Timeout: &timeout})
}

func (d *DockerOrchestrator) RestartInstance(ctx context.Context, name string, params CreateParams) error {
	// Stop and remove the container, then recreate it so mount changes take effect
	timeout := 30
	if err := d.client.ContainerStop(ctx, name, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stop container %s: %w", name, err)
	}
	if err := d.client.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}); err != nil && !dockerclient.IsErrNotFound(err) {
		return fmt.Errorf("remove container %s: %w", name, err)
	}

	// Ensure shared folder volumes exist
	for _, sfm := range params.SharedFolderMounts {
		volName := fmt.Sprintf("claworc-shared-%d", sfm.VolumeID)
		_, err := d.client.VolumeCreate(ctx, volume.CreateOptions{
			Name:   volName,
			Labels: map[string]string{"managed-by": labelManagedBy, "type": "shared-folder"},
		})
		if err != nil {
			log.Printf("Shared volume %s may already exist: %v", volName, err)
		}
	}

	return d.createContainer(ctx, params)
}

func (d *DockerOrchestrator) UpdateImage(ctx context.Context, name string, params CreateParams) error {
	// Force-pull the latest image (bypass local cache)
	log.Printf("Force-pulling image %s for instance %s", params.ContainerImage, utils.SanitizeForLog(name))
	reader, err := d.client.ImagePull(ctx, params.ContainerImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %s: %w", params.ContainerImage, err)
	}
	defer reader.Close()
	io.Copy(io.Discard, reader)
	log.Printf("Image %s pulled successfully", params.ContainerImage)

	// Stop and remove the old container (volumes are preserved)
	timeout := 30
	d.client.ContainerStop(ctx, name, container.StopOptions{Timeout: &timeout})
	if err := d.client.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}); err != nil && !dockerclient.IsErrNotFound(err) {
		return fmt.Errorf("remove container %s: %w", name, err)
	}

	// Recreate the container with the same config but fresh image
	return d.createContainer(ctx, params)
}

// createContainer builds and starts a container from CreateParams (without pulling or creating volumes).
func (d *DockerOrchestrator) createContainer(ctx context.Context, params CreateParams) error {
	var env []string
	if parts := strings.SplitN(params.VNCResolution, "x", 2); len(parts) == 2 {
		env = append(env, "DISPLAY_WIDTH="+parts[0], "DISPLAY_HEIGHT="+parts[1])
	}
	for k, v := range params.EnvVars {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	if params.Timezone != "" {
		env = append(env, fmt.Sprintf("TZ=%s", params.Timezone))
	}
	if params.UserAgent != "" {
		env = append(env, fmt.Sprintf("CHROMIUM_USER_AGENT=%s", params.UserAgent))
	}

	mounts := []mount.Mount{
		{Type: mount.TypeVolume, Source: d.volumeName(params.Name, "homebrew"), Target: "/home/linuxbrew/.linuxbrew"},
		{Type: mount.TypeVolume, Source: d.volumeName(params.Name, "home"), Target: "/home/claworc"},
	}
	for _, sfm := range params.SharedFolderMounts {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeVolume,
			Source: fmt.Sprintf("claworc-shared-%d", sfm.VolumeID),
			Target: sfm.MountPath,
		})
	}

	var nanoCPUs int64
	var memLimit int64
	if params.CPULimit != "" {
		nanoCPUs = parseCPUToNanoCPUs(params.CPULimit)
	}
	if params.MemoryLimit != "" {
		memLimit = parseMemoryToBytes(params.MemoryLimit)
	}

	shmSize, _ := units.RAMInBytes("2g")

	containerCfg := &container.Config{
		Image:    params.ContainerImage,
		Hostname: strings.TrimPrefix(params.Name, "bot-"),
		Env:      env,
		Labels:   map[string]string{"managed-by": labelManagedBy, "instance": params.Name},
		ExposedPorts: nat.PortSet{
			"22/tcp": struct{}{},
		},
		Healthcheck: &container.HealthConfig{
			Test:          []string{"CMD-SHELL", "bash -c '>/dev/tcp/127.0.0.1/22'"},
			Interval:      30_000_000_000,
			Timeout:       10_000_000_000,
			Retries:       3,
			StartInterval: 60_000_000_000,
		},
	}

	hostCfg := &container.HostConfig{
		Privileged: false,
		Mounts:     mounts,
		ShmSize:    shmSize,
		Resources: container.Resources{
			NanoCPUs: nanoCPUs,
			Memory:   memLimit,
		},
		PortBindings: nat.PortMap{
			"22/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}},
		},
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			networkName: {},
		},
	}

	resp, err := d.client.ContainerCreate(ctx, containerCfg, hostCfg, netCfg, nil, params.Name)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}

	if err := d.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return err
	}

	// Fix ownership of shared folder mounts so the claworc user (1000:1000) can write to them.
	// New Docker volumes are owned by root by default.
	for _, sfm := range params.SharedFolderMounts {
		execCfg := container.ExecOptions{
			Cmd: []string{"chown", "claworc:claworc", sfm.MountPath},
		}
		idResp, err := d.client.ContainerExecCreate(ctx, resp.ID, execCfg)
		if err != nil {
			log.Printf("Failed to create chown exec for %s: %v", sfm.MountPath, err)
			continue
		}
		if err := d.client.ContainerExecStart(ctx, idResp.ID, container.ExecStartOptions{}); err != nil {
			log.Printf("Failed to chown %s: %v", sfm.MountPath, err)
		}
	}

	return nil
}

func (d *DockerOrchestrator) GetInstanceStatus(ctx context.Context, name string) (string, error) {
	inspect, err := d.client.ContainerInspect(ctx, name)
	if err != nil {
		if dockerclient.IsErrNotFound(err) {
			return "stopped", nil
		}
		return "error", nil
	}

	status := inspect.State.Status
	health := ""
	if inspect.State.Health != nil {
		health = inspect.State.Health.Status
	}

	switch status {
	case "running":
		switch health {
		case "healthy":
			return "running", nil
		case "unhealthy":
			return "error", nil
		default:
			return "creating", nil
		}
	case "created", "restarting":
		return "creating", nil
	case "exited", "dead", "paused", "removing":
		return "stopped", nil
	default:
		return "stopped", nil
	}
}

func (d *DockerOrchestrator) GetInstanceImageInfo(ctx context.Context, name string) (string, error) {
	inspect, err := d.client.ContainerInspect(ctx, name)
	if err != nil {
		if dockerclient.IsErrNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("inspect container: %w", err)
	}
	tag := inspect.Config.Image
	sha := inspect.Image
	if len(sha) > 19 { // "sha256:" (7) + 12 chars
		sha = sha[:19]
	}
	return fmt.Sprintf("%s (%s)", tag, sha), nil
}

func (d *DockerOrchestrator) ConfigureSSHAccess(ctx context.Context, instanceID uint, publicKey string) error {
	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return fmt.Errorf("instance %d not found: %w", instanceID, err)
	}
	return configureSSHAccess(ctx, d.ExecInInstance, inst.Name, publicKey)
}

func (d *DockerOrchestrator) GetSSHAddress(ctx context.Context, instanceID uint) (string, int, error) {
	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return "", 0, fmt.Errorf("instance %d not found: %w", instanceID, err)
	}
	inspect, err := d.client.ContainerInspect(ctx, inst.Name)
	if err != nil {
		return "", 0, fmt.Errorf("inspect container for instance %d: %w", instanceID, err)
	}

	// Detect whether the control-plane itself is running inside a Docker container.
	// /.dockerenv is created by the Docker runtime in every container.
	runningInDocker := false
	if _, err := os.Stat("/.dockerenv"); err == nil {
		runningInDocker = true
	}

	// Inside Docker: use the container IP on the claworc bridge network for
	// direct container-to-container communication (no port mapping needed).
	if runningInDocker {
		if ep, ok := inspect.NetworkSettings.Networks[networkName]; ok && ep.IPAddress != "" {
			return ep.IPAddress, 22, nil
		}
	}

	// On the host (e.g. macOS / Windows): Docker bridge IPs are not routable
	// from the host OS, so use the published host port on the loopback instead.
	if bindings, ok := inspect.NetworkSettings.Ports["22/tcp"]; ok && len(bindings) > 0 {
		port := 0
		fmt.Sscanf(bindings[0].HostPort, "%d", &port)
		if port > 0 {
			return "127.0.0.1", port, nil
		}
	}

	// Fallback: on Linux hosts bridge IPs are routable from the host, so the
	// container IP still works even when we're not inside Docker ourselves.
	if ep, ok := inspect.NetworkSettings.Networks[networkName]; ok && ep.IPAddress != "" {
		return ep.IPAddress, 22, nil
	}

	return "", 0, fmt.Errorf("cannot determine SSH address for instance %d", instanceID)
}

func (d *DockerOrchestrator) UpdateResources(ctx context.Context, name string, params UpdateResourcesParams) error {
	updateCfg := container.UpdateConfig{
		Resources: container.Resources{
			NanoCPUs: parseCPUToNanoCPUs(params.CPULimit),
			Memory:   parseMemoryToBytes(params.MemoryLimit),
		},
	}
	_, err := d.client.ContainerUpdate(ctx, name, updateCfg)
	return err
}

func (d *DockerOrchestrator) GetContainerStats(ctx context.Context, name string) (*ContainerStats, error) {
	resp, err := d.client.ContainerStatsOneShot(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("container stats: %w", err)
	}
	defer resp.Body.Close()

	var statsJSON dockerStatsJSON
	if err := json.NewDecoder(resp.Body).Decode(&statsJSON); err != nil {
		return nil, fmt.Errorf("decode stats: %w", err)
	}

	// CPU usage calculation (same formula as docker stats CLI)
	cpuDelta := float64(statsJSON.CPUStats.CPUUsage.TotalUsage - statsJSON.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(statsJSON.CPUStats.SystemCPUUsage - statsJSON.PreCPUStats.SystemCPUUsage)
	numCPUs := float64(statsJSON.CPUStats.OnlineCPUs)
	if numCPUs == 0 {
		numCPUs = float64(len(statsJSON.CPUStats.CPUUsage.PercpuUsage))
	}

	var cpuCores float64
	if systemDelta > 0 && numCPUs > 0 {
		cpuCores = (cpuDelta / systemDelta) * numCPUs
	}
	cpuMillicores := int64(cpuCores * 1000)

	memUsage := statsJSON.MemoryStats.Usage
	memLimit := statsJSON.MemoryStats.Limit

	var cpuPercent float64
	if memLimit > 0 && statsJSON.CPUStats.CPUUsage.TotalUsage > 0 {
		// Calculate CPU % of limit using NanoCPUs from container config
		inspect, err := d.client.ContainerInspect(ctx, name)
		if err == nil && inspect.HostConfig.NanoCPUs > 0 {
			limitCores := float64(inspect.HostConfig.NanoCPUs) / 1e9
			cpuPercent = (cpuCores / limitCores) * 100
		}
	}

	return &ContainerStats{
		CPUUsageMillicores: cpuMillicores,
		CPUUsagePercent:    cpuPercent,
		MemoryUsageBytes:   int64(memUsage),
		MemoryLimitBytes:   int64(memLimit),
	}, nil
}

type dockerStatsJSON struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage  uint64   `json:"total_usage"`
			PercpuUsage []uint64 `json:"percpu_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     uint32 `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
	} `json:"memory_stats"`
}

func (d *DockerOrchestrator) UpdateInstanceConfig(ctx context.Context, name string, configJSON string) error {
	return updateInstanceConfig(ctx, d.ExecInInstance, d.InstanceFactory, name, configJSON)
}

func stripDockerLogHeaders(data []byte) string {
	// Docker multiplexed log format: [stream_type(1)][0(3)][size(4)][payload]
	// If the data starts with a valid header byte (0, 1, or 2), try to strip
	var result strings.Builder
	for len(data) > 0 {
		if len(data) >= 8 && (data[0] == 0 || data[0] == 1 || data[0] == 2) {
			size := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
			data = data[8:]
			if size > 0 && size <= len(data) {
				result.Write(data[:size])
				data = data[size:]
			} else {
				result.Write(data)
				break
			}
		} else {
			result.Write(data)
			break
		}
	}
	return result.String()
}

func (d *DockerOrchestrator) ExecInInstance(ctx context.Context, name string, cmd []string) (string, string, int, error) {
	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := d.client.ContainerExecCreate(ctx, name, execCfg)
	if err != nil {
		return "", "", -1, fmt.Errorf("exec create: %w", err)
	}

	resp, err := d.client.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", "", -1, fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	output, err := io.ReadAll(resp.Reader)
	if err != nil {
		return "", "", -1, fmt.Errorf("read exec output: %w", err)
	}

	// Get exit code
	inspectResp, err := d.client.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return string(output), "", -1, fmt.Errorf("exec inspect: %w", err)
	}

	// Docker exec with demux=false returns multiplexed output
	// For simplicity, treat all output as stdout
	cleaned := stripDockerLogHeaders(output)
	return cleaned, "", inspectResp.ExitCode, nil
}

func (d *DockerOrchestrator) StreamExecInInstance(ctx context.Context, name string, cmd []string, stdout io.Writer) (string, int, error) {
	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := d.client.ContainerExecCreate(ctx, name, execCfg)
	if err != nil {
		return "", -1, fmt.Errorf("exec create: %w", err)
	}

	resp, err := d.client.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", -1, fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	var stderrBuf strings.Builder
	if err := demuxDockerStream(resp.Reader, stdout, &stderrBuf); err != nil {
		return stderrBuf.String(), -1, fmt.Errorf("stream exec output: %w", err)
	}

	inspectResp, err := d.client.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return stderrBuf.String(), -1, fmt.Errorf("exec inspect: %w", err)
	}

	return stderrBuf.String(), inspectResp.ExitCode, nil
}

// demuxDockerStream reads Docker's multiplexed stream format and routes
// stdout (stream type 1) to stdoutW and stderr (stream type 2) to stderrW.
func demuxDockerStream(reader io.Reader, stdoutW io.Writer, stderrW io.Writer) error {
	header := make([]byte, 8)
	for {
		_, err := io.ReadFull(reader, header)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		streamType := header[0]
		size := int(header[4])<<24 | int(header[5])<<16 | int(header[6])<<8 | int(header[7])
		if size == 0 {
			continue
		}
		var dst io.Writer
		switch streamType {
		case 1:
			dst = stdoutW
		case 2:
			dst = stderrW
		default:
			dst = stdoutW
		}
		if _, err := io.CopyN(dst, reader, int64(size)); err != nil {
			return err
		}
	}
}

// Ensure DockerOrchestrator implements ContainerOrchestrator
var _ ContainerOrchestrator = (*DockerOrchestrator)(nil)
