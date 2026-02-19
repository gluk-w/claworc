package providers

import (
	"testing"
)

func TestParseGeminiChunk(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		wantModel  string
		wantInput  int64
		wantOutput int64
	}{
		{
			name:       "chunk with usage metadata",
			data:       []byte(`{"modelVersion":"gemini-2.0-flash","usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":30,"totalTokenCount":80}}`),
			wantModel:  "gemini-2.0-flash",
			wantInput:  50,
			wantOutput: 30,
		},
		{
			name:      "chunk without metadata",
			data:      []byte(`{"candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}`),
			wantModel: "",
		},
		{
			name: "invalid json",
			data: []byte(`{invalid`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, input, output := ParseGeminiChunk(tt.data)
			if model != tt.wantModel {
				t.Errorf("ParseGeminiChunk() model = %s, want %s", model, tt.wantModel)
			}
			if input != tt.wantInput {
				t.Errorf("ParseGeminiChunk() input = %d, want %d", input, tt.wantInput)
			}
			if output != tt.wantOutput {
				t.Errorf("ParseGeminiChunk() output = %d, want %d", output, tt.wantOutput)
			}
		})
	}
}
