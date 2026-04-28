package browserprov

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
)

// LocalProvider implements Provider against the active container orchestrator
// (Kubernetes or Docker). It delegates lifecycle to the orchestrator's
// browser-pod methods and dials CDP/VNC over plain TCP through the cluster
// (or Docker bridge) network.
type LocalProvider struct {
	orch orchestrator.ContainerOrchestrator
}

// NewLocalProvider returns a provider that uses the given orchestrator. The
// orchestrator's BackendName ("kubernetes" or "docker") is the provider name.
func NewLocalProvider(orch orchestrator.ContainerOrchestrator) *LocalProvider {
	return &LocalProvider{orch: orch}
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
		ProviderRef: fmt.Sprintf("%s:%d", endpoint.Host, endpoint.CDPPort),
		StartedAt:   time.Now().UTC(),
		LastUsedAt:  time.Now().UTC(),
	}, nil
}

func (p *LocalProvider) StopSession(ctx context.Context, instanceID uint) error {
	return p.orch.StopBrowserPod(ctx, instanceID)
}

func (p *LocalProvider) DeleteSession(ctx context.Context, instanceID uint) error {
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
	endpoint, err := p.orch.GetBrowserPodEndpoint(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(endpoint.Host, fmt.Sprintf("%d", endpoint.CDPPort))
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial CDP %s: %w", addr, err)
	}
	return conn, nil
}

func (p *LocalProvider) DialVNC(ctx context.Context, instanceID uint) (io.ReadWriteCloser, error) {
	endpoint, err := p.orch.GetBrowserPodEndpoint(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(endpoint.Host, fmt.Sprintf("%d", endpoint.VNCPort))
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial VNC %s: %w", addr, err)
	}
	return conn, nil
}
