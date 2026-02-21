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

// StreamOptions controls the behavior of log streaming.
type StreamOptions struct {
	// FollowByName controls whether tail follows the file by name (tail -F)
	// or by file descriptor (tail -f). Following by name (the default, true)
	// handles log rotation gracefully: when logrotate renames the file and
	// creates a new one, tail detects the change and switches to the new file.
	// Set to false to use tail -f (follow by descriptor) if log rotation is
	// not a concern and you want the simpler behavior.
	FollowByName bool
}

// DefaultStreamOptions returns the default streaming options with log rotation
// awareness enabled (FollowByName=true).
func DefaultStreamOptions() StreamOptions {
	return StreamOptions{
		FollowByName: true,
	}
}

// StreamLogs streams log lines from a remote file via SSH. It executes a tail
// command on the remote host and returns a channel that receives log lines.
//
// When follow is true, it uses "tail -F" (follow by name with retry) by default
// to continuously stream new lines, handling log rotation gracefully. The
// behavior can be customized via opts; pass nil to use DefaultStreamOptions().
//
// When follow is false, it tails the last `tail` lines and completes.
//
// The returned channel is closed when:
//   - The command completes (non-follow mode)
//   - The context is cancelled
//   - An error occurs reading the stream
func StreamLogs(ctx context.Context, sshClient *ssh.Client, logPath string, tail int, follow bool, opts ...StreamOptions) (<-chan string, error) {
	session, err := sshClient.NewSession()
	if err != nil {
		return nil, fmt.Errorf("create SSH session: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	o := DefaultStreamOptions()
	if len(opts) > 0 {
		o = opts[0]
	}

	cmd := buildTailCommand(logPath, tail, follow, o.FollowByName)
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
//
// When follow is true and followByName is true, uses "tail -F" which is
// equivalent to "--follow=name --retry". This handles log rotation by
// detecting when the file is renamed and reopening by name. When the log
// file is rotated (e.g. by logrotate), tail will notice the file has been
// replaced and will start reading from the new file automatically.
//
// When follow is true and followByName is false, uses "tail -f" which
// follows by file descriptor. This will stop delivering new lines after
// rotation since it tracks the old (renamed) file descriptor.
func buildTailCommand(logPath string, tail int, follow bool, followByName bool) string {
	if follow {
		flag := "-F" // follow by name with retry (handles log rotation)
		if !followByName {
			flag = "-f" // follow by file descriptor (no rotation awareness)
		}
		return fmt.Sprintf("tail %s -n %d %s", flag, tail, shellQuote(logPath))
	}
	return fmt.Sprintf("tail -n %d %s", tail, shellQuote(logPath))
}

// shellQuote wraps a string in single quotes for safe shell usage,
// escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
