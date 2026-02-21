package sshfiles

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
)

// executeCommand runs a command on the remote host via SSH and returns stdout,
// stderr, the exit code, and any transport-level error.
func executeCommand(sshClient *ssh.Client, cmd string) (stdout, stderr string, exitCode int, err error) {
	session, err := sshClient.NewSession()
	if err != nil {
		return "", "", -1, fmt.Errorf("create SSH session: %w", err)
	}
	defer session.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	err = session.Run(cmd)
	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			return stdoutBuf.String(), stderrBuf.String(), exitErr.ExitStatus(), nil
		}
		return stdoutBuf.String(), stderrBuf.String(), -1, fmt.Errorf("run command: %w", err)
	}

	return stdoutBuf.String(), stderrBuf.String(), 0, nil
}

// executeCommandWithStdin runs a command on the remote host via SSH, piping
// input to the command's stdin. Used for file writing operations.
func executeCommandWithStdin(sshClient *ssh.Client, cmd string, input []byte) error {
	session, err := sshClient.NewSession()
	if err != nil {
		return fmt.Errorf("create SSH session: %w", err)
	}
	defer session.Close()

	stdinPipe, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("create stdin pipe: %w", err)
	}

	var stderrBuf bytes.Buffer
	session.Stderr = &stderrBuf

	if err := session.Start(cmd); err != nil {
		return fmt.Errorf("start command: %w", err)
	}

	if _, err := io.Copy(stdinPipe, bytes.NewReader(input)); err != nil {
		return fmt.Errorf("write to stdin: %w", err)
	}
	stdinPipe.Close()

	if err := session.Wait(); err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			return fmt.Errorf("command exited with status %d: %s", exitErr.ExitStatus(), strings.TrimSpace(stderrBuf.String()))
		}
		return fmt.Errorf("wait for command: %w", err)
	}

	return nil
}

// ListDirectory lists the contents of a directory on the remote host via SSH.
// It executes `ls -la --color=never` and parses the output into FileEntry
// structs using the existing orchestrator.ParseLsOutput parser.
func ListDirectory(sshClient *ssh.Client, path string) ([]orchestrator.FileEntry, error) {
	stdout, stderr, exitCode, err := executeCommand(sshClient, fmt.Sprintf("ls -la --color=never %s", shellQuote(path)))
	if err != nil {
		return nil, fmt.Errorf("list directory %s: %w", path, err)
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("list directory %s: %s", path, strings.TrimSpace(stderr))
	}
	return orchestrator.ParseLsOutput(stdout), nil
}

// ReadFile reads the contents of a file on the remote host via SSH.
// It executes `cat` and returns the stdout as a byte slice.
func ReadFile(sshClient *ssh.Client, path string) ([]byte, error) {
	session, err := sshClient.NewSession()
	if err != nil {
		return nil, fmt.Errorf("create SSH session: %w", err)
	}
	defer session.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	err = session.Run(fmt.Sprintf("cat %s", shellQuote(path)))
	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			return nil, fmt.Errorf("read file %s: %s (exit %d)", path, strings.TrimSpace(stderrBuf.String()), exitErr.ExitStatus())
		}
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}

	return stdoutBuf.Bytes(), nil
}

// WriteFile writes data to a file on the remote host via SSH.
// It pipes the data to `cat > path` through stdin, avoiding shell argument
// length limits and encoding issues.
func WriteFile(sshClient *ssh.Client, path string, data []byte) error {
	err := executeCommandWithStdin(sshClient, fmt.Sprintf("cat > %s", shellQuote(path)), data)
	if err != nil {
		return fmt.Errorf("write file %s: %w", path, err)
	}
	return nil
}

// CreateDirectory creates a directory (and any necessary parents) on the
// remote host via SSH using `mkdir -p`.
func CreateDirectory(sshClient *ssh.Client, path string) error {
	_, stderr, exitCode, err := executeCommand(sshClient, fmt.Sprintf("mkdir -p %s", shellQuote(path)))
	if err != nil {
		return fmt.Errorf("create directory %s: %w", path, err)
	}
	if exitCode != 0 {
		return fmt.Errorf("create directory %s: %s", path, strings.TrimSpace(stderr))
	}
	return nil
}

// shellQuote wraps a string in single quotes for safe shell usage,
// escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
