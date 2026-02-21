package sshmanager

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gluk-w/claworc/control-plane/internal/logutil"
)

// Default keepalive interval for SSH connections.
const defaultKeepaliveInterval = 30 * time.Second

// SSHManager manages SSH connections to agent instances.
// It maintains a pool of active SSH clients keyed by instance name,
// enforces a maximum connection limit, and runs periodic keepalive
// health checks on established connections.
type SSHManager struct {
	mu             sync.RWMutex
	clients        map[string]*ssh.Client
	maxConnections int

	// keepalive lifecycle
	keepaliveCtx    context.Context
	keepaliveCancel context.CancelFunc
	keepaliveWg     sync.WaitGroup
}

// NewSSHManager creates a new SSHManager with the given maximum connection limit.
// A maxConnections value of 0 or less means unlimited connections.
func NewSSHManager(maxConnections int) *SSHManager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &SSHManager{
		clients:         make(map[string]*ssh.Client),
		maxConnections:  maxConnections,
		keepaliveCtx:    ctx,
		keepaliveCancel: cancel,
	}
	m.keepaliveWg.Add(1)
	go m.keepaliveLoop()
	return m
}

// Connect establishes an SSH connection to the given host and stores it in the
// connection pool. If a connection already exists for the instance, it is closed
// first. The private key is loaded from privateKeyPath to authenticate.
func (m *SSHManager) Connect(ctx context.Context, instanceName, host string, port int, privateKeyPath string) (*ssh.Client, error) {
	if instanceName == "" {
		return nil, fmt.Errorf("connect: instance name is empty")
	}
	if host == "" {
		return nil, fmt.Errorf("connect: host is empty")
	}
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("connect: invalid port %d", port)
	}

	// Check connection limit before proceeding
	m.mu.RLock()
	count := len(m.clients)
	_, exists := m.clients[instanceName]
	m.mu.RUnlock()

	if !exists && m.maxConnections > 0 && count >= m.maxConnections {
		return nil, fmt.Errorf("connect: maximum connections (%d) reached", m.maxConnections)
	}

	// Load and parse private key
	keyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("connect: read private key %s: %w", logutil.SanitizeForLog(privateKeyPath), err)
	}

	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("connect: parse private key: %w", err)
	}

	config := &ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	// Use context for connection timeout
	var client *ssh.Client
	dialDone := make(chan struct{})
	var dialErr error

	go func() {
		defer close(dialDone)
		client, dialErr = ssh.Dial("tcp", addr, config)
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("connect: context cancelled: %w", ctx.Err())
	case <-dialDone:
		if dialErr != nil {
			return nil, fmt.Errorf("connect to %s: %w", logutil.SanitizeForLog(addr), dialErr)
		}
	}

	// Close any existing connection for this instance
	m.mu.Lock()
	if old, ok := m.clients[instanceName]; ok && old != nil {
		old.Close()
	}
	m.clients[instanceName] = client
	m.mu.Unlock()

	log.Printf("[ssh] connected to %s at %s", logutil.SanitizeForLog(instanceName), logutil.SanitizeForLog(addr))
	return client, nil
}

// GetConnection returns the SSH client for the given instance and whether it exists.
func (m *SSHManager) GetConnection(instanceName string) (*ssh.Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	client, ok := m.clients[instanceName]
	return client, ok
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

// Close closes and removes the SSH connection for the given instance.
func (m *SSHManager) Close(instanceName string) error {
	m.mu.Lock()
	client, ok := m.clients[instanceName]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("close: no SSH connection for instance %q", instanceName)
	}
	delete(m.clients, instanceName)
	m.mu.Unlock()

	if client != nil {
		if err := client.Close(); err != nil {
			return fmt.Errorf("close SSH connection for %s: %w", logutil.SanitizeForLog(instanceName), err)
		}
	}
	log.Printf("[ssh] closed connection for %s", logutil.SanitizeForLog(instanceName))
	return nil
}

// CloseAll closes all SSH connections, stops the keepalive loop, and clears the
// client pool. Returns the first error encountered, if any.
func (m *SSHManager) CloseAll() error {
	// Stop the keepalive loop
	m.keepaliveCancel()
	m.keepaliveWg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	count := 0
	for name, client := range m.clients {
		if client != nil {
			if err := client.Close(); err != nil {
				log.Printf("[ssh] error closing connection for %s: %v", name, err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		count++
	}
	m.clients = make(map[string]*ssh.Client)
	if count > 0 {
		log.Printf("[ssh] closed all %d connection(s)", count)
	}
	return firstErr
}

// ConnectionCount returns the number of active connections.
func (m *SSHManager) ConnectionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.clients)
}

// keepaliveLoop runs periodic keepalive checks on all SSH connections.
// Connections that fail the keepalive check are removed from the pool.
func (m *SSHManager) keepaliveLoop() {
	defer m.keepaliveWg.Done()
	ticker := time.NewTicker(defaultKeepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.keepaliveCtx.Done():
			return
		case <-ticker.C:
			m.checkConnections()
		}
	}
}

// checkConnections sends a keepalive request to each connection and removes
// any that have become unresponsive.
func (m *SSHManager) checkConnections() {
	m.mu.RLock()
	names := make([]string, 0, len(m.clients))
	for name := range m.clients {
		names = append(names, name)
	}
	m.mu.RUnlock()

	for _, name := range names {
		m.mu.RLock()
		client, ok := m.clients[name]
		m.mu.RUnlock()
		if !ok || client == nil {
			continue
		}

		// Send a keepalive request (global request with want-reply)
		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		if err != nil {
			log.Printf("[ssh] keepalive failed for %s: %v, removing connection", logutil.SanitizeForLog(name), err)
			m.mu.Lock()
			delete(m.clients, name)
			m.mu.Unlock()
			client.Close()
		}
	}
}
