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

// TunnelManager creates and tracks SSH tunnels for agent instances.
type TunnelManager struct {
	sshManager *sshmanager.SSHManager

	mu      sync.RWMutex
	tunnels map[string][]*ActiveTunnel // keyed by instance name
}

// NewTunnelManager creates a TunnelManager backed by the given SSHManager.
func NewTunnelManager(sshManager *sshmanager.SSHManager) *TunnelManager {
	return &TunnelManager{
		sshManager: sshManager,
		tunnels:    make(map[string][]*ActiveTunnel),
	}
}

// CreateReverseTunnel establishes a reverse tunnel (SSH -R equivalent).
// Traffic arriving at remotePort on the agent is forwarded to localPort on the control plane.
// If localPort is 0, an available port is auto-assigned.
func (tm *TunnelManager) CreateReverseTunnel(ctx context.Context, instanceName string, remotePort, localPort int) (int, error) {
	return tm.createReverseTunnel(ctx, instanceName, remotePort, localPort, ServiceCustom)
}

// createReverseTunnel is the internal implementation that accepts a service label.
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

			// Bidirectional copy
			go bidirectionalCopy(tunnelCtx, conn, remote)
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

// bidirectionalCopy pipes data between two connections until one side closes or errors.
func bidirectionalCopy(ctx context.Context, a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		defer func() { done <- struct{}{} }()
		io.Copy(dst, src)
	}
	go cp(a, b)
	go cp(b, a)

	select {
	case <-done:
	case <-ctx.Done():
	}
	a.Close()
	b.Close()
	// Wait for the second copy to finish
	<-done
}
