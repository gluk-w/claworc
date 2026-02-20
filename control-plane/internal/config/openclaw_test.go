package config

import "testing"

func TestBaseURLEnvVar(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"OPENAI_API_KEY", "OPENAI_BASE_URL"},
		{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"},
		{"GROQ_API_KEY", "GROQ_BASE_URL"},
		{"XAI_API_KEY", "XAI_BASE_URL"},
		// Keys that don't end in _API_KEY return empty
		{"SOME_TOKEN", ""},
		{"API_KEY_THING", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := baseURLEnvVar(tt.input)
			if result != tt.expected {
				t.Errorf("baseURLEnvVar(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
