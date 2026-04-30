package browserprov

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"golang.org/x/crypto/ssh"
)

// LocalProvider implements Provider against the active container orchestrator
// (Kubernetes or Docker). The browser pod exposes only sshd on port 22; the
// control plane opens an SSH session to it and uses ssh.Client.Dial to reach
// the loopback-bound CDP (127.0.0.1:9222) and noVNC (127.0.0.1:3000) services
// inside the pod. SSH client connections are cached per instance ID.
type LocalProvider struct {
	orch orchestrator.ContainerOrchestrator
	keys *sshproxy.SSHManager // for the global client signer / public key

	mu             sync.Mutex
	browserClients map[uint]*ssh.Client
}

// NewLocalProvider returns a provider that uses the given orchestrator and
// reuses the SSHManager's global key pair to authenticate to browser pods.
// keys may be nil in test fixtures; callers that exercise DialCDP/DialVNC
// must provide one.
func NewLocalProvider(orch orchestrator.ContainerOrchestrator, keys *sshproxy.SSHManager) *LocalProvider {
	return &LocalProvider{
		orch:           orch,
		keys:           keys,
		browserClients: make(map[uint]*ssh.Client),
	}
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

// instanceName resolves the instance row so callers can pass either a name or
// an ID downstream. Returns "" with no error if the row is missing — callers
// should treat that as a deleted instance.
func (p *LocalProvider) instanceName(instanceID uint) (string, error) {
	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return "", fmt.Errorf("instance %d: %w", instanceID, err)
	}
	return inst.Name, nil
}

func (p *LocalProvider) EnsureSession(ctx context.Context, instanceID uint, params SessionParams) (*Session, error) {
	name, err := p.instanceName(instanceID)
	if err != nil {
		return nil, err
	}
	if params.Image == "" {
		return nil, errors.New("browserprov: SessionParams.Image is required")
	}
	endpoint, err := p.orch.EnsureBrowserPod(ctx, instanceID, orchestrator.BrowserPodParams{
		Name:          name,
		Image:         params.Image,
		StorageSize:   params.StorageSize,
		VNCResolution: params.VNCResolution,
		UserAgent:     params.UserAgent,
		Timezone:      params.Timezone,
		EnvVars:       params.EnvVars,
	})
	if err != nil {
		return nil, err
	}
	return &Session{
		InstanceID:  instanceID,
		Provider:    p.Name(),
		Status:      StatusRunning,
		Image:       params.Image,
		PodName:     name + "-browser",
		ProviderRef: fmt.Sprintf("ssh://%s:%d", endpoint.Host, endpoint.SSHPort),
		StartedAt:   time.Now().UTC(),
		LastUsedAt:  time.Now().UTC(),
	}, nil
}

func (p *LocalProvider) StopSession(ctx context.Context, instanceID uint) error {
	p.closeBrowserClient(instanceID)
	return p.orch.StopBrowserPod(ctx, instanceID)
}

func (p *LocalProvider) DeleteSession(ctx context.Context, instanceID uint) error {
	p.closeBrowserClient(instanceID)
	return p.orch.DeleteBrowserPod(ctx, instanceID)
}

func (p *LocalProvider) SessionStatus(ctx context.Context, instanceID uint) (Status, error) {
	s, err := p.orch.GetBrowserPodStatus(ctx, instanceID)
	if err != nil {
		return StatusError, err
	}
	switch s {
	case "running":
		return StatusRunning, nil
	case "starting":
		return StatusStarting, nil
	case "stopped":
		return StatusStopped, nil
	default:
		return StatusError, nil
	}
}

func (p *LocalProvider) DialCDP(ctx context.Context, instanceID uint) (io.ReadWriteCloser, error) {
	return p.dialLoopback(ctx, instanceID, "CDP", 9222)
}

func (p *LocalProvider) DialVNC(ctx context.Context, instanceID uint) (io.ReadWriteCloser, error) {
	return p.dialLoopback(ctx, instanceID, "VNC", 3000)
}

// VNCDialer returns a DialContext-compatible function that opens a new SSH
// channel to 127.0.0.1:3000 inside the browser pod on each invocation. The
// network/addr arguments are ignored — they exist only to satisfy
// http.Transport.DialContext's signature.
func (p *LocalProvider) VNCDialer(ctx context.Context, instanceID uint) (func(context.Context, string, string) (net.Conn, error), error) {
	// Eagerly establish (and cache) the SSH client so the first HTTP request
	// doesn't pay both the SSH handshake and the noVNC dial cost serially.
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
			// Cached client may be stale; redial once.
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
		// The cached client may be dead (pod restart, key rotation). Drop it
		// and retry once with a fresh session.
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

// ensureBrowserClient returns a cached ssh.Client to the browser pod for the
// given instance, dialing one if absent. The caller's ctx governs only the
// dial; once cached the client is owned by the provider.
func (p *LocalProvider) ensureBrowserClient(ctx context.Context, instanceID uint) (*ssh.Client, error) {
	p.mu.Lock()
	if c, ok := p.browserClients[instanceID]; ok {
		// Quick liveness check: a failing keepalive request drops the cached
		// client and forces a redial below.
		if _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err == nil {
			p.mu.Unlock()
			return c, nil
		}
		delete(p.browserClients, instanceID)
		_ = c.Close()
	}
	p.mu.Unlock()

	if p.keys == nil {
		return nil, errors.New("browserprov: SSHManager not configured")
	}

	endpoint, err := p.orch.GetBrowserPodEndpoint(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("get browser endpoint: %w", err)
	}
	if endpoint.SSHPort == 0 {
		endpoint.SSHPort = 22
	}

	// Provision the public key into /root/.ssh/authorized_keys on the browser
	// pod. Idempotent — repeated calls overwrite with the same content.
	if err := p.orch.ConfigureBrowserSSHAccess(ctx, instanceID, p.keys.GetPublicKey()); err != nil {
		return nil, fmt.Errorf("configure browser ssh access: %w", err)
	}

	addr := net.JoinHostPort(endpoint.Host, fmt.Sprintf("%d", endpoint.SSHPort))
	cfg := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(p.keys.Signer())},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // browser pod is reachable only via NetworkPolicy-restricted Service
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
		// Lost a race; keep the existing one.
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
