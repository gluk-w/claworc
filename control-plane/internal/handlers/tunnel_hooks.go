package handlers

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/tunnel"
)

// reconnectCtxs stores per-instance context.CancelFunc values used to stop
// ReconnectLoop goroutines when instances are stopped or deleted.
var reconnectCtxs sync.Map // map[uint]context.CancelFunc

// makeAddrResolver creates a tunnel.AddrResolver that delegates to the
// orchestrator's GetAgentTunnelAddr method.
func makeAddrResolver(orch orchestrator.ContainerOrchestrator) tunnel.AddrResolver {
	return func(ctx context.Context, name string) (string, error) {
		return orch.GetAgentTunnelAddr(ctx, name)
	}
}

// startTunnelForInstance connects the tunnel to the agent instance and starts
// a background reconnect loop. Any previous reconnect loop for this instance
// is cancelled first. Tunnel connection failures are logged but not propagated —
// the reconnect loop will keep retrying.
func startTunnelForInstance(inst *database.Instance) {
	orch := orchestrator.Get()
	if orch == nil {
		return
	}

	// Cancel any prior reconnect loop.
	if cancel, ok := reconnectCtxs.LoadAndDelete(inst.ID); ok {
		cancel.(context.CancelFunc)()
	}
	tunnel.DisconnectInstance(inst.ID)

	resolver := makeAddrResolver(orch)
	ctx, cancel := context.WithCancel(context.Background())
	reconnectCtxs.Store(inst.ID, cancel)

	// Best-effort initial connection — the reconnect loop will retry on failure.
	if err := tunnel.ConnectInstance(ctx, inst, resolver); err != nil {
		log.Printf("[tunnel] initial connect for instance %d (%s) failed (will retry): %v", inst.ID, inst.Name, err)
	}

	go tunnel.ReconnectLoop(ctx, inst, resolver, 10*time.Second)
}

// stopTunnelForInstance disconnects the tunnel and cancels the reconnect loop.
func stopTunnelForInstance(instanceID uint) {
	if cancel, ok := reconnectCtxs.LoadAndDelete(instanceID); ok {
		cancel.(context.CancelFunc)()
	}
	tunnel.DisconnectInstance(instanceID)
}

// ConnectRunningInstanceTunnels connects tunnels for all instances that have
// status "running" in the database. Call once during control-plane startup
// after the orchestrator and tunnel manager are initialised.
func ConnectRunningInstanceTunnels() {
	orch := orchestrator.Get()
	if orch == nil {
		log.Println("[tunnel] no orchestrator available, skipping startup tunnel connections")
		return
	}

	var instances []database.Instance
	if err := database.DB.Where("status = ?", "running").Find(&instances).Error; err != nil {
		log.Printf("[tunnel] failed to query running instances: %v", err)
		return
	}

	if len(instances) == 0 {
		return
	}

	log.Printf("[tunnel] connecting tunnels for %d running instance(s) on startup", len(instances))
	for i := range instances {
		startTunnelForInstance(&instances[i])
	}
}
