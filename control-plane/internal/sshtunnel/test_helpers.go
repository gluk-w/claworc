package sshtunnel

import (
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/gluk-w/claworc/control-plane/internal/sshterminal"
)

// SetGlobalForTest sets the global SSHManager and TunnelManager for tests.
func SetGlobalForTest(sm *sshmanager.SSHManager, tm *TunnelManager) {
	registryMu.Lock()
	defer registryMu.Unlock()
	globalSSHManager = sm
	globalTunnelManager = tm
}

// SetGlobalForTestWithSessions sets all global managers including SessionManager for tests.
func SetGlobalForTestWithSessions(sm *sshmanager.SSHManager, tm *TunnelManager, sessMgr *sshterminal.SessionManager) {
	registryMu.Lock()
	defer registryMu.Unlock()
	globalSSHManager = sm
	globalTunnelManager = tm
	globalSessionManager = sessMgr
}

// ResetGlobalForTest clears the global SSHManager and TunnelManager.
func ResetGlobalForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	globalSSHManager = nil
	globalTunnelManager = nil
	globalSessionManager = nil
}

// TestTunnelOpts holds parameters for creating a test tunnel.
type TestTunnelOpts struct {
	Service       string
	Type          string
	LocalPort     int
	RemotePort    int
	Closed        bool
	LastCheckTime time.Time
	LastCheckErr  error
}

// AddTestTunnel adds a synthetic ActiveTunnel to the TunnelManager for testing.
func AddTestTunnel(tm *TunnelManager, instanceName string, opts TestTunnelOpts) {
	tunnel := &ActiveTunnel{
		Config: TunnelConfig{
			LocalPort:  opts.LocalPort,
			RemotePort: opts.RemotePort,
			Type:       TunnelType(opts.Type),
			Protocol:   ProtocolTCP,
			Service:    ServiceLabel(opts.Service),
		},
		LocalPort: opts.LocalPort,
		StartedAt: time.Now(),
		closed:    opts.Closed,
		lastCheck: opts.LastCheckTime,
		lastError: opts.LastCheckErr,
	}
	tm.addTunnel(instanceName, tunnel)
}
