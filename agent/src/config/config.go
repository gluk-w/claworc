package config

import "os"

// Config holds all proxy configuration read from environment variables.
type Config struct {
	ListenAddr      string // PROXY_LISTEN_ADDR — the port the proxy listens on
	GatewayAddr     string // PROXY_GATEWAY_ADDR — internal OpenClaw gateway
	TunnelAddr      string // PROXY_TUNNEL_ADDR — port for the inbound mTLS tunnel from control plane
	TLSCert         string // PROXY_TLS_CERT — agent TLS certificate path
	TLSKey          string // PROXY_TLS_KEY — agent TLS key path
	ControlPlaneCA  string // PROXY_CONTROL_PLANE_CA — control-plane CA cert (for mTLS verification)
}

// Load reads configuration from environment variables with sensible defaults.
func Load() Config {
	return Config{
		ListenAddr:     envOrDefault("PROXY_LISTEN_ADDR", ":3000"),
		GatewayAddr:    envOrDefault("PROXY_GATEWAY_ADDR", "127.0.0.1:18789"),
		TunnelAddr:     envOrDefault("PROXY_TUNNEL_ADDR", ":3001"),
		TLSCert:        envOrDefault("PROXY_TLS_CERT", "/config/ssl/agent.crt"),
		TLSKey:         envOrDefault("PROXY_TLS_KEY", "/config/ssl/agent.key"),
		ControlPlaneCA: envOrDefault("PROXY_CONTROL_PLANE_CA", "/config/ssl/cp-ca.crt"),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
