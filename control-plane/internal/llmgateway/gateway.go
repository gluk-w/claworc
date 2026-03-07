// gateway.go implements the internal LLM proxy gateway.
//
// The gateway listens on 127.0.0.1:40001 (internal only — reachable from containers
// only via SSH agent-listener tunnel). It accepts requests with a claworc-vk-* token (passed
// via Authorization: Bearer, x-api-key, x-goog-api-key, or ?key= depending on the SDK),
// looks up the real provider URL and API key, and proxies the request to the actual LLM
// provider using the correct auth header for the provider's API type.

package llmgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

var gatewayServer *http.Server

// Start creates the LLM gateway HTTP server and starts it in a goroutine.
// host should be "127.0.0.1" — the gateway is internal only and reachable from
// containers via the SSH agent-listener tunnel.
func Start(ctx context.Context, host string, port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleProxy)

	addr := fmt.Sprintf("%s:%d", host, port)
	gatewayServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		log.Printf("LLM gateway listening on %s", addr)
		if err := gatewayServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("LLM gateway stopped: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := gatewayServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("LLM gateway shutdown error: %v", err)
		}
	}()

	return nil
}

// extractGatewayToken returns the first claworc-vk-* token found across all supported auth locations:
// Authorization: Bearer, x-api-key (Anthropic SDK), x-goog-api-key (Google SDK), ?key= query param (Google fallback).
func extractGatewayToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer claworc-vk-") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if v := r.Header.Get("x-api-key"); strings.HasPrefix(v, "claworc-vk-") {
		return v
	}
	if v := r.Header.Get("x-goog-api-key"); strings.HasPrefix(v, "claworc-vk-") {
		return v
	}
	if v := r.URL.Query().Get("key"); strings.HasPrefix(v, "claworc-vk-") {
		return v
	}
	return ""
}

// authAndResolve validates the gateway token and returns provider info, real API key, and API type.
func authAndResolve(r *http.Request) (instanceID, providerID uint, baseURL, apiKey, apiType string, err error) {
	token := extractGatewayToken(r)
	if token == "" {
		err = fmt.Errorf("missing or invalid gateway auth token")
		return
	}

	var key database.LLMGatewayKey
	if dbErr := database.DB.Preload("Provider").Where("gateway_key = ?", token).First(&key).Error; dbErr != nil {
		err = fmt.Errorf("invalid gateway key")
		return
	}

	instanceID = key.InstanceID
	providerID = key.ProviderID
	baseURL = strings.TrimRight(key.Provider.BaseURL, "/")
	apiKey = resolveRealAPIKey(instanceID, key.Provider.Key)
	apiType = key.Provider.APIType
	if apiType == "" {
		apiType = "openai-completions"
	}
	return
}

// resolveRealAPIKey finds the real API key for the given provider.
// Checks per-instance overrides first (using PROVIDER_API_KEY naming convention),
// then falls back to the global api_key:PROVIDER_API_KEY setting.
func resolveRealAPIKey(instanceID uint, providerKey string) string {
	keyName := strings.ToUpper(strings.ReplaceAll(providerKey, "-", "_")) + "_API_KEY"

	// Instance-level override
	var instKey database.InstanceAPIKey
	if database.DB.Where("instance_id = ? AND key_name = ?", instanceID, keyName).First(&instKey).Error == nil {
		if decrypted, err := utils.Decrypt(instKey.KeyValue); err == nil {
			return decrypted
		}
	}

	// Global setting
	if val, err := database.GetSetting("api_key:" + keyName); err == nil && val != "" {
		if decrypted, err := utils.Decrypt(val); err == nil {
			return decrypted
		}
	}

	return ""
}

// handleProxy is the single handler for all gateway requests.
func handleProxy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	instanceID, providerID, baseURL, apiKey, apiType, err := authAndResolve(r)
	if err != nil {
		http.Error(w, `{"error":{"message":"`+err.Error()+`","type":"authentication_error"}}`, http.StatusUnauthorized)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":{"message":"failed to read request body"}}`, http.StatusBadRequest)
		return
	}

	// Parse model from request body for logging
	var reqBody struct {
		Model string `json:"model"`
	}
	json.Unmarshal(body, &reqBody)

	// Build upstream URL: baseURL + request path.
	// Strip leading /v1 from the path if baseURL already ends with /v1 to avoid duplication
	// (e.g. baseURL "https://api.anthropic.com/openai/v1" + path "/v1/chat/completions").
	urlPath := r.URL.Path
	if strings.HasSuffix(baseURL, "/v1") && strings.HasPrefix(urlPath, "/v1/") {
		urlPath = urlPath[3:] // strip "/v1"
	}
	targetURL := baseURL + urlPath
	// Strip ?key= from forwarded query string (Google SDK sends the API key as a query param)
	query := r.URL.Query()
	query.Del("key")
	if encoded := query.Encode(); encoded != "" {
		targetURL += "?" + encoded
	}

	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, `{"error":{"message":"failed to build upstream request"}}`, http.StatusInternalServerError)
		return
	}

	// Copy headers, stripping all incoming auth headers (we set the correct one below)
	skipHeaders := map[string]bool{
		"authorization":  true,
		"x-api-key":      true,
		"x-goog-api-key": true,
		"host":           true,
	}
	for k, vs := range r.Header {
		if skipHeaders[strings.ToLower(k)] {
			continue
		}
		for _, v := range vs {
			upstreamReq.Header.Add(k, v)
		}
	}

	// Set outgoing auth header according to the provider's API type
	if apiKey != "" {
		switch apiType {
		case "anthropic-messages":
			upstreamReq.Header.Set("x-api-key", apiKey)
		case "google-generative-ai":
			upstreamReq.Header.Set("x-goog-api-key", apiKey)
		default:
			upstreamReq.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}

	if upstreamReq.Header.Get("Content-Type") == "" {
		upstreamReq.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		http.Error(w, `{"error":{"message":"upstream request failed"}}`, http.StatusBadGateway)
		logRequest(instanceID, providerID, reqBody.Model, 0, 0, http.StatusBadGateway, time.Since(start).Milliseconds(), err.Error())
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	isStreaming := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
	if isStreaming {
		// Stream response directly — no buffering
		io.Copy(w, resp.Body)
		logRequest(instanceID, providerID, reqBody.Model, 0, 0, resp.StatusCode, time.Since(start).Milliseconds(), "")
	} else {
		// Buffer response to extract token counts
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr == nil {
			w.Write(respBody)
		}
		var inputTokens, outputTokens int
		var errMsg string
		if readErr == nil {
			// Try all provider token count formats — only one will be non-zero for any given response
			var usage struct {
				Usage struct {
					// OpenAI format
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
					// Anthropic format (same "usage" key, different fields)
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
				// Google format
				UsageMetadata struct {
					PromptTokenCount     int `json:"promptTokenCount"`
					CandidatesTokenCount int `json:"candidatesTokenCount"`
				} `json:"usageMetadata"`
			}
			if json.Unmarshal(respBody, &usage) == nil {
				inputTokens = usage.Usage.PromptTokens + usage.Usage.InputTokens + usage.UsageMetadata.PromptTokenCount
				outputTokens = usage.Usage.CompletionTokens + usage.Usage.OutputTokens + usage.UsageMetadata.CandidatesTokenCount
			}
			if resp.StatusCode >= 400 {
				errMsg = string(respBody)
				if len(errMsg) > 500 {
					errMsg = errMsg[:500]
				}
			}
		}
		logRequest(instanceID, providerID, reqBody.Model, inputTokens, outputTokens, resp.StatusCode, time.Since(start).Milliseconds(), errMsg)
	}
}

// logRequest records a proxied request in the LLMRequestLog table.
func logRequest(instanceID, providerID uint, model string, inputTokens, outputTokens, statusCode int, latencyMs int64, errMsg string) {
	database.DB.Create(&database.LLMRequestLog{
		InstanceID:   instanceID,
		ProviderID:   providerID,
		ModelID:      model,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		StatusCode:   statusCode,
		LatencyMs:    latencyMs,
		ErrorMessage: errMsg,
		RequestedAt:  time.Now().UTC(),
	})
}
