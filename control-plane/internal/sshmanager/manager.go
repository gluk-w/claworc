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

// Default reconnection parameters.
const (
	DefaultMaxRetries      = 10
	reconnectBaseDelay     = 1 * time.Second
	reconnectMaxDelay      = 16 * time.Second
	reconnectBackoffFactor = 2
)


// ConnectionParams stores the parameters needed to reconnect an SSH connection.
type ConnectionParams struct {
	Host           string `json:"host"`
	Port           int    `json:"port"`
	PrivateKeyPath string `json:"private_key_path"`
}

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
// health checks on established connections. When a connection fails,
// it automatically attempts reconnection with exponential backoff.
type SSHManager struct {
	mu             sync.RWMutex
	clients        map[string]*ssh.Client
	metrics        map[string]*ConnectionMetrics
	params         map[string]*ConnectionParams
	reconnecting   map[string]bool // tracks instances currently being reconnected
	maxConnections int

	// connection state tracking
	stateTracker *ConnectionStateTracker

	// event tracking
	eventsMu sync.RWMutex
	events   map[string][]ConnectionEvent

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
		params:          make(map[string]*ConnectionParams),
		reconnecting:    make(map[string]bool),
		stateTracker:    NewConnectionStateTracker(),
		events:          make(map[string][]ConnectionEvent),
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

	m.stateTracker.SetState(instanceName, StateConnecting)

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
		m.stateTracker.SetState(instanceName, StateDisconnected)
		return nil, fmt.Errorf("connect: context cancelled: %w", ctx.Err())
	case <-dialDone:
		if dialErr != nil {
			m.stateTracker.SetState(instanceName, StateDisconnected)
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
	m.params[instanceName] = &ConnectionParams{
		Host:           host,
		Port:           port,
		PrivateKeyPath: privateKeyPath,
	}
	m.mu.Unlock()

	m.stateTracker.SetState(instanceName, StateConnected)
	m.emitEvent(instanceName, EventConnected, fmt.Sprintf("connected to %s", logutil.SanitizeForLog(addr)))
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
	delete(m.params, instanceName)
	m.stateTracker.ClearInstance(instanceName)
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
	delete(m.params, instanceName)
	m.mu.Unlock()

	if client != nil {
		if err := client.Close(); err != nil {
			return fmt.Errorf("close SSH connection for %s: %w", logutil.SanitizeForLog(instanceName), err)
		}
	}
	m.stateTracker.SetState(instanceName, StateDisconnected)
	m.emitEvent(instanceName, EventDisconnected, "connection closed")
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
	m.params = make(map[string]*ConnectionParams)
	m.reconnecting = make(map[string]bool)
	m.stateTracker.ClearAll()
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
// When the check fails, it also emits an EventHealthCheckFailed event.
func (m *SSHManager) recordHealthCheck(instanceName string, err error) {
	m.mu.Lock()
	met, ok := m.metrics[instanceName]
	if !ok {
		m.mu.Unlock()
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
	m.mu.Unlock()

	if err != nil {
		m.emitEvent(instanceName, EventHealthCheckFailed, err.Error())
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
// command-based health check. Failed connections trigger automatic reconnection.
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
			log.Printf("[ssh] keepalive failed for %s: %v, triggering reconnection", logutil.SanitizeForLog(name), err)
			m.recordHealthCheck(name, err)
			m.stateTracker.SetState(name, StateDisconnected)
			m.emitEvent(name, EventDisconnected, fmt.Sprintf("keepalive failed: %v", err))
			// Remove the dead client but keep params for reconnection
			m.mu.Lock()
			delete(m.clients, name)
			m.mu.Unlock()
			client.Close()
			// Trigger background reconnection
			m.triggerReconnect(name, "keepalive failure")
			continue
		}

		// Run the full health check command
		if hcErr := m.HealthCheck(name); hcErr != nil {
			log.Printf("[ssh] health check failed for %s: %v, triggering reconnection", logutil.SanitizeForLog(name), hcErr)
			m.stateTracker.SetState(name, StateDisconnected)
			m.emitEvent(name, EventDisconnected, fmt.Sprintf("health check failed: %v", hcErr))
			// Remove the dead client but keep params for reconnection
			m.mu.Lock()
			client, ok := m.clients[name]
			delete(m.clients, name)
			m.mu.Unlock()
			if ok && client != nil {
				client.Close()
			}
			m.triggerReconnect(name, "health check failure")
		}
	}
}

// triggerReconnect starts a background reconnection for the given instance
// if one is not already in progress.
func (m *SSHManager) triggerReconnect(instanceName, reason string) {
	m.mu.Lock()
	if m.reconnecting[instanceName] {
		m.mu.Unlock()
		return
	}
	params, hasParams := m.params[instanceName]
	if !hasParams {
		m.mu.Unlock()
		log.Printf("[ssh] no connection params for %s, cannot reconnect", logutil.SanitizeForLog(instanceName))
		return
	}
	paramsCopy := *params
	m.reconnecting[instanceName] = true
	m.mu.Unlock()

	m.stateTracker.SetState(instanceName, StateReconnecting)

	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.reconnecting, instanceName)
			m.mu.Unlock()
		}()
		err := m.reconnectWithBackoff(m.keepaliveCtx, instanceName, &paramsCopy, DefaultMaxRetries)
		if err != nil {
			log.Printf("[ssh] reconnection gave up for %s: %v", logutil.SanitizeForLog(instanceName), err)
		}
	}()
}

// reconnectWithBackoff attempts to reconnect to an SSH instance with exponential
// backoff. It starts with a 1s delay and doubles each time, capping at 16s.
// After maxRetries unsuccessful attempts, it gives up and marks the instance offline.
func (m *SSHManager) reconnectWithBackoff(ctx context.Context, instanceName string, params *ConnectionParams, maxRetries int) error {
	if maxRetries <= 0 {
		maxRetries = DefaultMaxRetries
	}

	m.emitEvent(instanceName, EventReconnecting, fmt.Sprintf("starting reconnection (max %d retries)", maxRetries))

	delay := reconnectBaseDelay
	for attempt := 1; attempt <= maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during reconnection")
		default:
		}

		log.Printf("[ssh] reconnecting %s (attempt %d/%d, reason: connection lost)",
			logutil.SanitizeForLog(instanceName), attempt, maxRetries)

		// Close any stale connection before reconnecting
		m.mu.Lock()
		if old, ok := m.clients[instanceName]; ok && old != nil {
			old.Close()
			delete(m.clients, instanceName)
		}
		m.mu.Unlock()

		connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := m.Connect(connectCtx, instanceName, params.Host, params.Port, params.PrivateKeyPath)
		cancel()

		if err == nil {
			m.emitEvent(instanceName, EventReconnectSuccess, fmt.Sprintf("reconnected after %d attempt(s)", attempt))
			log.Printf("[ssh] reconnected %s after %d attempt(s)", logutil.SanitizeForLog(instanceName), attempt)
			return nil
		}

		log.Printf("[ssh] reconnect attempt %d/%d for %s failed: %v",
			attempt, maxRetries, logutil.SanitizeForLog(instanceName), err)

		// Wait with exponential backoff before next attempt
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during reconnection backoff")
		case <-time.After(delay):
		}
		delay *= time.Duration(reconnectBackoffFactor)
		if delay > reconnectMaxDelay {
			delay = reconnectMaxDelay
		}
	}

	m.stateTracker.SetState(instanceName, StateFailed)
	m.emitEvent(instanceName, EventReconnectFailed, fmt.Sprintf("gave up after %d attempts", maxRetries))
	// Clean up params since we gave up
	m.mu.Lock()
	delete(m.params, instanceName)
	delete(m.metrics, instanceName)
	m.mu.Unlock()

	return fmt.Errorf("reconnection failed after %d attempts for %s", maxRetries, logutil.SanitizeForLog(instanceName))
}

// IsReconnecting returns whether a reconnection attempt is in progress for the instance.
func (m *SSHManager) IsReconnecting(instanceName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reconnecting[instanceName]
}

// GetConnectionParams returns the stored connection parameters for an instance, or nil.
func (m *SSHManager) GetConnectionParams(instanceName string) *ConnectionParams {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.params[instanceName]
	if !ok {
		return nil
	}
	cp := *p
	return &cp
}

// GetConnectionState returns the current connection state for the given instance.
func (m *SSHManager) GetConnectionState(instanceName string) ConnectionState {
	return m.stateTracker.GetState(instanceName)
}

// SetConnectionState updates the connection state for the given instance.
func (m *SSHManager) SetConnectionState(instanceName string, state ConnectionState) {
	m.stateTracker.SetState(instanceName, state)
}

// GetStateTransitions returns the state transition history for the given instance.
func (m *SSHManager) GetStateTransitions(instanceName string) []StateTransition {
	return m.stateTracker.GetTransitions(instanceName)
}

// GetAllConnectionStates returns a copy of all current connection states.
func (m *SSHManager) GetAllConnectionStates() map[string]ConnectionState {
	return m.stateTracker.GetAllStates()
}

// OnConnectionStateChange registers a callback for connection state changes.
func (m *SSHManager) OnConnectionStateChange(cb StateCallback) {
	m.stateTracker.OnStateChange(cb)
}

