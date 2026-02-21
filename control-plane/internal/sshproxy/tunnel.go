package sshproxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	// tunnelCheckInterval is how often the background goroutine checks tunnel health.
	tunnelCheckInterval = 60 * time.Second

	// reconnectBaseDelay is the initial delay for reconnection backoff.
	reconnectBaseDelay = 2 * time.Second

	// reconnectMaxDelay is the maximum delay for reconnection backoff.
	reconnectMaxDelay = 60 * time.Second

	// reconnectMaxAttempts is the maximum number of reconnection attempts before giving up.
	reconnectMaxAttempts = 5
)

// TunnelType represents the direction of the tunnel.
type TunnelType string

const (
	// TunnelTypeForward is a local-to-remote tunnel (ssh -L equivalent).
	TunnelTypeForward TunnelType = "forward"

	// TunnelTypeReverse is a remote-to-local tunnel (ssh -R equivalent).
	TunnelTypeReverse TunnelType = "reverse"
)

// TunnelConfig describes a tunnel to be established.
type TunnelConfig struct {
	LocalPort  int        // Port on the control plane side
	RemotePort int        // Port on the agent side
	Type       TunnelType // forward or reverse
}

// ActiveTunnel represents a running tunnel with its configuration and state.
type ActiveTunnel struct {
	Config    TunnelConfig
	Label     string // human-readable label (e.g. "VNC", "Gateway")
	LocalPort int    // the actual bound local port
	Status    string // "active", "connecting", "error"
	Error     string // last error message, if any
	LastCheck time.Time

	listener net.Listener // the local listener (for reverse tunnels)
	cancel   context.CancelFunc
}

// TunnelManager manages SSH tunnels for all instances.
type TunnelManager struct {
	sshMgr *SSHManager

	mu      sync.RWMutex
	tunnels map[uint][]*ActiveTunnel // instanceID -> tunnels

	cancel context.CancelFunc
}

// NewTunnelManager creates a new TunnelManager that uses the given SSHManager
// for obtaining SSH connections to instances.
func NewTunnelManager(sshMgr *SSHManager) *TunnelManager {
	return &TunnelManager{
		sshMgr:  sshMgr,
		tunnels: make(map[uint][]*ActiveTunnel),
	}
}

// CreateReverseTunnel creates a reverse tunnel (SSH -R equivalent) that forwards
// traffic from a remote port on the agent to a local port on the control plane.
// It allocates a free local port, starts a local listener, and for each incoming
// connection on that listener, opens a channel to the remote port via SSH.
func (tm *TunnelManager) CreateReverseTunnel(ctx context.Context, instanceID uint, label string, remotePort, localPort int) (int, error) {
	client, ok := tm.sshMgr.GetConnection(instanceID)
	if !ok {
		return 0, fmt.Errorf("no SSH connection for instance %d", instanceID)
	}

	// Bind a local listener
	listenAddr := fmt.Sprintf("127.0.0.1:%d", localPort)
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return 0, fmt.Errorf("listen on %s: %w", listenAddr, err)
	}

	boundPort := listener.Addr().(*net.TCPAddr).Port

	tunnelCtx, tunnelCancel := context.WithCancel(ctx)

	tunnel := &ActiveTunnel{
		Config: TunnelConfig{
			LocalPort:  boundPort,
			RemotePort: remotePort,
			Type:       TunnelTypeReverse,
		},
		Label:     label,
		LocalPort: boundPort,
		Status:    "active",
		LastCheck: time.Now(),
		listener:  listener,
		cancel:    tunnelCancel,
	}

	tm.addTunnel(instanceID, tunnel)

	// Accept connections and forward to remote via SSH
	go tm.acceptLoop(tunnelCtx, tunnel, listener, client, remotePort, instanceID)

	return boundPort, nil
}

// CreateTunnelForVNC creates a reverse tunnel for the agent's VNC/Selkies service (port 3000).
func (tm *TunnelManager) CreateTunnelForVNC(ctx context.Context, instanceID uint) (int, error) {
	port, err := tm.CreateReverseTunnel(ctx, instanceID, "VNC", 3000, 0)
	if err != nil {
		return 0, fmt.Errorf("create VNC tunnel for instance %d: %w", instanceID, err)
	}

	log.Printf("VNC tunnel for instance %d: localhost:%d -> agent:3000", instanceID, port)
	return port, nil
}

// CreateTunnelForGateway creates a reverse tunnel for the agent's gateway service.
func (tm *TunnelManager) CreateTunnelForGateway(ctx context.Context, instanceID uint, gatewayPort int) (int, error) {
	if gatewayPort == 0 {
		gatewayPort = 8080
	}

	port, err := tm.CreateReverseTunnel(ctx, instanceID, "Gateway", gatewayPort, 0)
	if err != nil {
		return 0, fmt.Errorf("create Gateway tunnel for instance %d: %w", instanceID, err)
	}

	log.Printf("Gateway tunnel for instance %d: localhost:%d -> agent:%d", instanceID, port, gatewayPort)
	return port, nil
}

// StartTunnelsForInstance establishes all required tunnels for an instance.
// It uses EnsureConnected to set up SSH access before creating tunnels.
func (tm *TunnelManager) StartTunnelsForInstance(ctx context.Context, instanceID uint, orch Orchestrator) error {
	// Ensure SSH connection is established (uploads key on-demand)
	_, err := tm.sshMgr.EnsureConnected(ctx, instanceID, orch)
	if err != nil {
		return fmt.Errorf("ensure connected for instance %d: %w", instanceID, err)
	}

	// Check if tunnels already exist
	tm.mu.RLock()
	existing := tm.tunnels[instanceID]
	tm.mu.RUnlock()

	if len(existing) > 0 {
		// Tunnels already exist; check if they're healthy
		allHealthy := true
		for _, t := range existing {
			if t.Status != "active" {
				allHealthy = false
				break
			}
		}
		if allHealthy {
			return nil
		}
		// Some tunnels are unhealthy; tear down and recreate
		tm.StopTunnelsForInstance(instanceID)
	}

	// Create VNC tunnel
	_, err = tm.CreateTunnelForVNC(ctx, instanceID)
	if err != nil {
		log.Printf("Failed to create VNC tunnel for instance %d: %v", instanceID, err)
		// Continue to try gateway tunnel
	}

	// Create Gateway tunnel
	_, err = tm.CreateTunnelForGateway(ctx, instanceID, 0)
	if err != nil {
		log.Printf("Failed to create Gateway tunnel for instance %d: %v", instanceID, err)
	}

	return nil
}

// StopTunnelsForInstance closes all tunnels for an instance and cleans up state.
func (tm *TunnelManager) StopTunnelsForInstance(instanceID uint) error {
	tm.mu.Lock()
	tunnels, ok := tm.tunnels[instanceID]
	if ok {
		delete(tm.tunnels, instanceID)
	}
	tm.mu.Unlock()

	if !ok {
		return nil
	}

	for _, t := range tunnels {
		t.cancel()
		if t.listener != nil {
			t.listener.Close()
		}
	}

	log.Printf("Stopped %d tunnels for instance %d", len(tunnels), instanceID)
	return nil
}

// StopAll closes all tunnels for all instances. Used during shutdown.
func (tm *TunnelManager) StopAll() {
	if tm.cancel != nil {
		tm.cancel()
	}

	tm.mu.Lock()
	allTunnels := tm.tunnels
	tm.tunnels = make(map[uint][]*ActiveTunnel)
	tm.mu.Unlock()

	count := 0
	for _, tunnels := range allTunnels {
		for _, t := range tunnels {
			t.cancel()
			if t.listener != nil {
				t.listener.Close()
			}
			count++
		}
	}

	log.Printf("Stopped all SSH tunnels (%d total)", count)
}

// GetTunnelsForInstance returns a copy of the active tunnels for an instance.
func (tm *TunnelManager) GetTunnelsForInstance(instanceID uint) []ActiveTunnel {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	tunnels := tm.tunnels[instanceID]
	result := make([]ActiveTunnel, len(tunnels))
	for i, t := range tunnels {
		result[i] = ActiveTunnel{
			Config:    t.Config,
			Label:     t.Label,
			LocalPort: t.LocalPort,
			Status:    t.Status,
			Error:     t.Error,
			LastCheck: t.LastCheck,
		}
	}
	return result
}

// GetVNCLocalPort returns the local port for the VNC tunnel of an instance, or 0 if not found.
func (tm *TunnelManager) GetVNCLocalPort(instanceID uint) int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	for _, t := range tm.tunnels[instanceID] {
		if t.Label == "VNC" && t.Status == "active" {
			return t.LocalPort
		}
	}
	return 0
}

// GetGatewayLocalPort returns the local port for the Gateway tunnel of an instance, or 0 if not found.
func (tm *TunnelManager) GetGatewayLocalPort(instanceID uint) int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	for _, t := range tm.tunnels[instanceID] {
		if t.Label == "Gateway" && t.Status == "active" {
			return t.LocalPort
		}
	}
	return 0
}

// acceptLoop accepts connections on the local listener and forwards them to the
// remote port over SSH. Each accepted connection is handled in a goroutine.
func (tm *TunnelManager) acceptLoop(ctx context.Context, tunnel *ActiveTunnel, listener net.Listener, client *ssh.Client, remotePort int, instanceID uint) {
	defer listener.Close()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Set a deadline so we periodically check ctx.Done()
		if tcpListener, ok := listener.(*net.TCPListener); ok {
			tcpListener.SetDeadline(time.Now().Add(1 * time.Second))
		}

		conn, err := listener.Accept()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			log.Printf("Tunnel accept error for instance %d:%d: %v", instanceID, remotePort, err)
			tm.setTunnelStatus(instanceID, tunnel, "error", err.Error())
			return
		}

		go tm.forwardConnection(ctx, conn, client, remotePort, instanceID, tunnel)
	}
}

// forwardConnection forwards a single local connection to the remote port over SSH.
func (tm *TunnelManager) forwardConnection(ctx context.Context, localConn net.Conn, client *ssh.Client, remotePort int, instanceID uint, tunnel *ActiveTunnel) {
	defer localConn.Close()

	remoteAddr := fmt.Sprintf("127.0.0.1:%d", remotePort)
	remoteConn, err := client.Dial("tcp", remoteAddr)
	if err != nil {
		log.Printf("SSH dial to %s:%d failed for instance %d: %v", "127.0.0.1", remotePort, instanceID, err)
		return
	}
	defer remoteConn.Close()

	// Bidirectional copy
	done := make(chan struct{}, 2)

	go func() {
		io.Copy(remoteConn, localConn)
		done <- struct{}{}
	}()

	go func() {
		io.Copy(localConn, remoteConn)
		done <- struct{}{}
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}

// setTunnelStatus updates the status of a tunnel.
func (tm *TunnelManager) setTunnelStatus(instanceID uint, tunnel *ActiveTunnel, status, errMsg string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tunnel.Status = status
	tunnel.Error = errMsg
	tunnel.LastCheck = time.Now()
}

// InstanceLister provides a way to get currently running instance IDs.
// This decouples the tunnel manager from the database package.
type InstanceLister func(ctx context.Context) ([]uint, error)

// StartBackgroundManager starts a background goroutine that maintains tunnels
// for all running instances. It periodically:
//   - Ensures tunnels exist for running instances
//   - Removes tunnels for stopped/deleted instances
//   - Logs tunnel status for observability
func (tm *TunnelManager) StartBackgroundManager(ctx context.Context, listRunning InstanceLister, orch Orchestrator) {
	bgCtx, bgCancel := context.WithCancel(ctx)
	tm.cancel = bgCancel

	go func() {
		// Initial delay to let instances start up
		select {
		case <-time.After(10 * time.Second):
		case <-bgCtx.Done():
			return
		}

		ticker := time.NewTicker(tunnelCheckInterval)
		defer ticker.Stop()

		for {
			tm.reconcile(bgCtx, listRunning, orch)

			select {
			case <-bgCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	log.Printf("SSH tunnel background manager started (interval: %s)", tunnelCheckInterval)
}

// reconcile ensures tunnels are up for running instances and removed for stopped ones.
func (tm *TunnelManager) reconcile(ctx context.Context, listRunning InstanceLister, orch Orchestrator) {
	running, err := listRunning(ctx)
	if err != nil {
		log.Printf("Tunnel reconcile: failed to list running instances: %v", err)
		return
	}

	runningSet := make(map[uint]bool, len(running))
	for _, id := range running {
		runningSet[id] = true
	}

	// Remove tunnels for instances that are no longer running
	tm.mu.RLock()
	var toRemove []uint
	for id := range tm.tunnels {
		if !runningSet[id] {
			toRemove = append(toRemove, id)
		}
	}
	tm.mu.RUnlock()

	for _, id := range toRemove {
		log.Printf("Tunnel reconcile: removing tunnels for stopped instance %d", id)
		tm.StopTunnelsForInstance(id)
	}

	// Ensure tunnels exist for running instances
	for _, id := range running {
		if err := tm.StartTunnelsForInstance(ctx, id, orch); err != nil {
			log.Printf("Tunnel reconcile: failed to start tunnels for instance %d: %v", id, err)
		}
	}

	// Log summary
	tm.mu.RLock()
	totalTunnels := 0
	for _, tunnels := range tm.tunnels {
		totalTunnels += len(tunnels)
	}
	tm.mu.RUnlock()

	if totalTunnels > 0 || len(running) > 0 {
		log.Printf("Tunnel reconcile: %d tunnels across %d instances (%d running)",
			totalTunnels, len(tm.tunnels), len(running))
	}
}

// addTunnel adds a tunnel to the instance's tunnel list.
func (tm *TunnelManager) addTunnel(instanceID uint, tunnel *ActiveTunnel) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.tunnels[instanceID] = append(tm.tunnels[instanceID], tunnel)
}
