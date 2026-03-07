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
	"net/url"
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

// authAndResolve validates the gateway token and returns provider info, real API key, API type, and provider models.
func authAndResolve(r *http.Request) (instanceID, providerID uint, providerKey, baseURL, apiKey, apiType string, providerModels []database.ProviderModel, err error) {
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
	providerKey = key.Provider.Key
	baseURL = strings.TrimRight(key.Provider.BaseURL, "/")
	apiKey = resolveRealAPIKey(instanceID, key.Provider.Key)
	apiType = key.Provider.APIType
	if apiType == "" {
		apiType = "openai-completions"
	}
	providerModels = database.ParseProviderModels(key.Provider.Models)
	return
}

// findModelCost returns the cost config for the given model ID, or nil if not found.
func findModelCost(models []database.ProviderModel, modelID string) *database.ProviderModelCost {
	for _, m := range models {
		if m.ID == modelID && m.Cost != nil {
			return m.Cost
		}
	}
	return nil
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

// buildTargetURL constructs the upstream URL from the provider base URL and the request path/query.
// Strips a leading /v1 from the path if baseURL already ends with /v1 to avoid double-prefixing.
// For openai-responses, prepends /v1 when the path doesn't already include it (the OpenClaw SDK
// appends /v1 to the configured base URL, so incoming paths arrive without the prefix).
// Always removes the ?key= query parameter (Google SDK sends the API key there).
func buildTargetURL(baseURL, requestPath, apiType string, query url.Values) string {
	if strings.HasSuffix(baseURL, "/v1") && strings.HasPrefix(requestPath, "/v1/") {
		requestPath = requestPath[3:]
	} else if apiType == "openai-responses" && !strings.HasSuffix(baseURL, "/v1") && !strings.HasPrefix(requestPath, "/v1/") {
		requestPath = "/v1" + requestPath
	}
	target := baseURL + requestPath
	query.Del("key")
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	return target
}

// buildUpstreamRequest creates the HTTP request for the upstream provider.
// Copies headers from the original request, stripping all auth headers, then sets the correct
// outgoing auth header for the provider's API type.
func buildUpstreamRequest(ctx context.Context, method, targetURL string, body []byte, origHeaders http.Header, apiKey, apiType string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	skip := map[string]bool{
		"authorization":  true,
		"x-api-key":      true,
		"x-goog-api-key": true,
		"host":           true,
	}
	for k, vs := range origHeaders {
		if skip[strings.ToLower(k)] {
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if apiKey != "" {
		switch apiType {
		case "anthropic-messages":
			req.Header.Set("x-api-key", apiKey)
		case "google-generative-ai":
			req.Header.Set("x-goog-api-key", apiKey)
		default:
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// parseUsage extracts token counts from a provider response body.
// Supports OpenAI, Anthropic, and Google formats — only one set of fields will be non-zero.
func parseUsage(respBody []byte) (inputTokens, outputTokens, cachedInputTokens int) {
	var u struct {
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			InputTokens          int `json:"input_tokens"`
			OutputTokens         int `json:"output_tokens"`
			CacheReadInputTokens int `json:"cache_read_input_tokens"`
		} `json:"usage"`
		UsageMetadata struct {
			PromptTokenCount        int `json:"promptTokenCount"`
			CandidatesTokenCount    int `json:"candidatesTokenCount"`
			CachedContentTokenCount int `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	}
	if json.Unmarshal(respBody, &u) == nil {
		inputTokens = u.Usage.PromptTokens + u.Usage.InputTokens + u.UsageMetadata.PromptTokenCount
		outputTokens = u.Usage.CompletionTokens + u.Usage.OutputTokens + u.UsageMetadata.CandidatesTokenCount
		cachedInputTokens = u.Usage.PromptTokensDetails.CachedTokens + u.Usage.CacheReadInputTokens + u.UsageMetadata.CachedContentTokenCount
	}
	return
}

// calculateCost computes the USD cost of a request given token counts and the provider model config.
// Returns 0 if no cost config is found for the model.
func calculateCost(models []database.ProviderModel, modelID string, inputTokens, outputTokens, cachedInputTokens int) float64 {
	cost := findModelCost(models, modelID)
	if cost == nil {
		return 0
	}
	nonCached := float64(inputTokens - cachedInputTokens)
	return (nonCached*cost.Input + float64(cachedInputTokens)*cost.CacheRead + float64(outputTokens)*cost.Output) / 1_000_000
}

// handleProxy is the single handler for all gateway requests.
func handleProxy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	instanceID, providerID, providerKey, baseURL, apiKey, apiType, providerModels, err := authAndResolve(r)
	if err != nil {
		log.Printf("[gateway] auth failed: %s path=%s", err, r.URL.Path)
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

	targetURL := buildTargetURL(baseURL, r.URL.Path, apiType, r.URL.Query())

	upstreamReq, err := buildUpstreamRequest(r.Context(), r.Method, targetURL, body, r.Header, apiKey, apiType)
	if err != nil {
		http.Error(w, `{"error":{"message":"failed to build upstream request"}}`, http.StatusInternalServerError)
		return
	}

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		latencyMs := time.Since(start).Milliseconds()
		http.Error(w, `{"error":{"message":"upstream request failed"}}`, http.StatusBadGateway)
		logRequest(instanceID, providerID, reqBody.Model, 0, 0, 0, 0, http.StatusBadGateway, latencyMs, err.Error())
		logLine(instanceID, providerKey, reqBody.Model, r.URL.Path, http.StatusBadGateway, latencyMs, err.Error())
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	isStreaming := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if isStreaming {
		w.Header().Set("X-Accel-Buffering", "no")
	}
	w.WriteHeader(resp.StatusCode)

	if isStreaming {
		// Flush each chunk immediately so the client receives SSE events in real time.
		flusher, canFlush := w.(http.Flusher)
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				if canFlush {
					flusher.Flush()
				}
			}
			if err != nil {
				break
			}
		}
		latencyMs := time.Since(start).Milliseconds()
		logRequest(instanceID, providerID, reqBody.Model, 0, 0, 0, 0, resp.StatusCode, latencyMs, "")
		logLine(instanceID, providerKey, reqBody.Model, r.URL.Path, resp.StatusCode, latencyMs, "")
	} else {
		// Buffer response to extract token counts
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr == nil {
			w.Write(respBody)
		}
		var inputTokens, outputTokens, cachedInputTokens int
		var costUSD float64
		var errMsg string
		if readErr == nil {
			inputTokens, outputTokens, cachedInputTokens = parseUsage(respBody)
			costUSD = calculateCost(providerModels, reqBody.Model, inputTokens, outputTokens, cachedInputTokens)
			if resp.StatusCode >= 400 {
				errMsg = string(respBody)
				if len(errMsg) > 500 {
					errMsg = errMsg[:500]
				}
			}
		}
		latencyMs := time.Since(start).Milliseconds()
		logRequest(instanceID, providerID, reqBody.Model, inputTokens, outputTokens, cachedInputTokens, costUSD, resp.StatusCode, latencyMs, errMsg)
		logLine(instanceID, providerKey, reqBody.Model, r.URL.Path, resp.StatusCode, latencyMs, errMsg)
	}
}

// logLine emits a structured access log line to stdout for each proxied request.
func logLine(instanceID uint, providerKey, model, path string, statusCode int, latencyMs int64, errMsg string) {
	if errMsg != "" {
		log.Printf("[gateway] instance=%d provider=%s model=%s path=%s status=%d latency=%dms error=%s",
			instanceID, providerKey, model, path, statusCode, latencyMs, errMsg)
	} else {
		log.Printf("[gateway] instance=%d provider=%s model=%s path=%s status=%d latency=%dms",
			instanceID, providerKey, model, path, statusCode, latencyMs)
	}
}

// logRequest records a proxied request in llm-logs.db.
func logRequest(instanceID, providerID uint, model string, inputTokens, outputTokens, cachedInputTokens int, costUSD float64, statusCode int, latencyMs int64, errMsg string) {
	database.LogsDB.Create(&database.LLMRequestLog{
		InstanceID:        instanceID,
		ProviderID:        providerID,
		ModelID:           model,
		InputTokens:       inputTokens,
		OutputTokens:      outputTokens,
		CachedInputTokens: cachedInputTokens,
		CostUSD:           costUSD,
		StatusCode:        statusCode,
		LatencyMs:         latencyMs,
		ErrorMessage:      errMsg,
		RequestedAt:       time.Now().UTC(),
	})
}
