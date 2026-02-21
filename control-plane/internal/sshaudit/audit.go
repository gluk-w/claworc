package sshaudit

import (
	"log"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/logutil"
	"gorm.io/gorm"
)

// Event types for SSH audit logging.
const (
	EventConnectionEstablished = "connection_established"
	EventConnectionTerminated  = "connection_terminated"
	EventCommandExecution      = "command_execution"
	EventFileOperation         = "file_operation"
	EventTerminalSessionStart  = "terminal_session_start"
	EventTerminalSessionEnd    = "terminal_session_end"
	EventKeyRotation           = "key_rotation"
	EventConnectionFailed      = "connection_failed"
	EventFingerprintMismatch   = "fingerprint_mismatch"
)

// DefaultRetentionDays is the default number of days to keep audit logs.
const DefaultRetentionDays = 90

// AuditEntry contains the fields needed to create an audit log entry.
type AuditEntry struct {
	InstanceID   uint
	InstanceName string
	EventType    string
	Username     string
	SourceIP     string
	Details      string
	DurationMs   int64
}

// Auditor provides methods for recording and querying SSH audit logs.
// It writes records to the database and also emits log lines for
// observability.
type Auditor struct {
	mu            sync.RWMutex
	db            *gorm.DB
	retentionDays int
	nowFn         func() time.Time // injectable clock for testing
}

// NewAuditor creates a new Auditor that writes to the given database.
// If retentionDays is 0, DefaultRetentionDays is used.
func NewAuditor(db *gorm.DB, retentionDays int) *Auditor {
	if retentionDays <= 0 {
		retentionDays = DefaultRetentionDays
	}
	return &Auditor{
		db:            db,
		retentionDays: retentionDays,
		nowFn:         time.Now,
	}
}

// Log records an audit event to the database and standard logger.
func (a *Auditor) Log(entry AuditEntry) error {
	record := database.SSHAuditLog{
		InstanceID:   entry.InstanceID,
		InstanceName: entry.InstanceName,
		EventType:    entry.EventType,
		Username:     entry.Username,
		SourceIP:     entry.SourceIP,
		Details:      entry.Details,
		Duration:     entry.DurationMs,
	}

	if err := a.db.Create(&record).Error; err != nil {
		log.Printf("[ssh-audit] failed to write audit log: %v", err)
		return err
	}

	log.Printf("[ssh-audit] %s instance=%s user=%s ip=%s details=%s",
		entry.EventType,
		logutil.SanitizeForLog(entry.InstanceName),
		entry.Username,
		entry.SourceIP,
		entry.Details,
	)
	return nil
}

// QueryOptions specifies filters for retrieving audit logs.
type QueryOptions struct {
	InstanceID   uint
	InstanceName string
	EventType    string
	Username     string
	Since        *time.Time
	Until        *time.Time
	Limit        int
	Offset       int
}

// QueryResult contains audit log entries and pagination metadata.
type QueryResult struct {
	Entries []database.SSHAuditLog `json:"entries"`
	Total   int64                  `json:"total"`
	Limit   int                    `json:"limit"`
	Offset  int                    `json:"offset"`
}

// Query retrieves audit log entries matching the given options.
func (a *Auditor) Query(opts QueryOptions) (*QueryResult, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	tx := a.db.Model(&database.SSHAuditLog{})

	if opts.InstanceID > 0 {
		tx = tx.Where("instance_id = ?", opts.InstanceID)
	}
	if opts.InstanceName != "" {
		tx = tx.Where("instance_name = ?", opts.InstanceName)
	}
	if opts.EventType != "" {
		tx = tx.Where("event_type = ?", opts.EventType)
	}
	if opts.Username != "" {
		tx = tx.Where("username = ?", opts.Username)
	}
	if opts.Since != nil {
		tx = tx.Where("created_at >= ?", *opts.Since)
	}
	if opts.Until != nil {
		tx = tx.Where("created_at <= ?", *opts.Until)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, err
	}

	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	if opts.Limit > 1000 {
		opts.Limit = 1000
	}

	var entries []database.SSHAuditLog
	if err := tx.Order("created_at DESC").Offset(opts.Offset).Limit(opts.Limit).Find(&entries).Error; err != nil {
		return nil, err
	}

	return &QueryResult{
		Entries: entries,
		Total:   total,
		Limit:   opts.Limit,
		Offset:  opts.Offset,
	}, nil
}

// PurgeOlderThan removes audit log entries older than the configured retention period.
// Returns the number of records deleted.
func (a *Auditor) PurgeOlderThan(days int) (int64, error) {
	if days <= 0 {
		days = a.retentionDays
	}
	cutoff := a.nowFn().AddDate(0, 0, -days)
	result := a.db.Where("created_at < ?", cutoff).Delete(&database.SSHAuditLog{})
	if result.Error != nil {
		log.Printf("[ssh-audit] purge failed: %v", result.Error)
		return 0, result.Error
	}
	if result.RowsAffected > 0 {
		log.Printf("[ssh-audit] purged %d audit log entries older than %d days", result.RowsAffected, days)
	}
	return result.RowsAffected, nil
}

// RetentionDays returns the configured retention period.
func (a *Auditor) RetentionDays() int {
	return a.retentionDays
}

// SetNowFunc sets the clock function used for testing.
func (a *Auditor) SetNowFunc(fn func() time.Time) {
	a.nowFn = fn
}
