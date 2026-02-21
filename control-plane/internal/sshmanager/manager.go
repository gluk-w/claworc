package sshmanager

import (
	"fmt"
	"sync"

	"golang.org/x/crypto/ssh"
)

// SSHManager manages SSH connections to agent instances.
// It maintains a pool of active SSH clients keyed by instance name.
type SSHManager struct {
	mu      sync.RWMutex
	clients map[string]*ssh.Client
}

// NewSSHManager creates a new SSHManager.
func NewSSHManager() *SSHManager {
	return &SSHManager{
		clients: make(map[string]*ssh.Client),
	}
}

// GetClient returns the SSH client for the given instance, or an error if no connection exists.
func (m *SSHManager) GetClient(instanceName string) (*ssh.Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	client, ok := m.clients[instanceName]
	if !ok {
		return nil, fmt.Errorf("no SSH connection for instance %q", instanceName)
	}
	return client, nil
}

// SetClient stores an SSH client for the given instance.
func (m *SSHManager) SetClient(instanceName string, client *ssh.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[instanceName] = client
}

// RemoveClient removes and returns the SSH client for the given instance.
func (m *SSHManager) RemoveClient(instanceName string) *ssh.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	client := m.clients[instanceName]
	delete(m.clients, instanceName)
	return client
}

// HasClient checks if an SSH connection exists for the given instance.
func (m *SSHManager) HasClient(instanceName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.clients[instanceName]
	return ok
}
