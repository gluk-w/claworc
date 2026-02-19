package llmproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

func doRequest(method, path string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, config.Cfg.ProxyURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+config.Cfg.ProxySecret)

	return httpClient.Do(req)
}

// RegisterInstance registers an instance token with the proxy.
func RegisterInstance(instanceName, token string) error {
	resp, err := doRequest("POST", "/admin/tokens", map[string]string{
		"instance_name": instanceName,
		"token":         token,
	})
	if err != nil {
		return fmt.Errorf("register instance: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register instance: HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// RevokeInstance revokes an instance's proxy token.
func RevokeInstance(instanceName string) error {
	resp, err := doRequest("DELETE", "/admin/tokens/"+instanceName, nil)
	if err != nil {
		return fmt.Errorf("revoke instance: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != 404 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("revoke instance: HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// DisableInstance disables an instance's proxy token without deleting it.
func DisableInstance(instanceName string) error {
	resp, err := doRequest("PUT", "/admin/tokens/"+instanceName+"/disable", nil)
	if err != nil {
		return fmt.Errorf("disable instance: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("disable instance: HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// EnableInstance re-enables an instance's proxy token.
func EnableInstance(instanceName string) error {
	resp, err := doRequest("PUT", "/admin/tokens/"+instanceName+"/enable", nil)
	if err != nil {
		return fmt.Errorf("enable instance: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("enable instance: HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

type syncKey struct {
	Provider string `json:"provider"`
	Scope    string `json:"scope"`
	Key      string `json:"key"`
}

// SyncAPIKeys sends API keys to the proxy.
func SyncAPIKeys(keys []syncKey) error {
	resp, err := doRequest("PUT", "/admin/keys", map[string]interface{}{
		"keys": keys,
	})
	if err != nil {
		return fmt.Errorf("sync keys: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sync keys: HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// SyncInstanceKeys resolves and syncs all API keys for an instance to the proxy.
// apiKeys: map of provider env var name to decrypted key value.
func SyncInstanceKeys(instanceName string, apiKeys map[string]string) error {
	var keys []syncKey
	for envVar, keyValue := range apiKeys {
		provider := envVarToProvider(envVar)
		if provider == "" {
			continue
		}
		keys = append(keys, syncKey{
			Provider: provider,
			Scope:    instanceName,
			Key:      keyValue,
		})
		// Also sync as global if no instance-specific scope
		keys = append(keys, syncKey{
			Provider: provider,
			Scope:    "global",
			Key:      keyValue,
		})
	}
	if len(keys) == 0 {
		return nil
	}
	return SyncAPIKeys(keys)
}

// envVarToProvider maps API key env var names to proxy provider slugs.
func envVarToProvider(envVar string) string {
	mapping := map[string]string{
		"ANTHROPIC_API_KEY":  "anthropic",
		"OPENAI_API_KEY":     "openai",
		"GOOGLE_API_KEY":     "google",
		"GEMINI_API_KEY":     "google",
		"MISTRAL_API_KEY":    "mistral",
		"GROQ_API_KEY":       "groq",
		"DEEPSEEK_API_KEY":   "deepseek",
		"XAI_API_KEY":        "xai",
		"COHERE_API_KEY":     "cohere",
		"TOGETHER_API_KEY":   "together",
		"FIREWORKS_API_KEY":  "fireworks",
		"CEREBRAS_API_KEY":   "cerebras",
		"PERPLEXITY_API_KEY": "perplexity",
		"OPENROUTER_API_KEY": "openrouter",
		"OLLAMA_API_KEY":     "ollama",
	}
	return mapping[envVar]
}

// providerToBaseURLEnv maps provider slugs to the *_BASE_URL env var name.
func ProviderToBaseURLEnv(provider string) string {
	mapping := map[string]string{
		"anthropic":  "ANTHROPIC_BASE_URL",
		"openai":     "OPENAI_BASE_URL",
		"google":     "GOOGLE_API_BASE_URL",
		"mistral":    "MISTRAL_BASE_URL",
		"groq":       "GROQ_BASE_URL",
		"deepseek":   "DEEPSEEK_BASE_URL",
		"xai":        "XAI_BASE_URL",
		"cohere":     "COHERE_BASE_URL",
		"together":   "TOGETHER_BASE_URL",
		"fireworks":  "FIREWORKS_BASE_URL",
		"cerebras":   "CEREBRAS_BASE_URL",
		"perplexity": "PERPLEXITY_BASE_URL",
		"openrouter": "OPENROUTER_BASE_URL",
		"ollama":     "OLLAMA_BASE_URL",
	}
	return mapping[provider]
}

// ProviderToAPIKeyEnv maps provider slugs to the *_API_KEY env var name.
func ProviderToAPIKeyEnv(provider string) string {
	mapping := map[string]string{
		"anthropic":  "ANTHROPIC_API_KEY",
		"openai":     "OPENAI_API_KEY",
		"google":     "GOOGLE_API_KEY",
		"mistral":    "MISTRAL_API_KEY",
		"groq":       "GROQ_API_KEY",
		"deepseek":   "DEEPSEEK_API_KEY",
		"xai":        "XAI_API_KEY",
		"cohere":     "COHERE_API_KEY",
		"together":   "TOGETHER_API_KEY",
		"fireworks":  "FIREWORKS_API_KEY",
		"cerebras":   "CEREBRAS_API_KEY",
		"perplexity": "PERPLEXITY_API_KEY",
		"openrouter": "OPENROUTER_API_KEY",
		"ollama":     "OLLAMA_API_KEY",
	}
	return mapping[provider]
}

type UsageSummary struct {
	Group            string `json:"group"`
	Requests         int64  `json:"requests"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	EstimatedCostUSD string `json:"estimated_cost_usd"`
}

// GetUsage returns aggregate usage data from the proxy.
func GetUsage(since, until, groupBy string) ([]UsageSummary, error) {
	path := "/admin/usage?"
	if since != "" {
		path += "since=" + since + "&"
	}
	if until != "" {
		path += "until=" + until + "&"
	}
	if groupBy != "" {
		path += "group_by=" + groupBy
	}

	resp, err := doRequest("GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("get usage: %w", err)
	}
	defer resp.Body.Close()

	var results []UsageSummary
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decode usage: %w", err)
	}
	return results, nil
}

type InstanceUsageSummary struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	Requests         int64  `json:"requests"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	EstimatedCostUSD string `json:"estimated_cost_usd"`
}

// GetInstanceUsage returns per-instance usage data from the proxy.
func GetInstanceUsage(instanceName, since, until string) ([]InstanceUsageSummary, error) {
	path := "/admin/usage/instances/" + instanceName + "?"
	if since != "" {
		path += "since=" + since + "&"
	}
	if until != "" {
		path += "until=" + until
	}

	resp, err := doRequest("GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("get instance usage: %w", err)
	}
	defer resp.Body.Close()

	var results []InstanceUsageSummary
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decode instance usage: %w", err)
	}
	return results, nil
}

type LimitsResponse struct {
	Budget     json.RawMessage `json:"budget"`
	RateLimits json.RawMessage `json:"rate_limits"`
}

// GetLimits returns budget and rate limits for an instance.
func GetLimits(instanceName string) (*LimitsResponse, error) {
	resp, err := doRequest("GET", "/admin/limits/"+instanceName, nil)
	if err != nil {
		return nil, fmt.Errorf("get limits: %w", err)
	}
	defer resp.Body.Close()

	var result LimitsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode limits: %w", err)
	}
	return &result, nil
}

// SetLimits sets budget and rate limits for an instance.
func SetLimits(instanceName string, body interface{}) error {
	resp, err := doRequest("PUT", "/admin/limits/"+instanceName, body)
	if err != nil {
		return fmt.Errorf("set limits: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("set limits: HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
