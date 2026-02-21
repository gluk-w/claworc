package sshtunnel

import (
	"sync"

	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
)

var (
	globalSSHManager    *sshmanager.SSHManager
	globalTunnelManager *TunnelManager
	registryMu          sync.RWMutex
)

// InitGlobal creates and stores the global SSHManager and TunnelManager instances.
// Call this once during application startup.
func InitGlobal() {
	registryMu.Lock()
	defer registryMu.Unlock()
	globalSSHManager = sshmanager.NewSSHManager(0)
	globalTunnelManager = NewTunnelManager(globalSSHManager)
}

// GetSSHManager returns the global SSHManager instance.
func GetSSHManager() *sshmanager.SSHManager {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return globalSSHManager
}

// GetTunnelManager returns the global TunnelManager instance.
func GetTunnelManager() *TunnelManager {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return globalTunnelManager
}
