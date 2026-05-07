package browserprov

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"golang.org/x/crypto/ssh"
)

// LocalProvider implements Provider against the active container orchestrator
// (Kubernetes or Docker) using only generic primitives. The browser workload
// is described by a WorkloadSpec; the orchestrator translates it into either
// a Docker container or a K8s Deployment+PVC+Service+NetworkPolicy. The pod
// exposes only sshd on port 22; CDP (127.0.0.1:9222) and noVNC
// (127.0.0.1:3000) stay loopback-bound and are reached over the SSH session.
type LocalProvider struct {
	orch orchestrator.ContainerOrchestrator
	keys *sshproxy.SSHManager

	mu             sync.Mutex
	browserClients map[uint]*ssh.Client

	// browserHostKeyMu guards browserHostKeys.
	browserHostKeyMu sync.RWMutex
	// browserHostKeys stores the TOFU host key for each browser workload keyed
	// by instance ID. On first connection the key is recorded; subsequent
	// connections must present the same key.
	browserHostKeys map[uint]ssh.PublicKey
}

// NewLocalProvider returns a provider that drives the given orchestrator and
// reuses the SSHManager's global key pair to authenticate to browser pods.
// keys may be nil in test fixtures; callers that exercise DialCDP/DialVNC
// must provide one.
func NewLocalProvider(orch orchestrator.ContainerOrchestrator, keys *sshproxy.SSHManager) *LocalProvider {
	return &LocalProvider{
		orch:            orch,
		keys:            keys,
		browserClients:  make(map[uint]*ssh.Client),
		browserHostKeys: make(map[uint]ssh.PublicKey),
	}
}

// browserHostKeyCallback returns a Trust On First Use (TOFU) SSH host-key
// callback for a browser workload associated with instanceID.  On first
// connection the presented key is recorded; all subsequent connections must
// present the identical key.  Use clearBrowserHostKey when a browser workload
// is recreated so the new host key can be accepted.
func (p *LocalProvider) browserHostKeyCallback(instanceID uint) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		p.browserHostKeyMu.RLock()
		known, exists := p.browserHostKeys[instanceID]
		p.browserHostKeyMu.RUnlock()

		if !exists {
			p.browserHostKeyMu.Lock()
			// Double-check under write lock to avoid TOCTOU.
			if known2, exists2 := p.browserHostKeys[instanceID]; exists2 {
				p.browserHostKeyMu.Unlock()
				if string(known2.Marshal()) != string(key.Marshal()) {
					return fmt.Errorf("browser host key mismatch for instance %d: expected %s, got %s",
						instanceID, ssh.FingerprintSHA256(known2), ssh.FingerprintSHA256(key))
				}
				return nil
			}
			p.browserHostKeys[instanceID] = key
			p.browserHostKeyMu.Unlock()
			return nil
		}

		if string(known.Marshal()) != string(key.Marshal()) {
			return fmt.Errorf("browser host key mismatch for instance %d: expected %s, got %s",
				instanceID, ssh.FingerprintSHA256(known), ssh.FingerprintSHA256(key))
		}
		return nil
	}
}

// clearBrowserHostKey removes the stored TOFU host key for an instance's
// browser workload.  Call this when the workload is deleted or recreated so
// the next connection can record the new host key.
func (p *LocalProvider) clearBrowserHostKey(instanceID uint) {
	p.browserHostKeyMu.Lock()
	delete(p.browserHostKeys, instanceID)
	p.browserHostKeyMu.Unlock()
}

func (p *LocalProvider) Name() string {
	if p.orch == nil {
		return "local"
	}
	return p.orch.BackendName()
}

func (p *LocalProvider) Capabilities() Capabilities {
	return Capabilities{
		SupportsVNC:               true,
		SupportsPersistentProfile: true,
		SupportsHeadful:           true,
	}
}

func (p *LocalProvider) instanceName(instanceID uint) (string, error) {
	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return "", fmt.Errorf("instance %d: %w", instanceID, err)
	}
	return inst.Name, nil
}

// browserWorkloadName is the workload identifier the orchestrator will use
// for the Deployment / container / Service / NetworkPolicy.
func browserWorkloadName(instanceName string) string {
	return instanceName + "-browser"
}

func (p *LocalProvider) browserDataVolume(instanceName string) string {
	return p.orch.VolumeNameFor(instanceName, "browser")
}

func (p *LocalProvider) agentHomeVolume(instanceName string) string {
	return p.orch.VolumeNameFor(instanceName, "home")
}

// buildApplySpec describes the browser workload to the orchestrator. Init
// containers ensure a few preconditions hold before Chromium starts:
//   - The agent's home volume has a Downloads/ subdirectory owned by UID 1000
//     so the main container's SubPath mount succeeds and Chromium can write
//     downloads.
//   - Any host-scoped Chromium Singleton{Lock,Cookie,Socket} files left over
//     from a clone are removed so a fresh hostname doesn't trip the
//     "profile in use" guard.
func (p *LocalProvider) buildApplySpec(instanceName string, params SessionParams) orchestrator.WorkloadSpec {
	workload := browserWorkloadName(instanceName)
	dataVol := p.browserDataVolume(instanceName)
	homeVol := p.agentHomeVolume(instanceName)

	storage := params.StorageSize
	if storage == "" {
		storage = "10Gi"
	}

	env := map[string]string{}
	if parts := strings.SplitN(params.VNCResolution, "x", 2); len(parts) == 2 {
		env["DISPLAY_WIDTH"] = parts[0]
		env["DISPLAY_HEIGHT"] = parts[1]
	}
	if params.Timezone != "" {
		env["TZ"] = params.Timezone
	}
	if params.UserAgent != "" {
		env["CHROMIUM_USER_AGENT"] = params.UserAgent
	}
	for k, v := range params.EnvVars {
		env[k] = v
	}

	return orchestrator.WorkloadSpec{
		Name:  workload,
		Image: params.Image,
		Env:   env,
		Labels: map[string]string{
			"claworc-role": "browser",
			"instance":     instanceName,
		},
		Pull:     orchestrator.PullAlways,
		Hostname: strings.TrimPrefix(workload, "bot-"),
		Volumes: []orchestrator.VolumeMount{
			{Name: dataVol, Size: storage, MountPath: "/home/claworc/chrome-data"},
			{Name: homeVol, MountPath: "/home/claworc/Downloads", SubPath: "Downloads"},
		},
		EmptyDirs: []orchestrator.EmptyDirMount{
			{Name: "dshm", MountPath: "/dev/shm", Medium: "Memory", SizeLimit: "2Gi"},
		},
		Ports:    []orchestrator.PortSpec{{Name: "ssh", ContainerPort: 22}},
		Affinity: &orchestrator.AffinitySpec{RequiredCoLocation: []string{instanceName}},
		InitContainers: []orchestrator.InitContainerSpec{
			{
				Name:    "prepare-downloads",
				Image:   "alpine:latest",
				Command: []string{"sh", "-c", "mkdir -p /agent-home/Downloads && chown 1000:1000 /agent-home/Downloads"},
				Mounts:  []orchestrator.VolumeMount{{Name: homeVol, MountPath: "/agent-home"}},
			},
			{
				Name:    "scrub-singletons",
				Image:   "alpine:latest",
				Command: []string{"sh", "-c", "rm -f /chrome-data/SingletonLock /chrome-data/SingletonCookie /chrome-data/SingletonSocket"},
				Mounts:  []orchestrator.VolumeMount{{Name: dataVol, MountPath: "/chrome-data"}},
			},
		},
	}
}

func (p *LocalProvider) EnsureSession(ctx context.Context, instanceID uint, params SessionParams) (*Session, error) {
	name, err := p.instanceName(instanceID)
	if err != nil {
		return nil, err
	}
	if params.Image == "" {
		return nil, errors.New("browserprov: SessionParams.Image is required")
	}

	spec := p.buildApplySpec(name, params)
	if err := p.orch.Apply(ctx, spec); err != nil {
		return nil, fmt.Errorf("apply browser workload: %w", err)
	}

	if err := p.waitForCDPReady(ctx, instanceID, 120*time.Second); err != nil {
		return nil, err
	}

	host, port, _ := p.orch.WorkloadSSHAddress(ctx, browserWorkloadName(name))
	return &Session{
		InstanceID:  instanceID,
		Provider:    p.Name(),
		Status:      StatusRunning,
		Image:       params.Image,
		PodName:     browserWorkloadName(name),
		ProviderRef: fmt.Sprintf("ssh://%s:%d", host, port),
		StartedAt:   time.Now().UTC(),
		LastUsedAt:  time.Now().UTC(),
	}, nil
}

// waitForCDPReady polls until an SSH session can be established AND a
// loopback dial to 127.0.0.1:9222 inside the pod succeeds. Replaces the
// runtime-specific readiness probes in the previous backends — both SSH and
// CDP being reachable is the only definition of "ready" the rest of the
// system depends on.
func (p *LocalProvider) waitForCDPReady(ctx context.Context, instanceID uint, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		client, err := p.ensureBrowserClient(ctx, instanceID)
		if err != nil {
			lastErr = err
			time.Sleep(2 * time.Second)
			continue
		}
		conn, err := client.Dial("tcp", "127.0.0.1:9222")
		if err != nil {
			lastErr = err
			time.Sleep(2 * time.Second)
			continue
		}
		_ = conn.Close()
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("browser CDP not ready within %s: %w", timeout, lastErr)
	}
	return fmt.Errorf("browser CDP not ready within %s", timeout)
}

func (p *LocalProvider) StopSession(ctx context.Context, instanceID uint) error {
	p.closeBrowserClient(instanceID)
	p.clearBrowserHostKey(instanceID)
	name, err := p.instanceName(instanceID)
	if err != nil {
		return err
	}
	return p.orch.StopInstance(ctx, browserWorkloadName(name))
}

// DeleteSession tears down the browser workload. The delete spec only lists
// the browser-data volume; the agent's home volume — referenced by Apply for
// the Downloads SubPath mount — is owned by the agent workload and must
// outlive the browser.
func (p *LocalProvider) DeleteSession(ctx context.Context, instanceID uint) error {
	p.closeBrowserClient(instanceID)
	p.clearBrowserHostKey(instanceID)
	name, err := p.instanceName(instanceID)
	if err != nil {
		return err
	}
	spec := orchestrator.WorkloadSpec{
		Name: browserWorkloadName(name),
		Volumes: []orchestrator.VolumeMount{
			{Name: p.browserDataVolume(name), MountPath: "/home/claworc/chrome-data"},
		},
	}
	return p.orch.DeleteWorkload(ctx, spec)
}

func (p *LocalProvider) SessionStatus(ctx context.Context, instanceID uint) (Status, error) {
	name, err := p.instanceName(instanceID)
	if err != nil {
		return StatusError, err
	}
	s, err := p.orch.GetInstanceStatus(ctx, browserWorkloadName(name))
	if err != nil {
		return StatusError, err
	}
	switch s {
	case "running":
		return StatusRunning, nil
	case "creating", "starting":
		return StatusStarting, nil
	case "stopped":
		return StatusStopped, nil
	default:
		return StatusError, nil
	}
}

// CloneSession copies the browser data volume from the source instance to the
// destination so the clone starts with the source's persisted Chrome state.
// Best-effort: a missing source volume (browser never launched) is treated as
// success. K8s currently no-ops this — see ContainerOrchestrator.CloneVolume.
func (p *LocalProvider) CloneSession(ctx context.Context, srcInstanceName, dstInstanceName string) error {
	return p.orch.CloneVolume(ctx,
		p.browserDataVolume(srcInstanceName),
		p.browserDataVolume(dstInstanceName),
	)
}

func (p *LocalProvider) DialCDP(ctx context.Context, instanceID uint) (io.ReadWriteCloser, error) {
	return p.dialLoopback(ctx, instanceID, "CDP", 9222)
}

func (p *LocalProvider) DialVNC(ctx context.Context, instanceID uint) (io.ReadWriteCloser, error) {
	return p.dialLoopback(ctx, instanceID, "VNC", 3000)
}

func (p *LocalProvider) TestConnection(ctx context.Context, instanceID uint) (string, error) {
	client, err := p.ensureBrowserClient(ctx, instanceID)
	if err != nil {
		return "", err
	}
	session, err := client.NewSession()
	if err != nil {
		// Session creation can fail on a stale cached client; redial once.
		p.closeBrowserClient(instanceID)
		client, err2 := p.ensureBrowserClient(ctx, instanceID)
		if err2 != nil {
			return "", fmt.Errorf("ssh session: %w", err)
		}
		session, err = client.NewSession()
		if err != nil {
			return "", fmt.Errorf("ssh session: %w", err)
		}
	}
	defer session.Close()
	out, err := session.CombinedOutput(`echo "SSH test successful"`)
	if err != nil {
		return string(out), fmt.Errorf("command execution failed: %w", err)
	}
	return string(out), nil
}

func (p *LocalProvider) Reconnect(_ context.Context, instanceID uint) error {
	p.closeBrowserClient(instanceID)
	return nil
}

// VNCDialer returns a DialContext-compatible function that opens a new SSH
// channel to 127.0.0.1:3000 inside the browser pod on each invocation. The
// network/addr arguments are ignored — they exist only to satisfy
// http.Transport.DialContext's signature.
func (p *LocalProvider) VNCDialer(ctx context.Context, instanceID uint) (func(context.Context, string, string) (net.Conn, error), error) {
	if _, err := p.ensureBrowserClient(ctx, instanceID); err != nil {
		return nil, err
	}
	return func(dctx context.Context, _, _ string) (net.Conn, error) {
		c, err := p.ensureBrowserClient(dctx, instanceID)
		if err != nil {
			return nil, err
		}
		conn, err := c.Dial("tcp", "127.0.0.1:3000")
		if err != nil {
			p.closeBrowserClient(instanceID)
			c, err2 := p.ensureBrowserClient(dctx, instanceID)
			if err2 != nil {
				return nil, err
			}
			return c.Dial("tcp", "127.0.0.1:3000")
		}
		return conn, nil
	}, nil
}

// dialLoopback opens (or reuses) the SSH session to the browser pod and
// returns a direct-tcpip channel to 127.0.0.1:port inside the pod.
func (p *LocalProvider) dialLoopback(ctx context.Context, instanceID uint, label string, port int) (io.ReadWriteCloser, error) {
	client, err := p.ensureBrowserClient(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("browser ssh for instance %d: %w", instanceID, err)
	}
	target := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := client.Dial("tcp", target)
	if err != nil {
		p.closeBrowserClient(instanceID)
		client, err2 := p.ensureBrowserClient(ctx, instanceID)
		if err2 != nil {
			return nil, fmt.Errorf("dial %s %s (after reconnect): %w", label, target, err)
		}
		conn, err = client.Dial("tcp", target)
		if err != nil {
			return nil, fmt.Errorf("dial %s %s: %w", label, target, err)
		}
	}
	return conn, nil
}

func (p *LocalProvider) ensureBrowserClient(ctx context.Context, instanceID uint) (*ssh.Client, error) {
	p.mu.Lock()
	if c, ok := p.browserClients[instanceID]; ok {
		if _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err == nil {
			p.mu.Unlock()
			return c, nil
		}
		delete(p.browserClients, instanceID)
		_ = c.Close()
		// The browser container may have restarted with a new host key;
		// clear the stored TOFU key so reconnection can record a fresh one.
		p.clearBrowserHostKey(instanceID)
	}
	p.mu.Unlock()

	if p.keys == nil {
		return nil, errors.New("browserprov: SSHManager not configured")
	}

	name, err := p.instanceName(instanceID)
	if err != nil {
		return nil, err
	}
	workload := browserWorkloadName(name)

	host, port, err := p.orch.WorkloadSSHAddress(ctx, workload)
	if err != nil {
		return nil, fmt.Errorf("get browser ssh address: %w", err)
	}
	if port == 0 {
		port = 22
	}

	if err := p.orch.EnsureSSHAccess(ctx, workload, p.keys.GetPublicKey()); err != nil {
		return nil, fmt.Errorf("ensure browser ssh access: %w", err)
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	cfg := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(p.keys.Signer())},
		HostKeyCallback: p.browserHostKeyCallback(instanceID),
		Timeout:         30 * time.Second,
	}
	dialer := net.Dialer{Timeout: 30 * time.Second}
	netConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial browser ssh %s: %w", addr, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(netConn, addr, cfg)
	if err != nil {
		netConn.Close()
		return nil, fmt.Errorf("ssh handshake to browser %s: %w", addr, err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)

	p.mu.Lock()
	if existing, ok := p.browserClients[instanceID]; ok {
		p.mu.Unlock()
		_ = client.Close()
		return existing, nil
	}
	p.browserClients[instanceID] = client
	p.mu.Unlock()
	return client, nil
}

func (p *LocalProvider) closeBrowserClient(instanceID uint) {
	p.mu.Lock()
	c, ok := p.browserClients[instanceID]
	if ok {
		delete(p.browserClients, instanceID)
	}
	p.mu.Unlock()
	if ok && c != nil {
		_ = c.Close()
	}
}
