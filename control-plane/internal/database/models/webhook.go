package models

import "time"

// WebhookApiKey is an API token that can be presented in the
// `Authorization: Bearer <token>` header to call an instance's webhook.
//
// Each row is per-instance; a single Instance can own many keys, and each
// key independently chooses whether it is public (accepted by the
// control-plane public route) or private (accepted only by the LLM
// gateway's internal /webhooks/ route). When an instance has zero keys
// its webhook URLs are inaccessible (handlers return 404).
//
// Key is the raw token, encrypted at rest with the existing Fernet helpers
// in utils/crypto.go (same pattern as Instance.GatewayToken and
// LLMProvider.APIKey). The admin UI may decrypt to display the value;
// auth at the request edge decrypts each candidate row and compares with
// constant-time equality.
type WebhookApiKey struct {
	ID         uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	InstanceID uint       `gorm:"not null;index" json:"instance_id"`
	Key        string     `gorm:"type:text;not null" json:"-"` // Fernet-encrypted
	Label      string     `gorm:"default:''" json:"label"`
	IsPrivate  bool       `gorm:"not null;default:false" json:"is_private"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `gorm:"autoCreateTime" json:"created_at"`
}

// WebhookLog records a single webhook invocation. It captures everything
// the audit/logs tab in instance settings displays: which endpoint
// received the call, who called it, which session id the message was
// routed to, basic counters, and any error.
//
// KeyLast4 is denormalized so log rows remain readable after a key has
// been deleted; KeyLast4 is the last 4 characters of the raw token that
// successfully authenticated the call, or empty when no key matched
// (e.g. a 401 was returned).
type WebhookLog struct {
	ID            uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	InstanceID    uint      `gorm:"not null;index" json:"instance_id"`
	SourceIP      string    `gorm:"default:''" json:"source_ip"`
	SessionName   string    `gorm:"column:session_name;default:'';index" json:"session_name"`
	RequestBytes  int       `gorm:"default:0" json:"request_bytes"`
	ResponseBytes int       `gorm:"default:0" json:"response_bytes"`
	StatusCode    int       `gorm:"default:0" json:"status_code"`
	DurationMs    int       `gorm:"default:0" json:"duration_ms"`
	ErrorMessage  string    `gorm:"type:text;default:''" json:"error_message,omitempty"`
	KeyLast4      string    `gorm:"default:''" json:"key_last4"`
	IsPrivate     bool      `gorm:"not null;default:false" json:"is_private"`
	CreatedAt     time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}
