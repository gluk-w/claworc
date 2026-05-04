package config

import (
	"log"

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

	// Terminal session settings
	TerminalHistoryLines   int    `envconfig:"TERMINAL_HISTORY_LINES" default:"1000"`
	TerminalRecordingDir   string `envconfig:"TERMINAL_RECORDING_DIR" default:""`
	TerminalSessionTimeout string `envconfig:"TERMINAL_SESSION_TIMEOUT" default:"30m"`

	// LLM gateway settings
	LLMGatewayPort int    `envconfig:"LLM_GATEWAY_PORT" default:"40001"`
	LLMResponseLog string `envconfig:"LLM_RESPONSE_LOG" default:""`
}

var Cfg Settings

func Load() {
	if err := envconfig.Process("CLAWORC", &Cfg); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
}
