// Package sshmanager manages persistent SSH connections to agent instances.
//
// It provides a centralized connection pool ([SSHManager]) that handles the full
// lifecycle of SSH connections: establishment, health monitoring, automatic
// reconnection with exponential backoff, rate limiting, and IP-based access
// control.
//
// # Architecture
//
// SSHManager maintains a map of SSH clients keyed by instance name. Each
// connection is associated with:
//   - [ConnectionParams]: host, port, and private key path for reconnection.
//   - [ConnectionMetrics]: health check statistics and uptime tracking.
//   - [ConnectionState]: current lifecycle state (connecting, connected,
//     reconnecting, disconnected, failed).
//   - [ConnectionEvent] history: a ring buffer of the last 100 state-change
//     events per instance, for debugging and UI display.
//
// # Connection Lifecycle
//
//  1. Connect: [SSHManager.Connect] establishes a new SSH connection using
//     ED25519 key authentication. The connection is stored in the pool with
//     its parameters (enabling reconnection) and initial metrics.
//
//  2. Health Monitoring: A background keepalive loop runs every 30 seconds.
//     It first sends an SSH keepalive request, then runs a command-based
//     health check ("echo ping"). Failed checks trigger automatic reconnection.
//
//  3. Reconnection: When a connection fails, [SSHManager] starts a background
//     goroutine that reconnects with exponential backoff (1s base, 2x factor,
//     16s max, up to 10 attempts). Only one reconnection attempt runs per
//     instance at a time.
//
//  4. Disconnection: [SSHManager.Close] cleanly terminates a single connection.
//     [SSHManager.CloseAll] stops the keepalive loop and closes everything.
//
// # Rate Limiting
//
// The [RateLimiter] protects against connection storms by enforcing two limits:
//   - Per-minute limit: max 10 connection attempts per instance per minute.
//   - Consecutive failure limit: after 5 consecutive failures, the instance is
//     blocked for 5 minutes.
//
// Rate limit status is queryable via [SSHManager.GetRateLimitStatus] and
// resettable via [SSHManager.ResetRateLimit].
//
// # IP Restriction
//
// [ParseAllowedIPs] and [CheckIPAllowed] enforce IP-based access control for
// SSH connections. The allow list supports individual IPs and CIDR ranges.
// An empty allow list permits all connections.
//
// # State Tracking
//
// [ConnectionStateTracker] maintains the current state of each connection and
// a history of state transitions (last 50 per instance). Callbacks can be
// registered via [SSHManager.OnConnectionStateChange] to react to state
// changes (e.g., updating the UI).
//
// Valid states: disconnected → connecting → connected → reconnecting → failed.
//
// # Usage
//
//	mgr := sshmanager.NewSSHManager(0) // 0 = unlimited connections
//	defer mgr.CloseAll()
//
//	// Connect to an instance
//	client, err := mgr.Connect(ctx, "my-instance", "10.0.0.5", 22, "/app/data/ssh-keys/my-instance.key")
//	if err != nil { ... }
//
//	// Check connection state
//	state := mgr.GetConnectionState("my-instance") // "connected"
//
//	// Get health metrics
//	metrics := mgr.GetMetrics("my-instance")
//	fmt.Printf("uptime: %s, healthy: %v\n", metrics.Uptime(), metrics.Healthy)
//
//	// Register state change callback
//	mgr.OnConnectionStateChange(func(name string, from, to sshmanager.ConnectionState) {
//	    log.Printf("instance %s: %s → %s", name, from, to)
//	})
//
// # Log Prefixes
//
// All log output uses the [ssh] prefix for connection-level events, enabling
// easy filtering with grep or structured log parsers.
package sshmanager
