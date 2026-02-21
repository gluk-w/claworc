// Package sshfiles provides SSH-based file operations for remote agent instances.
//
// All functions accept an *ssh.Client obtained from sshproxy.SSHManager and
// execute shell commands over SSH sessions. The SSH connection is assumed to
// already be authenticated (EnsureConnected handles key upload).
package sshfiles

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
)

// executeCommand creates a new SSH session, runs cmd, and returns stdout,
// stderr, the exit code, and any transport-level error.
func executeCommand(client *ssh.Client, cmd string) (stdout, stderr string, exitCode int, err error) {
	session, err := client.NewSession()
	if err != nil {
		return "", "", -1, fmt.Errorf("open ssh session: %w", err)
	}
	defer session.Close()

	var outBuf, errBuf bytes.Buffer
	session.Stdout = &outBuf
	session.Stderr = &errBuf

	runErr := session.Run(cmd)
	if runErr != nil {
		if exitErr, ok := runErr.(*ssh.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitStatus(), nil
		}
		return outBuf.String(), errBuf.String(), -1, runErr
	}

	return outBuf.String(), errBuf.String(), 0, nil
}

// executeCommandWithStdin creates a new SSH session, pipes input to the
// command's stdin, and waits for completion.
func executeCommandWithStdin(client *ssh.Client, cmd string, input []byte) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("open ssh session: %w", err)
	}
	defer session.Close()

	stdinPipe, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("create stdin pipe: %w", err)
	}

	var errBuf bytes.Buffer
	session.Stderr = &errBuf

	if err := session.Start(cmd); err != nil {
		return fmt.Errorf("start command: %w", err)
	}

	if _, err := io.Copy(stdinPipe, bytes.NewReader(input)); err != nil {
		return fmt.Errorf("write to stdin: %w", err)
	}
	stdinPipe.Close()

	if err := session.Wait(); err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			return fmt.Errorf("command exited %d: %s", exitErr.ExitStatus(), errBuf.String())
		}
		return err
	}

	return nil
}

// ListDirectory lists the contents of a remote directory via SSH.
// It executes `ls -la --color=never` and parses the output into FileEntry structs.
func ListDirectory(client *ssh.Client, path string) ([]orchestrator.FileEntry, error) {
	stdout, stderr, exitCode, err := executeCommand(client, fmt.Sprintf("ls -la --color=never %s", shellQuote(path)))
	if err != nil {
		return nil, fmt.Errorf("list directory: %w", err)
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("list directory: %s", strings.TrimSpace(stderr))
	}
	return orchestrator.ParseLsOutput(stdout), nil
}

// ReadFile reads the contents of a remote file via SSH.
func ReadFile(client *ssh.Client, path string) ([]byte, error) {
	stdout, stderr, exitCode, err := executeCommand(client, fmt.Sprintf("cat %s", shellQuote(path)))
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("read file: %s", strings.TrimSpace(stderr))
	}
	return []byte(stdout), nil
}

// WriteFile writes data to a remote file via SSH.
// For small files it pipes data directly to cat. For large files it uses
// base64-encoded chunks to avoid shell argument length limits.
func WriteFile(client *ssh.Client, path string, data []byte) error {
	// Use chunked base64 approach for consistency with the existing orchestrator
	// implementation and to handle large files safely.
	const chunkSize = 48000

	// Truncate / create the target file
	_, stderr, exitCode, err := executeCommand(client, fmt.Sprintf("> %s", shellQuote(path)))
	if err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("write file: %s", strings.TrimSpace(stderr))
	}

	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		b64 := base64.StdEncoding.EncodeToString(data[i:end])
		cmd := fmt.Sprintf("echo '%s' | base64 -d >> %s", b64, shellQuote(path))
		_, stderr, exitCode, err = executeCommand(client, cmd)
		if err != nil {
			return fmt.Errorf("write file: %w", err)
		}
		if exitCode != 0 {
			return fmt.Errorf("write file: %s", strings.TrimSpace(stderr))
		}
	}

	return nil
}

// CreateDirectory creates a remote directory (and any parent directories) via SSH.
func CreateDirectory(client *ssh.Client, path string) error {
	_, stderr, exitCode, err := executeCommand(client, fmt.Sprintf("mkdir -p %s", shellQuote(path)))
	if err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("create directory: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// shellQuote wraps a string in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
