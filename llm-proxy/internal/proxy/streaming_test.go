package proxy

import (
	"bytes"
	"strings"
	"testing"
)

func TestStreamingParser_ParseSSEStream(t *testing.T) {
	tests := []struct {
		name       string
		parserType string
		input      string
		wantModel  string
		wantInput  int64
		wantOutput int64
	}{
		{
			name:       "anthropic message_start and message_delta",
			parserType: "anthropic",
			input: `event: message_start
data: {"message":{"model":"claude-3-5-sonnet-20241022","usage":{"input_tokens":150}}}

event: message_delta
data: {"usage":{"output_tokens":85}}

event: message_stop
data: {}

`,
			wantModel:  "claude-3-5-sonnet-20241022",
			wantInput:  150,
			wantOutput: 85,
		},
		{
			name:       "openai chunk with usage",
			parserType: "openai",
			input: `data: {"choices":[{"delta":{"content":"Hello"}}]}

data: {"model":"gpt-4o","usage":{"prompt_tokens":100,"completion_tokens":50}}

data: [DONE]

`,
			wantModel:  "gpt-4o",
			wantInput:  100,
			wantOutput: 50,
		},
		{
			name:       "gemini with usage metadata",
			parserType: "gemini",
			input: `data: {"modelVersion":"gemini-2.0-flash","usageMetadata":{"promptTokenCount":75,"candidatesTokenCount":45}}

`,
			wantModel:  "gemini-2.0-flash",
			wantInput:  75,
			wantOutput: 45,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := &StreamingParser{ParserType: tt.parserType}
			reader := strings.NewReader(tt.input)
			var buf bytes.Buffer

			if err := parser.ParseSSEStream(reader, &buf); err != nil {
				t.Fatalf("ParseSSEStream() error = %v", err)
			}

			// Verify stream was passed through
			if buf.String() != tt.input {
				t.Errorf("Stream not passed through correctly")
			}

			if parser.Result.Model != tt.wantModel {
				t.Errorf("model = %s, want %s", parser.Result.Model, tt.wantModel)
			}
			if parser.Result.InputTokens != tt.wantInput {
				t.Errorf("input = %d, want %d", parser.Result.InputTokens, tt.wantInput)
			}
			if parser.Result.OutputTokens != tt.wantOutput {
				t.Errorf("output = %d, want %d", parser.Result.OutputTokens, tt.wantOutput)
			}
		})
	}
}

func TestParseNonStreamingBody(t *testing.T) {
	tests := []struct {
		name       string
		parserType string
		body       []byte
		wantModel  string
		wantInput  int64
		wantOutput int64
	}{
		{
			name:       "anthropic",
			parserType: "anthropic",
			body:       []byte(`{"model":"claude-3-5-sonnet-20241022","usage":{"input_tokens":120,"output_tokens":95}}`),
			wantModel:  "claude-3-5-sonnet-20241022",
			wantInput:  120,
			wantOutput: 95,
		},
		{
			name:       "openai",
			parserType: "openai",
			body:       []byte(`{"model":"gpt-4o","usage":{"prompt_tokens":80,"completion_tokens":60}}`),
			wantModel:  "gpt-4o",
			wantInput:  80,
			wantOutput: 60,
		},
		{
			name:       "gemini",
			parserType: "gemini",
			body:       []byte(`{"modelVersion":"gemini-2.0-pro","usageMetadata":{"promptTokenCount":200,"candidatesTokenCount":150}}`),
			wantModel:  "gemini-2.0-pro",
			wantInput:  200,
			wantOutput: 150,
		},
		{
			name:       "unknown parser defaults to openai",
			parserType: "unknown",
			body:       []byte(`{"model":"custom","usage":{"prompt_tokens":10,"completion_tokens":5}}`),
			wantModel:  "custom",
			wantInput:  10,
			wantOutput: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseNonStreamingBody(tt.parserType, tt.body)
			if result.Model != tt.wantModel {
				t.Errorf("model = %s, want %s", result.Model, tt.wantModel)
			}
			if result.InputTokens != tt.wantInput {
				t.Errorf("input = %d, want %d", result.InputTokens, tt.wantInput)
			}
			if result.OutputTokens != tt.wantOutput {
				t.Errorf("output = %d, want %d", result.OutputTokens, tt.wantOutput)
			}
		})
	}
}
