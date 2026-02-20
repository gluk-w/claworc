package orchestrator

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/go-units"
	"github.com/gluk-w/claworc/control-plane/internal/config"
)

const (
	labelManagedBy = "claworc"
	networkName    = "claworc"
)

var volumeSuffixes = []string{"homebrew", "clawd", "chrome"}

type DockerOrchestrator struct {
	client    *dockerclient.Client
	available bool
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
	if err := d.ensureImage(ctx, params.ContainerImage); err != nil {
		return err
	}

	// Create volumes
	for _, suffix := range volumeSuffixes {
		volName := d.volumeName(params.Name, suffix)
		_, err := d.client.VolumeCreate(ctx, volume.CreateOptions{
			Name:   volName,
			Labels: map[string]string{"managed-by": labelManagedBy, "instance": params.Name},
		})
		if err != nil {
			log.Printf("Volume %s may already exist: %v", volName, err)
		}
	}

	// Environment
	env := []string{"PUID=1000", "PGID=1000", "START_DOCKER=false"}
	if parts := strings.SplitN(params.VNCResolution, "x", 2); len(parts) == 2 {
		env = append(env, "SELKIES_MANUAL_WIDTH="+parts[0], "SELKIES_MANUAL_HEIGHT="+parts[1])
	}
	if token, ok := params.EnvVars["OPENCLAW_GATEWAY_TOKEN"]; ok && token != "" {
		env = append(env, fmt.Sprintf("OPENCLAW_GATEWAY_TOKEN=%s", token))
	}
	if params.AgentTLSCert != "" {
		env = append(env, fmt.Sprintf("AGENT_TLS_CERT=%s", params.AgentTLSCert))
	}
	if params.AgentTLSKey != "" {
		env = append(env, fmt.Sprintf("AGENT_TLS_KEY=%s", params.AgentTLSKey))
	}

	// Mounts
	mounts := []mount.Mount{
		{Type: mount.TypeVolume, Source: d.volumeName(params.Name, "homebrew"), Target: "/home/linuxbrew/.linuxbrew"},
		{Type: mount.TypeVolume, Source: d.volumeName(params.Name, "clawd"), Target: "/config/clawd"},
		{Type: mount.TypeVolume, Source: d.volumeName(params.Name, "chrome"), Target: "/config/chrome-data"},
	}

	// Resource limits
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
		Image:  params.ContainerImage,
		Env:    env,
		Labels: map[string]string{"managed-by": labelManagedBy, "instance": params.Name},
		Healthcheck: &container.HealthConfig{
			Test:          []string{"CMD", "curl", "-sf", "http://localhost:3000/"},
			Interval:      30_000_000_000,
			Timeout:       10_000_000_000,
			Retries:       3,
			StartInterval: 60_000_000_000,
		},
	}

	hostCfg := &container.HostConfig{
		Privileged: true,
		Mounts:     mounts,
		ShmSize:    shmSize,
		Resources: container.Resources{
			NanoCPUs: nanoCPUs,
			Memory:   memLimit,
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

	if token, ok := params.EnvVars["OPENCLAW_GATEWAY_TOKEN"]; ok && token != "" {
		go d.configureGatewayToken(context.Background(), params.Name, token)
	}

	return nil
}

func (d *DockerOrchestrator) waitForContainerRunning(ctx context.Context, name string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		inspect, err := d.client.ContainerInspect(ctx, name)
		if err == nil && inspect.State.Status == "running" {
			tag := inspect.Config.Image
			sha := inspect.Image
			if len(sha) > 19 { // "sha256:" (7) + 12 chars
				sha = sha[:19]
			}
			return fmt.Sprintf("%s (%s)", tag, sha), true
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(2 * time.Second):
		}
	}
	return "", false
}

func (d *DockerOrchestrator) configureGatewayToken(ctx context.Context, name, token string) {
	configureGatewayToken(ctx, d.ExecInInstance, name, token, d.waitForContainerRunning)
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
		log.Printf("Remove container %s: %v", name, err)
	}

	// Remove volumes
	for _, suffix := range volumeSuffixes {
		volName := d.volumeName(name, suffix)
		if err := d.client.VolumeRemove(ctx, volName, true); err != nil && !dockerclient.IsErrNotFound(err) {
			log.Printf("Remove volume %s: %v", volName, err)
		}
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

func (d *DockerOrchestrator) RestartInstance(ctx context.Context, name string) error {
	timeout := 30
	return d.client.ContainerRestart(ctx, name, container.StopOptions{Timeout: &timeout})
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

func (d *DockerOrchestrator) UpdateInstanceConfig(ctx context.Context, name string, configJSON string) error {
	return updateInstanceConfig(ctx, d.ExecInInstance, name, configJSON)
}

func (d *DockerOrchestrator) StreamInstanceLogs(ctx context.Context, name string, tail int, follow bool) (<-chan string, error) {
	cmd := fmt.Sprintf("openclaw logs --plain --limit %d", tail)
	if follow {
		cmd += " --follow"
	}
	cmdSlice := []string{"su", "-", "abc", "-c", cmd}

	execCfg := container.ExecOptions{
		Cmd:          cmdSlice,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := d.client.ContainerExecCreate(ctx, name, execCfg)
	if err != nil {
		if dockerclient.IsErrNotFound(err) {
			ch := make(chan string, 1)
			ch <- "Container not found"
			close(ch)
			return ch, nil
		}
		return nil, fmt.Errorf("exec create: %w", err)
	}

	resp, err := d.client.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("exec attach: %w", err)
	}

	ch := make(chan string, 100)
	go func() {
		defer close(ch)
		defer resp.Close()

		buf := make([]byte, 8192)
		for {
			n, err := resp.Reader.Read(buf)
			if n > 0 {
				data := buf[:n]
				text := stripDockerLogHeaders(data)
				for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
					if line != "" {
						select {
						case ch <- line:
						case <-ctx.Done():
							return
						}
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return ch, nil
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

func (d *DockerOrchestrator) ExecInteractive(ctx context.Context, name string, cmd []string) (*ExecSession, error) {
	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		ConsoleSize:  &[2]uint{24, 80},
	}

	execID, err := d.client.ContainerExecCreate(ctx, name, execCfg)
	if err != nil {
		return nil, fmt.Errorf("exec create: %w", err)
	}

	resp, err := d.client.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{Tty: true})
	if err != nil {
		return nil, fmt.Errorf("exec attach: %w", err)
	}

	return &ExecSession{
		Stdin:  resp.Conn,
		Stdout: resp.Conn,
		Resize: func(cols, rows uint16) error {
			return d.client.ContainerExecResize(ctx, execID.ID, container.ResizeOptions{
				Width:  uint(cols),
				Height: uint(rows),
			})
		},
		Close: func() error {
			resp.Close()
			return nil
		},
	}, nil
}

func (d *DockerOrchestrator) ListDirectory(ctx context.Context, name string, path string) ([]FileEntry, error) {
	return listDirectory(ctx, d.ExecInInstance, name, path)
}

func (d *DockerOrchestrator) ReadFile(ctx context.Context, name string, path string) ([]byte, error) {
	return readFile(ctx, d.ExecInInstance, name, path)
}

func (d *DockerOrchestrator) CreateFile(ctx context.Context, name string, path string, content string) error {
	return createFile(ctx, d.ExecInInstance, name, path, content)
}

func (d *DockerOrchestrator) CreateDirectory(ctx context.Context, name string, path string) error {
	return createDirectory(ctx, d.ExecInInstance, name, path)
}

func (d *DockerOrchestrator) WriteFile(ctx context.Context, name string, path string, data []byte) error {
	return writeFile(ctx, d.ExecInInstance, name, path, data)
}

func (d *DockerOrchestrator) GetGatewayWSURL(ctx context.Context, name string) (string, error) {
	inspect, err := d.client.ContainerInspect(ctx, name)
	if err != nil {
		return "", fmt.Errorf("inspect container: %w", err)
	}

	for _, net := range inspect.NetworkSettings.Networks {
		if net.IPAddress != "" {
			return fmt.Sprintf("ws://%s:3000/gateway", net.IPAddress), nil
		}
	}
	return "", fmt.Errorf("cannot determine container IP for %s", name)
}

func (d *DockerOrchestrator) GetHTTPTransport() http.RoundTripper {
	return nil
}

// Ensure DockerOrchestrator implements ContainerOrchestrator
var _ ContainerOrchestrator = (*DockerOrchestrator)(nil)
