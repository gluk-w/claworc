// bridge.go splices an authenticated inbound SSH connection onto the
// control plane's existing outbound SSH connection to the target instance.
// Each inbound "session" channel becomes one new channel on the shared
// per-instance *ssh.Client — never a new TCP/SSH connection.

package sshgateway

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
)

func (g *Gateway) handleConn(nc net.Conn) {
	nc.SetDeadline(time.Now().Add(handshakeTimeout))
	sc, chans, reqs, err := ssh.NewServerConn(nc, g.serverConfig())
	if err != nil {
		nc.Close()
		return
	}
	nc.SetDeadline(time.Time{})
	defer sc.Close()
	g.serveConn(sc, chans, reqs)
}

func (g *Gateway) serveConn(sc *ssh.ServerConn, chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request) {
	// Global requests (tcpip-forward, client keepalives, ...) are refused;
	// only session channels are supported in v1.
	go ssh.DiscardRequests(reqs)

	username := sc.Permissions.Extensions[extUsername]
	instanceID := permsUint(sc.Permissions, extInstanceID)

	// connDone closes when the inbound connection is gone (client disconnect
	// or gateway shutdown). Bridged sessions use it to tear down their
	// instance-side channel so nothing leaks on the shared client.
	connDone := make(chan struct{})
	go func() { sc.Wait(); close(connDone) }()
	stopWatch := make(chan struct{})
	go func() {
		select {
		case <-g.ctx.Done():
			sc.Close()
		case <-stopWatch:
		}
	}()
	defer close(stopWatch)

	var wg sync.WaitGroup
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.Prohibited, "only sessions are supported by the claworc gateway")
			continue
		}
		wg.Add(1)
		go func(nc ssh.NewChannel) {
			defer wg.Done()
			g.handleSession(sc, nc, connDone)
		}(nc)
	}
	wg.Wait()
	g.audit(sshaudit.EventGatewayDisconnect, instanceID, username,
		fmt.Sprintf("remote=%s", hostOnly(sc.RemoteAddr())))
}

func (g *Gateway) handleSession(sc *ssh.ServerConn, nc ssh.NewChannel, connDone <-chan struct{}) {
	in, inReqs, err := nc.Accept()
	if err != nil {
		return
	}
	defer in.Close()

	if reason := sc.Permissions.Extensions[extDenyReason]; reason != "" {
		g.denySession(sc, in, inReqs, reason)
		return
	}

	username := sc.Permissions.Extensions[extUsername]
	instanceID := permsUint(sc.Permissions, extInstanceID)
	inst, err := database.GetInstance(instanceID)
	if err != nil {
		failSession(in, inReqs, "claworc: instance no longer exists")
		return
	}

	client, err := g.cfg.Clients(g.ctx, inst)
	if err != nil {
		log.Printf("SSH gateway: connect to instance %d failed: %v", instanceID, err)
		failSession(in, inReqs, "claworc: instance not reachable")
		return
	}

	// Raw channel on the shared multiplexed client. Never close the client
	// itself — it is owned by SSHManager and shared with tunnels and web
	// terminals.
	out, outReqs, err := client.OpenChannel("session", nil)
	if err != nil {
		log.Printf("SSH gateway: open channel to instance %d failed: %v", instanceID, err)
		failSession(in, inReqs, "claworc: instance not reachable")
		return
	}
	defer out.Close()

	g.bridgeChannels(in, inReqs, out, outReqs, instanceID, username, connDone)
}

// bridgeChannels pipes data and channel requests between the user's channel
// (in) and the instance-side channel (out) until both close.
func (g *Gateway) bridgeChannels(in ssh.Channel, inReqs <-chan *ssh.Request, out ssh.Channel, outReqs <-chan *ssh.Request, instanceID uint, username string, connDone <-chan struct{}) {
	// If the inbound connection dies while the remote process is still
	// running, force-close the instance-side channel; otherwise the
	// exit-status drain below would wait forever and leak the channel on
	// the shared client.
	bridgeDone := make(chan struct{})
	defer close(bridgeDone)
	go func() {
		select {
		case <-connDone:
			out.Close()
		case <-bridgeDone:
		}
	}()

	// Client-originated requests, forwarded to the instance sshd.
	// pendingReqs tracks requests received but not yet replied to: a fast
	// remote can emit output + exit-status and close before SendRequest for
	// the triggering exec/shell even returns, and closing the inbound
	// channel before its Reply is sent makes the client fail with EOF.
	var pendingReqs sync.WaitGroup
	go func() {
		audited := false
		for req := range inReqs {
			pendingReqs.Add(1)
			switch req.Type {
			case "pty-req", "shell", "exec", "subsystem", "env",
				"window-change", "signal", "eow@openssh.com", "break":
				if !audited && (req.Type == "shell" || req.Type == "exec" || req.Type == "subsystem") {
					audited = true
					g.audit(sshaudit.EventGatewaySession, instanceID, username, sessionDetails(req))
				}
				ok, _ := out.SendRequest(req.Type, req.WantReply, req.Payload)
				if req.WantReply {
					req.Reply(ok, nil)
				}
			default:
				if req.WantReply {
					req.Reply(false, nil)
				}
			}
			pendingReqs.Done()
		}
	}()

	// Instance-originated requests (exit-status, exit-signal, eow) forwarded
	// to the client. exitForwarded closing means outReqs drained, i.e. the
	// exit-status has been relayed — only then is it safe to close `in`,
	// otherwise the client would see exit code -1/255.
	exitForwarded := make(chan struct{})
	go func() {
		defer close(exitForwarded)
		for req := range outReqs {
			ok, _ := in.SendRequest(req.Type, req.WantReply, req.Payload)
			if req.WantReply {
				req.Reply(ok, nil)
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(in, out) }()                   // stdout
	go func() { defer wg.Done(); io.Copy(in.Stderr(), out.Stderr()) }() // stderr
	go func() {
		io.Copy(out, in) // stdin; propagate client-side EOF
		out.CloseWrite()
	}()

	wg.Wait()
	<-exitForwarded
	pendingReqs.Wait()
}

// denySession handles a session whose key authenticated but whose instance
// selection was rejected: print an actionable message, list the agents the
// user may connect to as ready-to-copy login names, and exit non-zero.
// Listing only the user's own accessible instances leaks nothing they
// cannot already see in the dashboard.
func (g *Gateway) denySession(sc *ssh.ServerConn, in ssh.Channel, inReqs <-chan *ssh.Request, reason string) {
	username := sc.Permissions.Extensions[extUsername]
	msg := "claworc: instance not found or not authorized\r\n"
	if reason == denyMissingInstance {
		msg = fmt.Sprintf("claworc: no instance specified. Connect as %s+<instance-name>@<host>\r\n", username)
	}

	userID := permsUint(sc.Permissions, extUserID)
	if user, err := database.GetUserByID(userID); err == nil {
		if names := accessibleInstanceNames(userID, user.Role == "admin"); len(names) > 0 {
			msg += "\r\nYour agents:\r\n"
			for _, name := range names {
				msg += fmt.Sprintf("  %s+%s\r\n", username, name)
			}
		}
	}
	failSession(in, inReqs, strings.TrimSuffix(msg, "\r\n"))
}

// failSession lets the client attach (accepting pty/shell requests), prints
// a message, and terminates with exit status 1.
func failSession(in ssh.Channel, inReqs <-chan *ssh.Request, msg string) {
	started := make(chan struct{})
	var once sync.Once
	go func() {
		// Also unblocks the wait below if the client disconnects before
		// ever requesting a shell/exec.
		defer once.Do(func() { close(started) })
		for req := range inReqs {
			if req.WantReply {
				req.Reply(true, nil)
			}
			switch req.Type {
			case "shell", "exec", "subsystem":
				once.Do(func() { close(started) })
			}
		}
	}()
	<-started
	io.WriteString(in, msg+"\r\n")
	in.SendRequest("exit-status", false, exitStatusPayload(1))
	in.CloseWrite()
}

func sessionDetails(req *ssh.Request) string {
	switch req.Type {
	case "exec", "subsystem":
		name := parseSSHString(req.Payload)
		if len(name) > 200 {
			name = name[:200] + "..."
		}
		return req.Type + ":" + name
	default:
		return req.Type
	}
}

// parseSSHString decodes a length-prefixed SSH string (RFC 4251).
func parseSSHString(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}
	n := binary.BigEndian.Uint32(payload)
	if uint32(len(payload)-4) < n {
		return ""
	}
	return string(payload[4 : 4+n])
}

func exitStatusPayload(code uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, code)
	return b
}

func permsUint(perms *ssh.Permissions, key string) uint {
	if perms == nil {
		return 0
	}
	n, _ := strconv.ParseUint(perms.Extensions[key], 10, 32)
	return uint(n)
}
