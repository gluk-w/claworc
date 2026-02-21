// Package sshproxy provides SSH connection and tunnel management for agent instances.
//
// It consolidates three concerns into a single package:
//   - Key management (keys.go): ED25519 key pair generation, persistence, and loading.
//   - Connection management (manager.go): SSH connections to agent instances, keyed by
//     instance ID (uint) for stability across instance renames.
//   - Tunnel management (tunnel.go): SSH tunnels (reverse port forwards) over managed
//     connections, also keyed by instance ID.
//
// The central types are SSHManager and TunnelManager. SSHManager owns the SSH key pair
// and maintains one multiplexed SSH connection per instance. TunnelManager depends on
// SSHManager to obtain connections and creates tunnels over them. Callers typically
// interact through TunnelManager.StartTunnelsForInstance, which delegates to
// SSHManager.EnsureConnected for on-demand connection setup.
//
// All lookups use the database instance ID (uint) rather than the instance name (string).
// This ensures that connections and tunnels remain valid even if the instance display
// name changes, and avoids the need to track name-to-ID mappings.
package sshproxy

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

	// connectTimeout is the default timeout for establishing SSH connections.
	connectTimeout = 30 * time.Second
)

// Orchestrator defines the orchestrator methods needed by EnsureConnected.
type Orchestrator interface {
	ConfigureSSHAccess(ctx context.Context, instanceID uint, publicKey string) error
	GetSSHAddress(ctx context.Context, instanceID uint) (host string, port int, err error)
}

// SSHManager manages SSH connections to agent instances.
// It holds the global private key and public key, and maintains a map of active
// connections keyed by instance ID (uint). Instance IDs are stable across renames,
// making them safer than names for long-lived connection maps. SSH multiplexes
// channels over a single TCP connection, so one connection per instance suffices.
type SSHManager struct {
	keyMu     sync.RWMutex // protects signer and publicKey during key rotation
	signer    ssh.Signer
	publicKey string

	mu    sync.RWMutex
	conns map[uint]*managedConn // keyed by instance ID; IDs are stable across renames

	healthCancel context.CancelFunc // cancel function for the background health checker

	// Reconnection fields (protected by reconnMu, separate from conns mutex)
	reconnMu       sync.RWMutex
	orch           Orchestrator                // orchestrator for reconnection key upload and address lookup
	eventListeners []EventListener             // connection state change listeners
	reconnecting   map[uint]context.CancelFunc // active reconnection goroutines, keyed by instance ID

	// Connection state tracking (has its own mutex)
	stateTracker *stateTracker

	// Connection event log (has its own mutex)
	eventLog *eventLog
}

// managedConn wraps an SSH client with its cancel function for stopping keepalive.
type managedConn struct {
	client  *ssh.Client
	cancel  context.CancelFunc
	metrics *ConnectionMetrics
}

// getSigner returns the current SSH signer, safe for concurrent use during key rotation.
func (m *SSHManager) getSigner() ssh.Signer {
	m.keyMu.RLock()
	defer m.keyMu.RUnlock()
	return m.signer
}

// getPublicKey returns the current public key string, safe for concurrent use during key rotation.
func (m *SSHManager) getPublicKey() string {
	m.keyMu.RLock()
	defer m.keyMu.RUnlock()
	return m.publicKey
}

// ReloadKeys atomically replaces the SSH key pair used for future connections.
// Existing connections are not affected (they already completed their SSH handshake).
// This is used during key rotation to switch to the new key pair.
func (m *SSHManager) ReloadKeys(signer ssh.Signer, publicKey string) {
	m.keyMu.Lock()
	defer m.keyMu.Unlock()
	m.signer = signer
	m.publicKey = publicKey
	log.Printf("SSH keys reloaded (fingerprint: %s)", ssh.FingerprintSHA256(signer.PublicKey()))
}

// NewSSHManager creates a new SSHManager with the given private key signer
// and public key string (OpenSSH authorized_keys format).
func NewSSHManager(privateKey ssh.Signer, publicKey string) *SSHManager {
	return &SSHManager{
		signer:       privateKey,
		publicKey:    publicKey,
		conns:        make(map[uint]*managedConn),
		reconnecting: make(map[uint]context.CancelFunc),
		stateTracker: newStateTracker(),
		eventLog:     newEventLog(),
	}
}

// Connect establishes an SSH connection to the given host:port using the global
// private key, and stores it in the connection map keyed by instanceID.
// If a connection already exists for the instance, it is closed first.
func (m *SSHManager) Connect(ctx context.Context, instanceID uint, host string, port int) (*ssh.Client, error) {
	cfg := &ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(m.getSigner()),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         connectTimeout,
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	m.stateTracker.setState(instanceID, StateConnecting, fmt.Sprintf("connecting to %s", addr))

	// Use context for connection timeout
	dialer := net.Dialer{Timeout: connectTimeout}
	netConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		m.stateTracker.setState(instanceID, StateDisconnected, fmt.Sprintf("dial failed: %v", err))
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(netConn, addr, cfg)
	if err != nil {
		netConn.Close()
		m.stateTracker.setState(instanceID, StateDisconnected, fmt.Sprintf("ssh handshake failed: %v", err))
		return nil, fmt.Errorf("ssh handshake with %s: %w", addr, err)
	}

	client := ssh.NewClient(sshConn, chans, reqs)

	// Close any existing connection for this instance
	m.mu.Lock()
	if existing, ok := m.conns[instanceID]; ok {
		existing.cancel()
		existing.client.Close()
	}

	// Start keepalive goroutine
	keepCtx, keepCancel := context.WithCancel(context.Background())
	mc := &managedConn{
		client: client,
		cancel: keepCancel,
		metrics: &ConnectionMetrics{
			ConnectedAt: time.Now(),
		},
	}
	m.conns[instanceID] = mc
	m.mu.Unlock()

	go m.keepalive(keepCtx, instanceID, client)

	m.stateTracker.setState(instanceID, StateConnected, fmt.Sprintf("connected to %s", addr))
	log.Printf("SSH connected to instance %d (%s)", instanceID, addr)
	return client, nil
}

// GetConnection returns an existing SSH connection for the given instance ID.
// Returns the client and true if found, nil and false otherwise.
func (m *SSHManager) GetConnection(instanceID uint) (*ssh.Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mc, ok := m.conns[instanceID]
	if !ok {
		return nil, false
	}
	return mc.client, true
}

// Close closes the SSH connection for the given instance ID and removes it from the map.
func (m *SSHManager) Close(instanceID uint) error {
	m.mu.Lock()
	mc, ok := m.conns[instanceID]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.conns, instanceID)
	m.mu.Unlock()

	mc.cancel()
	if err := mc.client.Close(); err != nil {
		m.stateTracker.setState(instanceID, StateDisconnected, fmt.Sprintf("closed with error: %v", err))
		return fmt.Errorf("close ssh connection for instance %d: %w", instanceID, err)
	}
	m.stateTracker.setState(instanceID, StateDisconnected, "connection closed")
	log.Printf("SSH disconnected from instance %d", instanceID)
	return nil
}

// CloseAll closes all SSH connections. Used during shutdown.
func (m *SSHManager) CloseAll() error {
	m.StopHealthChecker()
	m.cancelAllReconnections()

	m.mu.Lock()
	conns := m.conns
	m.conns = make(map[uint]*managedConn)
	m.mu.Unlock()

	var firstErr error
	for id, mc := range conns {
		mc.cancel()
		if err := mc.client.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close ssh connection for instance %d: %w", id, err)
		}
	}
	log.Printf("All SSH connections closed (%d total)", len(conns))
	return firstErr
}

// IsConnected checks if a healthy connection exists for the given instance ID.
func (m *SSHManager) IsConnected(instanceID uint) bool {
	m.mu.RLock()
	mc, ok := m.conns[instanceID]
	m.mu.RUnlock()

	if !ok {
		return false
	}

	// Send a keepalive to verify the connection is still alive
	_, _, err := mc.client.SendRequest("keepalive@openssh.com", true, nil)
	return err == nil
}

// EnsureConnected is the single entry point for obtaining an SSH connection to
// an instance by its ID. It checks for an existing healthy connection first,
// and if none exists, uploads the public key via the orchestrator and
// establishes a new SSH connection. The instance ID is used as the map key
// so connections survive instance renames.
func (m *SSHManager) EnsureConnected(ctx context.Context, instanceID uint, orch Orchestrator) (*ssh.Client, error) {
	// 1. Check if a healthy connection already exists
	if m.IsConnected(instanceID) {
		client, _ := m.GetConnection(instanceID)
		return client, nil
	}

	// 2. Get instance SSH address from orchestrator
	host, port, err := orch.GetSSHAddress(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("get ssh address for instance %d: %w", instanceID, err)
	}

	// 3. Upload public key to the instance
	if err := orch.ConfigureSSHAccess(ctx, instanceID, m.getPublicKey()); err != nil {
		return nil, fmt.Errorf("configure ssh access for instance %d: %w", instanceID, err)
	}

	// 4. Establish SSH connection
	client, err := m.Connect(ctx, instanceID, host, port)
	if err != nil {
		return nil, fmt.Errorf("ssh connect to instance %d: %w", instanceID, err)
	}

	return client, nil
}

// GetPublicKeyFingerprint returns the SHA256 fingerprint of the global SSH public key.
func (m *SSHManager) GetPublicKeyFingerprint() string {
	return ssh.FingerprintSHA256(m.getSigner().PublicKey())
}

// GetPublicKey returns the global SSH public key in OpenSSH authorized_keys format.
func (m *SSHManager) GetPublicKey() string {
	return m.getPublicKey()
}

// keepalive sends periodic keepalive requests to detect dead connections.
// If the connection is dead, it is removed from the map.
func (m *SSHManager) keepalive(ctx context.Context, instanceID uint, client *ssh.Client) {
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
				log.Printf("SSH keepalive failed for instance %d: %v, removing connection", instanceID, err)
				m.mu.Lock()
				if mc, ok := m.conns[instanceID]; ok && mc.client == client {
					delete(m.conns, instanceID)
				}
				m.mu.Unlock()
				reason := fmt.Sprintf("keepalive failed: %v", err)
				m.stateTracker.setState(instanceID, StateDisconnected, reason)
				m.emitEvent(ConnectionEvent{
					InstanceID: instanceID,
					Type:       EventDisconnected,
					Timestamp:  time.Now(),
					Details:    reason,
				})
				m.triggerReconnect(instanceID, reason)
				return
			}
		}
	}
}
