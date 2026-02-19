package providers

import "encoding/json"

// ParseAnthropicSSE extracts token counts from Anthropic SSE events.
// Events: message_start (input_tokens), message_delta (output_tokens), message_stop
func ParseAnthropicSSE(eventType, data string) (inputTokens, outputTokens int64) {
	switch eventType {
	case "message_start":
		var msg struct {
			Message struct {
				Usage struct {
					InputTokens int64 `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(data), &msg) == nil {
			inputTokens = msg.Message.Usage.InputTokens
		}
	case "message_delta":
		var msg struct {
			Usage struct {
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &msg) == nil {
			outputTokens = msg.Usage.OutputTokens
		}
	}
	return
}

// ParseAnthropicBody extracts token usage from a non-streaming Anthropic response.
func ParseAnthropicBody(body []byte) (model string, inputTokens, outputTokens int64) {
	var resp struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &resp) == nil {
		return resp.Model, resp.Usage.InputTokens, resp.Usage.OutputTokens
	}
	return "", 0, 0
}
