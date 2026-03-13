package llmgateway

import "net/http"

// APIType encapsulates all per-provider behavior: auth headers, URL rewriting,
// usage parsing, and probe endpoints. Implementations are stateless value types.
type APIType interface {
	SetAuthHeader(req *http.Request, apiKey string)
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
