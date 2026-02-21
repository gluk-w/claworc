// Package sshterminal provides SSH-based interactive terminal sessions.
//
// It wraps golang.org/x/crypto/ssh to provide PTY-enabled shell sessions
// with bidirectional I/O and dynamic terminal resizing. Sessions are created
// over an existing SSH client connection (typically obtained from SSHManager),
// enabling connection multiplexing across all SSH-based features.
//
// Usage:
//
//	session, err := sshterminal.CreateInteractiveSession(sshClient, "/bin/bash")
//	if err != nil { ... }
//	defer session.Close()
//
//	// Write user input to the shell
//	session.Stdin.Write([]byte("ls -la\n"))
//
//	// Read shell output
//	buf := make([]byte, 4096)
//	n, _ := session.Stdout.Read(buf)
//
//	// Resize the terminal
//	session.Resize(120, 40)
package sshterminal

import (
	"fmt"
	"io"
	"log"
	"sync"

	"golang.org/x/crypto/ssh"
)

// DefaultShell is the shell started when no shell is specified.
const DefaultShell = "/bin/bash"

// defaultTermCols and defaultTermRows are the initial PTY dimensions.
const (
	defaultTermCols = 80
	defaultTermRows = 24
)

// TerminalSession wraps an SSH session providing an interactive PTY shell
// with bidirectional I/O and terminal resize support.
type TerminalSession struct {
	// Stdin is the writer for sending input to the remote shell.
	Stdin io.WriteCloser

	// Stdout is the reader for receiving output from the remote shell.
	// It carries both stdout and stderr (merged by the PTY).
	Stdout io.Reader

	// Session is the underlying SSH session.
	Session *ssh.Session

	mu     sync.Mutex
	closed bool
}

// Resize changes the PTY dimensions of the terminal session.
// It sends an SSH window-change request to update the remote terminal size.
func (ts *TerminalSession) Resize(cols, rows uint16) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.closed {
		return fmt.Errorf("terminal session is closed")
	}

	if err := ts.Session.WindowChange(int(rows), int(cols)); err != nil {
		return fmt.Errorf("resize terminal: %w", err)
	}
	return nil
}

// Close terminates the terminal session and releases associated resources.
// It is safe to call Close multiple times.
func (ts *TerminalSession) Close() error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.closed {
		return nil
	}
	ts.closed = true

	var firstErr error
	if ts.Stdin != nil {
		if err := ts.Stdin.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := ts.Session.Close(); err != nil && firstErr == nil {
		firstErr = err
	}

	log.Printf("[sshterminal] session closed")
	return firstErr
}

// CreateInteractiveSession creates a new interactive terminal session over SSH.
// It requests a PTY with standard terminal modes and starts the given shell.
// If shell is empty, DefaultShell ("/bin/bash") is used.
//
// The returned TerminalSession provides:
//   - Stdin: write user keystrokes here
//   - Stdout: read shell output (includes stderr, merged by PTY)
//   - Resize(): dynamically change terminal dimensions
//   - Close(): clean up the session
func CreateInteractiveSession(sshClient *ssh.Client, shell string) (*TerminalSession, error) {
	if shell == "" {
		shell = DefaultShell
	}

	session, err := sshClient.NewSession()
	if err != nil {
		return nil, fmt.Errorf("create SSH session: %w", err)
	}

	// Set terminal modes for a proper interactive experience
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,     // Enable echo
		ssh.TTY_OP_ISPEED: 14400, // Input speed in baud
		ssh.TTY_OP_OSPEED: 14400, // Output speed in baud
	}

	// Request a PTY with xterm-256color for full color support
	if err := session.RequestPty("xterm-256color", defaultTermRows, defaultTermCols, modes); err != nil {
		session.Close()
		return nil, fmt.Errorf("request PTY: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	// Start the shell process
	if err := session.Start(shell); err != nil {
		session.Close()
		return nil, fmt.Errorf("start shell %q: %w", shell, err)
	}

	log.Printf("[sshterminal] interactive session started shell=%q", shell)

	return &TerminalSession{
		Stdin:   stdin,
		Stdout:  stdout,
		Session: session,
	}, nil
}
