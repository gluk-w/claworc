package providers

import (
	"testing"
)

func TestParseAnthropicSSE(t *testing.T) {
	tests := []struct {
		name         string
		eventType    string
		data         string
		wantInput    int64
		wantOutput   int64
	}{
		{
			name:      "message_start with input tokens",
			eventType: "message_start",
			data:      `{"message":{"usage":{"input_tokens":150}}}`,
			wantInput: 150,
		},
		{
			name:       "message_delta with output tokens",
			eventType:  "message_delta",
			data:       `{"usage":{"output_tokens":85}}`,
			wantOutput: 85,
		},
		{
			name:      "message_stop",
			eventType: "message_stop",
			data:      `{}`,
		},
		{
			name:      "invalid json",
			eventType: "message_start",
			data:      `{invalid`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, output := ParseAnthropicSSE(tt.eventType, tt.data)
			if input != tt.wantInput {
				t.Errorf("ParseAnthropicSSE() input = %d, want %d", input, tt.wantInput)
			}
			if output != tt.wantOutput {
				t.Errorf("ParseAnthropicSSE() output = %d, want %d", output, tt.wantOutput)
			}
		})
	}
}

func TestParseAnthropicBody(t *testing.T) {
	tests := []struct {
		name       string
		body       []byte
		wantModel  string
		wantInput  int64
		wantOutput int64
	}{
		{
			name:       "valid response",
			body:       []byte(`{"model":"claude-3-5-sonnet-20241022","usage":{"input_tokens":120,"output_tokens":95}}`),
			wantModel:  "claude-3-5-sonnet-20241022",
			wantInput:  120,
			wantOutput: 95,
		},
		{
			name: "invalid json",
			body: []byte(`{invalid`),
		},
		{
			name: "missing usage",
			body: []byte(`{"model":"claude-3-5-sonnet-20241022"}`),
			wantModel: "claude-3-5-sonnet-20241022",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, input, output := ParseAnthropicBody(tt.body)
			if model != tt.wantModel {
				t.Errorf("ParseAnthropicBody() model = %s, want %s", model, tt.wantModel)
			}
			if input != tt.wantInput {
				t.Errorf("ParseAnthropicBody() input = %d, want %d", input, tt.wantInput)
			}
			if output != tt.wantOutput {
				t.Errorf("ParseAnthropicBody() output = %d, want %d", output, tt.wantOutput)
			}
		})
	}
}
