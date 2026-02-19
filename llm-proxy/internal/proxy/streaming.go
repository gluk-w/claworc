package proxy

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/gluk-w/claworc/llm-proxy/internal/providers"
)

// UsageResult holds extracted token usage from a response.
type UsageResult struct {
	Model        string
	InputTokens  int64
	OutputTokens int64
}

// StreamingParser reads SSE lines and extracts token usage.
type StreamingParser struct {
	ParserType string
	Result     UsageResult
}

// ParseSSEStream reads an SSE stream, writes each line to the writer, and extracts usage.
func (sp *StreamingParser) ParseSSEStream(reader io.Reader, writer io.Writer) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024) // 1MB buffer

	var currentEvent string

	for scanner.Scan() {
		line := scanner.Text()

		// Write line through to client
		if _, err := writer.Write([]byte(line + "\n")); err != nil {
			return err
		}
		// Flush if possible
		if f, ok := writer.(interface{ Flush() }); ok {
			f.Flush()
		}

		// Parse SSE event type
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
			continue
		}

		// Parse SSE data
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				continue
			}
			sp.parseData(currentEvent, data)
		}
	}

	return scanner.Err()
}

func (sp *StreamingParser) parseData(eventType, data string) {
	switch sp.ParserType {
	case "anthropic":
		input, output := providers.ParseAnthropicSSE(eventType, data)
		if input > 0 {
			sp.Result.InputTokens = input
		}
		if output > 0 {
			sp.Result.OutputTokens = output
		}
		// Extract model from message_start
		if eventType == "message_start" {
			var msg struct {
				Message struct {
					Model string `json:"model"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(data), &msg) == nil && msg.Message.Model != "" {
				sp.Result.Model = msg.Message.Model
			}
		}

	case "openai":
		model, input, output := providers.ParseOpenAISSE(data)
		if model != "" {
			sp.Result.Model = model
		}
		if input > 0 {
			sp.Result.InputTokens = input
		}
		if output > 0 {
			sp.Result.OutputTokens = output
		}

	case "gemini":
		model, input, output := providers.ParseGeminiChunk([]byte(data))
		if model != "" {
			sp.Result.Model = model
		}
		if input > 0 {
			sp.Result.InputTokens = input
		}
		if output > 0 {
			sp.Result.OutputTokens = output
		}

	case "cohere":
		model, input, output := providers.ParseCohereSSE(data)
		if model != "" {
			sp.Result.Model = model
		}
		if input > 0 {
			sp.Result.InputTokens = input
		}
		if output > 0 {
			sp.Result.OutputTokens = output
		}

	default:
		// Try OpenAI format as fallback
		model, input, output := providers.ParseOpenAISSE(data)
		if model != "" {
			sp.Result.Model = model
		}
		if input > 0 {
			sp.Result.InputTokens = input
		}
		if output > 0 {
			sp.Result.OutputTokens = output
		}
	}
}

// ParseNonStreamingBody parses a complete response body for token usage.
func ParseNonStreamingBody(parserType string, body []byte) UsageResult {
	var result UsageResult

	switch parserType {
	case "anthropic":
		result.Model, result.InputTokens, result.OutputTokens = providers.ParseAnthropicBody(body)
	case "openai":
		result.Model, result.InputTokens, result.OutputTokens = providers.ParseOpenAIBody(body)
	case "gemini":
		result.Model, result.InputTokens, result.OutputTokens = providers.ParseGeminiBody(body)
	case "cohere":
		result.Model, result.InputTokens, result.OutputTokens = providers.ParseCohereBody(body)
	default:
		result.Model, result.InputTokens, result.OutputTokens = providers.ParseOpenAIBody(body)
	}

	return result
}
