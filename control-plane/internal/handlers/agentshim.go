package handlers

import (
	"context"
	"fmt"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	// Register the native OpenClaw adapter and the exec-shim adapter with
	// the agentshim factory.
	_ "github.com/gluk-w/claworc/control-plane/internal/agentshim/openclawnative"
	_ "github.com/gluk-w/claworc/control-plane/internal/agentshim/shimexec"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	gossh "golang.org/x/crypto/ssh"
)

// init wires the process-wide agentshim factory to the handlers' transport
// managers. The closures read SSHMgr/TunnelMgr at call time (they are
// assigned by main after startup), mirroring how getTunnelPort works.
func init() {
	agentshim.SetDefaultFactory(&agentshim.Factory{
		TunnelPort: func(instanceID uint, service string) (int, error) {
			return getTunnelPort(instanceID, service)
		},
		SSHClient: shimSSHClient,
	})
}

// shimSSHClient resolves an established SSH connection for an instance,
// honoring its source-IP restrictions — the same path config handlers used
// before the shim extraction.
func shimSSHClient(ctx context.Context, inst database.Instance) (*gossh.Client, error) {
	if SSHMgr == nil {
		return nil, fmt.Errorf("SSH manager not initialized")
	}
	orch := orchestrator.Get()
	if orch == nil {
		return nil, fmt.Errorf("no orchestrator available")
	}
	return SSHMgr.EnsureConnectedWithIPCheck(ctx, inst.ID, orch, inst.AllowedSourceIPs)
}
