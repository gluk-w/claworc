package providers

import (
	"testing"
)

func TestParseCohereSSE(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		wantModel  string
		wantInput  int64
		wantOutput int64
	}{
		{
			name:       "response with meta tokens",
			data:       `{"response":{"meta":{"tokens":{"input_tokens":100,"output_tokens":75}}}}`,
			wantInput:  100,
			wantOutput: 75,
		},
		{
			name:       "v2 format with usage",
			data:       `{"usage":{"tokens":{"input_tokens":50,"output_tokens":40}}}`,
			wantInput:  50,
			wantOutput: 40,
		},
		{
			name: "chunk without tokens",
			data: `{"text":"Hello"}`,
		},
		{
			name: "invalid json",
			data: `{invalid`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, input, output := ParseCohereSSE(tt.data)
			if model != tt.wantModel {
				t.Errorf("ParseCohereSSE() model = %s, want %s", model, tt.wantModel)
			}
			if input != tt.wantInput {
				t.Errorf("ParseCohereSSE() input = %d, want %d", input, tt.wantInput)
			}
			if output != tt.wantOutput {
				t.Errorf("ParseCohereSSE() output = %d, want %d", output, tt.wantOutput)
			}
		})
	}
}
