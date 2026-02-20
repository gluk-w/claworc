package tunnel

import "sync"

// TunnelManager tracks active tunnel connections to agent instances.
type TunnelManager struct {
	mu      sync.RWMutex
	clients map[uint]*TunnelClient
}

// NewTunnelManager creates a TunnelManager ready for use.
func NewTunnelManager() *TunnelManager {
	return &TunnelManager{
		clients: make(map[uint]*TunnelClient),
	}
}

// Get returns the TunnelClient for the given instance ID, or nil if none exists.
func (tm *TunnelManager) Get(instanceID uint) *TunnelClient {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.clients[instanceID]
}

// Set stores a TunnelClient for the given instance ID.
func (tm *TunnelManager) Set(instanceID uint, c *TunnelClient) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.clients[instanceID] = c
}

// Remove closes and removes the TunnelClient for the given instance ID.
func (tm *TunnelManager) Remove(instanceID uint) {
	tm.mu.Lock()
	c, ok := tm.clients[instanceID]
	if ok {
		delete(tm.clients, instanceID)
	}
	tm.mu.Unlock()

	if ok && c != nil {
		c.Close()
	}
}
