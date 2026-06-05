package config

import (
	"log"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Settings struct {
	DataPath     string   `envconfig:"DATA_PATH" default:"/app/data"`
	BackupsPath  string   `envconfig:"BACKUPS_PATH" default:""`
	// Database is a URL-style connection string covering driver, credentials,
	// host, and database name. Empty means "use SQLite at DataPath" (default
	// behavior, fully backwards compatible). See docs/databases.md.
	Database     string   `envconfig:"DATABASE" default:""`
	K8sNamespace string   `envconfig:"K8S_NAMESPACE" default:"claworc"`
	DockerHost   string   `envconfig:"DOCKER_HOST" default:""`
	AuthDisabled bool     `envconfig:"AUTH_DISABLED" default:"false"`
	RPOrigins    []string `envconfig:"RP_ORIGINS" default:"http://localhost:8000"`
	RPID         string   `envconfig:"RP_ID" default:"localhost"`

	// AllowedHostMounts is the operator-controlled allowlist of host path
	// prefixes within which shared folders may be backed by a host bind mount.
	// Empty (the default) disables host-backed shared folders entirely.
	AllowedHostMounts []string `envconfig:"ALLOWED_HOST_MOUNTS" default:""`

	// Terminal session settings
	TerminalHistoryLines   int    `envconfig:"TERMINAL_HISTORY_LINES" default:"1000"`
	TerminalRecordingDir   string `envconfig:"TERMINAL_RECORDING_DIR" default:""`
	TerminalSessionTimeout string `envconfig:"TERMINAL_SESSION_TIMEOUT" default:"30m"`

	// LLM gateway settings
	LLMGatewayPort int    `envconfig:"LLM_GATEWAY_PORT" default:"40001"`
	LLMResponseLog string `envconfig:"LLM_RESPONSE_LOG" default:""`

	// WebhookIdleTimeout bounds how long the synchronous webhook bridge waits
	// for the *next* event from OpenClaw before giving up. The deadline resets
	// on every frame received, so an actively-streaming agent is never cut off;
	// only a genuine stall trips it.
	WebhookIdleTimeout time.Duration `envconfig:"WEBHOOK_IDLE_TIMEOUT" default:"120s"`
}

var Cfg Settings

func Load() {
	if err := envconfig.Process("CLAWORC", &Cfg); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
}
