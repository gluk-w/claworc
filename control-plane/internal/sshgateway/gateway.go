// Package sshgateway implements the inbound SSH gateway: users connect with
// `ssh <username>+<instance>@<claworc-host>`, authenticate with a per-user
// public key registered in Claworc, and their session is bridged onto the
// control plane's existing multiplexed SSH connection to the target instance.
// The gateway never dials its own connection to an agent — it only opens new
// channels on the shared per-instance client owned by sshproxy.SSHManager.
package sshgateway

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
)

const handshakeTimeout = 30 * time.Second

// ClientProvider returns a live multiplexed outbound SSH client for an
// instance. The production implementation wraps
// SSHManager.EnsureConnectedWithIPCheck so the existing per-instance
// connection (already carrying tunnels and web terminals) is reused.
type ClientProvider func(ctx context.Context, inst *database.Instance) (*ssh.Client, error)

// Config holds the gateway's dependencies.
type Config struct {
	Addr     string // listen address, e.g. ":2222"
	HostKey  ssh.Signer
	Clients  ClientProvider
	Auditor  *sshaudit.Auditor
	MaxConns int // max concurrent inbound connections; 0 = default 64
}

// Gateway is the inbound SSH server.
type Gateway struct {
	cfg      Config
	ln       net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	limiter  *ipLimiter
	conns    atomic.Int64
	maxConns int64
}

// New creates a Gateway. Call Start to begin accepting connections.
func New(cfg Config) *Gateway {
	maxConns := int64(cfg.MaxConns)
	if maxConns <= 0 {
		maxConns = 64
	}
	return &Gateway{cfg: cfg, limiter: newIPLimiter(), maxConns: maxConns}
}

// Start begins listening and serving in background goroutines.
func (g *Gateway) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", g.cfg.Addr)
	if err != nil {
		return fmt.Errorf("ssh gateway listen on %s: %w", g.cfg.Addr, err)
	}
	g.ln = ln
	g.ctx, g.cancel = context.WithCancel(ctx)
	log.Printf("SSH gateway listening on %s", ln.Addr())

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		for {
			nc, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) || g.ctx.Err() != nil {
					return
				}
				log.Printf("SSH gateway accept error: %v", err)
				continue
			}
			if g.conns.Load() >= g.maxConns || !g.limiter.Allow(remoteIP(nc)) {
				nc.Close()
				continue
			}
			g.conns.Add(1)
			g.wg.Add(1)
			go func() {
				defer g.wg.Done()
				defer g.conns.Add(-1)
				g.handleConn(nc)
			}()
		}
	}()
	return nil
}

// Stop closes the listener and waits briefly for in-flight connections.
func (g *Gateway) Stop() {
	if g.ln != nil {
		g.ln.Close()
	}
	if g.cancel != nil {
		g.cancel()
	}
	done := make(chan struct{})
	go func() { g.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Printf("SSH gateway: shutdown timed out waiting for connections")
	}
}

// Addr returns the bound listener address (useful with ":0" in tests).
func (g *Gateway) Addr() net.Addr {
	if g.ln == nil {
		return nil
	}
	return g.ln.Addr()
}

func (g *Gateway) serverConfig() *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{
		MaxAuthTries:      3,
		ServerVersion:     "SSH-2.0-Claworc",
		PublicKeyCallback: g.authenticate,
		BannerCallback: func(cm ssh.ConnMetadata) string {
			if _, instance := ParseSSHUser(cm.User()); instance == "" {
				return "claworc: connect as <username>+<instance-name>@<host>\r\n"
			}
			return ""
		},
		AuthLogCallback: func(cm ssh.ConnMetadata, method string, err error) {
			if err != nil && method == "publickey" {
				ip := hostOnly(cm.RemoteAddr())
				g.limiter.RecordFailure(ip)
				g.audit(sshaudit.EventGatewayLoginFailed, 0, cm.User(),
					fmt.Sprintf("remote=%s method=%s", ip, method))
			}
		},
	}
	cfg.AddHostKey(g.cfg.HostKey)
	return cfg
}

func (g *Gateway) audit(event sshaudit.EventType, instanceID uint, user, details string) {
	if g.cfg.Auditor != nil {
		g.cfg.Auditor.Log(event, instanceID, user, details)
	}
}

func remoteIP(nc net.Conn) string {
	return hostOnly(nc.RemoteAddr())
}

func hostOnly(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}
