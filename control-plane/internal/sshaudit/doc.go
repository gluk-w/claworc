// Package sshaudit provides audit logging for all SSH-related operations.
//
// It records security-relevant events to the database and standard logger,
// enabling compliance monitoring, incident investigation, and operational
// visibility into SSH activity.
//
// # Event Types
//
// The following events are tracked:
//   - [EventConnectionEstablished]: SSH connection successfully established.
//   - [EventConnectionTerminated]: SSH connection closed (includes duration).
//   - [EventConnectionFailed]: SSH connection attempt failed.
//   - [EventCommandExecution]: Command executed via SSH exec.
//   - [EventFileOperation]: File read, write, or directory operation via SSH.
//   - [EventTerminalSessionStart]: Interactive terminal session opened.
//   - [EventTerminalSessionEnd]: Interactive terminal session closed.
//   - [EventKeyRotation]: SSH key pair rotated (includes new fingerprint).
//   - [EventFingerprintMismatch]: Key fingerprint does not match expected value.
//   - [EventIPRestricted]: Connection blocked by IP restriction.
//
// # Architecture
//
// [Auditor] is the core type that writes audit records. It wraps a GORM database
// connection and writes to the ssh_audit_logs table. Each entry includes instance
// ID/name, event type, username, source IP, free-form details, and duration.
//
// The package uses a global singleton pattern: [InitGlobal] creates the Auditor
// during application startup, and [GetAuditor] returns it for use throughout
// the codebase.
//
// # Helper Functions
//
// Convenience functions in helpers.go ([LogConnection], [LogDisconnection],
// [LogCommand], [LogFileOperation], etc.) provide type-safe event logging.
// They automatically check for a nil global auditor, making them safe to call
// even before initialization (the events are silently dropped).
//
// # Retention and Purging
//
// Audit logs are retained for [DefaultRetentionDays] (90 days) by default.
// [Auditor.PurgeOlderThan] removes entries beyond the retention period. This
// should be called periodically (e.g., via a cron-like scheduler) to prevent
// unbounded database growth.
//
// # Querying
//
// [Auditor.Query] retrieves audit entries with flexible filtering by instance,
// event type, username, and time range. Results include pagination metadata.
//
// # Usage
//
//	// During application startup
//	sshaudit.InitGlobal(db, 90) // 90-day retention
//
//	// Log events using helpers (safe even if auditor not initialized)
//	sshaudit.LogConnection(instanceID, "my-instance", "root", "10.0.0.1")
//	sshaudit.LogCommand(instanceID, "my-instance", "root", "ls -la", "success")
//	sshaudit.LogKeyRotation(instanceID, "my-instance", "admin", "SHA256:abc...")
//
//	// Query audit logs
//	result, err := sshaudit.GetAuditor().Query(sshaudit.QueryOptions{
//	    InstanceName: "my-instance",
//	    EventType:    sshaudit.EventConnectionEstablished,
//	    Limit:        50,
//	})
//
//	// Purge old entries
//	deleted, err := sshaudit.GetAuditor().PurgeOlderThan(0) // uses default retention
//
// # Log Prefixes
//
// Audit log messages use the [ssh-audit] prefix for easy filtering.
package sshaudit
