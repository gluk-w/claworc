package handlers

import (
	"context"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	"github.com/gluk-w/claworc/control-plane/internal/database"
)

type fakeCapsClient struct {
	agentshim.Client
	caps  agentshim.Capabilities
	calls int
}

func (f *fakeCapsClient) Capabilities(context.Context) (agentshim.Capabilities, error) {
	f.calls++
	return f.caps, nil
}

// TestLiveAgentCapabilities: running instances are probed once and cached
// until invalidated; stopped instances are never probed.
func TestLiveAgentCapabilities(t *testing.T) {
	fake := &fakeCapsClient{caps: agentshim.Capabilities{Chat: true, ChatAbort: true}}
	orig := agentClientFor
	agentClientFor = func(context.Context, uint) (agentshim.Client, error) { return fake, nil }
	liveCapsCache.Delete(uint(9001))
	defer func() {
		agentClientFor = orig
		liveCapsCache.Delete(uint(9001))
	}()
	inst := database.Instance{ID: 9001}

	if _, ok := liveAgentCapabilities(context.Background(), inst, "stopped"); ok || fake.calls != 0 {
		t.Fatalf("stopped instance must not be probed (ok=%v calls=%d)", ok, fake.calls)
	}
	for i := 0; i < 2; i++ {
		caps, ok := liveAgentCapabilities(context.Background(), inst, "running")
		if !ok || !caps.ChatAbort {
			t.Fatalf("live caps = %+v ok=%v", caps, ok)
		}
	}
	if fake.calls != 1 {
		t.Errorf("probed %d times, want 1 (cached)", fake.calls)
	}
	liveCapsCache.Delete(uint(9001)) // what invalidateAgentCaches does
	liveAgentCapabilities(context.Background(), inst, "running")
	if fake.calls != 2 {
		t.Errorf("probed %d times after invalidation, want 2", fake.calls)
	}
}
