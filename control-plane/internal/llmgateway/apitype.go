package llmgateway

import "net/http"

// APITypeOpenAICodexResponses is the api_type identifier for OpenAI's
// Codex/ChatGPT subscription endpoint, which uses OAuth credentials rather
// than a static API key.
const APITypeOpenAICodexResponses = "openai-codex-responses"

// AuthMaterial bundles the credentials needed to set outgoing auth headers on
// an upstream provider request. Static-key providers populate APIKey;
// OAuth-based providers (currently openai-codex-responses) populate the OAuth*
// fields. Implementations of APIType pick whichever fields they need.
type AuthMaterial struct {
	APIKey       string // for static-key types
	OAuthAccess  string // OAuth access token (Bearer)
	OAuthAccount string // chatgpt-account-id for openai-codex-responses
}

// APIType encapsulates all per-provider behavior: auth headers, URL rewriting,
// usage parsing, and probe endpoints. Implementations are stateless value types.
type APIType interface {
	SetAuthHeader(req *http.Request, mat AuthMaterial)
	RewritePath(baseURL, requestPath string) string
	ParseUsage(body []byte) (inputTokens, outputTokens, cachedInputTokens int)
	ParseStreamingUsage(body []byte) (inputTokens, outputTokens, cachedInputTokens int)
	ProbeURL(baseURL string) string
	ProbeHeaders(req *http.Request)
}

// GetAPIType returns the APIType implementation for the given api type string.
func GetAPIType(apiType string) APIType {
	switch apiType {
	case "openai-responses":
		return openAIResponses{}
	case APITypeOpenAICodexResponses:
		return openAICodexResponses{}
	case "anthropic-messages":
		return anthropicMessages{}
	case "google-generative-ai":
		return googleGenerativeAI{}
	case "ollama":
		return ollamaAPI{}
	case "bedrock-converse", "bedrock-converse-stream":
		return bedrockConverse{}
	default:
		return openAICompletions{}
	}
}

// IsOAuthAPIType reports whether the given api_type uses OAuth credentials
// rather than a static API key. Used by the gateway to decide whether to
// resolve an access token (with refresh) instead of decrypting APIKey.
func IsOAuthAPIType(apiType string) bool {
	return apiType == APITypeOpenAICodexResponses
}
