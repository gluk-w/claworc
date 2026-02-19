package providers

import "encoding/json"

// ParseCohereSSE extracts token counts from Cohere SSE events.
// The final event has response.meta.tokens with input_tokens and output_tokens.
func ParseCohereSSE(data string) (model string, inputTokens, outputTokens int64) {
	// Try chat endpoint format
	var resp struct {
		Response *struct {
			Meta *struct {
				Tokens *struct {
					InputTokens  int64 `json:"input_tokens"`
					OutputTokens int64 `json:"output_tokens"`
				} `json:"tokens"`
			} `json:"meta"`
		} `json:"response"`
	}
	if json.Unmarshal([]byte(data), &resp) == nil && resp.Response != nil && resp.Response.Meta != nil && resp.Response.Meta.Tokens != nil {
		return "", resp.Response.Meta.Tokens.InputTokens, resp.Response.Meta.Tokens.OutputTokens
	}

	// Try v2 chat format with usage field
	var v2 struct {
		Usage *struct {
			Tokens *struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(data), &v2) == nil && v2.Usage != nil && v2.Usage.Tokens != nil {
		return "", v2.Usage.Tokens.InputTokens, v2.Usage.Tokens.OutputTokens
	}

	return "", 0, 0
}

// ParseCohereBody extracts token usage from a non-streaming Cohere response.
func ParseCohereBody(body []byte) (model string, inputTokens, outputTokens int64) {
	return ParseCohereSSE(string(body))
}
