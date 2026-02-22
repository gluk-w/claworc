// SSH-based file operations for remote agent instances.
//
// All functions accept an *ssh.Client obtained from SSHManager and
// execute shell commands over SSH sessions. The SSH connection is assumed to
// already be authenticated (EnsureConnected handles key upload).
package sshproxy

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
)

// executeCommand creates a new SSH session, runs cmd, and returns stdout,
// stderr, the exit code, and any transport-level error.
// Logs execution time for performance monitoring.
func executeCommand(client *ssh.Client, cmd string) (stdout, stderr string, exitCode int, err error) {
	start := time.Now()

	session, err := client.NewSession()
	if err != nil {
		return "", "", -1, fmt.Errorf("open ssh session: %w", err)
	}
	defer session.Close()

	var outBuf, errBuf bytes.Buffer
	session.Stdout = &outBuf
	session.Stderr = &errBuf

	runErr := session.Run(cmd)
	elapsed := time.Since(start)

	// Log command execution time. Truncate long commands to keep logs readable.
	cmdLabel := cmd
	if len(cmdLabel) > 80 {
		cmdLabel = cmdLabel[:80] + "..."
	}
	if elapsed > 500*time.Millisecond {
		log.Printf("[sshfiles] SLOW command (%s): %s", elapsed, cmdLabel)
	}

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
// Logs execution time and input size for performance monitoring.
func executeCommandWithStdin(client *ssh.Client, cmd string, input []byte) error {
	start := time.Now()

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

	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		log.Printf("[sshfiles] SLOW stdin command (%s, %d bytes): %s", elapsed, len(input), cmd)
	}

	return nil
}

// ListDirectory lists the contents of a remote directory via SSH.
// It executes `ls -la --color=never` and parses the output into FileEntry structs.
func ListDirectory(client *ssh.Client, path string) ([]orchestrator.FileEntry, error) {
	start := time.Now()
	stdout, stderr, exitCode, err := executeCommand(client, fmt.Sprintf("ls -la --color=never %s", shellQuote(path)))
	if err != nil {
		return nil, fmt.Errorf("list directory: %w", err)
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("list directory: %s", strings.TrimSpace(stderr))
	}
	log.Printf("[sshfiles] ListDirectory %s completed in %s", path, time.Since(start))
	return orchestrator.ParseLsOutput(stdout), nil
}

// ReadFile reads the contents of a remote file via SSH.
func ReadFile(client *ssh.Client, path string) ([]byte, error) {
	start := time.Now()
	stdout, stderr, exitCode, err := executeCommand(client, fmt.Sprintf("cat %s", shellQuote(path)))
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("read file: %s", strings.TrimSpace(stderr))
	}
	log.Printf("[sshfiles] ReadFile %s (%d bytes) completed in %s", path, len(stdout), time.Since(start))
	return []byte(stdout), nil
}

// WriteFile writes data to a remote file via SSH.
// For small files it pipes data directly to cat. For large files it uses
// base64-encoded chunks to avoid shell argument length limits.
func WriteFile(client *ssh.Client, path string, data []byte) error {
	start := time.Now()
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

	log.Printf("[sshfiles] WriteFile %s (%d bytes) completed in %s", path, len(data), time.Since(start))
	return nil
}

// CreateDirectory creates a remote directory (and any parent directories) via SSH.
func CreateDirectory(client *ssh.Client, path string) error {
	start := time.Now()
	_, stderr, exitCode, err := executeCommand(client, fmt.Sprintf("mkdir -p %s", shellQuote(path)))
	if err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("create directory: %s", strings.TrimSpace(stderr))
	}
	log.Printf("[sshfiles] CreateDirectory %s completed in %s", path, time.Since(start))
	return nil
}
