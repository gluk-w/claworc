package sshmanager

import (
	"bytes"
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

// HealthCheckTimeout is the maximum time allowed for a single health check command.
const HealthCheckTimeout = 5 * time.Second

// ConnectionMetrics tracks health and performance data for a single SSH connection.
type ConnectionMetrics struct {
	ConnectedAt      time.Time `json:"connected_at"`
	LastHealthCheck  time.Time `json:"last_health_check"`
	SuccessfulChecks int64     `json:"successful_checks"`
	FailedChecks     int64     `json:"failed_checks"`
	Healthy          bool      `json:"healthy"`
}

// Uptime returns the duration since the connection was established.
func (cm *ConnectionMetrics) Uptime() time.Duration {
	if cm.ConnectedAt.IsZero() {
		return 0
	}
	return time.Since(cm.ConnectedAt)
}

// SSHManager manages SSH connections to agent instances.
// It maintains a pool of active SSH clients keyed by instance name,
// enforces a maximum connection limit, and runs periodic keepalive
// health checks on established connections.
type SSHManager struct {
	mu             sync.RWMutex
	clients        map[string]*ssh.Client
	metrics        map[string]*ConnectionMetrics
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
		metrics:         make(map[string]*ConnectionMetrics),
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
	m.metrics[instanceName] = &ConnectionMetrics{
		ConnectedAt: time.Now(),
		Healthy:     true,
	}
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
	if _, ok := m.metrics[instanceName]; !ok {
		m.metrics[instanceName] = &ConnectionMetrics{
			ConnectedAt: time.Now(),
			Healthy:     true,
		}
	}
}

// RemoveClient removes and returns the SSH client for the given instance.
func (m *SSHManager) RemoveClient(instanceName string) *ssh.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	client := m.clients[instanceName]
	delete(m.clients, instanceName)
	delete(m.metrics, instanceName)
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
	delete(m.metrics, instanceName)
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
	m.metrics = make(map[string]*ConnectionMetrics)
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

// HealthCheck performs a health check on the SSH connection for the given instance
// by executing a lightweight command (`echo ping`) with a 5-second timeout.
// It updates the connection metrics and returns an error if the check fails.
func (m *SSHManager) HealthCheck(instanceName string) error {
	m.mu.RLock()
	client, ok := m.clients[instanceName]
	m.mu.RUnlock()
	if !ok || client == nil {
		return fmt.Errorf("health check: no SSH connection for instance %q", instanceName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), HealthCheckTimeout)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		session, err := client.NewSession()
		if err != nil {
			errCh <- fmt.Errorf("create session: %w", err)
			return
		}
		defer session.Close()

		var out bytes.Buffer
		session.Stdout = &out
		if err := session.Run("echo ping"); err != nil {
			errCh <- fmt.Errorf("run command: %w", err)
			return
		}
		if out.String() != "ping\n" {
			errCh <- fmt.Errorf("unexpected output: %q", out.String())
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		err := fmt.Errorf("health check timed out for %s", logutil.SanitizeForLog(instanceName))
		m.recordHealthCheck(instanceName, err)
		return err
	case err := <-errCh:
		m.recordHealthCheck(instanceName, err)
		return err
	}
}

// recordHealthCheck updates the connection metrics after a health check.
func (m *SSHManager) recordHealthCheck(instanceName string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	met, ok := m.metrics[instanceName]
	if !ok {
		return
	}
	met.LastHealthCheck = time.Now()
	if err != nil {
		met.FailedChecks++
		met.Healthy = false
	} else {
		met.SuccessfulChecks++
		met.Healthy = true
	}
}

// GetMetrics returns a copy of the connection metrics for the given instance.
// Returns nil if no metrics exist for the instance.
func (m *SSHManager) GetMetrics(instanceName string) *ConnectionMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	met, ok := m.metrics[instanceName]
	if !ok {
		return nil
	}
	cp := *met
	return &cp
}

// GetAllMetrics returns a copy of all connection metrics keyed by instance name.
func (m *SSHManager) GetAllMetrics() map[string]*ConnectionMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*ConnectionMetrics, len(m.metrics))
	for name, met := range m.metrics {
		cp := *met
		result[name] = &cp
	}
	return result
}

// keepaliveLoop runs periodic health checks on all SSH connections.
// Connections that fail the health check are marked unhealthy.
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

// checkConnections performs health checks on all connections. It first sends
// a lightweight keepalive request, and for connections that pass, runs a full
// command-based health check. Failed connections are removed from the pool.
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
			m.recordHealthCheck(name, err)
			m.mu.Lock()
			delete(m.clients, name)
			m.mu.Unlock()
			client.Close()
			continue
		}

		// Run the full health check command
		if hcErr := m.HealthCheck(name); hcErr != nil {
			log.Printf("[ssh] health check failed for %s: %v, marking unhealthy", logutil.SanitizeForLog(name), hcErr)
		}
	}
}
