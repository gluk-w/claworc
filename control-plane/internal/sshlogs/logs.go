// Package sshlogs provides SSH-based log streaming for remote instances.
//
// Performance characteristics (SSH exec vs previous K8s/Docker exec approach):
//   - SSH exec reuses a persistent multiplexed connection per instance, avoiding
//     the per-request overhead of K8s exec (which creates a new SPDY stream and
//     authenticates with the API server each time).
//   - Streaming with "tail -f" maintains a single SSH session for the duration,
//     providing real-time log delivery with minimal latency.
//   - All operations log their duration at the [sshlogs] log prefix for monitoring.
package sshlogs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// LogType identifies a category of logs on the agent.
type LogType string

const (
	LogTypeOpenClaw LogType = "openclaw"
	LogTypeBrowser  LogType = "browser"
	LogTypeSystem   LogType = "system"
)

// defaultLogPathMap maps each log type to its default file path on the agent.
//
// Agent log locations (s6-overlay services):
//   - openclaw: OpenClaw gateway stdout/stderr → /var/log/openclaw.log
//   - browser:  Chromium stdout/stderr         → /tmp/browser.log
//   - system:   SSH daemon stderr              → /var/log/sshd.log
//
// The agent uses s6-overlay (not systemd), so journalctl is not available.
// Logs are written via shell redirection in each service's run script.
var defaultLogPathMap = map[LogType]string{
	LogTypeOpenClaw: "/var/log/openclaw.log",
	LogTypeBrowser:  "/tmp/browser.log",
	LogTypeSystem:   "/var/log/sshd.log",
}

// DefaultLogPaths is the set of standard log file paths checked on the agent.
var DefaultLogPaths = []string{
	"/var/log/openclaw.log",
	"/tmp/browser.log",
	"/var/log/sshd.log",
}

// AllLogTypes returns the list of supported log types.
func AllLogTypes() []LogType {
	return []LogType{LogTypeOpenClaw, LogTypeBrowser, LogTypeSystem}
}

// DefaultPathForType returns the default log file path for a given log type.
func DefaultPathForType(lt LogType) (string, bool) {
	p, ok := defaultLogPathMap[lt]
	return p, ok
}

// ResolveLogPath returns the log file path for a log type, using custom paths
// from the instance configuration if available, otherwise falling back to the
// default path. The customPaths map is keyed by LogType string values.
func ResolveLogPath(lt LogType, customPaths map[string]string) (string, bool) {
	if customPaths != nil {
		if p, ok := customPaths[string(lt)]; ok && p != "" {
			return p, true
		}
	}
	return DefaultPathForType(lt)
}

// StreamLogs streams log lines from a remote file via SSH. It executes a tail
// command on the remote host and returns a channel that receives log lines.
//
// When follow is true, it uses "tail -f" to continuously stream new lines.
// When follow is false, it tails the last `tail` lines and completes.
//
// The returned channel is closed when:
//   - The command completes (non-follow mode)
//   - The context is cancelled
//   - An error occurs reading the stream
func StreamLogs(ctx context.Context, sshClient *ssh.Client, logPath string, tail int, follow bool) (<-chan string, error) {
	session, err := sshClient.NewSession()
	if err != nil {
		return nil, fmt.Errorf("create SSH session: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	cmd := buildTailCommand(logPath, tail, follow)
	log.Printf("[sshlogs] starting stream cmd=%q", cmd)

	if err := session.Start(cmd); err != nil {
		session.Close()
		return nil, fmt.Errorf("start tail command: %w", err)
	}

	ch := make(chan string, 100)

	go func() {
		defer close(ch)
		defer session.Close()

		start := time.Now()
		lineCount := 0
		scanner := bufio.NewScanner(stdout)

		for scanner.Scan() {
			line := scanner.Text()
			lineCount++
			select {
			case ch <- line:
			case <-ctx.Done():
				log.Printf("[sshlogs] context cancelled after %d lines duration=%s", lineCount, time.Since(start))
				return
			}
		}

		if err := scanner.Err(); err != nil {
			// Don't log errors caused by session close during context cancellation
			select {
			case <-ctx.Done():
			default:
				log.Printf("[sshlogs] scanner error after %d lines duration=%s err=%v", lineCount, time.Since(start), err)
			}
		}

		log.Printf("[sshlogs] stream ended lines=%d duration=%s", lineCount, time.Since(start))
	}()

	return ch, nil
}

// GetAvailableLogFiles checks which of the standard log files exist on the
// remote host and returns their paths.
func GetAvailableLogFiles(sshClient *ssh.Client) ([]string, error) {
	session, err := sshClient.NewSession()
	if err != nil {
		return nil, fmt.Errorf("create SSH session: %w", err)
	}
	defer session.Close()

	// Build a command that tests each path and prints the ones that exist.
	// The "; true" at the end ensures the overall exit code is 0 even if
	// the last test -f fails.
	var checks []string
	for _, path := range DefaultLogPaths {
		checks = append(checks, fmt.Sprintf("test -f %s && echo %s", shellQuote(path), shellQuote(path)))
	}
	cmd := strings.Join(checks, "; ") + "; true"

	var stdoutBuf bytes.Buffer
	session.Stdout = &stdoutBuf

	start := time.Now()
	err = session.Run(cmd)
	elapsed := time.Since(start)

	if err != nil {
		if _, ok := err.(*ssh.ExitError); !ok {
			return nil, fmt.Errorf("check log files: %w", err)
		}
	}

	log.Printf("[sshlogs] checked log files duration=%s", elapsed)

	var available []string
	for _, line := range strings.Split(strings.TrimSpace(stdoutBuf.String()), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			available = append(available, line)
		}
	}

	return available, nil
}

// buildTailCommand constructs the tail command string.
func buildTailCommand(logPath string, tail int, follow bool) string {
	if follow {
		return fmt.Sprintf("tail -f -n %d %s", tail, shellQuote(logPath))
	}
	return fmt.Sprintf("tail -n %d %s", tail, shellQuote(logPath))
}

// shellQuote wraps a string in single quotes for safe shell usage,
// escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
