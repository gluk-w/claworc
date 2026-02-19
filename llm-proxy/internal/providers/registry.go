package providers

import "strings"

type AuthStyle int

const (
	AuthBearer AuthStyle = iota // Authorization: Bearer <key>
	AuthXAPIKey                 // x-api-key: <key>
	AuthGoogAPIKey              // x-goog-api-key: <key>
)

type Provider struct {
	Name        string
	UpstreamURL string // e.g. "https://api.anthropic.com"
	AuthStyle   AuthStyle
	ParserType  string // "anthropic", "openai", "gemini", "cohere"
}

var registry = map[string]Provider{
	"anthropic": {
		Name:        "anthropic",
		UpstreamURL: "https://api.anthropic.com",
		AuthStyle:   AuthXAPIKey,
		ParserType:  "anthropic",
	},
	"openai": {
		Name:        "openai",
		UpstreamURL: "https://api.openai.com",
		AuthStyle:   AuthBearer,
		ParserType:  "openai",
	},
	"google": {
		Name:        "google",
		UpstreamURL: "https://generativelanguage.googleapis.com",
		AuthStyle:   AuthGoogAPIKey,
		ParserType:  "gemini",
	},
	"mistral": {
		Name:        "mistral",
		UpstreamURL: "https://api.mistral.ai",
		AuthStyle:   AuthBearer,
		ParserType:  "openai",
	},
	"groq": {
		Name:        "groq",
		UpstreamURL: "https://api.groq.com/openai",
		AuthStyle:   AuthBearer,
		ParserType:  "openai",
	},
	"deepseek": {
		Name:        "deepseek",
		UpstreamURL: "https://api.deepseek.com",
		AuthStyle:   AuthBearer,
		ParserType:  "openai",
	},
	"xai": {
		Name:        "xai",
		UpstreamURL: "https://api.x.ai",
		AuthStyle:   AuthBearer,
		ParserType:  "openai",
	},
	"cohere": {
		Name:        "cohere",
		UpstreamURL: "https://api.cohere.com",
		AuthStyle:   AuthBearer,
		ParserType:  "cohere",
	},
	"together": {
		Name:        "together",
		UpstreamURL: "https://api.together.xyz",
		AuthStyle:   AuthBearer,
		ParserType:  "openai",
	},
	"fireworks": {
		Name:        "fireworks",
		UpstreamURL: "https://api.fireworks.ai/inference",
		AuthStyle:   AuthBearer,
		ParserType:  "openai",
	},
	"cerebras": {
		Name:        "cerebras",
		UpstreamURL: "https://api.cerebras.ai",
		AuthStyle:   AuthBearer,
		ParserType:  "openai",
	},
	"perplexity": {
		Name:        "perplexity",
		UpstreamURL: "https://api.perplexity.ai",
		AuthStyle:   AuthBearer,
		ParserType:  "openai",
	},
	"openrouter": {
		Name:        "openrouter",
		UpstreamURL: "https://openrouter.ai/api",
		AuthStyle:   AuthBearer,
		ParserType:  "openai",
	},
	"ollama": {
		Name:        "ollama",
		UpstreamURL: "http://localhost:11434",
		AuthStyle:   AuthBearer,
		ParserType:  "openai",
	},
	"llamacpp": {
		Name:        "llamacpp",
		UpstreamURL: "http://localhost:8080",
		AuthStyle:   AuthBearer,
		ParserType:  "openai",
	},
}

// customUpstreams stores user-configured upstream URLs for providers like Ollama/llama.cpp
// that run on user-specified hosts rather than fixed cloud endpoints.
var customUpstreams = map[string]string{}

// SetCustomUpstream allows overriding the upstream URL for a provider.
func SetCustomUpstream(providerName, url string) {
	customUpstreams[providerName] = url
}

func Get(name string) (Provider, bool) {
	p, ok := registry[strings.ToLower(name)]
	if ok {
		if customURL, found := customUpstreams[p.Name]; found && customURL != "" {
			p.UpstreamURL = customURL
		}
	}
	return p, ok
}

func All() map[string]Provider {
	return registry
}

// SetAuthHeader sets the appropriate auth header for the provider.
func (p Provider) SetAuthHeader(key string) (headerName, headerValue string) {
	switch p.AuthStyle {
	case AuthXAPIKey:
		return "x-api-key", key
	case AuthGoogAPIKey:
		return "x-goog-api-key", key
	default:
		return "Authorization", "Bearer " + key
	}
}
