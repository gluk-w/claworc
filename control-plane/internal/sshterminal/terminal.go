// Package sshterminal provides interactive terminal sessions over SSH connections.
//
// It wraps golang.org/x/crypto/ssh to create PTY-backed shell sessions with
// support for terminal resizing. The package is used by the terminal WebSocket
// handler to provide browser-based terminal access to agent instances.
package sshterminal

import (
	"fmt"
	"io"

	"golang.org/x/crypto/ssh"
)

// TerminalSession wraps an SSH session with PTY support for interactive shell access.
type TerminalSession struct {
	Stdin   io.WriteCloser
	Stdout  io.Reader
	Session *ssh.Session
}

// Resize changes the terminal dimensions of the PTY.
func (ts *TerminalSession) Resize(cols, rows uint16) error {
	return ts.Session.WindowChange(int(rows), int(cols))
}

// Close terminates the SSH session and releases resources.
func (ts *TerminalSession) Close() error {
	return ts.Session.Close()
}

// CreateInteractiveSession opens a new SSH session with a PTY and starts the
// specified shell. If shell is empty, it defaults to "/bin/bash".
func CreateInteractiveSession(client *ssh.Client, shell string) (*TerminalSession, error) {
	if shell == "" {
		shell = "/bin/bash"
	}

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("create ssh session: %w", err)
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	if err := session.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		session.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := session.Start(shell); err != nil {
		session.Close()
		return nil, fmt.Errorf("start shell %q: %w", shell, err)
	}

	return &TerminalSession{
		Stdin:   stdin,
		Stdout:  stdout,
		Session: session,
	}, nil
}
