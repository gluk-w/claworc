package shimexec

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// startExecServer runs an in-process SSH server that handles exec requests
// the way sshd does — `/bin/sh -c <command>` with the channel as stdio — and
// returns a client connected to it.
func startExecServer(t *testing.T) *gossh.Client {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &gossh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go serveExecConn(nc, cfg)
		}
	}()

	client, err := gossh.Dial("tcp", ln.Addr().String(), &gossh.ClientConfig{
		User:            "test",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func serveExecConn(nc net.Conn, cfg *gossh.ServerConfig) {
	_, chans, reqs, err := gossh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	go gossh.DiscardRequests(reqs)
	for nch := range chans {
		if nch.ChannelType() != "session" {
			_ = nch.Reject(gossh.UnknownChannelType, "session only")
			continue
		}
		ch, chReqs, err := nch.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range chReqs {
				if req.Type != "exec" || len(req.Payload) < 4 {
					_ = req.Reply(false, nil)
					continue
				}
				n := binary.BigEndian.Uint32(req.Payload)
				command := string(req.Payload[4 : 4+n])
				_ = req.Reply(true, nil)

				cmd := exec.Command("/bin/sh", "-c", command)
				cmd.Stdin = ch
				cmd.Stdout = ch
				cmd.Stderr = ch.Stderr()
				code := 0
				if err := cmd.Run(); err != nil {
					code = 255
					if ee, ok := err.(*exec.ExitError); ok {
						code = ee.ExitCode()
					}
				}
				status := make([]byte, 4)
				binary.BigEndian.PutUint32(status, uint32(code))
				_, _ = ch.SendRequest("exit-status", false, status)
				return
			}
		}()
	}
}

func newTestSSHRunner(t *testing.T) *SSHRunner {
	client := startExecServer(t)
	return NewSSHRunner(func(context.Context) (*gossh.Client, error) { return client, nil })
}

// TestSSHRunner_ArgsNeverReachTheShell checks that the SSH exec command is
// always the constant execScript and that hostile arguments still arrive at
// the program verbatim.
func TestSSHRunner_ArgsNeverReachTheShell(t *testing.T) {
	r := newTestSSHRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// "cat" is an allowed program; it echoes the verb's stdin, which proves
	// the header was consumed and the payload passed through.
	var out, errOut bytes.Buffer
	code, err := r.Run(ctx, []string{"cat", "-"}, strings.NewReader("payload; $(id)\n"), &out, &errOut)
	if err != nil || code != 0 {
		t.Fatalf("Run: code=%d err=%v stderr=%q", code, err, errOut.String())
	}
	if out.String() != "payload; $(id)\n" {
		t.Errorf("stdout = %q", out.String())
	}

	// A file argument with shell metacharacters is passed as one literal
	// path, not evaluated.
	out.Reset()
	errOut.Reset()
	code, err = r.Run(ctx, []string{"cat", "/nonexistent/$(touch /tmp/claworc-pwned); id"}, nil, &out, &errOut)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code == 0 || !strings.Contains(errOut.String(), "/nonexistent/$(touch /tmp/claworc-pwned); id") {
		t.Errorf("hostile path not passed literally: code=%d stderr=%q", code, errOut.String())
	}

	if _, err := r.Run(ctx, []string{"sh", "-c", "id"}, nil, &out, &errOut); err == nil {
		t.Error("non-shim program accepted")
	}
}

// TestSSHRunner_StartWritesHeaderBeforeCallerInput covers the streaming path
// with a caller-owned stdin pipe: the header must precede later writes.
func TestSSHRunner_StartWritesHeaderBeforeCallerInput(t *testing.T) {
	r := newTestSSHRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h, err := r.Start(ctx, []string{"cat"}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := io.WriteString(h.Stdin(), "line one\n"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(h.Stdout()).ReadString('\n')
	if err != nil || line != "line one\n" {
		t.Fatalf("stream echo = %q, %v", line, err)
	}
	h.Stdin().Close()
	if code, err := h.Wait(); err != nil || code != 0 {
		t.Errorf("Wait = %d, %v (stderr %q)", code, err, h.StderrTail())
	}
}
