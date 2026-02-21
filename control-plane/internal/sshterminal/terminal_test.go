package sshterminal

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"golang.org/x/crypto/ssh"
)

// testSSHServer starts an in-process SSH server that supports PTY and shell sessions.
// The server echoes stdin back with an "echo:" prefix and reports PTY status on start.
func testSSHServer(t *testing.T, authorizedKey ssh.PublicKey) (addr string, cleanup func()) {
	t.Helper()

	_, hostKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := sshproxy.ParsePrivateKey(hostKeyPEM)
	if err != nil {
		t.Fatalf("parse host key: %v", err)
	}

	config := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if ssh.FingerprintSHA256(key) == ssh.FingerprintSHA256(authorizedKey) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	config.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			netConn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleTestConnection(netConn, config)
		}
	}()

	return listener.Addr().String(), func() {
		listener.Close()
		<-done
	}
}

func handleTestConnection(netConn net.Conn, config *ssh.ServerConfig) {
	sshConn, chans, reqs, err := ssh.NewServerConn(netConn, config)
	if err != nil {
		netConn.Close()
		return
	}
	defer sshConn.Close()

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
		go handleTestSession(ch, requests)
	}
}

func handleTestSession(ch ssh.Channel, requests <-chan *ssh.Request) {
	defer ch.Close()

	var hasPTY bool

	for req := range requests {
		switch req.Type {
		case "pty-req":
			hasPTY = true
			if req.WantReply {
				req.Reply(true, nil)
			}

		case "window-change":
			if len(req.Payload) >= 8 {
				cols := binary.BigEndian.Uint32(req.Payload[0:4])
				rows := binary.BigEndian.Uint32(req.Payload[4:8])
				ch.Write([]byte(fmt.Sprintf("resize:%dx%d\n", cols, rows)))
			}
			if req.WantReply {
				req.Reply(true, nil)
			}

		case "exec", "shell":
			if req.WantReply {
				req.Reply(true, nil)
			}
			if hasPTY {
				ch.Write([]byte("PTY:true\n"))
			} else {
				ch.Write([]byte("PTY:false\n"))
			}
			// Echo stdin back with prefix in a goroutine
			go func() {
				buf := make([]byte, 4096)
				for {
					n, err := ch.Read(buf)
					if n > 0 {
						ch.Write([]byte("echo:"))
						ch.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}()
			// Continue processing requests (e.g. window-change) after shell starts

		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// newTestClient creates a key pair, starts a test SSH server, connects to it,
// and returns the SSH client. Resources are cleaned up via t.Cleanup.
func newTestClient(t *testing.T) *ssh.Client {
	t.Helper()

	_, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, cleanup := testSSHServer(t, signer.PublicKey())
	t.Cleanup(cleanup)

	clientCfg := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	client, err := ssh.Dial("tcp", addr, clientCfg)
	if err != nil {
		t.Fatalf("dial SSH server: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	return client
}

// readUntil reads from r until the accumulated output contains the target string
// or the timeout expires.
func readUntil(t *testing.T, r io.Reader, target string, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	var accumulated string
	buf := make([]byte, 4096)
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for %q, got: %q", target, accumulated)
		default:
		}
		n, err := r.Read(buf)
		if n > 0 {
			accumulated += string(buf[:n])
		}
		if strings.Contains(accumulated, target) {
			return accumulated
		}
		if err != nil {
			t.Fatalf("read error waiting for %q: %v, accumulated: %q", target, err, accumulated)
		}
	}
}

func TestCreateInteractiveSession_DefaultShell(t *testing.T) {
	client := newTestClient(t)

	ts, err := CreateInteractiveSession(client, "")
	if err != nil {
		t.Fatalf("CreateInteractiveSession() error: %v", err)
	}
	defer ts.Close()

	if ts.Stdin == nil {
		t.Error("Stdin is nil")
	}
	if ts.Stdout == nil {
		t.Error("Stdout is nil")
	}
	if ts.Session == nil {
		t.Error("Session is nil")
	}
}

func TestCreateInteractiveSession_PTYAllocated(t *testing.T) {
	client := newTestClient(t)

	ts, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession() error: %v", err)
	}
	defer ts.Close()

	readUntil(t, ts.Stdout, "PTY:true", 2*time.Second)
}

func TestCreateInteractiveSession_InputOutput(t *testing.T) {
	client := newTestClient(t)

	ts, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession() error: %v", err)
	}
	defer ts.Close()

	// Consume the initial PTY:true line
	readUntil(t, ts.Stdout, "PTY:true", 2*time.Second)

	// Write input
	testInput := "hello world"
	if _, err := ts.Stdin.Write([]byte(testInput)); err != nil {
		t.Fatalf("write to stdin: %v", err)
	}

	// Read echoed output
	readUntil(t, ts.Stdout, "echo:"+testInput, 2*time.Second)
}

func TestCreateInteractiveSession_Resize(t *testing.T) {
	client := newTestClient(t)

	ts, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession() error: %v", err)
	}
	defer ts.Close()

	// Consume initial output
	readUntil(t, ts.Stdout, "PTY:true", 2*time.Second)

	if err := ts.Resize(120, 40); err != nil {
		t.Fatalf("Resize() error: %v", err)
	}

	readUntil(t, ts.Stdout, "resize:120x40", 2*time.Second)
}

func TestCreateInteractiveSession_Close(t *testing.T) {
	client := newTestClient(t)

	ts, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession() error: %v", err)
	}

	if err := ts.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// Writing to stdin after close should fail
	_, err = ts.Stdin.Write([]byte("test"))
	if err == nil {
		t.Error("expected error writing to stdin after Close()")
	}
}

func TestCreateInteractiveSession_StdoutClosesOnSessionEnd(t *testing.T) {
	client := newTestClient(t)

	ts, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession() error: %v", err)
	}

	ts.Close()

	// Drain any buffered data and verify we eventually get an error/EOF
	buf := make([]byte, 256)
	for {
		_, err = ts.Stdout.Read(buf)
		if err != nil {
			break
		}
	}
	if err != io.EOF {
		t.Logf("got expected non-EOF error: %v", err)
	}
}

func TestCreateInteractiveSession_MultipleResizes(t *testing.T) {
	client := newTestClient(t)

	ts, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession() error: %v", err)
	}
	defer ts.Close()

	// Consume initial output
	readUntil(t, ts.Stdout, "PTY:true", 2*time.Second)

	resizes := []struct{ cols, rows uint16 }{
		{80, 24},
		{120, 40},
		{200, 50},
	}

	for _, r := range resizes {
		if err := ts.Resize(r.cols, r.rows); err != nil {
			t.Fatalf("Resize(%d, %d) error: %v", r.cols, r.rows, err)
		}
	}

	// Verify all resize confirmations arrive in order
	output := readUntil(t, ts.Stdout, "resize:200x50", 3*time.Second)
	for _, r := range resizes {
		expected := fmt.Sprintf("resize:%dx%d", r.cols, r.rows)
		if !strings.Contains(output, expected) {
			t.Errorf("missing resize confirmation %q in output %q", expected, output)
		}
	}
}
