// Package sshlogs provides SSH-based log streaming for remote agent instances.
//
// It streams log files from agents by executing tail commands over SSH exec
// sessions. All operations reuse the persistent multiplexed SSH connection
// managed by [sshmanager.SSHManager].
//
// # Performance
//
// SSH exec reuses a persistent connection per instance, avoiding the per-request
// overhead of K8s exec (which creates a new SPDY stream and authenticates with
// the API server each time). Streaming with "tail -F" maintains a single SSH
// session for the duration, providing real-time log delivery with minimal latency.
//
// # Log Types
//
// Three log categories are supported, each mapped to a default file path on
// the agent:
//   - [LogTypeOpenClaw] → /var/log/openclaw.log (OpenClaw gateway stdout/stderr)
//   - [LogTypeBrowser] → /tmp/browser.log (Chromium stdout/stderr)
//   - [LogTypeSystem] → /var/log/sshd.log (SSH daemon stderr)
//
// Custom log paths per instance can be configured via [ResolveLogPath].
//
// # Log Rotation Handling
//
// By default, follow-mode streaming uses "tail -F" (equivalent to
// --follow=name --retry). This follows the log file by name rather than by file
// descriptor, so when a log rotation tool (e.g., logrotate) renames the current
// log file and creates a new one with the same name, tail automatically detects
// the change and switches to the new file. This ensures continuous streaming
// across log rotations without requiring client reconnection.
//
// The alternative "tail -f" (--follow=descriptor) can be selected via
// [StreamOptions].FollowByName=false if needed, but it will stop delivering new
// lines after rotation since it tracks the old (renamed) file descriptor.
//
// # Streaming API
//
// [StreamLogs] returns a channel of log lines. In follow mode, the channel
// stays open and delivers new lines in real time. In non-follow mode, it
// delivers the last N lines and closes.
//
// # Usage
//
//	client, _ := sshManager.GetClient("my-instance")
//
//	// Stream last 100 lines with real-time follow
//	ch, err := sshlogs.StreamLogs(ctx, client, "/var/log/openclaw.log", 100, true)
//	if err != nil { ... }
//	for line := range ch {
//	    fmt.Println(line)
//	}
//
//	// Get snapshot of last 50 lines (no follow)
//	ch, err = sshlogs.StreamLogs(ctx, client, "/var/log/openclaw.log", 50, false)
//
//	// Check which log files exist on the agent
//	available, err := sshlogs.GetAvailableLogFiles(client)
//
// # Log Prefixes
//
// All operations log at the [sshlogs] prefix for monitoring and filtering.
package sshlogs
