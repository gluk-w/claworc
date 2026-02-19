package providers

import "encoding/json"

// ParseOpenAISSE extracts token counts from OpenAI-compatible SSE chunks.
// The final chunk typically contains usage.prompt_tokens / usage.completion_tokens.
// Also used by: Groq, DeepSeek, Mistral, Together, Fireworks, Cerebras, Perplexity, OpenRouter, xAI
func ParseOpenAISSE(data string) (model string, inputTokens, outputTokens int64) {
	var chunk struct {
		Model string `json:"model"`
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(data), &chunk) == nil && chunk.Usage != nil {
		return chunk.Model, chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens
	}
	if chunk.Model != "" {
		return chunk.Model, 0, 0
	}
	return "", 0, 0
}

// ParseOpenAIBody extracts token usage from a non-streaming OpenAI-compatible response.
func ParseOpenAIBody(body []byte) (model string, inputTokens, outputTokens int64) {
	var resp struct {
		Model string `json:"model"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &resp) == nil {
		return resp.Model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	}
	return "", 0, 0
}
