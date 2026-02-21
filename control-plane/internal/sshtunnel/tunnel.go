package sshtunnel

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
)

// TunnelType indicates the direction of the tunnel.
type TunnelType string

const (
	// TunnelForward is a local-to-remote port forward (SSH -L).
	TunnelForward TunnelType = "forward"
	// TunnelReverse is a remote-to-local port forward (SSH -R).
	TunnelReverse TunnelType = "reverse"
)

// TunnelProtocol indicates the transport protocol for the tunnel.
type TunnelProtocol string

const (
	ProtocolTCP  TunnelProtocol = "tcp"
	ProtocolUnix TunnelProtocol = "unix"
)

// Well-known agent service ports.
const (
	DefaultVNCPort     = 3000 // Selkies/VNC port on agent
	DefaultGatewayPort = 8080 // OpenClaw gateway port on agent
)

// ServiceLabel identifies the purpose of a tunnel.
type ServiceLabel string

const (
	ServiceVNC     ServiceLabel = "vnc"
	ServiceGateway ServiceLabel = "gateway"
	ServiceCustom  ServiceLabel = "custom"
)

// TunnelConfig describes the desired tunnel parameters.
type TunnelConfig struct {
	LocalPort  int            // Port on the control plane side
	RemotePort int            // Port on the agent side
	Type       TunnelType     // Forward or reverse
	Protocol   TunnelProtocol // TCP or Unix socket
	Service    ServiceLabel   // Purpose of this tunnel (vnc, gateway, custom)
}

// TunnelMetrics contains health and performance metrics for a tunnel.
type TunnelMetrics struct {
	Service             ServiceLabel `json:"service"`
	LocalPort           int          `json:"local_port"`
	RemotePort          int          `json:"remote_port"`
	CreatedAt           time.Time    `json:"created_at"`
	LastCheck           time.Time    `json:"last_check"`
	LastSuccessfulCheck time.Time    `json:"last_successful_check"`
	LastError           string       `json:"last_error,omitempty"`
	BytesTransferred    int64        `json:"bytes_transferred"`
	Healthy             bool         `json:"healthy"`
}

// ActiveTunnel represents a running tunnel with its configuration and lifecycle controls.
type ActiveTunnel struct {
	Config    TunnelConfig
	LocalPort int       // Actual bound local port (may differ from Config.LocalPort if auto-assigned)
	StartedAt time.Time
	cancel    context.CancelFunc
	listener  net.Listener
	mu        sync.Mutex
	closed    bool
	lastCheck time.Time
	lastError error
	// Health metrics
	lastSuccessfulCheck time.Time
	bytesTransferred    int64
}

// Close shuts down the active tunnel.
func (t *ActiveTunnel) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	if t.cancel != nil {
		t.cancel()
	}
	if t.listener != nil {
		return t.listener.Close()
	}
	return nil
}

// IsClosed returns whether the tunnel has been closed.
func (t *ActiveTunnel) IsClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

// LastCheck returns the time of the last health check and any error.
func (t *ActiveTunnel) LastCheck() (time.Time, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastCheck, t.lastError
}

// updateHealthCheck records the result of a health check.
func (t *ActiveTunnel) updateHealthCheck(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastCheck = time.Now()
	t.lastError = err
	if err == nil {
		t.lastSuccessfulCheck = time.Now()
	}
}

// Metrics returns a snapshot of the tunnel's health and performance metrics.
func (t *ActiveTunnel) Metrics() TunnelMetrics {
	t.mu.Lock()
	defer t.mu.Unlock()
	m := TunnelMetrics{
		Service:             t.Config.Service,
		LocalPort:           t.LocalPort,
		RemotePort:          t.Config.RemotePort,
		CreatedAt:           t.StartedAt,
		LastCheck:           t.lastCheck,
		LastSuccessfulCheck: t.lastSuccessfulCheck,
		BytesTransferred:    t.bytesTransferred,
		Healthy:             !t.closed && (t.lastError == nil || t.lastCheck.IsZero()),
	}
	if t.lastError != nil {
		m.LastError = t.lastError.Error()
	}
	return m
}

// addBytesTransferred adds n bytes to the tunnel's transfer counter.
func (t *ActiveTunnel) addBytesTransferred(n int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bytesTransferred += n
}

// countingConn wraps a net.Conn to track bytes transferred through a tunnel.
type countingConn struct {
	net.Conn
	tunnel *ActiveTunnel
}

func (c *countingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.tunnel.addBytesTransferred(int64(n))
	}
	return n, err
}

func (c *countingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.tunnel.addBytesTransferred(int64(n))
	}
	return n, err
}

// Health check and reconnection parameters.
//
// Two tiers of health monitoring run concurrently:
//   - Per-instance monitor (defaultHealthCheckInterval): detects closed tunnels
//     and triggers reconnection with exponential backoff.
//   - Global health check (tunnelHealthCheckInterval): probes all tunnel ports
//     via TCP and closes unresponsive tunnels so per-instance monitors can
//     recreate them.
const (
	defaultHealthCheckInterval = 10 * time.Second // Per-instance monitor interval
	tunnelHealthCheckInterval  = 60 * time.Second // Global probe-all-tunnels interval
	tunnelHealthProbeTimeout   = 5 * time.Second  // TCP dial timeout for port probe
	reconnectBaseDelay         = 1 * time.Second   // Initial reconnection backoff delay
	reconnectMaxDelay          = 60 * time.Second  // Maximum reconnection backoff delay
	reconnectBackoffFactor     = 2                  // Backoff multiplier per failed attempt
)

// TunnelManager creates and tracks SSH tunnels for agent instances.
// All tunnels for a given instance multiplex over a single SSH connection,
// so adding VNC + Gateway tunnels does not create additional SSH sessions.
// Each tunnel binds an ephemeral local port (127.0.0.1:0) and forwards
// inbound connections through the SSH channel to the agent's service port.
//
// A background goroutine probes all active tunnel ports every 60 seconds
// and closes tunnels whose local listeners are no longer accepting connections.
// Per-instance monitors (started via StartTunnelsForInstance) then detect the
// closed tunnels and attempt reconnection with exponential backoff.
//
// Performance: On loopback, SSH tunnel overhead is ~55µs per HTTP request
// and supports >27,000 req/s with 10 concurrent clients. WebSocket messages
// add ~55µs latency per round-trip vs direct connection.
type TunnelManager struct {
	sshManager *sshmanager.SSHManager

	mu      sync.RWMutex
	tunnels map[string][]*ActiveTunnel // keyed by instance name

	monMu    sync.Mutex
	monitors map[string]context.CancelFunc // per-instance health monitor cancellers

	// Global health check lifecycle
	healthCtx    context.Context
	healthCancel context.CancelFunc
	healthWg     sync.WaitGroup

	// Per-instance reconnection counters
	metricsMu  sync.RWMutex
	reconnects map[string]int64
}

// NewTunnelManager creates a TunnelManager backed by the given SSHManager.
// It starts a background health check goroutine that probes all active tunnel
// ports every 60 seconds.
func NewTunnelManager(sshManager *sshmanager.SSHManager) *TunnelManager {
	ctx, cancel := context.WithCancel(context.Background())
	tm := &TunnelManager{
		sshManager:   sshManager,
		tunnels:      make(map[string][]*ActiveTunnel),
		monitors:     make(map[string]context.CancelFunc),
		healthCtx:    ctx,
		healthCancel: cancel,
		reconnects:   make(map[string]int64),
	}
	tm.healthWg.Add(1)
	go tm.globalHealthCheckLoop()
	return tm
}

// CreateReverseTunnel establishes a reverse tunnel (SSH -R equivalent).
// Traffic arriving at remotePort on the agent is forwarded to localPort on the control plane.
// If localPort is 0, an available port is auto-assigned.
func (tm *TunnelManager) CreateReverseTunnel(ctx context.Context, instanceName string, remotePort, localPort int) (int, error) {
	return tm.createReverseTunnel(ctx, instanceName, remotePort, localPort, ServiceCustom)
}

// createReverseTunnel is the internal implementation that accepts a service label.
// It binds a local TCP listener and spawns a goroutine that accepts connections,
// forwarding each through the SSH channel via client.Dial("direct-tcpip").
// Multiple concurrent connections are supported — each gets its own SSH channel
// but all share the same underlying SSH session (connection multiplexing).
func (tm *TunnelManager) createReverseTunnel(ctx context.Context, instanceName string, remotePort, localPort int, service ServiceLabel) (int, error) {
	client, err := tm.sshManager.GetClient(instanceName)
	if err != nil {
		return 0, fmt.Errorf("get SSH client: %w", err)
	}

	// Open a local listener for the control plane side
	listenAddr := fmt.Sprintf("127.0.0.1:%d", localPort)
	localListener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return 0, fmt.Errorf("listen on local port: %w", err)
	}

	// Determine actual bound port
	boundPort := localListener.Addr().(*net.TCPAddr).Port

	tunnelCtx, cancel := context.WithCancel(ctx)

	tunnel := &ActiveTunnel{
		Config: TunnelConfig{
			LocalPort:  localPort,
			RemotePort: remotePort,
			Type:       TunnelReverse,
			Protocol:   ProtocolTCP,
			Service:    service,
		},
		LocalPort: boundPort,
		StartedAt: time.Now(),
		cancel:    cancel,
		listener:  localListener,
	}

	// Accept connections on local listener and forward them through SSH to the remote port
	go func() {
		defer localListener.Close()
		for {
			select {
			case <-tunnelCtx.Done():
				return
			default:
			}

			// Set a deadline so we can check for context cancellation
			if dl, ok := localListener.(*net.TCPListener); ok {
				dl.SetDeadline(time.Now().Add(1 * time.Second))
			}

			conn, err := localListener.Accept()
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				if tunnelCtx.Err() != nil {
					return
				}
				log.Printf("[tunnel] accept error for %s remote:%d: %v", instanceName, remotePort, err)
				return
			}

			// Dial the remote port through the SSH connection
			remoteAddr := fmt.Sprintf("127.0.0.1:%d", remotePort)
			remote, err := client.Dial("tcp", remoteAddr)
			if err != nil {
				log.Printf("[tunnel] SSH dial to %s:%s failed: %v", instanceName, remoteAddr, err)
				conn.Close()
				continue
			}

			// Bidirectional copy with byte counting
			counted := &countingConn{Conn: conn, tunnel: tunnel}
			go bidirectionalCopy(tunnelCtx, counted, remote)
		}
	}()

	tm.addTunnel(instanceName, tunnel)

	log.Printf("[tunnel] reverse tunnel created: %s local:%d -> remote:%d (service=%s)", instanceName, boundPort, remotePort, service)
	return boundPort, nil
}

// CreateTunnelForVNC creates a reverse tunnel from the agent's VNC port (3000)
// to an auto-assigned local port on the control plane.
func (tm *TunnelManager) CreateTunnelForVNC(ctx context.Context, instanceName string) (int, error) {
	return tm.createReverseTunnel(ctx, instanceName, DefaultVNCPort, 0, ServiceVNC)
}

// CreateTunnelForGateway creates a reverse tunnel from the agent's gateway port
// to an auto-assigned local port on the control plane.
func (tm *TunnelManager) CreateTunnelForGateway(ctx context.Context, instanceName string, gatewayPort int) (int, error) {
	if gatewayPort <= 0 {
		gatewayPort = DefaultGatewayPort
	}
	return tm.createReverseTunnel(ctx, instanceName, gatewayPort, 0, ServiceGateway)
}

// GetTunnels returns a snapshot of active tunnels for the given instance.
func (tm *TunnelManager) GetTunnels(instanceName string) []*ActiveTunnel {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	tunnels := tm.tunnels[instanceName]
	result := make([]*ActiveTunnel, len(tunnels))
	copy(result, tunnels)
	return result
}

// GetAllTunnels returns a snapshot of all active tunnels keyed by instance name.
func (tm *TunnelManager) GetAllTunnels() map[string][]*ActiveTunnel {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	result := make(map[string][]*ActiveTunnel, len(tm.tunnels))
	for name, tunnels := range tm.tunnels {
		copied := make([]*ActiveTunnel, len(tunnels))
		copy(copied, tunnels)
		result[name] = copied
	}
	return result
}

// CloseTunnels closes all tunnels for the given instance and removes them from state.
func (tm *TunnelManager) CloseTunnels(instanceName string) {
	tm.mu.Lock()
	tunnels := tm.tunnels[instanceName]
	delete(tm.tunnels, instanceName)
	tm.mu.Unlock()

	for _, t := range tunnels {
		if err := t.Close(); err != nil {
			log.Printf("[tunnel] error closing tunnel for %s: %v", instanceName, err)
		}
	}
	if len(tunnels) > 0 {
		log.Printf("[tunnel] closed %d tunnel(s) for %s", len(tunnels), instanceName)
	}
}

// CloseAll closes every tunnel across all instances.
func (tm *TunnelManager) CloseAll() {
	tm.mu.Lock()
	allTunnels := tm.tunnels
	tm.tunnels = make(map[string][]*ActiveTunnel)
	tm.mu.Unlock()

	count := 0
	for name, tunnels := range allTunnels {
		for _, t := range tunnels {
			if err := t.Close(); err != nil {
				log.Printf("[tunnel] error closing tunnel for %s: %v", name, err)
			}
			count++
		}
	}
	if count > 0 {
		log.Printf("[tunnel] closed all %d tunnel(s)", count)
	}
}

// StartTunnelsForInstance creates all standard tunnels (VNC + Gateway) for the
// given instance and starts a background health monitor that will attempt to
// reconnect tunnels on failure with exponential backoff.
func (tm *TunnelManager) StartTunnelsForInstance(ctx context.Context, instanceName string) error {
	// Create VNC tunnel
	vncPort, err := tm.CreateTunnelForVNC(ctx, instanceName)
	if err != nil {
		return fmt.Errorf("create VNC tunnel: %w", err)
	}
	log.Printf("[tunnel] VNC tunnel for %s ready on local port %d", instanceName, vncPort)

	// Create Gateway tunnel
	gwPort, err := tm.CreateTunnelForGateway(ctx, instanceName, DefaultGatewayPort)
	if err != nil {
		// Clean up VNC tunnel on failure
		tm.CloseTunnels(instanceName)
		return fmt.Errorf("create gateway tunnel: %w", err)
	}
	log.Printf("[tunnel] gateway tunnel for %s ready on local port %d", instanceName, gwPort)

	// Start health monitoring goroutine
	monCtx, monCancel := context.WithCancel(ctx)
	tm.monMu.Lock()
	// Cancel any existing monitor for this instance
	if prev, ok := tm.monitors[instanceName]; ok {
		prev()
	}
	tm.monitors[instanceName] = monCancel
	tm.monMu.Unlock()

	go tm.monitorInstance(monCtx, instanceName)

	log.Printf("[tunnel] all tunnels started for %s", instanceName)
	return nil
}

// StopTunnelsForInstance stops the health monitor and closes all tunnels for the instance.
func (tm *TunnelManager) StopTunnelsForInstance(instanceName string) error {
	// Stop health monitor
	tm.monMu.Lock()
	if cancel, ok := tm.monitors[instanceName]; ok {
		cancel()
		delete(tm.monitors, instanceName)
	}
	tm.monMu.Unlock()

	// Close all tunnels
	tm.CloseTunnels(instanceName)

	log.Printf("[tunnel] all tunnels stopped for %s", instanceName)
	return nil
}

// Shutdown stops the global health check, all per-instance monitors, and closes
// all tunnels. Use this during application shutdown to ensure clean cleanup.
func (tm *TunnelManager) Shutdown() {
	// Stop global health check
	if tm.healthCancel != nil {
		tm.healthCancel()
		tm.healthWg.Wait()
	}

	// Stop all monitors
	tm.monMu.Lock()
	for name, cancel := range tm.monitors {
		cancel()
		delete(tm.monitors, name)
	}
	tm.monMu.Unlock()

	// Close all tunnels
	tm.CloseAll()
	log.Printf("[tunnel] shutdown complete")
}

// monitorInstance periodically checks tunnel health and attempts reconnection.
func (tm *TunnelManager) monitorInstance(ctx context.Context, instanceName string) {
	ticker := time.NewTicker(defaultHealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tm.checkAndReconnectTunnels(ctx, instanceName)
		}
	}
}

// checkAndReconnectTunnels inspects each tunnel for the instance and recreates any that
// have failed. Reconnection uses exponential backoff per service type.
func (tm *TunnelManager) checkAndReconnectTunnels(ctx context.Context, instanceName string) {
	// First remove closed tunnels from tracking
	tm.removeClosed(instanceName)

	tunnels := tm.GetTunnels(instanceName)

	// Check which service types have active tunnels
	hasVNC := false
	hasGateway := false
	for _, t := range tunnels {
		if t.IsClosed() {
			continue
		}
		switch t.Config.Service {
		case ServiceVNC:
			hasVNC = true
		case ServiceGateway:
			hasGateway = true
		}
		// Update health check timestamp for active tunnels
		t.updateHealthCheck(nil)
	}

	// Don't attempt reconnection if the SSH client is gone
	if !tm.sshManager.HasClient(instanceName) {
		if !hasVNC || !hasGateway {
			log.Printf("[tunnel] SSH client missing for %s, skipping reconnection", instanceName)
		}
		return
	}

	// Reconnect missing VNC tunnel
	if !hasVNC {
		tm.reconnectTunnel(ctx, instanceName, ServiceVNC)
	}
	// Reconnect missing gateway tunnel
	if !hasGateway {
		tm.reconnectTunnel(ctx, instanceName, ServiceGateway)
	}
}

// reconnectTunnel attempts to recreate a tunnel with exponential backoff.
func (tm *TunnelManager) reconnectTunnel(ctx context.Context, instanceName string, service ServiceLabel) {
	delay := reconnectBaseDelay
	for attempt := 1; ; attempt++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		log.Printf("[tunnel] reconnecting %s tunnel for %s (attempt %d)", service, instanceName, attempt)

		var err error
		switch service {
		case ServiceVNC:
			_, err = tm.CreateTunnelForVNC(ctx, instanceName)
		case ServiceGateway:
			_, err = tm.CreateTunnelForGateway(ctx, instanceName, DefaultGatewayPort)
		default:
			log.Printf("[tunnel] unknown service label %q, cannot reconnect", service)
			return
		}

		if err == nil {
			tm.incrementReconnects(instanceName)
			log.Printf("[tunnel] reconnected %s tunnel for %s after %d attempt(s)", service, instanceName, attempt)
			return
		}

		log.Printf("[tunnel] reconnect %s tunnel for %s failed (attempt %d): %v", service, instanceName, attempt, err)

		// Wait with exponential backoff
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay *= time.Duration(reconnectBackoffFactor)
		if delay > reconnectMaxDelay {
			delay = reconnectMaxDelay
		}
	}
}

// CheckTunnelHealth verifies a specific tunnel is functional by attempting a TCP
// connection to its local listening port. tunnelType should be a ServiceLabel
// value (e.g., "vnc", "gateway").
func (tm *TunnelManager) CheckTunnelHealth(instanceName string, tunnelType string) error {
	service := ServiceLabel(tunnelType)
	tunnels := tm.GetTunnels(instanceName)

	for _, t := range tunnels {
		if t.Config.Service != service {
			continue
		}
		if t.IsClosed() {
			return fmt.Errorf("tunnel %s for %s is closed", tunnelType, instanceName)
		}

		err := tm.probeTunnelPort(t)
		t.updateHealthCheck(err)
		if err != nil {
			return fmt.Errorf("tunnel %s for %s health check failed: %w", tunnelType, instanceName, err)
		}
		return nil
	}

	return fmt.Errorf("no %s tunnel found for instance %s", tunnelType, instanceName)
}

// GetTunnelMetrics returns health and performance metrics for all tunnels
// belonging to the given instance.
func (tm *TunnelManager) GetTunnelMetrics(instanceName string) []TunnelMetrics {
	tunnels := tm.GetTunnels(instanceName)
	metrics := make([]TunnelMetrics, 0, len(tunnels))
	for _, t := range tunnels {
		metrics = append(metrics, t.Metrics())
	}
	return metrics
}

// GetReconnectionCount returns the total number of successful tunnel
// reconnections for the given instance.
func (tm *TunnelManager) GetReconnectionCount(instanceName string) int64 {
	tm.metricsMu.RLock()
	defer tm.metricsMu.RUnlock()
	return tm.reconnects[instanceName]
}

func (tm *TunnelManager) addTunnel(instanceName string, tunnel *ActiveTunnel) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.tunnels[instanceName] = append(tm.tunnels[instanceName], tunnel)
}

// removeClosed removes tunnels that have been closed from the tracking map.
func (tm *TunnelManager) removeClosed(instanceName string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tunnels := tm.tunnels[instanceName]
	active := tunnels[:0]
	for _, t := range tunnels {
		if !t.IsClosed() {
			active = append(active, t)
		}
	}
	if len(active) == 0 {
		delete(tm.tunnels, instanceName)
	} else {
		tm.tunnels[instanceName] = active
	}
}

// globalHealthCheckLoop runs a periodic health check across all active tunnels.
// It probes each tunnel's local listening port via TCP and closes tunnels that
// are no longer accepting connections so that per-instance monitors can
// recreate them.
func (tm *TunnelManager) globalHealthCheckLoop() {
	defer tm.healthWg.Done()
	ticker := time.NewTicker(tunnelHealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-tm.healthCtx.Done():
			return
		case <-ticker.C:
			tm.runGlobalHealthCheck()
		}
	}
}

// runGlobalHealthCheck probes every active tunnel and logs health status.
// Tunnels that fail the TCP probe are closed so that the per-instance monitor
// can detect the closure and attempt reconnection.
func (tm *TunnelManager) runGlobalHealthCheck() {
	allTunnels := tm.GetAllTunnels()
	if len(allTunnels) == 0 {
		return
	}

	healthy, unhealthy := 0, 0
	for instanceName, tunnels := range allTunnels {
		for _, t := range tunnels {
			if t.IsClosed() {
				continue
			}

			err := tm.probeTunnelPort(t)
			t.updateHealthCheck(err)

			if err != nil {
				unhealthy++
				log.Printf("[tunnel-health] %s %s tunnel (port %d) unhealthy: %v",
					instanceName, t.Config.Service, t.LocalPort, err)
				// Close the tunnel so the per-instance monitor can recreate it
				if closeErr := t.Close(); closeErr != nil {
					log.Printf("[tunnel-health] error closing unhealthy tunnel for %s: %v", instanceName, closeErr)
				}
			} else {
				healthy++
			}
		}
	}

	log.Printf("[tunnel-health] check complete: %d healthy, %d unhealthy", healthy, unhealthy)
}

// probeTunnelPort attempts a TCP connection to the tunnel's local listening port
// to verify it is still accepting connections.
func (tm *TunnelManager) probeTunnelPort(t *ActiveTunnel) error {
	addr := fmt.Sprintf("127.0.0.1:%d", t.LocalPort)
	conn, err := net.DialTimeout("tcp", addr, tunnelHealthProbeTimeout)
	if err != nil {
		return fmt.Errorf("TCP probe to %s failed: %w", addr, err)
	}
	conn.Close()
	return nil
}

// incrementReconnects increments the reconnection counter for an instance.
func (tm *TunnelManager) incrementReconnects(instanceName string) {
	tm.metricsMu.Lock()
	defer tm.metricsMu.Unlock()
	tm.reconnects[instanceName]++
}

// bidirectionalCopy pipes data between two connections until one side closes or errors.
// It spawns two goroutines (a→b and b→a). When either direction finishes (EOF or error)
// or the context is cancelled, both connections are closed to unblock the other goroutine,
// then we wait for the second copy to finish to avoid goroutine leaks.
func bidirectionalCopy(ctx context.Context, a, b net.Conn) {
	done := make(chan struct{}, 2) // buffered for 2 so both goroutines can signal without blocking
	cp := func(dst, src net.Conn) {
		defer func() { done <- struct{}{} }()
		io.Copy(dst, src)
	}
	go cp(a, b)
	go cp(b, a)

	// Wait for the first copy to finish or context cancellation
	select {
	case <-done:
	case <-ctx.Done():
	}
	// Close both connections to unblock the other copy goroutine
	a.Close()
	b.Close()
	// Wait for the second copy to finish
	<-done
}
