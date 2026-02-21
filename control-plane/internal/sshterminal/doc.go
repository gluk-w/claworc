// Package sshterminal provides SSH-based interactive terminal sessions with
// PTY support, session management, scrollback persistence, and recording.
//
// It wraps golang.org/x/crypto/ssh to provide PTY-enabled shell sessions with
// bidirectional I/O and dynamic terminal resizing. Sessions are created over an
// existing SSH client connection (typically obtained from [sshmanager.SSHManager]),
// enabling connection multiplexing across all SSH-based features.
//
// # Core Components
//
//   - [TerminalSession]: Low-level PTY session with stdin/stdout and resize support.
//   - [SessionManager]: High-level multi-session manager with lifecycle tracking.
//   - [ManagedSession]: A TerminalSession wrapped with metadata, scrollback, and state.
//   - [ScrollbackBuffer]: Thread-safe circular buffer storing terminal output for replay.
//   - [SessionRecording]: Optional timestamped I/O capture in asciinema-compatible format.
//   - [RateLimiter]: Token bucket rate limiter for WebSocket message throttling.
//
// # Session Lifecycle
//
//  1. Created via [SessionManager.CreateSession] → state=[SessionActive].
//     A background goroutine starts relaying SSH stdout to the scrollback buffer.
//
//  2. WebSocket disconnects → state=[SessionDetached]. The SSH session stays alive
//     and continues buffering output in the [ScrollbackBuffer].
//
//  3. WebSocket reconnects → state=[SessionActive]. The scrollback buffer is
//     replayed to the new connection so the user sees output produced while away.
//
//  4. SSH session ends or explicit close → state=[SessionClosed]. Resources are
//     released and the session is eligible for cleanup.
//
// # Security
//
//   - Shell whitelist: Only shells in [AllowedShells] (/bin/bash, /bin/sh, /bin/zsh)
//     may be started. [ValidateShell] enforces this.
//   - Input size limit: [MaxInputMessageSize] (64 KB) prevents oversized messages.
//   - Terminal dimensions: Capped at [MaxTermCols] (500) x [MaxTermRows] (200).
//   - Message rate limiting: [MessageRateLimit] (100/s) with [MessageRateBurst] (200)
//     burst prevents client-side abuse.
//
// # Idle Session Cleanup
//
// [SessionManager.CleanupIdle] removes detached sessions that have been idle longer
// than [DefaultIdleTimeout] (30 minutes). This should be called periodically.
//
// # Usage
//
//	// Low-level: create a terminal session directly
//	session, err := sshterminal.CreateInteractiveSession(sshClient, "/bin/bash")
//	if err != nil { ... }
//	defer session.Close()
//	session.Stdin.Write([]byte("ls -la\n"))
//	buf := make([]byte, 4096)
//	n, _ := session.Stdout.Read(buf)
//	session.Resize(120, 40)
//
//	// High-level: use SessionManager for managed sessions
//	mgr := sshterminal.NewSessionManager()
//	ms, err := mgr.CreateSession(ctx, sshClient, instanceID, userID, "")
//	if err != nil { ... }
//
//	// Reconnect: replay scrollback
//	snapshot := ms.Scrollback.Snapshot()
//	// Send snapshot to WebSocket, then continue relaying live output
//
//	// List sessions for an instance
//	sessions := mgr.ListSessions(instanceID, true) // activeOnly=true
//
// # Log Prefixes
//
// Terminal sessions log at the [sshterminal] prefix. Session manager operations
// log at the [session-mgr] prefix.
package sshterminal
