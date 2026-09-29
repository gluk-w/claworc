package handlers

import (
	"context"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// liveCapsTTL bounds how long probed agent capabilities (the shim's `meta`)
// are reused for API responses. Capabilities only change with the image, and
// image/type changes invalidate explicitly (invalidateAgentCaches).
const liveCapsTTL = 2 * time.Minute

// liveCapsProbeTimeout bounds the SSH probe on the request path; on timeout
// the static registry placeholder is served instead.
const liveCapsProbeTimeout = 3 * time.Second

type liveCapsEntry struct {
	caps agentshim.Capabilities
	at   time.Time
}

var liveCapsCache sync.Map // uint -> liveCapsEntry

// agentCapabilities returns the instance agent's live capabilities (probed
// via the agentshim factory and cached for liveCapsTTL). ok is false when
// they could not be probed — e.g. the instance is stopped or SSH is down —
// and callers fall back to the static registry placeholder.
func agentCapabilities(ctx context.Context, inst database.Instance) (agentshim.Capabilities, bool) {
	if v, found := liveCapsCache.Load(inst.ID); found {
		if e := v.(liveCapsEntry); time.Since(e.at) < liveCapsTTL {
			return e.caps, true
		}
	}
	ctx, cancel := context.WithTimeout(ctx, liveCapsProbeTimeout)
	defer cancel()
	client, err := agentClientFor(ctx, inst.ID)
	if err != nil {
		return agentshim.Capabilities{}, false
	}
	caps, err := client.Capabilities(ctx)
	if err != nil {
		return agentshim.Capabilities{}, false
	}
	liveCapsCache.Store(inst.ID, liveCapsEntry{caps: caps, at: time.Now()})
	return caps, true
}

// invalidateAgentCaches drops every cached per-instance view of the agent —
// the factory's shim probe, live capabilities, and the control UI spec — so
// the next request re-probes. Call whenever the image or agent type changes.
func invalidateAgentCaches(instanceID uint) {
	agentshim.DefaultFactory().InvalidateShimProbe(instanceID)
	liveCapsCache.Delete(instanceID)
	controlUISpecCache.Delete(instanceID)
}

// liveAgentCapabilities is agentCapabilities gated on the instance running:
// stopped instances are never probed.
func liveAgentCapabilities(ctx context.Context, inst database.Instance, status string) (agentshim.Capabilities, bool) {
	if status != "running" {
		return agentshim.Capabilities{}, false
	}
	return agentCapabilities(ctx, inst)
}
