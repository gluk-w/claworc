package config

import (
	"log"
	"os"
	"strconv"

	"github.com/kelseyhightower/envconfig"
)

type Settings struct {
	DataPath    string `envconfig:"DATA_PATH" default:"/app/data"`
	BackupsPath string `envconfig:"BACKUPS_PATH" default:""`
	// Port is the HTTP listen port for the control plane server.
	Port int `envconfig:"PORT" default:"8000"`
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

	// Internal proxy settings
	InternalProxyPort int    `envconfig:"INTERNAL_PROXY_PORT" default:"40001"`
	LLMResponseLog    string `envconfig:"LLM_RESPONSE_LOG" default:""`
}

var Cfg Settings

func Load() {
	if err := envconfig.Process("CLAWORC", &Cfg); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	applyLegacyEnvFallbacks()
}

// applyLegacyEnvFallbacks honors deprecated env var names when their modern
// replacement is unset, so existing deployments keep working after a rename.
func applyLegacyEnvFallbacks() {
	// CLAWORC_LLM_GATEWAY_PORT → CLAWORC_INTERNAL_PROXY_PORT
	if _, ok := os.LookupEnv("CLAWORC_INTERNAL_PROXY_PORT"); !ok {
		if legacy, ok := os.LookupEnv("CLAWORC_LLM_GATEWAY_PORT"); ok && legacy != "" {
			if port, err := strconv.Atoi(legacy); err == nil {
				Cfg.InternalProxyPort = port
				log.Printf("config: CLAWORC_LLM_GATEWAY_PORT is deprecated; use CLAWORC_INTERNAL_PROXY_PORT")
			} else {
				log.Printf("config: ignoring invalid CLAWORC_LLM_GATEWAY_PORT=%q: %v", legacy, err)
			}
		}
	}
}
