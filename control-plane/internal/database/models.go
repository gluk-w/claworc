package database

import "time"

type Instance struct {
	ID              uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name            string    `gorm:"uniqueIndex;not null" json:"name"`
	DisplayName     string    `gorm:"not null" json:"display_name"`
	PortChrome      int       `gorm:"not null;default:0" json:"port_chrome"`
	PortTerminal    int       `gorm:"not null;default:0" json:"port_terminal"`
	Status          string    `gorm:"not null;default:creating" json:"status"`
	CPURequest      string    `gorm:"default:500m" json:"cpu_request"`
	CPULimit        string    `gorm:"default:2000m" json:"cpu_limit"`
	MemoryRequest   string    `gorm:"default:1Gi" json:"memory_request"`
	MemoryLimit     string    `gorm:"default:4Gi" json:"memory_limit"`
	StorageHomebrew string    `gorm:"default:10Gi" json:"storage_homebrew"`
	StorageClawd    string    `gorm:"default:5Gi" json:"storage_clawd"`
	StorageChrome   string    `gorm:"default:5Gi" json:"storage_chrome"`
	AnthropicAPIKey string    `json:"-"` // deprecated: migrated to InstanceAPIKey
	OpenAIAPIKey    string    `json:"-"` // deprecated: migrated to InstanceAPIKey
	BraveAPIKey     string    `json:"-"`
	ContainerImage  string    `json:"container_image"`
	VNCResolution   string    `json:"vnc_resolution"`
	ClawdbotConfig  string    `json:"clawdbot_config"`
	GatewayToken    string    `json:"-"`
	ModelsConfig    string    `gorm:"type:text;default:'{}'" json:"-"` // JSON: {"disabled":["model"],"extra":["model"]}
	DefaultModel    string    `gorm:"default:''" json:"-"`
	SortOrder       int       `gorm:"not null;default:0" json:"sort_order"`
	CreatedAt       time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime" json:"updated_at"`

	APIKeys []InstanceAPIKey `gorm:"foreignKey:InstanceID" json:"-"`
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
