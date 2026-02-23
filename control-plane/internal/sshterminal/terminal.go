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

// AllowedShells is the set of shells permitted for interactive sessions.
// Any shell not in this list will be rejected by CreateInteractiveSession.
var AllowedShells = map[string]bool{
	"/bin/bash": true,
	"/bin/sh":   true,
	"/bin/zsh":  true,
}

// MaxInputMessageSize is the maximum size in bytes for a single terminal input
// message. Messages exceeding this limit are rejected to prevent DoS.
const MaxInputMessageSize = 64 * 1024 // 64 KB

// MaxResizeCols and MaxResizeRows define upper bounds for terminal resize
// requests. Values beyond these are rejected to prevent abuse.
const (
	MaxResizeCols uint16 = 500
	MaxResizeRows uint16 = 500
)

// ValidateShell checks if the given shell command is in the AllowedShells
// whitelist. If the shell is empty (defaults to /bin/bash), it is accepted.
// Commands like "su - user" are allowed when the base command is an allowed
// shell or a known safe command.
func ValidateShell(shell string) error {
	if shell == "" {
		return nil // defaults to /bin/bash
	}

	// Allow exact matches from the whitelist
	if AllowedShells[shell] {
		return nil
	}

	// Allow "su" commands (used to switch to agent user)
	// Only "su" and "su - <user>" forms are permitted
	if len(shell) >= 2 && shell[:2] == "su" {
		// Must be exactly "su" or start with "su " or "su\t"
		if len(shell) == 2 || shell[2] == ' ' || shell[2] == '\t' {
			// Reject if it contains shell metacharacters that could enable injection
			for _, c := range shell {
				switch c {
				case ';', '&', '|', '$', '`', '(', ')', '{', '}', '<', '>', '\n', '\\', '"', '\'', '!':
					return fmt.Errorf("shell command %q contains forbidden character %q", shell, string(c))
				}
			}
			return nil
		}
	}

	return fmt.Errorf("shell %q is not in the allowed list", shell)
}

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
// specified shell. If shell is empty, it defaults to "/bin/bash". The shell
// must be in AllowedShells or be a permitted "su" command; otherwise an error
// is returned to prevent command injection.
func CreateInteractiveSession(client *ssh.Client, shell string) (*TerminalSession, error) {
	if err := ValidateShell(shell); err != nil {
		return nil, fmt.Errorf("validate shell: %w", err)
	}
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
