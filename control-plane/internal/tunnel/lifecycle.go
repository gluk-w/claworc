package tunnel

import (
	"context"
	"log"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/crypto"
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

	// Load the control-plane client certificate for mTLS. If unavailable
	// we still connect (the agent may fall back to RequireAnyClientCert).
	cpCert, _, cpErr := crypto.GetControlPlaneCert()
	if cpErr != nil {
		log.Printf("[tunnel] instance %d (%s): warning: could not load control-plane client cert: %v", inst.ID, inst.Name, cpErr)
	}

	client := NewTunnelClient(inst.ID, inst.Name)
	if err := client.Connect(ctx, addr, agentCertPEM, cpCert); err != nil {
		log.Printf("[tunnel] instance %d (%s): connect failed: %v", inst.ID, inst.Name, err)
		return err
	}

	// Close any stale connection before storing the new one.
	Manager.Remove(inst.ID)
	Manager.Set(inst.ID, client)
	client.StartPing(ctx)
	log.Printf("[tunnel] instance %d (%s): connected", inst.ID, inst.Name)
	return nil
}

// DisconnectInstance tears down the tunnel for the given instance.
func DisconnectInstance(instanceID uint) {
	Manager.Remove(instanceID)
	log.Printf("[tunnel] instance %d: disconnected", instanceID)
}

// Backoff defaults for reconnection. Tests may override these.
var (
	backoffMin = 1 * time.Second
	backoffMax = 60 * time.Second
)

// ReconnectLoop periodically checks the tunnel health and reconnects if needed
// using exponential backoff (1s → 2s → 4s → … → 60s cap). The backoff resets
// to 1s after a successful reconnect. It runs until ctx is cancelled.
// Callers should launch it in a goroutine.
func ReconnectLoop(ctx context.Context, inst *database.Instance, resolver AddrResolver) {
	backoff := backoffMin

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			existing := Manager.Get(inst.ID)
			if existing != nil && !existing.IsClosed() {
				// Healthy — reset backoff and keep checking at base interval.
				backoff = backoffMin
				continue
			}
			log.Printf("[tunnel] instance %d (%s): session dead, reconnecting (backoff %s)…", inst.ID, inst.Name, backoff)
			if err := ConnectInstance(ctx, inst, resolver); err != nil {
				log.Printf("[tunnel] instance %d (%s): reconnect failed: %v", inst.ID, inst.Name, err)
				// Exponential backoff on failure.
				backoff *= 2
				if backoff > backoffMax {
					backoff = backoffMax
				}
			} else {
				// Success — reset backoff.
				backoff = backoffMin
			}
		}
	}
}
