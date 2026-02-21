// Package sshtunnel creates and manages SSH port-forwarding tunnels between the
// control plane and agent instances.
//
// Tunnels enable the control plane to access services running inside agent pods
// (VNC desktop at port 3000, OpenClaw gateway at port 8080) without requiring
// direct network connectivity. All tunnels multiplex over a single SSH connection
// per instance, managed by [sshmanager.SSHManager].
//
// # Tunnel Architecture
//
// Each tunnel binds an ephemeral local port on 127.0.0.1 and forwards inbound
// TCP connections through the SSH channel to the agent's service port. This is
// analogous to SSH -L (local port forward). Multiple concurrent connections are
// supported — each gets its own SSH channel but all share the same underlying SSH
// session (connection multiplexing).
//
// Standard tunnel setup per instance:
//   - VNC tunnel: local:auto → agent:3000 (Selkies/VNC)
//   - Gateway tunnel: local:auto → agent:8080 (OpenClaw gateway)
//
// # Lifecycle Management
//
// [TunnelManager] manages the full tunnel lifecycle:
//
//  1. Creation: [TunnelManager.StartTunnelsForInstance] creates both VNC and
//     Gateway tunnels and starts a per-instance health monitor.
//
//  2. Health Monitoring: Two layers of health checks run concurrently:
//     - Global check (every 60s): probes all tunnel ports via TCP and closes
//     tunnels that are no longer accepting connections.
//     - Per-instance monitor (every 10s): detects closed tunnels and recreates
//     them with exponential backoff (1s base, 2x factor, 60s max).
//
//  3. Reconnection: When a tunnel fails, the per-instance monitor automatically
//     recreates it. Byte transfer counters and reconnection counts are tracked
//     for observability.
//
//  4. Shutdown: [TunnelManager.Shutdown] stops all monitors, closes all tunnels,
//     and waits for background goroutines to exit cleanly.
//
// # Global Registry
//
// The registry.go file provides a global singleton pattern via [InitGlobal],
// [GetSSHManager], [GetTunnelManager], and [GetSessionManager]. Call [InitGlobal]
// once during application startup; all handlers then retrieve managers via the
// getter functions.
//
// # Performance
//
// On loopback, SSH tunnel overhead is approximately 55µs per HTTP request and
// supports >27,000 req/s with 10 concurrent clients. WebSocket messages add
// approximately 55µs latency per round-trip vs direct connection.
//
// # Usage
//
//	// During application startup
//	sshtunnel.InitGlobal()
//	tunnelMgr := sshtunnel.GetTunnelManager()
//
//	// Start tunnels after SSH connection is established
//	err := tunnelMgr.StartTunnelsForInstance(ctx, "my-instance")
//	if err != nil { ... }
//
//	// Get tunnel info (e.g., to proxy VNC traffic)
//	tunnels := tunnelMgr.GetTunnels("my-instance")
//	for _, t := range tunnels {
//	    fmt.Printf("service=%s local_port=%d\n", t.Config.Service, t.LocalPort)
//	}
//
//	// Check tunnel health
//	err = tunnelMgr.CheckTunnelHealth("my-instance", "vnc")
//
//	// Get metrics for observability
//	metrics := tunnelMgr.GetTunnelMetrics("my-instance")
//
//	// Clean shutdown
//	tunnelMgr.StopTunnelsForInstance("my-instance")
//
// # Log Prefixes
//
// Tunnel operations use the [tunnel] prefix. Health checks use [tunnel-health].
package sshtunnel
