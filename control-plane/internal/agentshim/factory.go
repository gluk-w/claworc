package agentshim

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
	gossh "golang.org/x/crypto/ssh"
)

// InstanceDeps bundles the per-instance transport-level dependencies an
// adapter needs. Everything here is resolved lazily so that operations which
// don't need a given dependency (e.g. config reads don't need the gateway
// tunnel) never pay for — or fail on — its resolution.
type InstanceDeps struct {
	// Instance is the database record snapshot taken by the factory.
	Instance database.Instance
	// GatewayToken is the decrypted intra-container agent auth token
	// (may be empty).
	GatewayToken string
	// TunnelPort resolves the local port of an active SSH tunnel for the
	// given service type (e.g. "gateway").
	TunnelPort func(service string) (int, error)
	// SSHClient resolves an established SSH client to the instance.
	SSHClient func(ctx context.Context) (*gossh.Client, error)
}

// Constructor builds an adapter Client from instance dependencies.
type Constructor func(deps InstanceDeps) Client

var adapters = map[string]Constructor{}

// RegisterAdapter registers an adapter constructor for an agent type.
// Adapters call this from init(); the map is not mutated afterwards.
func RegisterAdapter(agentType string, ctor Constructor) {
	adapters[agentType] = ctor
}

// ShimAdapterType is the registry key of the exec-based shim adapter
// (internal/agentshim/shimexec), the standard path for every agent image
// implementing docs/shim.md.
const ShimAdapterType = "shimexec"

// shimProbePath is the file whose executability marks a shim-capable image.
const shimProbePath = "/opt/claworc/shim/meta"

// shimProbeTTL bounds how long a probe result is trusted. Shim presence only
// changes with image content, but a same-tag image can be re-pulled with
// different content, so results self-heal on a short TTL.
const shimProbeTTL = 5 * time.Minute

type shimProbeEntry struct {
	image   string
	hasShim bool
	at      time.Time
}

// Factory builds agent Clients for instances. Its function fields are the
// wiring seams: production code points them at the handlers' tunnel manager
// and SSH manager; tests inject stubs.
type Factory struct {
	// TunnelPort resolves the local port of an active SSH tunnel for an
	// instance and service type (e.g. "gateway").
	TunnelPort func(instanceID uint, service string) (int, error)
	// SSHClient resolves an established SSH client for an instance,
	// honoring its source-IP restrictions.
	SSHClient func(ctx context.Context, inst database.Instance) (*gossh.Client, error)
	// ProbeShim reports whether the instance's image ships the exec shim
	// contract. Nil selects the default SSH probe (test -x on shimProbePath).
	// Tests inject stubs here.
	ProbeShim func(ctx context.Context, deps InstanceDeps) (bool, error)

	// probeCache caches shim-probe results per instance ID, keyed to the
	// image string and bounded by shimProbeTTL.
	probeCache sync.Map // uint -> shimProbeEntry
}

// ForInstance loads the instance record, decrypts its gateway token, and
// returns the agent Client for its agent type. Today every image is OpenClaw,
// so the type switch has a single arm; a shimexec adapter for images
// implementing the exec-based shim contract (docs/shim.md) plugs in here
// later.
func (f *Factory) ForInstance(ctx context.Context, instanceID uint) (Client, error) {
	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return nil, fmt.Errorf("instance %d not found: %w", instanceID, err)
	}

	var token string
	if inst.GatewayToken != "" {
		if tok, err := utils.Decrypt(inst.GatewayToken); err == nil && tok != "" {
			token = tok
		}
	}

	deps := InstanceDeps{
		Instance:     inst,
		GatewayToken: token,
		TunnelPort: func(service string) (int, error) {
			if f.TunnelPort == nil {
				return 0, fmt.Errorf("agentshim factory: no tunnel port resolver configured")
			}
			return f.TunnelPort(instanceID, service)
		},
		SSHClient: func(ctx context.Context) (*gossh.Client, error) {
			if f.SSHClient == nil {
				return nil, fmt.Errorf("agentshim factory: no SSH client resolver configured")
			}
			return f.SSHClient(ctx, inst)
		},
	}

	// Agent-type dispatch. Non-OpenClaw types always go through the exec
	// shim contract. OpenClaw prefers the shim when the image ships it and
	// falls back to the native gateway adapter for pre-shim images — the
	// backward-compatibility guarantee: legacy deployments keep working
	// without any image rebuild.
	adapterKey := ShimAdapterType
	if inst.EffectiveAgentType() == "openclaw" && !f.imageHasShim(ctx, deps) {
		adapterKey = "openclaw"
	}

	ctor, ok := adapters[adapterKey]
	if !ok {
		return nil, fmt.Errorf("no agent adapter registered for type %q", adapterKey)
	}
	return ctor(deps), nil
}

// InvalidateShimProbe drops the cached shim-probe result for an instance.
// Call after image updates so the next ForInstance re-probes immediately.
func (f *Factory) InvalidateShimProbe(instanceID uint) {
	f.probeCache.Delete(instanceID)
}

// imageHasShim reports whether the instance's image ships the exec shim,
// caching the answer per (instance, image) with a TTL. Probe failures are
// fail-open to the legacy adapter and are not cached.
func (f *Factory) imageHasShim(ctx context.Context, deps InstanceDeps) bool {
	id := deps.Instance.ID
	image := deps.Instance.ContainerImage
	if e, ok := f.probeCache.Load(id); ok {
		entry := e.(shimProbeEntry)
		if entry.image == image && time.Since(entry.at) < shimProbeTTL {
			return entry.hasShim
		}
	}

	probe := f.ProbeShim
	if probe == nil {
		probe = defaultShimProbe
	}
	hasShim, err := probe(ctx, deps)
	if err != nil {
		return false
	}
	f.probeCache.Store(id, shimProbeEntry{image: image, hasShim: hasShim, at: time.Now()})
	return hasShim
}

// defaultShimProbe checks shim presence with a single cheap exec over the
// instance's SSH connection.
func defaultShimProbe(ctx context.Context, deps InstanceDeps) (bool, error) {
	client, err := deps.SSHClient(ctx)
	if err != nil {
		return false, err
	}
	sess, err := client.NewSession()
	if err != nil {
		return false, err
	}
	defer sess.Close()

	done := make(chan error, 1)
	go func() { done <- sess.Run("test -x " + shimProbePath) }()
	select {
	case err := <-done:
		// Exit status 1 means "no shim", not a probe failure.
		if err == nil {
			return true, nil
		}
		var exitErr *gossh.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, err
	case <-time.After(5 * time.Second):
		return false, fmt.Errorf("shim probe timed out")
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// defaultFactory is set once at package wiring time (handlers init) and read
// afterwards; see SetDefaultFactory.
var defaultFactory *Factory

// SetDefaultFactory installs the process-wide factory. Called once from the
// handlers package's wiring before any request is served.
func SetDefaultFactory(f *Factory) { defaultFactory = f }

// DefaultFactory returns the process-wide factory. It always returns a
// non-nil Factory; an unwired factory yields descriptive errors from its
// resolvers rather than nil-pointer panics.
func DefaultFactory() *Factory {
	if defaultFactory == nil {
		return &Factory{}
	}
	return defaultFactory
}
