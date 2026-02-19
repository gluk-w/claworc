package config

import (
	"log"

	"github.com/kelseyhightower/envconfig"
)

type Settings struct {
	DatabasePath string   `envconfig:"DATABASE_PATH" default:"/app/data/claworc.db"`
	K8sNamespace string   `envconfig:"K8S_NAMESPACE" default:"claworc"`
	DockerHost   string   `envconfig:"DOCKER_HOST" default:""`
	AuthDisabled bool     `envconfig:"AUTH_DISABLED" default:"false"`
	RPOrigins    []string `envconfig:"RP_ORIGINS" default:"http://localhost:8000"`
	RPID         string   `envconfig:"RP_ID" default:"localhost"`
	ProxyEnabled bool     `envconfig:"PROXY_ENABLED" default:"false"`
	ProxyURL     string   `envconfig:"PROXY_URL" default:"http://llm-proxy:8080"`
	ProxySecret  string   `envconfig:"PROXY_SECRET" default:""`
}

var Cfg Settings

func Load() {
	if err := envconfig.Process("CLAWORC", &Cfg); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
}
