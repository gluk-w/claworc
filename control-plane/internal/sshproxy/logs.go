// SSH-based log streaming for remote agent instances.
//
// All functions accept an *ssh.Client obtained from sshproxy.SSHManager and
// execute shell commands over SSH sessions. The SSH connection is assumed to
// already be authenticated (EnsureConnected handles key upload).
//
// # Agent Log Paths
//
// The agent container uses s6-overlay for process supervision. Service output
// is redirected to files under /var/log/claworc/:
//
//   - /var/log/claworc/openclaw.log — OpenClaw gateway stdout/stderr
//   - /var/log/claworc/sshd.log     — SSH daemon stderr (debug via -e flag)
//
// Standard system logs are also available at their usual Ubuntu paths:
//
//   - /var/log/syslog   — general system messages
//   - /var/log/auth.log — SSH/auth events
//
// The agent does NOT use systemd (it uses s6-overlay), so journalctl is not
// available. All logs must be read as files via tail over SSH.
//
// # Log Rotation Handling
//
// When streaming in follow mode, log rotation must be handled gracefully. The
// agent may use logrotate (or similar) to rotate log files, which typically:
//
//  1. Renames the current file (e.g. syslog → syslog.1)
//  2. Creates a new empty file at the original path
//  3. Optionally compresses the rotated file
//
// By default, StreamLogs uses tail -F (--follow=name --retry), which detects
// rotation and reopens the file by name. This ensures continuous streaming
// even when the underlying file is replaced. When tail detects rotation it
// emits a diagnostic line to stderr (not forwarded to the SSE stream):
//
//	tail: '/var/log/syslog' has been replaced; following new file
//
// If FollowName is set to false in StreamOptions, tail -f is used instead,
// which follows the file descriptor. In that mode, tail continues reading
// the old (renamed) file after rotation and will NOT pick up the new file.
// This is only useful for files that are never rotated or when you
// intentionally want to read the pre-rotation content to completion.
package sshproxy

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Standard log file paths on the agent container.
const (
	LogPathOpenClaw = "/var/log/claworc/openclaw.log"
	LogPathSSHD     = "/var/log/claworc/sshd.log"
	LogPathSyslog   = "/var/log/syslog"
	LogPathAuth     = "/var/log/auth.log"
)

// LogType represents a named category of log stream.
type LogType string

const (
	LogTypeOpenClaw LogType = "openclaw"
	LogTypeSSHD     LogType = "sshd"
	LogTypeSystem   LogType = "system"
	LogTypeAuth     LogType = "auth"
)

// DefaultLogPaths maps each LogType to its default file path on the agent.
var DefaultLogPaths = map[LogType]string{
	LogTypeOpenClaw: LogPathOpenClaw,
	LogTypeSSHD:     LogPathSSHD,
	LogTypeSystem:   LogPathSyslog,
	LogTypeAuth:     LogPathAuth,
}

// AllLogTypes returns the list of supported log types in display order.
func AllLogTypes() []LogType {
	return []LogType{LogTypeOpenClaw, LogTypeSystem, LogTypeAuth, LogTypeSSHD}
}

// StreamOptions configures how log streaming behaves.
type StreamOptions struct {
	// Tail is the number of lines to read from the end of the file before
	// streaming new content. Defaults to 100 if zero.
	Tail int

	// Follow keeps the stream open after reaching EOF, sending new lines as
	// they are appended to the file. When false, the stream ends at EOF.
	Follow bool

	// FollowName controls whether tail follows the file by name (tail -F) or
	// by file descriptor (tail -f). Following by name handles log rotation
	// gracefully: when the file is renamed and a new file is created at the
	// same path, tail detects this and switches to the new file.
	//
	// Defaults to true. Set to false only if you want to continue reading
	// a rotated (renamed) file rather than the newly created replacement.
	FollowName *bool
}

// DefaultStreamOptions returns StreamOptions with sensible defaults:
// 100-line tail, follow enabled, follow-by-name enabled.
func DefaultStreamOptions() StreamOptions {
	followName := true
	return StreamOptions{
		Tail:       100,
		Follow:     true,
		FollowName: &followName,
	}
}

// followByName returns whether the stream should follow by file name.
// Defaults to true if FollowName is nil.
func (o StreamOptions) followByName() bool {
	if o.FollowName == nil {
		return true
	}
	return *o.FollowName
}

// ResolveLogPath returns the file path for a log type. If customPaths contains
// an override for the type it is used; otherwise the default path is returned.
// Returns empty string if the type is unknown and not in customPaths.
func ResolveLogPath(logType LogType, customPaths map[LogType]string) string {
	if customPaths != nil {
		if p, ok := customPaths[logType]; ok {
			return p
		}
	}
	return DefaultLogPaths[logType]
}

// StreamLogs streams log output from a remote file via SSH using tail.
//
// It opens a persistent SSH session, runs `tail -n {tail} [-F|-f] {logPath}`,
// and sends each line to the returned channel. The channel is closed when the
// context is cancelled, the SSH session ends, or the stream reaches EOF (non-follow mode).
//
// Log rotation behavior is controlled by StreamOptions.FollowName (default true):
//
//   - FollowName=true  → tail -F (follow by name + retry). Handles log rotation
//     by detecting when the file is replaced and reopening it. This is the
//     recommended mode for long-lived streams on files managed by logrotate.
//   - FollowName=false → tail -f (follow by descriptor). Continues reading the
//     original file descriptor after rotation. Use this only when you want to
//     read the old file to completion.
//
// The caller must cancel the context to stop streaming; this closes the SSH
// session and drains the goroutine.
func StreamLogs(ctx context.Context, client *ssh.Client, logPath string, opts StreamOptions) (<-chan string, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open ssh session: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	tail := opts.Tail
	if tail <= 0 {
		tail = 100
	}

	// Build tail command. The flag choice determines log rotation behavior:
	//   -F (--follow=name --retry): reopens the file by name after rotation
	//   -f (--follow=descriptor):   keeps reading the old file descriptor
	cmd := fmt.Sprintf("tail -n %d", tail)
	if opts.Follow {
		if opts.followByName() {
			cmd += " -F" // follow by name — handles log rotation
		} else {
			cmd += " -f" // follow by descriptor — ignores rotation
		}
	}
	cmd += " " + shellQuote(logPath)

	if err := session.Start(cmd); err != nil {
		session.Close()
		return nil, fmt.Errorf("start tail command: %w", err)
	}

	ch := make(chan string, 100)

	go func() {
		defer close(ch)
		defer session.Close()

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			select {
			case ch <- line:
			case <-ctx.Done():
				return
			}
		}

		if err := scanner.Err(); err != nil {
			// Only log if we weren't cancelled — cancellation closes the
			// session which causes an expected read error.
			select {
			case <-ctx.Done():
			default:
				log.Printf("[sshlogs] scanner error for %s: %v", logPath, err)
			}
		}
	}()

	// Watch for context cancellation to close the session, which unblocks
	// the scanner in the goroutine above.
	go func() {
		<-ctx.Done()
		session.Close()
	}()

	return ch, nil
}

// GetAvailableLogFiles returns the list of log file paths that exist on the
// remote agent. It checks claworc service logs and standard system log
// locations, returning only those that are present.
func GetAvailableLogFiles(client *ssh.Client) ([]string, error) {
	candidates := []string{
		LogPathOpenClaw,
		LogPathSSHD,
		LogPathSyslog,
		LogPathAuth,
		"/var/log/kern.log",
		"/var/log/dpkg.log",
		"/var/log/alternatives.log",
	}

	// Build a single command that tests each file and prints those that exist.
	var checks []string
	for _, path := range candidates {
		checks = append(checks, fmt.Sprintf("[ -f %s ] && echo %s", shellQuote(path), shellQuote(path)))
	}
	cmd := strings.Join(checks, "; ")

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open ssh session: %w", err)
	}
	defer session.Close()

	out, err := session.CombinedOutput(cmd)
	if err != nil {
		// The compound command may exit non-zero if the last test fails,
		// which is fine — we still get stdout from earlier successful tests.
		// Only fail on transport-level errors (session already closed, etc.).
		if _, ok := err.(*ssh.ExitError); !ok {
			return nil, fmt.Errorf("check log files: %w", err)
		}
	}

	var found []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			found = append(found, line)
		}
	}
	return found, nil
}

// shellQuote wraps a string in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
