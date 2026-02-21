// Package sshfiles provides SSH-based file operations for remote agent instances.
//
// It implements directory listing, file reading, file writing, and directory
// creation by executing shell commands over SSH exec sessions. All operations
// reuse the persistent multiplexed SSH connection managed by [sshmanager.SSHManager],
// avoiding the per-request overhead of the previous K8s/Docker exec approach.
//
// # Performance
//
// SSH exec reuses a persistent connection per instance, yielding significantly
// lower latency than K8s exec (which creates a new SPDY stream and authenticates
// with the API server each time):
//   - SSH exec: 1-5ms on loopback
//   - K8s exec: 20-100ms
//
// File writes via stdin piping ("cat > path") avoid shell argument length limits
// that required base64 encoding in the K8s exec approach, eliminating ~33% data
// overhead for binary files.
//
// # Operations
//
//   - [ListDirectory]: Runs "ls -la" and parses output into FileEntry structs.
//   - [ReadFile]: Runs "cat" and returns file contents as a byte slice.
//   - [WriteFile]: Pipes data via stdin to "cat > path", avoiding shell escaping.
//   - [CreateDirectory]: Runs "mkdir -p" to create directories recursively.
//
// All path arguments are shell-quoted to prevent injection. The shellQuote
// function escapes embedded single quotes using the standard '\\'' technique.
//
// # Usage
//
//	client, _ := sshManager.GetClient("my-instance")
//
//	// List directory contents
//	entries, err := sshfiles.ListDirectory(client, "/home/user")
//
//	// Read a file
//	data, err := sshfiles.ReadFile(client, "/etc/hostname")
//
//	// Write a file
//	err = sshfiles.WriteFile(client, "/tmp/config.json", jsonBytes)
//
//	// Create a directory
//	err = sshfiles.CreateDirectory(client, "/home/user/workspace")
//
// # Log Prefixes
//
// All operations log timing and result at the [sshfiles] prefix. Handler-level
// timing is logged at the [files] prefix.
package sshfiles
