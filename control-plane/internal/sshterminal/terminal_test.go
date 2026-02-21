package sshterminal

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// --- Test SSH server infrastructure ---

// ptyHandler receives the SSH channel, the parsed command (from shell request
// or exec), and the initial PTY dimensions. It should write output and read
// input as needed for the test scenario.
type ptyHandler struct {
	// onPTY is called when a pty-req is received, before the shell starts.
	// Returns true to accept the PTY request.
	onPTY func(term string, cols, rows uint32, modes gossh.TerminalModes) bool

	// onShell is called when a shell request is received.
	onShell func(ch gossh.Channel)

	// onExec is called when an exec request is received.
	onExec func(cmd string, ch gossh.Channel)

	// onWindowChange is called when a window-change request is received.
	onWindowChange func(cols, rows uint32)
}

// startPTYServer starts a test SSH server that supports PTY requests, shell
// sessions, and window-change events for terminal testing.
func startPTYServer(t *testing.T, handler ptyHandler) (client *gossh.Client, cleanup func()) {
	t.Helper()

	// Generate server host key
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := gossh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("create host signer: %v", err)
	}

	// Generate client key pair
	clientPub, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	clientSSHPub, err := gossh.NewPublicKey(clientPub)
	if err != nil {
		t.Fatalf("convert client pub key: %v", err)
	}

	serverCfg := &gossh.ServerConfig{
		PublicKeyCallback: func(conn gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
			if bytes.Equal(key.Marshal(), clientSSHPub.Marshal()) {
				return &gossh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	serverCfg.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handlePTYConn(conn, serverCfg, handler)
		}
	}()

	// Build client config
	clientSigner, err := gossh.NewSignerFromKey(clientPriv)
	if err != nil {
		t.Fatalf("create client signer: %v", err)
	}

	clientCfg := &gossh.ClientConfig{
		User:            "root",
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(clientSigner)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}

	sshClient, err := gossh.Dial("tcp", listener.Addr().String(), clientCfg)
	if err != nil {
		listener.Close()
		t.Fatalf("dial SSH: %v", err)
	}

	return sshClient, func() {
		sshClient.Close()
		listener.Close()
	}
}

func handlePTYConn(netConn net.Conn, config *gossh.ServerConfig, handler ptyHandler) {
	defer netConn.Close()
	srvConn, chans, reqs, err := gossh.NewServerConn(netConn, config)
	if err != nil {
		return
	}
	defer srvConn.Close()
	go gossh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(gossh.UnknownChannelType, "unsupported channel type")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go handlePTYSession(ch, requests, handler)
	}
}

func handlePTYSession(ch gossh.Channel, reqs <-chan *gossh.Request, handler ptyHandler) {
	defer ch.Close()

	for req := range reqs {
		switch req.Type {
		case "pty-req":
			// Parse PTY request payload
			term, cols, rows, modes := parsePTYReq(req.Payload)
			accept := true
			if handler.onPTY != nil {
				accept = handler.onPTY(term, cols, rows, modes)
			}
			if req.WantReply {
				req.Reply(accept, nil)
			}

		case "shell":
			if req.WantReply {
				req.Reply(true, nil)
			}
			// Handle window-change requests in the background so the
			// shell handler can run synchronously and ch.Close() fires
			// when the shell handler returns.
			go handleWindowChangeRequests(reqs, handler)
			if handler.onShell != nil {
				handler.onShell(ch)
			}
			return

		case "exec":
			if len(req.Payload) < 4 {
				req.Reply(false, nil)
				continue
			}
			cmdLen := int(req.Payload[0])<<24 | int(req.Payload[1])<<16 | int(req.Payload[2])<<8 | int(req.Payload[3])
			if len(req.Payload) < 4+cmdLen {
				req.Reply(false, nil)
				continue
			}
			cmd := string(req.Payload[4 : 4+cmdLen])
			if req.WantReply {
				req.Reply(true, nil)
			}
			// Handle window-change requests in the background so the
			// exec handler can run synchronously and ch.Close() fires
			// when the exec handler returns.
			go handleWindowChangeRequests(reqs, handler)
			if handler.onExec != nil {
				handler.onExec(cmd, ch)
			}
			return

		case "window-change":
			if len(req.Payload) >= 8 {
				cols := binary.BigEndian.Uint32(req.Payload[0:4])
				rows := binary.BigEndian.Uint32(req.Payload[4:8])
				if handler.onWindowChange != nil {
					handler.onWindowChange(cols, rows)
				}
			}
			if req.WantReply {
				req.Reply(true, nil)
			}

		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// handleWindowChangeRequests processes window-change requests in the background
// after a shell or exec has started.
func handleWindowChangeRequests(reqs <-chan *gossh.Request, handler ptyHandler) {
	for req := range reqs {
		switch req.Type {
		case "window-change":
			if len(req.Payload) >= 8 {
				cols := binary.BigEndian.Uint32(req.Payload[0:4])
				rows := binary.BigEndian.Uint32(req.Payload[4:8])
				if handler.onWindowChange != nil {
					handler.onWindowChange(cols, rows)
				}
			}
			if req.WantReply {
				req.Reply(true, nil)
			}
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// parsePTYReq parses the pty-req payload format.
// Format: string(term) + uint32(cols) + uint32(rows) + uint32(pxWidth) + uint32(pxHeight) + string(modes)
func parsePTYReq(payload []byte) (term string, cols, rows uint32, modes gossh.TerminalModes) {
	modes = make(gossh.TerminalModes)
	if len(payload) < 4 {
		return
	}
	termLen := binary.BigEndian.Uint32(payload[0:4])
	payload = payload[4:]

	if uint32(len(payload)) < termLen {
		return
	}
	term = string(payload[:termLen])
	payload = payload[termLen:]

	if len(payload) < 16 { // cols + rows + pxWidth + pxHeight
		return
	}
	cols = binary.BigEndian.Uint32(payload[0:4])
	rows = binary.BigEndian.Uint32(payload[4:8])
	// skip pxWidth (payload[8:12]) and pxHeight (payload[12:16])
	payload = payload[16:]

	// Parse encoded terminal modes
	if len(payload) < 4 {
		return
	}
	modesLen := binary.BigEndian.Uint32(payload[0:4])
	payload = payload[4:]

	if uint32(len(payload)) < modesLen {
		return
	}
	modesData := payload[:modesLen]

	// Terminal modes are encoded as: opcode(1 byte) + value(4 bytes), ending with TTY_OP_END (0)
	for len(modesData) >= 5 {
		opcode := modesData[0]
		if opcode == 0 { // TTY_OP_END
			break
		}
		value := binary.BigEndian.Uint32(modesData[1:5])
		modes[opcode] = value
		modesData = modesData[5:]
	}

	return
}

// sendExitStatus sends an exit-status request on the SSH channel.
func sendExitStatus(ch gossh.Channel, exitCode int) {
	payload := gossh.Marshal(struct{ Status uint32 }{uint32(exitCode)})
	ch.SendRequest("exit-status", false, payload)
}

// --- CreateInteractiveSession tests ---

func TestCreateInteractiveSessionDefaultShell(t *testing.T) {
	var receivedTerm string
	var receivedCols, receivedRows uint32
	var receivedModes gossh.TerminalModes
	shellStarted := make(chan struct{})

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			receivedTerm = term
			receivedCols = cols
			receivedRows = rows
			receivedModes = modes
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			close(shellStarted)
			ch.Write([]byte("shell output\r\n"))
			// Wait for channel to close
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
			sendExitStatus(ch, 0)
		},
	})
	defer cleanup()

	session, err := CreateInteractiveSession(client, "")
	if err != nil {
		t.Fatalf("CreateInteractiveSession: %v", err)
	}
	defer session.Close()

	// Wait for shell to start
	select {
	case <-shellStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for shell to start")
	}

	// Verify PTY request parameters
	if receivedTerm != "xterm-256color" {
		t.Errorf("expected term 'xterm-256color', got %q", receivedTerm)
	}
	if receivedCols != defaultTermCols {
		t.Errorf("expected %d cols, got %d", defaultTermCols, receivedCols)
	}
	if receivedRows != defaultTermRows {
		t.Errorf("expected %d rows, got %d", defaultTermRows, receivedRows)
	}

	// Verify terminal modes
	if v, ok := receivedModes[gossh.ECHO]; !ok || v != 1 {
		t.Errorf("expected ECHO=1, got %d (exists=%v)", v, ok)
	}
	if v, ok := receivedModes[gossh.TTY_OP_ISPEED]; !ok || v != 14400 {
		t.Errorf("expected TTY_OP_ISPEED=14400, got %d (exists=%v)", v, ok)
	}
	if v, ok := receivedModes[gossh.TTY_OP_OSPEED]; !ok || v != 14400 {
		t.Errorf("expected TTY_OP_OSPEED=14400, got %d (exists=%v)", v, ok)
	}
}

func TestCreateInteractiveSessionCustomShell(t *testing.T) {
	var receivedCmd string
	shellStarted := make(chan struct{})

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			receivedCmd = cmd
			close(shellStarted)
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
			sendExitStatus(ch, 0)
		},
	})
	defer cleanup()

	session, err := CreateInteractiveSession(client, "/bin/zsh")
	if err != nil {
		t.Fatalf("CreateInteractiveSession: %v", err)
	}
	defer session.Close()

	select {
	case <-shellStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for shell to start")
	}

	if receivedCmd != "/bin/zsh" {
		t.Errorf("expected shell '/bin/zsh', got %q", receivedCmd)
	}
}

func TestCreateInteractiveSessionStdinStdout(t *testing.T) {
	shellReady := make(chan struct{})

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			close(shellReady)

			// Echo back whatever is written to stdin
			buf := make([]byte, 4096)
			for {
				n, err := ch.Read(buf)
				if n > 0 {
					ch.Write(buf[:n])
				}
				if err != nil {
					break
				}
			}
			sendExitStatus(ch, 0)
		},
	})
	defer cleanup()

	session, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession: %v", err)
	}
	defer session.Close()

	select {
	case <-shellReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for shell")
	}

	// Write to stdin
	testInput := "hello world\n"
	_, err = session.Stdin.Write([]byte(testInput))
	if err != nil {
		t.Fatalf("write to stdin: %v", err)
	}

	// Read from stdout
	buf := make([]byte, 4096)
	n, err := session.Stdout.Read(buf)
	if err != nil {
		t.Fatalf("read from stdout: %v", err)
	}

	output := string(buf[:n])
	if output != testInput {
		t.Errorf("expected echoed output %q, got %q", testInput, output)
	}
}

// --- Resize tests ---

func TestResize(t *testing.T) {
	var mu sync.Mutex
	var resizeCols, resizeRows uint32
	resized := make(chan struct{}, 10)
	shellReady := make(chan struct{})

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			close(shellReady)
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
			sendExitStatus(ch, 0)
		},
		onWindowChange: func(cols, rows uint32) {
			mu.Lock()
			resizeCols = cols
			resizeRows = rows
			mu.Unlock()
			resized <- struct{}{}
		},
	})
	defer cleanup()

	session, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession: %v", err)
	}
	defer session.Close()

	select {
	case <-shellReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for shell")
	}

	// Resize the terminal
	if err := session.Resize(120, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	// Wait for the resize event
	select {
	case <-resized:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for resize event")
	}

	mu.Lock()
	gotCols := resizeCols
	gotRows := resizeRows
	mu.Unlock()

	if gotCols != 120 {
		t.Errorf("expected 120 cols, got %d", gotCols)
	}
	if gotRows != 40 {
		t.Errorf("expected 40 rows, got %d", gotRows)
	}
}

func TestResizeMultipleTimes(t *testing.T) {
	var mu sync.Mutex
	resizeHistory := make([]struct{ cols, rows uint32 }, 0)
	resized := make(chan struct{}, 10)
	shellReady := make(chan struct{})

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			close(shellReady)
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
			sendExitStatus(ch, 0)
		},
		onWindowChange: func(cols, rows uint32) {
			mu.Lock()
			resizeHistory = append(resizeHistory, struct{ cols, rows uint32 }{cols, rows})
			mu.Unlock()
			resized <- struct{}{}
		},
	})
	defer cleanup()

	session, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession: %v", err)
	}
	defer session.Close()

	select {
	case <-shellReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for shell")
	}

	// Resize multiple times
	sizes := []struct{ cols, rows uint16 }{
		{100, 30},
		{200, 50},
		{80, 24},
	}

	for _, s := range sizes {
		if err := session.Resize(s.cols, s.rows); err != nil {
			t.Fatalf("Resize(%d, %d): %v", s.cols, s.rows, err)
		}
		select {
		case <-resized:
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for resize to %dx%d", s.cols, s.rows)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if len(resizeHistory) != 3 {
		t.Fatalf("expected 3 resize events, got %d", len(resizeHistory))
	}

	for i, s := range sizes {
		if resizeHistory[i].cols != uint32(s.cols) || resizeHistory[i].rows != uint32(s.rows) {
			t.Errorf("resize %d: expected %dx%d, got %dx%d",
				i, s.cols, s.rows, resizeHistory[i].cols, resizeHistory[i].rows)
		}
	}
}

// --- Close tests ---

func TestClose(t *testing.T) {
	shellReady := make(chan struct{})

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			close(shellReady)
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
			sendExitStatus(ch, 0)
		},
	})
	defer cleanup()

	session, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession: %v", err)
	}

	select {
	case <-shellReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for shell")
	}

	// Close should succeed
	if err := session.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	// Double close should be safe (no error)
	if err := session.Close(); err != nil {
		t.Errorf("double Close: %v", err)
	}
}

func TestResizeAfterClose(t *testing.T) {
	shellReady := make(chan struct{})

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			close(shellReady)
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
			sendExitStatus(ch, 0)
		},
	})
	defer cleanup()

	session, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession: %v", err)
	}

	select {
	case <-shellReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for shell")
	}

	session.Close()

	// Resize after close should return error
	err = session.Resize(120, 40)
	if err == nil {
		t.Error("expected error on Resize after Close, got nil")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("expected 'closed' in error, got %q", err.Error())
	}
}

// --- Output streaming tests ---

func TestOutputStreaming(t *testing.T) {
	shellReady := make(chan struct{})

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			close(shellReady)
			// Simulate shell output with delays
			ch.Write([]byte("line 1\r\n"))
			time.Sleep(20 * time.Millisecond)
			ch.Write([]byte("line 2\r\n"))
			time.Sleep(20 * time.Millisecond)
			ch.Write([]byte("line 3\r\n"))
			sendExitStatus(ch, 0)
		},
	})
	defer cleanup()

	session, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession: %v", err)
	}
	defer session.Close()

	select {
	case <-shellReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for shell")
	}

	// Read all output
	var output bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(&output, session.Stdout)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for output to complete")
	}

	result := output.String()
	if !strings.Contains(result, "line 1") {
		t.Errorf("output missing 'line 1': %q", result)
	}
	if !strings.Contains(result, "line 2") {
		t.Errorf("output missing 'line 2': %q", result)
	}
	if !strings.Contains(result, "line 3") {
		t.Errorf("output missing 'line 3': %q", result)
	}
}

// --- ANSI escape code tests ---

func TestANSIEscapeCodesPreserved(t *testing.T) {
	shellReady := make(chan struct{})

	// ANSI color codes
	redText := "\033[31mred\033[0m"
	boldText := "\033[1mbold\033[0m"
	cursorMove := "\033[10;5H"

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			close(shellReady)
			ch.Write([]byte(redText))
			ch.Write([]byte(boldText))
			ch.Write([]byte(cursorMove))
			sendExitStatus(ch, 0)
		},
	})
	defer cleanup()

	session, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession: %v", err)
	}
	defer session.Close()

	select {
	case <-shellReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for shell")
	}

	var output bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(&output, session.Stdout)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for output")
	}

	result := output.String()
	if !strings.Contains(result, "\033[31m") {
		t.Errorf("ANSI red code not preserved in output: %q", result)
	}
	if !strings.Contains(result, "\033[1m") {
		t.Errorf("ANSI bold code not preserved in output: %q", result)
	}
	if !strings.Contains(result, "\033[10;5H") {
		t.Errorf("ANSI cursor move not preserved in output: %q", result)
	}
}

// --- Concurrent session tests ---

func TestConcurrentSessions(t *testing.T) {
	var mu sync.Mutex
	sessionCount := 0

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			mu.Lock()
			sessionCount++
			id := sessionCount
			mu.Unlock()

			ch.Write([]byte(fmt.Sprintf("session %d\r\n", id)))
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
			sendExitStatus(ch, 0)
		},
	})
	defer cleanup()

	const numSessions = 3
	sessions := make([]*TerminalSession, numSessions)
	var wg sync.WaitGroup

	// Create multiple concurrent sessions
	for i := 0; i < numSessions; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s, err := CreateInteractiveSession(client, "/bin/bash")
			if err != nil {
				t.Errorf("session %d: CreateInteractiveSession: %v", idx, err)
				return
			}
			sessions[idx] = s
		}(i)
	}
	wg.Wait()

	// Read output from each session
	for i, s := range sessions {
		if s == nil {
			continue
		}
		buf := make([]byte, 4096)
		n, err := s.Stdout.Read(buf)
		if err != nil {
			t.Errorf("session %d: read: %v", i, err)
			continue
		}
		output := string(buf[:n])
		if !strings.Contains(output, "session") {
			t.Errorf("session %d: unexpected output %q", i, output)
		}
	}

	// Clean up
	for _, s := range sessions {
		if s != nil {
			s.Close()
		}
	}

	mu.Lock()
	if sessionCount != numSessions {
		t.Errorf("expected %d sessions, got %d", numSessions, sessionCount)
	}
	mu.Unlock()
}

// --- Special key / control character tests ---

func TestSpecialKeysControlCharacters(t *testing.T) {
	shellReady := make(chan struct{})

	// Collect all bytes received by the shell
	var mu sync.Mutex
	var received []byte

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			close(shellReady)
			buf := make([]byte, 4096)
			for {
				n, err := ch.Read(buf)
				if n > 0 {
					mu.Lock()
					received = append(received, buf[:n]...)
					mu.Unlock()
					// Echo back
					ch.Write(buf[:n])
				}
				if err != nil {
					break
				}
			}
			sendExitStatus(ch, 0)
		},
	})
	defer cleanup()

	session, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession: %v", err)
	}
	defer session.Close()

	select {
	case <-shellReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for shell")
	}

	// Send control characters: Ctrl+C (0x03), Ctrl+D (0x04), Ctrl+Z (0x1a)
	controlChars := []byte{0x03, 0x04, 0x1a}
	for _, c := range controlChars {
		_, err := session.Stdin.Write([]byte{c})
		if err != nil {
			t.Fatalf("write control char 0x%02x: %v", c, err)
		}
	}

	// Give time for transmission
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	for _, c := range controlChars {
		found := false
		for _, r := range received {
			if r == c {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("control character 0x%02x not received by shell", c)
		}
	}
}

func TestArrowKeysAndTabEscapeSequences(t *testing.T) {
	shellReady := make(chan struct{})

	var mu sync.Mutex
	var received []byte

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			close(shellReady)
			buf := make([]byte, 4096)
			for {
				n, err := ch.Read(buf)
				if n > 0 {
					mu.Lock()
					received = append(received, buf[:n]...)
					mu.Unlock()
					ch.Write(buf[:n])
				}
				if err != nil {
					break
				}
			}
			sendExitStatus(ch, 0)
		},
	})
	defer cleanup()

	session, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession: %v", err)
	}
	defer session.Close()

	select {
	case <-shellReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for shell")
	}

	// Arrow key escape sequences (xterm style)
	arrowUp := "\x1b[A"
	arrowDown := "\x1b[B"
	arrowRight := "\x1b[C"
	arrowLeft := "\x1b[D"
	tab := "\x09"

	sequences := []string{arrowUp, arrowDown, arrowRight, arrowLeft, tab}
	for _, seq := range sequences {
		_, err := session.Stdin.Write([]byte(seq))
		if err != nil {
			t.Fatalf("write sequence %q: %v", seq, err)
		}
	}

	// Give time for transmission
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	receivedStr := string(received)
	for _, seq := range sequences {
		if !strings.Contains(receivedStr, seq) {
			t.Errorf("escape sequence %q not found in received data %q", seq, receivedStr)
		}
	}
}

func TestInteractiveREPLSession(t *testing.T) {
	shellReady := make(chan struct{})

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			close(shellReady)

			// Simulate a REPL: send prompt, wait for input, process it, repeat
			ch.Write([]byte(">>> "))

			buf := make([]byte, 4096)
			var line []byte
			for {
				n, err := ch.Read(buf)
				if err != nil {
					break
				}
				for i := 0; i < n; i++ {
					if buf[i] == '\n' || buf[i] == '\r' {
						input := strings.TrimSpace(string(line))
						line = nil
						if input == "exit" {
							ch.Write([]byte("\r\nbye\r\n"))
							sendExitStatus(ch, 0)
							return
						}
						// Echo the input as REPL output
						ch.Write([]byte(fmt.Sprintf("\r\nresult: %s\r\n>>> ", input)))
					} else {
						line = append(line, buf[i])
					}
				}
			}
			sendExitStatus(ch, 0)
		},
	})
	defer cleanup()

	// Use /bin/sh (an allowed shell) — the mock server simulates a REPL regardless
	session, err := CreateInteractiveSession(client, "/bin/sh")
	if err != nil {
		t.Fatalf("CreateInteractiveSession: %v", err)
	}
	defer session.Close()

	select {
	case <-shellReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for shell")
	}

	// Read initial prompt
	buf := make([]byte, 4096)
	n, err := session.Stdout.Read(buf)
	if err != nil {
		t.Fatalf("read initial prompt: %v", err)
	}
	prompt := string(buf[:n])
	if !strings.Contains(prompt, ">>>") {
		t.Errorf("expected REPL prompt '>>>', got %q", prompt)
	}

	// Send input
	session.Stdin.Write([]byte("1+1\n"))

	// Read result - may arrive in multiple reads
	var output strings.Builder
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for REPL output, got so far: %q", output.String())
		default:
		}
		n, err = session.Stdout.Read(buf)
		if err != nil {
			break
		}
		output.Write(buf[:n])
		if strings.Contains(output.String(), "result:") {
			break
		}
	}

	if !strings.Contains(output.String(), "result: 1+1") {
		t.Errorf("expected REPL output containing 'result: 1+1', got %q", output.String())
	}

	// Send exit
	session.Stdin.Write([]byte("exit\n"))

	// Read remaining output
	var finalOutput strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(&finalOutput, session.Stdout)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for REPL to exit")
	}

	if !strings.Contains(finalOutput.String(), "bye") {
		t.Errorf("expected 'bye' in final output, got %q", finalOutput.String())
	}
}

func TestLongRunningStreamingOutput(t *testing.T) {
	shellReady := make(chan struct{})

	const numLines = 50
	const lineDelay = 10 * time.Millisecond

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			close(shellReady)
			// Simulate a long-running command that produces output over time
			for i := 0; i < numLines; i++ {
				ch.Write([]byte(fmt.Sprintf("progress: %d/%d\r\n", i+1, numLines)))
				time.Sleep(lineDelay)
			}
			ch.Write([]byte("done\r\n"))
			sendExitStatus(ch, 0)
		},
	})
	defer cleanup()

	session, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession: %v", err)
	}
	defer session.Close()

	select {
	case <-shellReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for shell")
	}

	var output bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(&output, session.Stdout)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for streaming output")
	}

	result := output.String()
	// Verify first and last progress lines
	if !strings.Contains(result, "progress: 1/50") {
		t.Errorf("missing first progress line in output")
	}
	if !strings.Contains(result, "progress: 50/50") {
		t.Errorf("missing last progress line in output")
	}
	if !strings.Contains(result, "done") {
		t.Errorf("missing 'done' marker in output")
	}
}

func TestRapidInputStress(t *testing.T) {
	shellReady := make(chan struct{})

	var mu sync.Mutex
	var totalReceived int

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			close(shellReady)
			buf := make([]byte, 32 * 1024)
			for {
				n, err := ch.Read(buf)
				if n > 0 {
					mu.Lock()
					totalReceived += n
					mu.Unlock()
				}
				if err != nil {
					break
				}
			}
			sendExitStatus(ch, 0)
		},
	})
	defer cleanup()

	session, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession: %v", err)
	}
	defer session.Close()

	select {
	case <-shellReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for shell")
	}

	// Send 1000 rapid messages without waiting between them
	const numMessages = 1000
	const messageSize = 64
	message := strings.Repeat("x", messageSize) + "\n"
	totalSent := 0

	for i := 0; i < numMessages; i++ {
		n, err := session.Stdin.Write([]byte(message))
		if err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		totalSent += n
	}

	// Give time for data to be processed
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	got := totalReceived
	mu.Unlock()

	if got != totalSent {
		t.Errorf("sent %d bytes but shell received %d bytes", totalSent, got)
	}
}

// --- DefaultShell constant test ---

func TestDefaultShellConstant(t *testing.T) {
	if DefaultShell != "/bin/bash" {
		t.Errorf("expected DefaultShell to be '/bin/bash', got %q", DefaultShell)
	}
}

// --- Large data transfer test ---

func TestLargeDataTransfer(t *testing.T) {
	shellReady := make(chan struct{})

	// Generate a large payload (100KB of repeated data)
	largeData := strings.Repeat("A", 100*1024) + "\n"

	client, cleanup := startPTYServer(t, ptyHandler{
		onPTY: func(term string, cols, rows uint32, modes gossh.TerminalModes) bool {
			return true
		},
		onExec: func(cmd string, ch gossh.Channel) {
			close(shellReady)
			ch.Write([]byte(largeData))
			sendExitStatus(ch, 0)
		},
	})
	defer cleanup()

	session, err := CreateInteractiveSession(client, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateInteractiveSession: %v", err)
	}
	defer session.Close()

	select {
	case <-shellReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for shell")
	}

	var output bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(&output, session.Stdout)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout reading large output")
	}

	if output.Len() != len(largeData) {
		t.Errorf("expected %d bytes, got %d", len(largeData), output.Len())
	}
}
