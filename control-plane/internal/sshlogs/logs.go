// Package sshlogs provides SSH-based log streaming for remote agent instances.
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
package sshlogs

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
// It opens a persistent SSH session, runs `tail -n {tail} [-F] {logPath}`,
// and sends each line to the returned channel. The channel is closed when the
// context is cancelled, the SSH session ends, or the stream reaches EOF (non-follow mode).
//
// When follow is true, tail -F is used instead of tail -f so that log rotation
// is handled gracefully (tail follows by name, reconnecting to the new file).
//
// The caller must cancel the context to stop streaming; this closes the SSH
// session and drains the goroutine.
func StreamLogs(ctx context.Context, client *ssh.Client, logPath string, tail int, follow bool) (<-chan string, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open ssh session: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	// Build tail command
	cmd := fmt.Sprintf("tail -n %d", tail)
	if follow {
		cmd += " -F" // -F follows by name (handles log rotation)
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
