package sshgateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

// fakeAgentSSHD is an in-process stand-in for an agent container's sshd.
// It answers exec requests with "ran:<cmd>" and a parseable exit status,
// and counts accepted TCP connections so tests can assert connection reuse.
type fakeAgentSSHD struct {
	addr      string
	connCount atomic.Int64
	cleanup   func()
}

func startFakeAgentSSHD(t *testing.T, authorizedKey ssh.PublicKey) *fakeAgentSSHD {
	t.Helper()

	_, hostKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.ParsePrivateKey(hostKeyPEM)
	if err != nil {
		t.Fatalf("parse host key: %v", err)
	}

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), authorizedKey.Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	s := &fakeAgentSSHD{addr: ln.Addr().String()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			s.connCount.Add(1)
			go s.handleConn(nc, cfg)
		}
	}()
	s.cleanup = func() {
		ln.Close()
		<-done
	}
	return s
}

func (s *fakeAgentSSHD) handleConn(nc net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		nc.Close()
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(ch, requests)
	}
}

func (s *fakeAgentSSHD) handleSession(ch ssh.Channel, requests <-chan *ssh.Request) {
	defer ch.Close()
	for req := range requests {
		switch req.Type {
		case "pty-req", "env", "window-change":
			if req.WantReply {
				req.Reply(true, nil)
			}
		case "shell":
			if req.WantReply {
				req.Reply(true, nil)
			}
			// Echo one line of stdin back, then exit 0.
			buf := make([]byte, 4096)
			n, _ := ch.Read(buf)
			if n > 0 {
				ch.Write([]byte("echo:"))
				ch.Write(buf[:n])
			}
			sendExit(ch, 0)
			return
		case "exec":
			if req.WantReply {
				req.Reply(true, nil)
			}
			cmd := parseSSHString(req.Payload)
			fmt.Fprintf(ch, "ran:%s\n", cmd)
			code := uint32(0)
			if strings.HasPrefix(cmd, "exit ") {
				fmt.Sscanf(cmd, "exit %d", &code)
			}
			sendExit(ch, code)
			return
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

func sendExit(ch ssh.Channel, code uint32) {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, code)
	ch.SendRequest("exit-status", false, b)
	ch.CloseWrite()
}

// testEnv wires DB, fake sshd, a shared outbound client, and a running gateway.
type testEnv struct {
	gw       *Gateway
	sshd     *fakeAgentSSHD
	signer   ssh.Signer // user's key, registered for "stan"
	provided atomic.Int64
}

func setupGateway(t *testing.T) *testEnv {
	t.Helper()
	setupTestDB(t)

	user := seedUser(t, "stan", "admin")
	seedInstance(t, "bot-my-agent", 0)
	userSigner := generateUserKey(t, user.ID)

	// The control plane's outbound client identity.
	_, cpKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate control-plane key: %v", err)
	}
	cpSigner, _ := ssh.ParsePrivateKey(cpKeyPEM)

	sshd := startFakeAgentSSHD(t, cpSigner.PublicKey())
	t.Cleanup(sshd.cleanup)

	env := &testEnv{sshd: sshd, signer: userSigner}

	// Shared outbound client, dialed lazily exactly once — mirrors
	// SSHManager.EnsureConnected's reuse behavior.
	var once sync.Once
	var client *ssh.Client
	var dialErr error
	provider := func(ctx context.Context, inst *database.Instance) (*ssh.Client, error) {
		env.provided.Add(1)
		once.Do(func() {
			client, dialErr = ssh.Dial("tcp", sshd.addr, &ssh.ClientConfig{
				User:            "root",
				Auth:            []ssh.AuthMethod{ssh.PublicKeys(cpSigner)},
				HostKeyCallback: ssh.InsecureIgnoreHostKey(),
				Timeout:         5 * time.Second,
			})
		})
		return client, dialErr
	}
	t.Cleanup(func() {
		if client != nil {
			client.Close()
		}
	})

	_, gwKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate gateway host key: %v", err)
	}
	gwSigner, _ := ssh.ParsePrivateKey(gwKeyPEM)

	gw := New(Config{Addr: "127.0.0.1:0", HostKey: gwSigner, Clients: provider})
	if err := gw.Start(context.Background()); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	t.Cleanup(gw.Stop)
	env.gw = gw
	return env
}

func (e *testEnv) dial(t *testing.T, sshUser string, signer ssh.Signer) *ssh.Client {
	t.Helper()
	client, err := ssh.Dial("tcp", e.gw.Addr().String(), &ssh.ClientConfig{
		User:            sshUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial gateway as %q: %v", sshUser, err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestGatewayExecBridging(t *testing.T) {
	env := setupGateway(t)
	client := env.dial(t, "stan+my-agent", env.signer)

	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	out, err := sess.Output("hostname")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got := string(out); got != "ran:hostname\n" {
		t.Errorf("exec output = %q, want %q", got, "ran:hostname\n")
	}
}

func TestGatewayExitStatusForwarded(t *testing.T) {
	env := setupGateway(t)
	client := env.dial(t, "stan+my-agent", env.signer)

	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	err = sess.Run("exit 7")
	exitErr, ok := err.(*ssh.ExitError)
	if !ok {
		t.Fatalf("expected *ssh.ExitError, got %v", err)
	}
	if exitErr.ExitStatus() != 7 {
		t.Errorf("exit status = %d, want 7", exitErr.ExitStatus())
	}
}

func TestGatewayConnectionReuse(t *testing.T) {
	env := setupGateway(t)
	client := env.dial(t, "stan+my-agent", env.signer)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess, err := client.NewSession()
			if err != nil {
				t.Errorf("new session: %v", err)
				return
			}
			defer sess.Close()
			if _, err := sess.Output("hostname"); err != nil {
				t.Errorf("exec: %v", err)
			}
		}()
	}
	wg.Wait()

	// A second inbound SSH connection must also reuse the outbound one.
	client2 := env.dial(t, "stan+my-agent", env.signer)
	sess, err := client2.NewSession()
	if err != nil {
		t.Fatalf("new session on second connection: %v", err)
	}
	defer sess.Close()
	if _, err := sess.Output("hostname"); err != nil {
		t.Fatalf("exec on second connection: %v", err)
	}

	if got := env.sshd.connCount.Load(); got != 1 {
		t.Errorf("agent sshd saw %d connections, want 1 (must reuse the existing SSH connection)", got)
	}
	if env.provided.Load() < 3 {
		t.Errorf("provider called %d times, want >=3 (once per session)", env.provided.Load())
	}
}

func TestGatewayWrongKeyRejected(t *testing.T) {
	env := setupGateway(t)

	_, otherPEM, _ := sshproxy.GenerateKeyPair()
	otherSigner, _ := ssh.ParsePrivateKey(otherPEM)

	_, err := ssh.Dial("tcp", env.gw.Addr().String(), &ssh.ClientConfig{
		User:            "stan+my-agent",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(otherSigner)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected authentication failure with unregistered key")
	}
}

func TestGatewayMissingInstanceMessage(t *testing.T) {
	env := setupGateway(t)
	client := env.dial(t, "stan", env.signer)

	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	out, err := sess.Output("whatever")
	exitErr, ok := err.(*ssh.ExitError)
	if !ok {
		t.Fatalf("expected *ssh.ExitError, got %v (output %q)", err, out)
	}
	if exitErr.ExitStatus() != 1 {
		t.Errorf("exit status = %d, want 1", exitErr.ExitStatus())
	}
	text := string(out)
	if !strings.Contains(text, "no instance specified") {
		t.Errorf("output %q should mention missing instance", text)
	}
	if !strings.Contains(text, "stan+my-agent") {
		t.Errorf("output %q should list copyable login stan+my-agent", text)
	}
	if env.sshd.connCount.Load() != 0 {
		t.Errorf("denied session must not touch the agent")
	}
}

func TestGatewayUnknownInstance(t *testing.T) {
	env := setupGateway(t)
	client := env.dial(t, "stan+nope", env.signer)

	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	out, err := sess.Output("x")
	if _, ok := err.(*ssh.ExitError); !ok {
		t.Fatalf("expected *ssh.ExitError, got %v", err)
	}
	if !strings.Contains(string(out), "not found or not authorized") {
		t.Errorf("output %q should be the generic denial", string(out))
	}
	if !strings.Contains(string(out), "stan+my-agent") {
		t.Errorf("output %q should list copyable login names", string(out))
	}
}

func TestGatewayRejectsDirectTCPIP(t *testing.T) {
	env := setupGateway(t)
	client := env.dial(t, "stan+my-agent", env.signer)

	// direct-tcpip open must be rejected (v1 supports sessions only).
	if _, err := client.Dial("tcp", "127.0.0.1:80"); err == nil {
		t.Fatal("expected direct-tcpip to be rejected")
	}
}

func TestGatewayRateLimiterBansAfterFailures(t *testing.T) {
	env := setupGateway(t)

	_, otherPEM, _ := sshproxy.GenerateKeyPair()
	otherSigner, _ := ssh.ParsePrivateKey(otherPEM)

	cfg := &ssh.ClientConfig{
		User:            "stan+my-agent",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(otherSigner)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	// Each dial burns up to MaxAuthTries failures; a handful trips the ban.
	for i := 0; i < maxFailures; i++ {
		ssh.Dial("tcp", env.gw.Addr().String(), cfg)
	}

	if env.gw.limiter.Allow("127.0.0.1") {
		t.Fatal("expected 127.0.0.1 to be banned after repeated auth failures")
	}
	// Banned IPs are dropped at accept time even with the right key.
	if _, err := ssh.Dial("tcp", env.gw.Addr().String(), &ssh.ClientConfig{
		User:            "stan+my-agent",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(env.signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}); err == nil {
		t.Fatal("expected banned IP to be refused")
	}
}
