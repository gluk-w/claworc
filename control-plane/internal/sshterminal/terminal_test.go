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

// --- Shell Validation Tests ---

func TestValidateShell_AllowedShells(t *testing.T) {
	allowed := []string{
		"",          // empty defaults to /bin/bash
		"/bin/bash",
		"/bin/sh",
		"/bin/zsh",
		"su - abc",  // su with user switch
		"su",        // bare su
		"su abc",    // su with user
		"su - root",
	}
	for _, shell := range allowed {
		if err := ValidateShell(shell); err != nil {
			t.Errorf("ValidateShell(%q) = %v, want nil", shell, err)
		}
	}
}

func TestValidateShell_RejectedShells(t *testing.T) {
	rejected := []string{
		"/usr/bin/python3",
		"/tmp/evil",
		"bash",
		"rm -rf /",
		"/bin/bash; rm -rf /",
		"su; rm -rf /",
		"su - abc; cat /etc/passwd",
		"su - abc && evil",
		"su - abc | tee /tmp/log",
		"su - abc$(whoami)",
		"su - abc`whoami`",
		"su - abc > /tmp/out",
		"su - abc < /dev/null",
		"/bin/bash\nrm -rf /",
		"sudo bash",
		"su\\n-c evil",
	}
	for _, shell := range rejected {
		if err := ValidateShell(shell); err == nil {
			t.Errorf("ValidateShell(%q) = nil, want error", shell)
		}
	}
}

func TestCreateInteractiveSession_RejectsInvalidShell(t *testing.T) {
	client := newTestClient(t)

	_, err := CreateInteractiveSession(client, "/usr/bin/python3")
	if err == nil {
		t.Fatal("CreateInteractiveSession should reject invalid shell")
	}
	if !strings.Contains(err.Error(), "validate shell") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCreateInteractiveSession_RejectsInjectionAttempt(t *testing.T) {
	client := newTestClient(t)

	_, err := CreateInteractiveSession(client, "/bin/bash; rm -rf /")
	if err == nil {
		t.Fatal("CreateInteractiveSession should reject shell with semicolon injection")
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

func TestCreateInteractiveSession_ANSIEscapeCodes(t *testing.T) {
	client := newTestClient(t)

	ts, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession() error: %v", err)
	}
	defer ts.Close()

	readUntil(t, ts.Stdout, "PTY:true", 2*time.Second)

	// Send ANSI color codes as a single payload so the echo server returns them as one chunk
	ansiInput := "\x1b[31mRed\x1b[0m\x1b[1;32mBoldGreen\x1b[0m"
	if _, err := ts.Stdin.Write([]byte(ansiInput)); err != nil {
		t.Fatalf("write ANSI sequence: %v", err)
	}

	// Verify escape codes pass through the SSH channel intact
	output := readUntil(t, ts.Stdout, "BoldGreen", 3*time.Second)
	if !strings.Contains(output, "\x1b[31m") {
		t.Error("ANSI red color sequence was corrupted")
	}
	if !strings.Contains(output, "\x1b[0m") {
		t.Error("ANSI reset sequence was corrupted")
	}
	if !strings.Contains(output, "\x1b[1;32m") {
		t.Error("ANSI bold green sequence was corrupted")
	}
}

func TestCreateInteractiveSession_SpecialKeys(t *testing.T) {
	client := newTestClient(t)

	ts, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession() error: %v", err)
	}
	defer ts.Close()

	readUntil(t, ts.Stdout, "PTY:true", 2*time.Second)

	// Send all special keys as a single payload so they're echoed together
	// Ctrl+C (0x03), Tab (0x09), ArrowUp (ESC[A), ArrowLeft (ESC[D)
	payload := []byte{0x03, 0x09}
	payload = append(payload, 0x1b, '[', 'A') // ArrowUp
	payload = append(payload, 0x1b, '[', 'D') // ArrowLeft
	payload = append(payload, "KEYS_END"...)

	if _, err := ts.Stdin.Write(payload); err != nil {
		t.Fatalf("write special keys: %v", err)
	}

	// Verify the payload passes through intact (including control bytes and escape sequences)
	output := readUntil(t, ts.Stdout, "KEYS_END", 3*time.Second)
	if !strings.Contains(output, "\x03") {
		t.Error("Ctrl+C byte was lost")
	}
	if !strings.Contains(output, "\x09") {
		t.Error("Tab byte was lost")
	}
	if !strings.Contains(output, "\x1b[A") {
		t.Error("ArrowUp escape sequence was corrupted")
	}
	if !strings.Contains(output, "\x1b[D") {
		t.Error("ArrowLeft escape sequence was corrupted")
	}
}

func TestCreateInteractiveSession_RapidInput(t *testing.T) {
	client := newTestClient(t)

	ts, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession() error: %v", err)
	}
	defer ts.Close()

	readUntil(t, ts.Stdout, "PTY:true", 2*time.Second)

	// Send 100 rapid messages to check for buffer overflow
	const messageCount = 100

	for i := 0; i < messageCount; i++ {
		msg := fmt.Sprintf("rapid_%d|", i)
		if _, err := ts.Stdin.Write([]byte(msg)); err != nil {
			t.Fatalf("write rapid message %d: %v", i, err)
		}
	}
	// Send a unique end marker to confirm all data was delivered
	marker := "RAPID_END_MARKER"
	if _, err := ts.Stdin.Write([]byte(marker)); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	// Verify the end marker arrives, which means all prior messages were processed
	readUntil(t, ts.Stdout, marker, 5*time.Second)
}

func TestCreateInteractiveSession_LargePayload(t *testing.T) {
	client := newTestClient(t)

	ts, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession() error: %v", err)
	}
	defer ts.Close()

	readUntil(t, ts.Stdout, "PTY:true", 2*time.Second)

	// Send a large payload to test streaming and buffering
	large := strings.Repeat("X", 8192) + "LARGE_END"
	if _, err := ts.Stdin.Write([]byte(large)); err != nil {
		t.Fatalf("write large payload: %v", err)
	}

	readUntil(t, ts.Stdout, "LARGE_END", 5*time.Second)
}

func TestCreateInteractiveSession_MultipleSessions(t *testing.T) {
	// Verify that multiple independent sessions can be created on one SSH client
	client := newTestClient(t)

	ts1, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession(1) error: %v", err)
	}
	defer ts1.Close()

	ts2, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession(2) error: %v", err)
	}
	defer ts2.Close()

	// Both sessions should get PTY:true
	readUntil(t, ts1.Stdout, "PTY:true", 2*time.Second)
	readUntil(t, ts2.Stdout, "PTY:true", 2*time.Second)

	// Send different data to each and verify they're independent
	ts1.Stdin.Write([]byte("session1"))
	ts2.Stdin.Write([]byte("session2"))

	out1 := readUntil(t, ts1.Stdout, "echo:session1", 2*time.Second)
	out2 := readUntil(t, ts2.Stdout, "echo:session2", 2*time.Second)

	if strings.Contains(out1, "session2") {
		t.Error("session1 received session2 data")
	}
	if strings.Contains(out2, "session1") {
		t.Error("session2 received session1 data")
	}
}
