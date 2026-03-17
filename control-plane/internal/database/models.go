package database

import (
	"encoding/json"
	"time"
)

type Skill struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Slug      string    `gorm:"uniqueIndex;not null" json:"slug"`
	Name      string    `gorm:"not null" json:"name"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

type Instance struct {
	ID               uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name             string    `gorm:"uniqueIndex;not null" json:"name"`
	DisplayName      string    `gorm:"not null" json:"display_name"`
	Status           string    `gorm:"not null;default:creating" json:"status"`
	CPURequest       string    `gorm:"default:500m" json:"cpu_request"`
	CPULimit         string    `gorm:"default:2000m" json:"cpu_limit"`
	MemoryRequest    string    `gorm:"default:1Gi" json:"memory_request"`
	MemoryLimit      string    `gorm:"default:4Gi" json:"memory_limit"`
	StorageHomebrew  string    `gorm:"default:10Gi" json:"storage_homebrew"`
	StorageHome      string    `gorm:"default:10Gi" json:"storage_home"`
	BraveAPIKey      string    `json:"-"`
	ContainerImage   string    `json:"container_image"`
	VNCResolution    string    `json:"vnc_resolution"`
	GatewayToken     string    `json:"-"`
	ModelsConfig     string    `gorm:"type:text;default:'{}'" json:"-"` // JSON: {"disabled":["model"],"extra":["model"]}
	DefaultModel     string    `gorm:"default:''" json:"-"`
	LogPaths         string    `gorm:"type:text;default:''" json:"log_paths"`          // JSON: {"openclaw":"/custom/path.log",...}
	AllowedSourceIPs string    `gorm:"type:text;default:''" json:"allowed_source_ips"` // Comma-separated IPs/CIDRs for SSH connection restrictions
	EnabledProviders string    `gorm:"type:text;default:'[]'" json:"-"`                // JSON array of LLMProvider IDs enabled for this instance
	Timezone         string    `gorm:"default:''" json:"timezone"`
	UserAgent        string    `gorm:"default:''" json:"user_agent"`
	BindMounts       string    `gorm:"type:text;default:'[]'" json:"bind_mounts"` // JSON: [{"host_path":"/x","container_path":"/y","read_only":true}]
	SortOrder        int       `gorm:"not null;default:0" json:"sort_order"`
	CreatedAt        time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt        time.Time `gorm:"autoUpdateTime" json:"updated_at"`

	APIKeys []InstanceAPIKey `gorm:"foreignKey:InstanceID" json:"-"`
}

// ProviderModel represents a model entry in the OpenClaw provider config.
type ProviderModel struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Reasoning     bool               `json:"reasoning,omitempty"`
	Input         []string           `json:"input,omitempty"`
	ContextWindow *int               `json:"contextWindow,omitempty"`
	MaxTokens     *int               `json:"maxTokens,omitempty"`
	Cost          *ProviderModelCost `json:"cost,omitempty"`
}

// ProviderModelCost holds per-token cost information.
type ProviderModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// LLMProvider is admin-defined LLM provider config.
// All providers are accessed via OpenAI-compat base URLs through the internal LLM gateway.
type LLMProvider struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Key       string    `gorm:"uniqueIndex;not null;size:100" json:"key"` // URL-safe key: "anthropic", "anthropic-2"
	Provider  string    `gorm:"size:100" json:"provider"`                 // catalog provider key, empty for custom
	Name      string    `gorm:"not null" json:"name"`                     // display name
	BaseURL   string    `gorm:"not null" json:"base_url"`                 // OpenAI-compat base URL for this provider
	APIType   string    `gorm:"size:100;default:'openai-completions'" json:"api_type"`
	Models    string    `gorm:"type:text;default:'[]'" json:"-"` // JSON []ProviderModel
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// ParseProviderModels deserializes the raw JSON models field.
func ParseProviderModels(raw string) []ProviderModel {
	if raw == "" || raw == "[]" {
		return []ProviderModel{}
	}
	var models []ProviderModel
	json.Unmarshal([]byte(raw), &models)
	if models == nil {
		return []ProviderModel{}
	}
	return models
}

// LLMGatewayKey is a per-instance per-provider auth key issued to OpenClaw instances.
// OpenClaw uses this as the gateway auth token when calling the internal LLM gateway.
type LLMGatewayKey struct {
	ID         uint        `gorm:"primaryKey;autoIncrement"`
	InstanceID uint        `gorm:"not null;uniqueIndex:idx_lgk_inst_prov"`
	ProviderID uint        `gorm:"not null;uniqueIndex:idx_lgk_inst_prov"` // FK → LLMProvider.ID
	GatewayKey string      `gorm:"not null;uniqueIndex"`                   // "claworc-vk-<random>"
	Provider   LLMProvider `gorm:"foreignKey:ProviderID"`
}

// LLMRequestLog records each proxied LLM request for auditing and usage tracking.
type LLMRequestLog struct {
	ID                uint      `gorm:"primaryKey;autoIncrement"`
	InstanceID        uint      `gorm:"not null;index"`
	ProviderID        uint      `gorm:"not null"`
	ModelID           string    `gorm:"not null"`
	InputTokens       int       `gorm:"not null;default:0"`
	OutputTokens      int       `gorm:"not null;default:0"`
	CachedInputTokens int       `gorm:"not null;default:0"`
	CostUSD           float64   `gorm:"not null;default:0"`
	StatusCode        int       `gorm:"not null"`
	LatencyMs         int64     `gorm:"not null"`
	ErrorMessage      string    `gorm:"type:text"`
	RequestedAt       time.Time `gorm:"not null;index"`
}

type InstanceAPIKey struct {
	ID         uint   `gorm:"primaryKey;autoIncrement"`
	InstanceID uint   `gorm:"not null;uniqueIndex:idx_inst_key"`
	KeyName    string `gorm:"not null;uniqueIndex:idx_inst_key"` // e.g. "ANTHROPIC_API_KEY"
	KeyValue   string `json:"-"`                                 // Fernet-encrypted
}

type Setting struct {
	Key       string    `gorm:"primaryKey" json:"key"`
	Value     string    `gorm:"not null" json:"value"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

type User struct {
	ID           uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Username     string    `gorm:"uniqueIndex;not null;size:64" json:"username"`
	PasswordHash string    `gorm:"not null" json:"-"`
	Role         string    `gorm:"not null;default:user" json:"role"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

type UserInstance struct {
	UserID     uint `gorm:"primaryKey" json:"user_id"`
	InstanceID uint `gorm:"primaryKey" json:"instance_id"`
}

type WebAuthnCredential struct {
	ID              string    `gorm:"primaryKey;size:256" json:"id"`
	UserID          uint      `gorm:"not null;index" json:"user_id"`
	Name            string    `json:"name"`
	PublicKey       []byte    `gorm:"not null" json:"-"`
	AttestationType string    `json:"-"`
	Transport       string    `json:"-"`
	SignCount       uint32    `gorm:"default:0" json:"-"`
	AAGUID          []byte    `json:"-"`
	CreatedAt       time.Time `gorm:"autoCreateTime" json:"created_at"`
}
