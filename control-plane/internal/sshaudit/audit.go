// Package sshaudit provides SSH access audit logging backed by a SQLite database table.
//
// All SSH-related events (connections, disconnections, command execution, file operations,
// terminal sessions, key uploads, key rotations) are recorded as AuditEntry rows. A
// configurable retention policy (default 90 days) automatically purges old entries via
// a background goroutine.
//
// The Auditor is safe for concurrent use and can be registered as an EventListener
// on the SSHManager to automatically capture connection lifecycle events.
package sshaudit

import (
	"context"
	"log"
	"sync"
	"time"

	"gorm.io/gorm"
)

// EventType classifies the kind of SSH audit event.
type EventType string

const (
	EventConnection      EventType = "connection"
	EventDisconnection   EventType = "disconnection"
	EventCommandExec     EventType = "command_exec"
	EventFileOperation   EventType = "file_operation"
	EventTerminalSession EventType = "terminal_session"
	EventKeyUpload       EventType = "key_upload"
	EventKeyRotation     EventType = "key_rotation"
)

// AuditEntry is the GORM model for the ssh_audit_logs table.
type AuditEntry struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	EventType  string    `gorm:"not null;index" json:"event_type"`
	InstanceID uint      `gorm:"index" json:"instance_id"`
	User       string    `json:"user"`
	Details    string    `gorm:"type:text" json:"details"`
	CreatedAt  time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}

// TableName overrides the GORM table name.
func (AuditEntry) TableName() string {
	return "ssh_audit_logs"
}

// Auditor manages SSH audit logging.
type Auditor struct {
	db             *gorm.DB
	retentionDays  int
	mu             sync.RWMutex
	cleanupCancel  context.CancelFunc
}

// NewAuditor creates an Auditor and auto-migrates the audit table.
// retentionDays controls how long entries are kept (0 = no automatic cleanup).
func NewAuditor(db *gorm.DB, retentionDays int) (*Auditor, error) {
	if err := db.AutoMigrate(&AuditEntry{}); err != nil {
		return nil, err
	}
	return &Auditor{
		db:            db,
		retentionDays: retentionDays,
	}, nil
}

// SetRetentionDays updates the retention policy at runtime.
func (a *Auditor) SetRetentionDays(days int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.retentionDays = days
}

// RetentionDays returns the current retention policy in days.
func (a *Auditor) RetentionDays() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.retentionDays
}

// Log records an audit event.
func (a *Auditor) Log(eventType EventType, instanceID uint, user, details string) {
	entry := AuditEntry{
		EventType:  string(eventType),
		InstanceID: instanceID,
		User:       user,
		Details:    details,
	}
	if err := a.db.Create(&entry).Error; err != nil {
		log.Printf("sshaudit: failed to log event: %v", err)
	}
}

// LogConnection logs an SSH connection establishment.
func (a *Auditor) LogConnection(instanceID uint, user, details string) {
	a.Log(EventConnection, instanceID, user, details)
}

// LogDisconnection logs an SSH connection termination.
func (a *Auditor) LogDisconnection(instanceID uint, user, details string) {
	a.Log(EventDisconnection, instanceID, user, details)
}

// LogCommandExec logs a command executed over SSH.
func (a *Auditor) LogCommandExec(instanceID uint, user, details string) {
	a.Log(EventCommandExec, instanceID, user, details)
}

// LogFileOperation logs a file operation performed via SSH.
func (a *Auditor) LogFileOperation(instanceID uint, user, details string) {
	a.Log(EventFileOperation, instanceID, user, details)
}

// LogTerminalSession logs a terminal session start or end.
func (a *Auditor) LogTerminalSession(instanceID uint, user, details string) {
	a.Log(EventTerminalSession, instanceID, user, details)
}

// LogKeyUpload logs a public key upload to an instance.
func (a *Auditor) LogKeyUpload(instanceID uint, details string) {
	a.Log(EventKeyUpload, instanceID, "system", details)
}

// LogKeyRotation logs a key rotation event.
func (a *Auditor) LogKeyRotation(details string) {
	a.Log(EventKeyRotation, 0, "system", details)
}

// QueryOptions controls filtering and pagination for audit log queries.
type QueryOptions struct {
	InstanceID *uint
	EventType  *EventType
	Limit      int
	Offset     int
}

// Query returns audit entries matching the given options, newest first.
func (a *Auditor) Query(opts QueryOptions) ([]AuditEntry, int64, error) {
	q := a.db.Model(&AuditEntry{})
	if opts.InstanceID != nil {
		q = q.Where("instance_id = ?", *opts.InstanceID)
	}
	if opts.EventType != nil {
		q = q.Where("event_type = ?", string(*opts.EventType))
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	var entries []AuditEntry
	err := q.Order("created_at DESC").Limit(limit).Offset(opts.Offset).Find(&entries).Error
	return entries, total, err
}

// PurgeOlderThan deletes audit entries older than the given duration.
// Returns the number of entries deleted.
func (a *Auditor) PurgeOlderThan(d time.Duration) (int64, error) {
	cutoff := time.Now().Add(-d)
	result := a.db.Where("created_at < ?", cutoff).Delete(&AuditEntry{})
	return result.RowsAffected, result.Error
}

// StartRetentionCleanup starts a background goroutine that purges old entries daily.
// Call the returned cancel function to stop the cleanup goroutine.
func (a *Auditor) StartRetentionCleanup(ctx context.Context) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.cleanupCancel = cancel
	a.mu.Unlock()

	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				days := a.RetentionDays()
				if days <= 0 {
					continue
				}
				deleted, err := a.PurgeOlderThan(time.Duration(days) * 24 * time.Hour)
				if err != nil {
					log.Printf("sshaudit: retention cleanup error: %v", err)
				} else if deleted > 0 {
					log.Printf("sshaudit: purged %d audit entries older than %d days", deleted, days)
				}
			}
		}
	}()

	return cancel
}
