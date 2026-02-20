package tunnel

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// TunnelClient manages a single yamux-over-WebSocket tunnel to an agent instance.
type TunnelClient struct {
	instanceID   uint
	instanceName string
	session      *yamux.Session
	mu           sync.Mutex
}

// NewTunnelClient creates a new TunnelClient for the given instance.
func NewTunnelClient(instanceID uint, instanceName string) *TunnelClient {
	return &TunnelClient{
		instanceID:   instanceID,
		instanceName: instanceName,
	}
}

// Connect dials the agent's tunnel endpoint over WebSocket with mTLS and
// establishes a yamux client session. The agentCertPEM is the PEM-encoded
// certificate the agent presented during TLS; it acts as the pinned CA for
// cert verification (no shared CA needed). If clientCert is non-nil, it is
// presented to the agent for mutual authentication.
func (tc *TunnelClient) Connect(ctx context.Context, agentAddr string, agentCertPEM string, clientCert *tls.Certificate) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if tc.session != nil {
		return fmt.Errorf("tunnel client already connected to instance %d", tc.instanceID)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(agentCertPEM)) {
		return fmt.Errorf("failed to parse agent certificate PEM")
	}

	tlsCfg := &tls.Config{
		RootCAs:    pool,
		ServerName: fmt.Sprintf("agent-%s", tc.instanceName),
		MinVersion: tls.VersionTLS12,
	}

	if clientCert != nil {
		tlsCfg.Certificates = []tls.Certificate{*clientCert}
	}

	wsConn, _, err := websocket.Dial(ctx, fmt.Sprintf("wss://%s/tunnel", agentAddr), &websocket.DialOptions{
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsCfg,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("websocket dial to %s: %w", agentAddr, err)
	}

	netConn := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)

	session, err := yamux.Client(netConn, nil)
	if err != nil {
		wsConn.CloseNow()
		return fmt.Errorf("yamux client init: %w", err)
	}

	tc.session = session
	return nil
}

// OpenStream opens a new yamux stream over the tunnel. The returned net.Conn
// can be used for bidirectional communication with the agent.
func (tc *TunnelClient) OpenStream(ctx context.Context) (net.Conn, error) {
	tc.mu.Lock()
	s := tc.session
	tc.mu.Unlock()

	if s == nil {
		return nil, fmt.Errorf("tunnel client not connected for instance %d", tc.instanceID)
	}

	return s.Open()
}

// OpenChannel opens a new yamux stream, writes the channel header (e.g.
// "neko\n"), and returns the stream positioned for the caller to send
// HTTP requests or raw data. The agent-side router reads this header and
// dispatches the stream to the appropriate handler.
func (tc *TunnelClient) OpenChannel(ctx context.Context, channel string) (net.Conn, error) {
	conn, err := tc.OpenStream(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := conn.Write([]byte(channel + "\n")); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write channel header %q: %w", channel, err)
	}

	return conn, nil
}

// IsClosed reports whether the underlying yamux session is nil or closed.
func (tc *TunnelClient) IsClosed() bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.session == nil || tc.session.IsClosed()
}

// SetSession replaces the yamux session directly. Intended for testing.
func (tc *TunnelClient) SetSession(session *yamux.Session) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.session = session
}

// Close tears down the yamux session and underlying WebSocket connection.
func (tc *TunnelClient) Close() error {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if tc.session == nil {
		return nil
	}

	err := tc.session.Close()
	tc.session = nil
	return err
}

// Ping defaults. Tests may override PingInterval.
var PingInterval = 30 * time.Second

const PingTimeout = 5 * time.Second

// StartPing launches a goroutine that sends a "ping\n" message over the
// tunnel every PingInterval. It expects the agent to respond with "pong\n"
// within PingTimeout. On failure the yamux session is closed, which causes
// IsClosed() to return true and the reconnect loop to trigger a reconnect.
// The goroutine exits when ctx is cancelled or the session closes.
func (tc *TunnelClient) StartPing(ctx context.Context) {
	go tc.pingLoop(ctx)
}

func (tc *TunnelClient) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if tc.IsClosed() {
				return
			}
			if err := tc.sendPing(ctx); err != nil {
				log.Printf("[tunnel] instance %d (%s): ping failed: %v — closing session",
					tc.instanceID, tc.instanceName, err)
				tc.Close()
				return
			}
		}
	}
}

func (tc *TunnelClient) sendPing(ctx context.Context) error {
	conn, err := tc.OpenChannel(ctx, ChannelPing)
	if err != nil {
		return fmt.Errorf("open ping channel: %w", err)
	}
	defer conn.Close()

	// Set a deadline for the pong response.
	conn.SetReadDeadline(time.Now().Add(PingTimeout))

	// Read the response line.
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read pong: %w", err)
	}

	if line != "pong\n" {
		return fmt.Errorf("unexpected ping response: %q", line)
	}

	return nil
}
