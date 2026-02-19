package database

import "time"

type InstanceToken struct {
	ID           uint      `gorm:"primaryKey;autoIncrement"`
	InstanceName string    `gorm:"uniqueIndex;not null"`
	Token        string    `gorm:"uniqueIndex;not null"`
	Enabled      bool      `gorm:"not null;default:true"`
	CreatedAt    time.Time `gorm:"autoCreateTime"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime"`
}

type ProviderKey struct {
	ID           uint      `gorm:"primaryKey;autoIncrement"`
	ProviderName string    `gorm:"not null;uniqueIndex:idx_provider_scope"`
	Scope        string    `gorm:"not null;uniqueIndex:idx_provider_scope"` // "global" or instance name
	KeyValue     string    `gorm:"not null"`
	CreatedAt    time.Time `gorm:"autoCreateTime"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime"`
}

type UsageRecord struct {
	ID                 uint      `gorm:"primaryKey;autoIncrement"`
	InstanceName       string    `gorm:"not null;index"`
	Provider           string    `gorm:"not null;index"`
	Model              string    `gorm:"not null"`
	InputTokens        int64     `gorm:"not null;default:0"`
	OutputTokens       int64     `gorm:"not null;default:0"`
	EstimatedCostMicro int64     `gorm:"not null;default:0"` // microdollars
	StatusCode         int       `gorm:"not null;default:0"`
	DurationMs         int64     `gorm:"not null;default:0"`
	CreatedAt          time.Time `gorm:"autoCreateTime;index"`
}

type BudgetLimit struct {
	ID             uint      `gorm:"primaryKey;autoIncrement"`
	InstanceName   string    `gorm:"uniqueIndex;not null"`
	LimitMicro     int64     `gorm:"not null;default:0"` // microdollars
	PeriodType     string    `gorm:"not null;default:monthly"` // "daily" or "monthly"
	AlertThreshold float64   `gorm:"not null;default:0.8"`
	HardLimit      bool      `gorm:"not null;default:false"`
	CreatedAt      time.Time `gorm:"autoCreateTime"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`
}

type RateLimit struct {
	ID                uint      `gorm:"primaryKey;autoIncrement"`
	InstanceName      string    `gorm:"not null;uniqueIndex:idx_instance_provider"`
	Provider          string    `gorm:"not null;uniqueIndex:idx_instance_provider"` // "*" = all
	RequestsPerMinute int       `gorm:"not null;default:0"`
	TokensPerMinute   int       `gorm:"not null;default:0"`
	CreatedAt         time.Time `gorm:"autoCreateTime"`
	UpdatedAt         time.Time `gorm:"autoUpdateTime"`
}

type ModelPricing struct {
	ID              uint   `gorm:"primaryKey;autoIncrement"`
	Provider        string `gorm:"not null"`
	ModelPattern    string `gorm:"not null"`
	InputPriceMicro int64  `gorm:"not null;default:0"`  // microdollars per 1M tokens
	OutputPriceMicro int64 `gorm:"not null;default:0"` // microdollars per 1M tokens
}
