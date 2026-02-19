package providers

import (
	"testing"
)

func TestParseOpenAISSE(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		wantModel  string
		wantInput  int64
		wantOutput int64
	}{
		{
			name:       "chunk with usage",
			data:       `{"model":"gpt-4o","usage":{"prompt_tokens":250,"completion_tokens":180}}`,
			wantModel:  "gpt-4o",
			wantInput:  250,
			wantOutput: 180,
		},
		{
			name:      "chunk without usage",
			data:      `{"choices":[{"delta":{"content":"Hello"}}]}`,
			wantModel: "",
		},
		{
			name:      "model only",
			data:      `{"model":"gpt-4o-mini"}`,
			wantModel: "gpt-4o-mini",
		},
		{
			name: "invalid json",
			data: `{invalid`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, input, output := ParseOpenAISSE(tt.data)
			if model != tt.wantModel {
				t.Errorf("ParseOpenAISSE() model = %s, want %s", model, tt.wantModel)
			}
			if input != tt.wantInput {
				t.Errorf("ParseOpenAISSE() input = %d, want %d", input, tt.wantInput)
			}
			if output != tt.wantOutput {
				t.Errorf("ParseOpenAISSE() output = %d, want %d", output, tt.wantOutput)
			}
		})
	}
}

func TestParseOpenAIBody(t *testing.T) {
	tests := []struct {
		name       string
		body       []byte
		wantModel  string
		wantInput  int64
		wantOutput int64
	}{
		{
			name:       "valid response",
			body:       []byte(`{"model":"gpt-4o","usage":{"prompt_tokens":100,"completion_tokens":50}}`),
			wantModel:  "gpt-4o",
			wantInput:  100,
			wantOutput: 50,
		},
		{
			name: "invalid json",
			body: []byte(`{invalid`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, input, output := ParseOpenAIBody(tt.body)
			if model != tt.wantModel {
				t.Errorf("ParseOpenAIBody() model = %s, want %s", model, tt.wantModel)
			}
			if input != tt.wantInput {
				t.Errorf("ParseOpenAIBody() input = %d, want %d", input, tt.wantInput)
			}
			if output != tt.wantOutput {
				t.Errorf("ParseOpenAIBody() output = %d, want %d", output, tt.wantOutput)
			}
		})
	}
}
