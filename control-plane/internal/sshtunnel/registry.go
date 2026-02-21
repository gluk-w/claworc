package sshtunnel

import (
	"sync"

	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/gluk-w/claworc/control-plane/internal/sshterminal"
)

var (
	globalSSHManager     *sshmanager.SSHManager
	globalTunnelManager  *TunnelManager
	globalSessionManager *sshterminal.SessionManager
	registryMu           sync.RWMutex
)

// InitGlobal creates and stores the global SSHManager and TunnelManager instances.
// Call this once during application startup.
func InitGlobal() {
	registryMu.Lock()
	defer registryMu.Unlock()
	globalSSHManager = sshmanager.NewSSHManager(0)
	globalTunnelManager = NewTunnelManager(globalSSHManager)
	globalSessionManager = sshterminal.NewSessionManager()
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

// GetSessionManager returns the global SessionManager instance.
func GetSessionManager() *sshterminal.SessionManager {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return globalSessionManager
}
