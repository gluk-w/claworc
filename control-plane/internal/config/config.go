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

	// SSH gateway settings. The gateway lets users `ssh <user>+<instance>@host`
	// and be bridged onto the control plane's existing SSH connection to that
	// instance. SSHGatewayPublicHost is display-only: the hostname shown in the
	// frontend usage snippet (empty = frontend uses window.location.hostname).
	SSHGatewayEnabled    bool   `envconfig:"SSH_GATEWAY_ENABLED" default:"true"`
	SSHGatewayPort       int    `envconfig:"SSH_GATEWAY_PORT" default:"2222"`
	SSHGatewayPublicHost string `envconfig:"SSH_GATEWAY_PUBLIC_HOST" default:""`

	// WebhookIdleTimeout bounds how long the synchronous webhook bridge waits
	// for the *next* event from OpenClaw before giving up. The deadline resets
	// on every frame received, so an actively-streaming agent is never cut off;
	// only a genuine stall trips it.
	WebhookIdleTimeout time.Duration `envconfig:"WEBHOOK_IDLE_TIMEOUT" default:"120s"`

	// Channel health monitor settings. The monitor polls each running
	// instance's OpenClaw gateway (channels.status) and exposes per-channel
	// health via the API. Disabling it removes the background polling and
	// makes the channel health endpoints report "disabled".
	ChannelHealthEnabled  bool          `envconfig:"CHANNEL_HEALTH_ENABLED" default:"true"`
	ChannelHealthInterval time.Duration `envconfig:"CHANNEL_HEALTH_INTERVAL" default:"60s"`

	// Channel health escalation thresholds, counted in consecutive failing
	// checks (overall unhealthy/unreachable). Alerts fire once per incident
	// at the alert threshold; auto-restart (opt-in via the
	// channel_auto_restart_enabled setting) fires at the restart threshold,
	// capped per rolling hour and followed by a cooldown that covers the
	// container rebuild and channel reconnect window.
	ChannelHealthAlertThreshold    int           `envconfig:"CHANNEL_HEALTH_ALERT_THRESHOLD" default:"3"`
	ChannelHealthRestartThreshold  int           `envconfig:"CHANNEL_HEALTH_RESTART_THRESHOLD" default:"5"`
	ChannelHealthRestartMaxPerHour int           `envconfig:"CHANNEL_HEALTH_RESTART_MAX_PER_HOUR" default:"3"`
	ChannelHealthRestartCooldown   time.Duration `envconfig:"CHANNEL_HEALTH_RESTART_COOLDOWN" default:"10m"`
}

var Cfg Settings

func Load() {
	if err := envconfig.Process("CLAWORC", &Cfg); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
}
