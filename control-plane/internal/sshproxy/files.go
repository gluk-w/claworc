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

	// Log execution time for slow commands. Command content is intentionally omitted
	// from the log to prevent leaking sensitive data (e.g. base64-encoded file content).
	if elapsed > 500*time.Millisecond {
		log.Printf("[sshfiles] SLOW command (%s)", elapsed)
	}

	if runErr != nil {
		if exitErr, ok := runErr.(*ssh.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitStatus(), nil
		}
		return outBuf.String(), errBuf.String(), -1, runErr
	}

	return outBuf.String(), errBuf.String(), 0, nil
}

// RunCommand is the exported equivalent of executeCommand for use outside this package.
func RunCommand(client *ssh.Client, cmd string) (stdout, stderr string, exitCode int, err error) {
	return executeCommand(client, cmd)
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
		// Command content is intentionally omitted to prevent leaking sensitive data.
		log.Printf("[sshfiles] SLOW stdin command (%s, %d bytes)", elapsed, len(input))
	}

	return nil
}

// FileEntry represents a single entry returned by ListDirectory.
type FileEntry struct {
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Size        *string `json:"size"`
	Permissions string  `json:"permissions"`
}

// ParseLsOutput parses the output of `ls -la` into FileEntry slices.
func ParseLsOutput(output string) []FileEntry {
	var entries []FileEntry
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) <= 1 {
		return entries
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 9 {
			continue
		}
		permissions := parts[0]
		size := parts[4]
		entryName := strings.Join(parts[8:], " ")

		if entryName == "." || entryName == ".." {
			continue
		}

		isDir := strings.HasPrefix(permissions, "d")
		isLink := strings.HasPrefix(permissions, "l")

		entryType := "file"
		if isDir {
			entryType = "directory"
		} else if isLink {
			entryType = "symlink"
		}

		var sizePtr *string
		if !isDir {
			sizePtr = &size
		}

		entries = append(entries, FileEntry{
			Name:        entryName,
			Type:        entryType,
			Size:        sizePtr,
			Permissions: permissions,
		})
	}
	return entries
}

// suClaworc wraps a shell command to run as the claworc user.
// Uses shellQuote so the command (which may itself contain single-quoted paths)
// is safely re-escaped for the su -c argument.
func suClaworc(cmd string) string {
	return "su claworc -c " + shellQuote(cmd)
}

// sanitizePath removes null bytes and ASCII control characters (0x00–0x1F, 0x7F)
// from a filesystem path. This prevents injection of control characters into shell
// commands before the path is further quoted with shellQuote.
func sanitizePath(p string) string {
	var b strings.Builder
	b.Grow(len(p))
	for _, r := range p {
		if r >= 0x20 && r != 0x7F {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ListDirectory lists the contents of a remote directory via SSH.
// It executes `ls -la --color=never` and parses the output into FileEntry structs.
func ListDirectory(client *ssh.Client, path string) ([]FileEntry, error) {
	start := time.Now()
	path = sanitizePath(path)
	stdout, stderr, exitCode, err := executeCommand(client, suClaworc(fmt.Sprintf("ls -la --color=never %s", shellQuote(path))))
	if err != nil {
		return nil, fmt.Errorf("list directory: %w", err)
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("list directory: %s", strings.TrimSpace(stderr))
	}
	log.Printf("[sshfiles] ListDirectory completed in %s", time.Since(start))
	return ParseLsOutput(stdout), nil
}

// ReadFile reads the contents of a remote file via SSH.
func ReadFile(client *ssh.Client, path string) ([]byte, error) {
	start := time.Now()
	path = sanitizePath(path)
	stdout, stderr, exitCode, err := executeCommand(client, suClaworc(fmt.Sprintf("cat %s", shellQuote(path))))
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("read file: %s", strings.TrimSpace(stderr))
	}
	log.Printf("[sshfiles] ReadFile completed (%d bytes) in %s", len(stdout), time.Since(start))
	return []byte(stdout), nil
}

// WriteFile writes data to a remote file via SSH.
// For small files it pipes data directly to cat. For large files it uses
// base64-encoded chunks to avoid shell argument length limits.
func WriteFile(client *ssh.Client, path string, data []byte) error {
	start := time.Now()
	path = sanitizePath(path)
	// Use chunked base64 approach for consistency with the existing orchestrator
	// implementation and to handle large files safely.
	const chunkSize = 48000

	// Truncate / create the target file
	_, stderr, exitCode, err := executeCommand(client, suClaworc(fmt.Sprintf("> %s", shellQuote(path))))
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
		cmd := suClaworc(fmt.Sprintf("echo '%s' | base64 -d >> %s", b64, shellQuote(path)))
		_, stderr, exitCode, err = executeCommand(client, cmd)
		if err != nil {
			return fmt.Errorf("write file: %w", err)
		}
		if exitCode != 0 {
			return fmt.Errorf("write file: %s", strings.TrimSpace(stderr))
		}
	}

	log.Printf("[sshfiles] WriteFile completed (%d bytes) in %s", len(data), time.Since(start))
	return nil
}

// CreateDirectory creates a remote directory (and any parent directories) via SSH.
func CreateDirectory(client *ssh.Client, path string) error {
	start := time.Now()
	path = sanitizePath(path)
	_, stderr, exitCode, err := executeCommand(client, suClaworc(fmt.Sprintf("mkdir -p %s", shellQuote(path))))
	if err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("create directory: %s", strings.TrimSpace(stderr))
	}
	log.Printf("[sshfiles] CreateDirectory completed in %s", time.Since(start))
	return nil
}

// DeletePath removes a file or directory (recursively) via SSH.
func DeletePath(client *ssh.Client, path string) error {
	start := time.Now()
	path = sanitizePath(path)
	_, stderr, exitCode, err := executeCommand(client, suClaworc(fmt.Sprintf("rm -rf %s", shellQuote(path))))
	if err != nil {
		return fmt.Errorf("delete path: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("delete path: %s", strings.TrimSpace(stderr))
	}
	log.Printf("[sshfiles] DeletePath completed in %s", time.Since(start))
	return nil
}

// RenamePath renames or moves a file or directory via SSH.
func RenamePath(client *ssh.Client, oldPath, newPath string) error {
	start := time.Now()
	oldPath = sanitizePath(oldPath)
	newPath = sanitizePath(newPath)
	_, stderr, exitCode, err := executeCommand(client, suClaworc(fmt.Sprintf("mv %s %s", shellQuote(oldPath), shellQuote(newPath))))
	if err != nil {
		return fmt.Errorf("rename path: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("rename path: %s", strings.TrimSpace(stderr))
	}
	log.Printf("[sshfiles] RenamePath completed in %s", time.Since(start))
	return nil
}

// SearchFiles searches for files by name within a directory via SSH.
// Returns matching FileEntry items (up to 200 results). Hidden files are excluded.
func SearchFiles(client *ssh.Client, dir, query string) ([]FileEntry, error) {
	start := time.Now()
	dir = sanitizePath(dir)
	query = sanitizePath(query)
	// -not -path "*/\.*" excludes hidden files/dirs; head -200 caps results.
	cmd := suClaworc(fmt.Sprintf("find %s -iname %s -not -path '*/\\.*' 2>/dev/null | head -200", shellQuote(dir), shellQuote("*"+query+"*")))
	stdout, stderr, exitCode, err := executeCommand(client, cmd)
	if err != nil {
		return nil, fmt.Errorf("search files: %w", err)
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("search files: %s", strings.TrimSpace(stderr))
	}

	var entries []FileEntry
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Use stat to determine type
		statOut, _, statCode, statErr := executeCommand(client, suClaworc(fmt.Sprintf("stat -c '%%F' %s 2>/dev/null", shellQuote(line))))
		entryType := "file"
		if statErr == nil && statCode == 0 {
			if strings.Contains(strings.TrimSpace(statOut), "directory") {
				entryType = "directory"
			}
		}
		entries = append(entries, FileEntry{
			Name:        line,
			Type:        entryType,
			Permissions: "",
		})
	}
	log.Printf("[sshfiles] SearchFiles results=%d completed in %s", len(entries), time.Since(start))
	return entries, nil
}
