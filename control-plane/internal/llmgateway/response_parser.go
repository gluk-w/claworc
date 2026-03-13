package llmgateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
)

// ParseUsageOpenAICompletions parses token counts from an OpenAI chat/completions response.
func ParseUsageOpenAICompletions(body []byte) (inputTokens, outputTokens, cachedInputTokens int) {
	var u struct {
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &u) == nil {
		cachedInputTokens = u.Usage.PromptTokensDetails.CachedTokens
		inputTokens = u.Usage.PromptTokens - cachedInputTokens
		outputTokens = u.Usage.CompletionTokens
	}
	return
}

// ParseUsageOpenAIResponses parses token counts from an OpenAI responses API response.
// The responses API uses input_tokens/output_tokens with cached tokens under input_tokens_details.
func ParseUsageOpenAIResponses(body []byte) (inputTokens, outputTokens, cachedInputTokens int) {
	var u struct {
		Usage struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &u) == nil {
		inputTokens = u.Usage.InputTokens
		outputTokens = u.Usage.OutputTokens
		cachedInputTokens = u.Usage.InputTokensDetails.CachedTokens
	}
	return
}

// ParseUsageAnthropicMessages parses token counts from an Anthropic messages response.
func ParseUsageAnthropicMessages(body []byte) (inputTokens, outputTokens, cachedInputTokens int) {
	var u struct {
		Usage struct {
			InputTokens          int `json:"input_tokens"`
			OutputTokens         int `json:"output_tokens"`
			CacheReadInputTokens int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &u) == nil {
		inputTokens = u.Usage.InputTokens
		outputTokens = u.Usage.OutputTokens
		cachedInputTokens = u.Usage.CacheReadInputTokens
	}
	return
}

// ParseUsageGoogleGenerativeAI parses token counts from a Google Generative AI response.
func ParseUsageGoogleGenerativeAI(body []byte) (inputTokens, outputTokens, cachedInputTokens int) {
	var u struct {
		UsageMetadata struct {
			PromptTokenCount        int `json:"promptTokenCount"`
			CandidatesTokenCount    int `json:"candidatesTokenCount"`
			CachedContentTokenCount int `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	}
	if json.Unmarshal(body, &u) == nil {
		inputTokens = u.UsageMetadata.PromptTokenCount
		outputTokens = u.UsageMetadata.CandidatesTokenCount
		cachedInputTokens = u.UsageMetadata.CachedContentTokenCount
	}
	return
}

// ParseUsageOpenAICompletionsStream extracts token counts from a buffered OpenAI SSE stream.
// Token counts are only present when the request included stream_options: {include_usage: true},
// which causes a final chunk (before [DONE]) to carry the usage object. Without that option the
// stream contains no usage data and all counts will be zero.
func ParseUsageOpenAICompletionsStream(body []byte) (inputTokens, outputTokens, cachedInputTokens int) {
	var chunk struct {
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		if json.Unmarshal([]byte(data), &chunk) == nil && chunk.Usage.PromptTokens > 0 {
			cachedInputTokens = chunk.Usage.PromptTokensDetails.CachedTokens
			inputTokens = chunk.Usage.PromptTokens - cachedInputTokens
			outputTokens = chunk.Usage.CompletionTokens
		}
	}
	return
}

// ParseUsageOpenAIResponsesStream extracts token counts from a buffered OpenAI Responses API SSE stream.
// Token counts are carried in the response.completed event under response.usage.
func ParseUsageOpenAIResponsesStream(body []byte) (inputTokens, outputTokens, cachedInputTokens int) {
	var event struct {
		Type     string `json:"type"`
		Response struct {
			Usage struct {
				InputTokens        int `json:"input_tokens"`
				OutputTokens       int `json:"output_tokens"`
				InputTokensDetails struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"input_tokens_details"`
			} `json:"usage"`
		} `json:"response"`
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if !strings.Contains(data, `"response.completed"`) {
			continue
		}
		if json.Unmarshal([]byte(data), &event) == nil && event.Type == "response.completed" {
			inputTokens = event.Response.Usage.InputTokens
			outputTokens = event.Response.Usage.OutputTokens
			cachedInputTokens = event.Response.Usage.InputTokensDetails.CachedTokens
			return
		}
	}
	return
}

// ParseUsageAnthropicMessagesStream extracts token counts from a buffered Anthropic SSE stream.
// Input tokens and cache counts come from the message_start event; final output token count
// comes from the message_delta event (which supersedes the preliminary count in message_start).
func ParseUsageAnthropicMessagesStream(body []byte) (inputTokens, outputTokens, cachedInputTokens int) {
	type startUsage struct {
		InputTokens          int `json:"input_tokens"`
		CacheReadInputTokens int `json:"cache_read_input_tokens"`
		OutputTokens         int `json:"output_tokens"`
	}
	type deltaUsage struct {
		OutputTokens int `json:"output_tokens"`
	}
	var msgStart struct {
		Type    string `json:"type"`
		Message struct {
			Usage startUsage `json:"usage"`
		} `json:"message"`
	}
	var msgDelta struct {
		Type  string     `json:"type"`
		Usage deltaUsage `json:"usage"`
	}

	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if strings.HasPrefix(data, `{"type":"message_start"`) {
			if json.Unmarshal([]byte(data), &msgStart) == nil {
				inputTokens = msgStart.Message.Usage.InputTokens
				cachedInputTokens = msgStart.Message.Usage.CacheReadInputTokens
			}
		} else if strings.HasPrefix(data, `{"type":"message_delta"`) {
			if json.Unmarshal([]byte(data), &msgDelta) == nil {
				outputTokens = msgDelta.Usage.OutputTokens
			}
		}
	}
	return
}

// ParseUsageOllama parses token counts from an Ollama non-streaming response.
// Token counts are top-level fields: prompt_eval_count (input) and eval_count (output).
// Ollama does not report cached tokens.
func ParseUsageOllama(body []byte) (inputTokens, outputTokens, cachedInputTokens int) {
	var r struct {
		PromptEvalCount int `json:"prompt_eval_count"`
		EvalCount       int `json:"eval_count"`
	}
	if json.Unmarshal(body, &r) == nil {
		inputTokens = r.PromptEvalCount
		outputTokens = r.EvalCount
	}
	return
}

// ParseUsageOllamaStream parses token counts from an Ollama streaming response.
// The stream is newline-delimited JSON (not SSE). Only the final object (done: true)
// carries token counts.
func ParseUsageOllamaStream(body []byte) (inputTokens, outputTokens, cachedInputTokens int) {
	var r struct {
		Done            bool `json:"done"`
		PromptEvalCount int  `json:"prompt_eval_count"`
		EvalCount       int  `json:"eval_count"`
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if json.Unmarshal([]byte(line), &r) == nil && r.Done {
			inputTokens = r.PromptEvalCount
			outputTokens = r.EvalCount
			return
		}
	}
	return
}

// ParseUsageBedrockConverseStream parses token counts from an AWS Bedrock Converse stream
// metadata event. The final event carries usage under metadata.usage with camelCase field names.
// Bedrock does not report cached tokens in this event.
func ParseUsageBedrockConverseStream(body []byte) (inputTokens, outputTokens, cachedInputTokens int) {
	var r struct {
		Metadata struct {
			Usage struct {
				InputTokens  int `json:"inputTokens"`
				OutputTokens int `json:"outputTokens"`
			} `json:"usage"`
		} `json:"metadata"`
	}
	if json.Unmarshal(body, &r) == nil {
		inputTokens = r.Metadata.Usage.InputTokens
		outputTokens = r.Metadata.Usage.OutputTokens
	}
	return
}

// parseProxyUsage delegates to the correct streaming or non-streaming parser via the APIType interface.
func parseProxyUsage(body []byte, at APIType, isStreaming bool) (inputTokens, outputTokens, cachedInputTokens int) {
	if isStreaming {
		return at.ParseStreamingUsage(body)
	}
	return at.ParseUsage(body)
}

// ParseUsage dispatches to the correct parser based on apiType string.
// Public backward-compat wrapper used by external callers.
func ParseUsage(apiType string, body []byte) (inputTokens, outputTokens, cachedInputTokens int) {
	return GetAPIType(apiType).ParseUsage(body)
}
