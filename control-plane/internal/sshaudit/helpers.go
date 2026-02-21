package sshaudit

import (
	"net/http"
	"strings"
)

// LogConnection logs an SSH connection establishment event.
func LogConnection(instanceID uint, instanceName, username, sourceIP string) {
	if a := GetAuditor(); a != nil {
		a.Log(AuditEntry{
			InstanceID:   instanceID,
			InstanceName: instanceName,
			EventType:    EventConnectionEstablished,
			Username:     username,
			SourceIP:     sourceIP,
		})
	}
}

// LogDisconnection logs an SSH connection termination event.
func LogDisconnection(instanceID uint, instanceName, username, reason string, durationMs int64) {
	if a := GetAuditor(); a != nil {
		a.Log(AuditEntry{
			InstanceID:   instanceID,
			InstanceName: instanceName,
			EventType:    EventConnectionTerminated,
			Username:     username,
			Details:      reason,
			DurationMs:   durationMs,
		})
	}
}

// LogConnectionFailed logs a failed SSH connection attempt.
func LogConnectionFailed(instanceID uint, instanceName, username, reason string) {
	if a := GetAuditor(); a != nil {
		a.Log(AuditEntry{
			InstanceID:   instanceID,
			InstanceName: instanceName,
			EventType:    EventConnectionFailed,
			Username:     username,
			Details:      reason,
		})
	}
}

// LogCommand logs an SSH command execution event.
func LogCommand(instanceID uint, instanceName, username, command, result string) {
	if a := GetAuditor(); a != nil {
		a.Log(AuditEntry{
			InstanceID:   instanceID,
			InstanceName: instanceName,
			EventType:    EventCommandExecution,
			Username:     username,
			Details:      "cmd=" + command + " result=" + result,
		})
	}
}

// LogFileOperation logs an SSH file operation event.
func LogFileOperation(instanceID uint, instanceName, username, operation, filePath string) {
	if a := GetAuditor(); a != nil {
		a.Log(AuditEntry{
			InstanceID:   instanceID,
			InstanceName: instanceName,
			EventType:    EventFileOperation,
			Username:     username,
			Details:      operation + ": " + filePath,
		})
	}
}

// LogTerminalSessionStart logs the start of a terminal session.
func LogTerminalSessionStart(instanceID uint, instanceName, username, sessionID, sourceIP string) {
	if a := GetAuditor(); a != nil {
		a.Log(AuditEntry{
			InstanceID:   instanceID,
			InstanceName: instanceName,
			EventType:    EventTerminalSessionStart,
			Username:     username,
			SourceIP:     sourceIP,
			Details:      "session_id=" + sessionID,
		})
	}
}

// LogTerminalSessionEnd logs the end of a terminal session.
func LogTerminalSessionEnd(instanceID uint, instanceName, username, sessionID string, durationMs int64) {
	if a := GetAuditor(); a != nil {
		a.Log(AuditEntry{
			InstanceID:   instanceID,
			InstanceName: instanceName,
			EventType:    EventTerminalSessionEnd,
			Username:     username,
			Details:      "session_id=" + sessionID,
			DurationMs:   durationMs,
		})
	}
}

// LogFingerprintMismatch logs a security event when an SSH key fingerprint
// does not match the expected value, which may indicate key tampering.
func LogFingerprintMismatch(instanceID uint, instanceName, username, details string) {
	if a := GetAuditor(); a != nil {
		a.Log(AuditEntry{
			InstanceID:   instanceID,
			InstanceName: instanceName,
			EventType:    EventFingerprintMismatch,
			Username:     username,
			Details:      details,
		})
	}
}

// LogKeyRotation logs an SSH key rotation event.
func LogKeyRotation(instanceID uint, instanceName, username, fingerprint string) {
	if a := GetAuditor(); a != nil {
		a.Log(AuditEntry{
			InstanceID:   instanceID,
			InstanceName: instanceName,
			EventType:    EventKeyRotation,
			Username:     username,
			Details:      "new_fingerprint=" + fingerprint,
		})
	}
}

// ExtractSourceIP extracts the client IP from an HTTP request,
// preferring X-Forwarded-For and X-Real-IP headers.
func ExtractSourceIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-Ip"); xri != "" {
		return xri
	}
	// Fall back to remote address (strip port)
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}
