package tunnel

import (
	"context"
	"log"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// AddrResolver returns the tunnel endpoint address (host:port) for a given
// instance name. Callers supply a concrete implementation that delegates to
// the orchestrator (e.g. Docker container IP or K8s service DNS).
type AddrResolver func(ctx context.Context, name string) (string, error)

// ConnectInstance establishes (or re-uses) a yamux tunnel to the agent for the
// given instance. If a healthy connection already exists it returns nil.
//
// The resolver callback is used to obtain the agent's tunnel address so that
// this package doesn't import the orchestrator package directly (avoiding
// circular dependencies).
func ConnectInstance(ctx context.Context, inst *database.Instance, resolver AddrResolver) error {
	// Fast path: already connected and healthy.
	if existing := Manager.Get(inst.ID); existing != nil && !existing.IsClosed() {
		return nil
	}

	// Resolve agent tunnel address.
	addr, err := resolver(ctx, inst.Name)
	if err != nil {
		return err
	}

	// The agent's public cert is stored in the DB at creation time.
	agentCertPEM := inst.AgentCert
	if agentCertPEM == "" {
		log.Printf("[tunnel] instance %d (%s): no agent cert stored, cannot connect", inst.ID, inst.Name)
		return nil
	}

	client := NewTunnelClient(inst.ID, inst.Name)
	if err := client.Connect(ctx, addr, agentCertPEM); err != nil {
		log.Printf("[tunnel] instance %d (%s): connect failed: %v", inst.ID, inst.Name, err)
		return err
	}

	// Close any stale connection before storing the new one.
	Manager.Remove(inst.ID)
	Manager.Set(inst.ID, client)
	log.Printf("[tunnel] instance %d (%s): connected", inst.ID, inst.Name)
	return nil
}

// DisconnectInstance tears down the tunnel for the given instance.
func DisconnectInstance(instanceID uint) {
	Manager.Remove(instanceID)
	log.Printf("[tunnel] instance %d: disconnected", instanceID)
}

// ReconnectLoop periodically checks the tunnel health and reconnects if needed.
// It runs until ctx is cancelled. Callers should launch it in a goroutine.
func ReconnectLoop(ctx context.Context, inst *database.Instance, resolver AddrResolver, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			existing := Manager.Get(inst.ID)
			if existing != nil && !existing.IsClosed() {
				continue // healthy
			}
			log.Printf("[tunnel] instance %d (%s): session dead, reconnecting…", inst.ID, inst.Name)
			if err := ConnectInstance(ctx, inst, resolver); err != nil {
				log.Printf("[tunnel] instance %d (%s): reconnect failed: %v", inst.ID, inst.Name, err)
			}
		}
	}
}
