package database

import (
	"encoding/json"
	"strings"
	"time"
)

// IsLegacyEmbedded reports whether the given container image refers to the
// legacy combined agent+browser image. Legacy instances run Chromium and VNC
// inside the same container as OpenClaw and use the agent's reverse VNC tunnel
// for desktop streaming. Anything else (typically glukw/claworc-agent:latest)
// is treated as the on-demand browser-pod layout.
//
// Empty string is treated as legacy: pre-upgrade instances often store ""
// and rely on default_container_image. Treating "" as legacy preserves their
// behavior across the upgrade; new instances created via the API are
// populated with the explicit default_agent_image string at creation time.
func IsLegacyEmbedded(containerImage string) bool {
	if containerImage == "" {
		return true
	}
	return strings.Contains(containerImage, "openclaw-vnc-")
}

type Skill struct {
	ID              uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Slug            string    `gorm:"uniqueIndex;not null" json:"slug"`
	Name            string    `gorm:"not null" json:"name"`
	Summary         string    `json:"summary"`
	RequiredEnvVars string    `gorm:"type:text;default:'[]'" json:"-"` // JSON []string of env var names the skill declares it needs
	CreatedAt       time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

type Instance struct {
	ID               uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	Name             string `gorm:"uniqueIndex;not null" json:"name"`
	DisplayName      string `gorm:"not null" json:"display_name"`
	Status           string `gorm:"not null;default:creating" json:"status"`
	CPURequest       string `gorm:"default:500m" json:"cpu_request"`
	CPULimit         string `gorm:"default:2000m" json:"cpu_limit"`
	MemoryRequest    string `gorm:"default:1Gi" json:"memory_request"`
	MemoryLimit      string `gorm:"default:4Gi" json:"memory_limit"`
	StorageHomebrew  string `gorm:"default:10Gi" json:"storage_homebrew"`
	StorageHome      string `gorm:"default:10Gi" json:"storage_home"`
	BraveAPIKey      string `json:"-"`
	ContainerImage   string `json:"container_image"`
	VNCResolution    string `json:"vnc_resolution"`
	GatewayToken     string `json:"-"`
	ModelsConfig     string `gorm:"type:text;default:'{}'" json:"-"` // JSON: {"disabled":["model"],"extra":["model"]}
	DefaultModel     string `gorm:"default:''" json:"-"`
	LogPaths         string `gorm:"type:text;default:''" json:"log_paths"`          // JSON: {"openclaw":"/custom/path.log",...}
	AllowedSourceIPs string `gorm:"type:text;default:''" json:"allowed_source_ips"` // Comma-separated IPs/CIDRs for SSH connection restrictions
	EnabledProviders string `gorm:"type:text;default:'[]'" json:"-"`                // JSON array of LLMProvider IDs enabled for this instance
	Timezone         string `gorm:"default:''" json:"timezone"`
	UserAgent        string `gorm:"default:''" json:"user_agent"`
	EnvVars          string `gorm:"type:text;default:'{}'" json:"-"` // JSON map KEY -> fernet-encrypted value
	SortOrder        int    `gorm:"not null;default:0" json:"sort_order"`
	// On-demand browser-pod fields. Only consulted when ContainerImage does
	// not match IsLegacyEmbedded(). All four are optional and fall back to
	// admin-level defaults from the settings table.
	BrowserProvider    string `gorm:"default:''" json:"browser_provider"` // "kubernetes" | "docker" | future: "cloudflare"
	BrowserImage       string `gorm:"default:''" json:"browser_image"`    // e.g. glukw/claworc-browser-chromium:latest
	BrowserIdleMinutes *int   `json:"browser_idle_minutes,omitempty"`     // overrides default_browser_idle_minutes
	BrowserStorage     string `gorm:"default:''" json:"browser_storage"`  // PVC size, e.g. "10Gi"
	// BrowserActive toggles whether the browser pane is shown next to the chat
	// on this instance's page. Toggling it off also stops the browser pod.
	BrowserActive bool      `gorm:"not null;default:true" json:"browser_active"`
	CreatedAt     time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt     time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// BrowserSession tracks the lifecycle of an instance's on-demand browser pod
// (or, in the future, an external SaaS browser session). One row per Instance;
// the row exists from the moment the instance is created in non-legacy mode
// and persists across pod spawn/reap cycles. Status transitions
// "stopped" → "starting" → "running" → "stopped" (via idle reaper) → ...
type BrowserSession struct {
	InstanceID  uint       `gorm:"primaryKey" json:"instance_id"`
	Provider    string     `gorm:"not null;default:''" json:"provider"`    // "kubernetes" | "docker" | "cloudflare"
	Status      string     `gorm:"not null;default:stopped" json:"status"` // stopped|starting|running|error
	Image       string     `gorm:"default:''" json:"image"`                // for K8s/Docker; empty for SaaS
	PodName     string     `gorm:"default:''" json:"pod_name"`             // K8s deployment / Docker container name
	ProviderRef string     `gorm:"default:''" json:"provider_ref"`         // opaque provider session id
	LastUsedAt  time.Time  `gorm:"autoCreateTime" json:"last_used_at"`
	StartedAt   time.Time  `json:"started_at"`
	StoppedAt   *time.Time `json:"stopped_at,omitempty"`
	ErrorMsg    string     `gorm:"type:text" json:"error_msg,omitempty"`
	UpdatedAt   time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
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

// LLMProvider stores admin-defined LLM provider configuration. Each provider
// represents an upstream LLM service (e.g. Anthropic, OpenAI, a self-hosted
// Ollama instance) accessed via an OpenAI-compatible base URL through the
// internal LLM gateway.
//
// Global providers (InstanceID == nil) are shared across all instances.
// Instance-specific providers (InstanceID != nil) belong to a single instance.
//
// The APIKey field holds the Fernet-encrypted real API key for the upstream
// service. It is tagged json:"-" so it is never serialized into API responses;
// consumers that need a display value should decrypt and mask it explicitly.
type LLMProvider struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Key        string    `gorm:"not null;size:100;uniqueIndex:idx_provider_key_instance" json:"key"` // URL-safe key: "anthropic", "anthropic-2"
	InstanceID *uint     `gorm:"uniqueIndex:idx_provider_key_instance" json:"instance_id,omitempty"` // NULL = global, set = instance-specific
	Provider   string    `gorm:"size:100" json:"provider"`                                           // catalog provider key, empty for custom
	Name       string    `gorm:"not null" json:"name"`                                               // display name
	BaseURL    string    `gorm:"not null" json:"base_url"`                                           // OpenAI-compat base URL for this provider
	APIType    string    `gorm:"size:100;default:'openai-completions'" json:"api_type"`
	APIKey     string    `gorm:"type:text;default:''" json:"-"`   // Fernet-encrypted upstream API key
	Models     string    `gorm:"type:text;default:'[]'" json:"-"` // JSON []ProviderModel
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`
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

type Setting struct {
	Key       string    `gorm:"primaryKey" json:"key"`
	Value     string    `gorm:"not null" json:"value"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

type User struct {
	ID                 uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	Username           string     `gorm:"uniqueIndex;not null;size:64" json:"username"`
	PasswordHash       string     `gorm:"not null" json:"-"`
	Role               string     `gorm:"not null;default:user" json:"role"`
	CanCreateInstances bool       `gorm:"not null;default:false" json:"can_create_instances"`
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"`
	CreatedAt          time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

type UserInstance struct {
	UserID     uint `gorm:"primaryKey" json:"user_id"`
	InstanceID uint `gorm:"primaryKey" json:"instance_id"`
}

type Backup struct {
	ID           uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	InstanceID   uint       `gorm:"not null;index" json:"instance_id"`
	InstanceName string     `gorm:"not null" json:"instance_name"`
	Status       string     `gorm:"not null;default:running" json:"status"`
	FilePath     string     `gorm:"not null" json:"file_path"`
	Paths        string     `gorm:"type:text;default:''" json:"paths"`
	SizeBytes    int64      `json:"size_bytes"`
	ErrorMessage string     `gorm:"type:text" json:"error_message,omitempty"`
	Note         string     `gorm:"type:text" json:"note"`
	CreatedAt    time.Time  `gorm:"autoCreateTime" json:"created_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

type BackupSchedule struct {
	ID             uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	InstanceIDs    string     `gorm:"type:text;not null" json:"instance_ids"`
	CronExpression string     `gorm:"not null" json:"cron_expression"`
	Paths          string     `gorm:"type:text;not null;default:'[\"HOME\"]'" json:"paths"`
	RetentionDays  int        `gorm:"not null;default:0" json:"retention_days"`
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	CreatedAt      time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

// SharedFolder represents a named shared volume that can be mounted into
// multiple instances at the same path. InstanceIDs is a JSON array of
// instance IDs this folder is mapped to.
type SharedFolder struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string    `gorm:"not null" json:"name"`
	MountPath   string    `gorm:"not null" json:"mount_path"`
	OwnerID     uint      `gorm:"not null;index" json:"owner_id"`
	InstanceIDs string    `gorm:"type:text;default:'[]'" json:"-"` // JSON array of uint IDs
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// ParseSharedFolderInstanceIDs deserializes the JSON instance IDs field.
func ParseSharedFolderInstanceIDs(raw string) []uint {
	if raw == "" || raw == "[]" {
		return []uint{}
	}
	var ids []uint
	json.Unmarshal([]byte(raw), &ids)
	if ids == nil {
		return []uint{}
	}
	return ids
}

// EncodeSharedFolderInstanceIDs serializes instance IDs to JSON.
func EncodeSharedFolderInstanceIDs(ids []uint) string {
	if len(ids) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ids)
	return string(b)
}

// KanbanBoard is a global Kanban board grouping tasks dispatched to OpenClaw
// instances. EligibleInstances is a JSON array of Instance IDs that the
// moderator may choose from when routing tasks created on this board.
type KanbanBoard struct {
	ID                uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name              string    `gorm:"not null" json:"name"`
	Description       string    `gorm:"type:text" json:"description"`
	EligibleInstances string    `gorm:"type:text;default:'[]'" json:"-"` // JSON []uint
	CreatedAt         time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt         time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// KanbanTask is one card on a Kanban board. Status moves through
// todo → dispatching → in_progress → done|failed. AssignedInstanceID is set
// by the moderator's dispatch step.
type KanbanTask struct {
	ID                   uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	BoardID              uint      `gorm:"not null;index" json:"board_id"`
	Title                string    `gorm:"not null" json:"title"`
	Description          string    `gorm:"type:text" json:"description"`
	Status               string    `gorm:"not null;default:todo" json:"status"`
	AssignedInstanceID   *uint     `gorm:"index" json:"assigned_instance_id,omitempty"`
	OpenClawSessionID    string    `json:"openclaw_session_id"`
	OpenClawRunID        string    `json:"openclaw_run_id"`
	EvaluatorProviderKey string    `json:"evaluator_provider_key"`
	EvaluatorModel       string    `json:"evaluator_model"`
	CreatedAt            time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt            time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// KanbanComment captures both moderator-authored notes and streamed agent
// output. The "assistant" comment for a run is appended to in place as
// chunks arrive over the gateway WebSocket.
type KanbanComment struct {
	ID                uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	TaskID            uint      `gorm:"not null;index" json:"task_id"`
	Kind              string    `gorm:"not null" json:"kind"` // routing|assistant|tool|moderator|evaluation|error
	Author            string    `json:"author"`
	Body              string    `gorm:"type:text" json:"body"`
	OpenClawSessionID string    `json:"openclaw_session_id"`
	CreatedAt         time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt         time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// KanbanArtifact is a file the agent explicitly mentioned in chat output and
// the moderator pulled from the instance workspace via SSH.
type KanbanArtifact struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	TaskID      uint      `gorm:"not null;index" json:"task_id"`
	Path        string    `gorm:"not null" json:"path"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	StoragePath string    `json:"-"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// InstanceSoul is a cached, periodically refreshed LLM summary of an
// instance's workspace markdown plus a JSON list of its installed skill
// slugs. The moderator uses these for ranking candidates at dispatch time.
type InstanceSoul struct {
	InstanceID uint      `gorm:"primaryKey" json:"instance_id"`
	Summary    string    `gorm:"type:text" json:"summary"`
	Skills     string    `gorm:"type:text;default:'[]'" json:"-"` // JSON []string
	UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`
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
