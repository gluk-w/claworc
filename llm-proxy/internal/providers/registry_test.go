package providers

import (
	"testing"
)

func TestGet(t *testing.T) {
	tests := []struct {
		name         string
		providerName string
		wantOK       bool
		wantURL      string
	}{
		{
			name:         "anthropic",
			providerName: "anthropic",
			wantOK:       true,
			wantURL:      "https://api.anthropic.com",
		},
		{
			name:         "openai",
			providerName: "openai",
			wantOK:       true,
			wantURL:      "https://api.openai.com",
		},
		{
			name:         "case insensitive",
			providerName: "ANTHROPIC",
			wantOK:       true,
			wantURL:      "https://api.anthropic.com",
		},
		{
			name:         "unknown provider",
			providerName: "unknown",
			wantOK:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := Get(tt.providerName)
			if ok != tt.wantOK {
				t.Errorf("Get() ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && p.UpstreamURL != tt.wantURL {
				t.Errorf("Get() URL = %s, want %s", p.UpstreamURL, tt.wantURL)
			}
		})
	}
}

func TestSetAuthHeader(t *testing.T) {
	tests := []struct {
		name       string
		provider   Provider
		key        string
		wantHeader string
		wantValue  string
	}{
		{
			name:       "Bearer auth",
			provider:   Provider{AuthStyle: AuthBearer},
			key:        "sk-test",
			wantHeader: "Authorization",
			wantValue:  "Bearer sk-test",
		},
		{
			name:       "x-api-key",
			provider:   Provider{AuthStyle: AuthXAPIKey},
			key:        "sk-ant-test",
			wantHeader: "x-api-key",
			wantValue:  "sk-ant-test",
		},
		{
			name:       "x-goog-api-key",
			provider:   Provider{AuthStyle: AuthGoogAPIKey},
			key:        "AIza-test",
			wantHeader: "x-goog-api-key",
			wantValue:  "AIza-test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header, value := tt.provider.SetAuthHeader(tt.key)
			if header != tt.wantHeader {
				t.Errorf("SetAuthHeader() header = %s, want %s", header, tt.wantHeader)
			}
			if value != tt.wantValue {
				t.Errorf("SetAuthHeader() value = %s, want %s", value, tt.wantValue)
			}
		})
	}
}

func TestSetCustomUpstream(t *testing.T) {
	SetCustomUpstream("ollama", "http://localhost:11434")

	p, ok := Get("ollama")
	if !ok {
		t.Fatal("ollama provider not found")
	}

	if p.UpstreamURL != "http://localhost:11434" {
		t.Errorf("Custom upstream not applied, got %s", p.UpstreamURL)
	}

	// Clean up
	delete(customUpstreams, "ollama")
}
