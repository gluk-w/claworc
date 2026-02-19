package config

import (
	"log"

	"github.com/kelseyhightower/envconfig"
)

type Settings struct {
	DatabasePath   string `envconfig:"DATABASE_PATH" default:"/app/data/llm-proxy.db"`
	AdminSecret    string `envconfig:"ADMIN_SECRET" default:""`
	ListenAddr     string `envconfig:"LISTEN_ADDR" default:":8080"`
	OllamaURL      string `envconfig:"OLLAMA_URL" default:""`
	LlamaCppURL    string `envconfig:"LLAMACPP_URL" default:""`
}

var Cfg Settings

func Load() {
	if err := envconfig.Process("LLM_PROXY", &Cfg); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
}
