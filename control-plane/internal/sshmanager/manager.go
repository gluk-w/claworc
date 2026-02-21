package sshmanager

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	// keepaliveInterval is how often we send keepalive requests.
	keepaliveInterval = 30 * time.Second

	// keepaliveTimeout is how long we wait for a keepalive response.
	keepaliveTimeout = 10 * time.Second

	// connectTimeout is the default timeout for establishing SSH connections.
	connectTimeout = 30 * time.Second
)

// Orchestrator defines the orchestrator methods needed by EnsureConnected.
type Orchestrator interface {
	ConfigureSSHAccess(ctx context.Context, name string, publicKey string) error
	GetSSHAddress(ctx context.Context, name string) (host string, port int, err error)
}

// SSHManager manages SSH connections to agent instances.
// It holds the global private key and public key, and maintains a map of active
// connections, one per instance. SSH multiplexes channels over a single TCP
// connection, so a single connection per instance is sufficient.
type SSHManager struct {
	signer    ssh.Signer
	publicKey string

	mu    sync.RWMutex
	conns map[string]*managedConn
}

// managedConn wraps an SSH client with its cancel function for stopping keepalive.
type managedConn struct {
	client *ssh.Client
	cancel context.CancelFunc
}

// NewSSHManager creates a new SSHManager with the given private key signer
// and public key string (OpenSSH authorized_keys format).
func NewSSHManager(privateKey ssh.Signer, publicKey string) *SSHManager {
	return &SSHManager{
		signer:    privateKey,
		publicKey: publicKey,
		conns:     make(map[string]*managedConn),
	}
}

// Connect establishes an SSH connection to the given host:port using the global
// private key, and stores it in the connection map keyed by instanceName.
// If a connection already exists for the instance, it is closed first.
func (m *SSHManager) Connect(ctx context.Context, instanceName, host string, port int) (*ssh.Client, error) {
	cfg := &ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(m.signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         connectTimeout,
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	// Use context for connection timeout
	dialer := net.Dialer{Timeout: connectTimeout}
	netConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(netConn, addr, cfg)
	if err != nil {
		netConn.Close()
		return nil, fmt.Errorf("ssh handshake with %s: %w", addr, err)
	}

	client := ssh.NewClient(sshConn, chans, reqs)

	// Close any existing connection for this instance
	m.mu.Lock()
	if existing, ok := m.conns[instanceName]; ok {
		existing.cancel()
		existing.client.Close()
	}

	// Start keepalive goroutine
	keepCtx, keepCancel := context.WithCancel(context.Background())
	mc := &managedConn{
		client: client,
		cancel: keepCancel,
	}
	m.conns[instanceName] = mc
	m.mu.Unlock()

	go m.keepalive(keepCtx, instanceName, client)

	log.Printf("SSH connected to %s (%s)", instanceName, addr)
	return client, nil
}

// GetConnection returns an existing SSH connection for the given instance.
// Returns the client and true if found, nil and false otherwise.
func (m *SSHManager) GetConnection(instanceName string) (*ssh.Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mc, ok := m.conns[instanceName]
	if !ok {
		return nil, false
	}
	return mc.client, true
}

// Close closes a specific instance's SSH connection and removes it from the map.
func (m *SSHManager) Close(instanceName string) error {
	m.mu.Lock()
	mc, ok := m.conns[instanceName]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.conns, instanceName)
	m.mu.Unlock()

	mc.cancel()
	if err := mc.client.Close(); err != nil {
		return fmt.Errorf("close ssh connection for %s: %w", instanceName, err)
	}
	log.Printf("SSH disconnected from %s", instanceName)
	return nil
}

// CloseAll closes all SSH connections. Used during shutdown.
func (m *SSHManager) CloseAll() error {
	m.mu.Lock()
	conns := m.conns
	m.conns = make(map[string]*managedConn)
	m.mu.Unlock()

	var firstErr error
	for name, mc := range conns {
		mc.cancel()
		if err := mc.client.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close ssh connection for %s: %w", name, err)
		}
	}
	log.Printf("All SSH connections closed (%d total)", len(conns))
	return firstErr
}

// IsConnected checks if a healthy connection exists for the given instance.
func (m *SSHManager) IsConnected(instanceName string) bool {
	m.mu.RLock()
	mc, ok := m.conns[instanceName]
	m.mu.RUnlock()

	if !ok {
		return false
	}

	// Send a keepalive to verify the connection is still alive
	_, _, err := mc.client.SendRequest("keepalive@openssh.com", true, nil)
	return err == nil
}

// EnsureConnected is the single entry point for obtaining an SSH connection to
// an instance. It checks for an existing healthy connection first, and if none
// exists, uploads the public key via the orchestrator and establishes a new
// SSH connection.
func (m *SSHManager) EnsureConnected(ctx context.Context, instanceName string, orch Orchestrator) (*ssh.Client, error) {
	// 1. Check if a healthy connection already exists
	if m.IsConnected(instanceName) {
		client, _ := m.GetConnection(instanceName)
		return client, nil
	}

	// 2. Get instance SSH address from orchestrator
	host, port, err := orch.GetSSHAddress(ctx, instanceName)
	if err != nil {
		return nil, fmt.Errorf("get ssh address for %s: %w", instanceName, err)
	}

	// 3. Upload public key to the instance
	if err := orch.ConfigureSSHAccess(ctx, instanceName, m.publicKey); err != nil {
		return nil, fmt.Errorf("configure ssh access for %s: %w", instanceName, err)
	}

	// 4. Establish SSH connection
	client, err := m.Connect(ctx, instanceName, host, port)
	if err != nil {
		return nil, fmt.Errorf("ssh connect to %s: %w", instanceName, err)
	}

	return client, nil
}

// keepalive sends periodic keepalive requests to detect dead connections.
// If the connection is dead, it is removed from the map.
func (m *SSHManager) keepalive(ctx context.Context, instanceName string, client *ssh.Client) {
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// SendRequest with wantReply=true acts as a keepalive check
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				log.Printf("SSH keepalive failed for %s: %v, removing connection", instanceName, err)
				m.mu.Lock()
				if mc, ok := m.conns[instanceName]; ok && mc.client == client {
					delete(m.conns, instanceName)
				}
				m.mu.Unlock()
				return
			}
		}
	}
}
