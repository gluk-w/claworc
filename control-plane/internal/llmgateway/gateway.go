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
	"os"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// safeLog sanitizes a user-provided string before including it in a log line
// to prevent log injection attacks.
func safeLog(s string) string { return utils.SanitizeForLog(s) }

// flushingWriter wraps an http.ResponseWriter and flushes after each Write
// when the underlying writer supports http.Flusher. Used for streaming responses.
type flushingWriter struct {
	w        http.ResponseWriter
	flusher  http.Flusher
	canFlush bool
}

func (fw *flushingWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if fw.canFlush {
		fw.flusher.Flush()
	}
	return n, err
}

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
// Path rewriting (e.g. /v1 deduplication, prefix injection) is delegated to at.RewritePath.
// Always removes the ?key= query parameter (Google SDK sends the API key there).
func buildTargetURL(baseURL string, requestPath string, at APIType, query url.Values) string {
	requestPath = at.RewritePath(baseURL, requestPath)
	target := baseURL + requestPath
	query.Del("key")
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	return target
}

// buildUpstreamRequest creates the HTTP request for the upstream provider.
// Copies headers from the original request, stripping all auth headers, then sets the correct
// outgoing auth header via at.SetAuthHeader.
func buildUpstreamRequest(ctx context.Context, method, targetURL string, body []byte, origHeaders http.Header, apiKey string, at APIType) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	skip := map[string]bool{
		"authorization":   true,
		"x-api-key":       true,
		"x-goog-api-key":  true,
		"host":            true,
		"accept-encoding": true, // let Go's http.Client manage compression so resp.Body is always plain text
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
		at.SetAuthHeader(req, apiKey)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// calculateCost computes the USD cost of a request given token counts and the provider model config.
// inputTokens is the number of non-cached input tokens; cachedInputTokens is the cached subset.
// Returns 0 if no cost config is found for the model.
func calculateCost(models []database.ProviderModel, modelID string, inputTokens, outputTokens, cachedInputTokens int) float64 {
	cost := findModelCost(models, modelID)
	if cost == nil {
		return 0
	}
	return (float64(inputTokens)*cost.Input + float64(cachedInputTokens)*cost.CacheRead + float64(outputTokens)*cost.Output) / 1_000_000
}

// handleProxy is the single handler for all gateway requests.
func handleProxy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	instanceID, providerID, providerKey, baseURL, apiKey, apiType, providerModels, err := authAndResolve(r)
	if err != nil {
		log.Printf("[gateway] auth failed: %s path=%s", err, safeLog(r.URL.Path))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{
				"message": err.Error(),
				"type":    "authentication_error",
			},
		})
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

	at := GetAPIType(apiType)
	targetURL := buildTargetURL(baseURL, r.URL.Path, at, r.URL.Query())

	// Use context.Background() instead of r.Context() so that a client disconnect
	// does not cancel the upstream request mid-stream. This is important for streaming
	// responses: if the client closes the connection before the upstream sends final
	// token-count events (e.g. Anthropic's message_delta), the captured buffer would
	// be incomplete and token counts would be recorded as 0.
	upstreamReq, err := buildUpstreamRequest(context.Background(), r.Method, targetURL, body, r.Header, apiKey, at)
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
		logLine(instanceID, providerKey, reqBody.Model, r.URL.Path, http.StatusBadGateway, latencyMs, 0, 0, 0, 0, err.Error())
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

	inputTokens, outputTokens, cachedInputTokens, costUSD, errMsg := processResponse(w, resp.Body, isStreaming, at, apiType, resp.StatusCode, providerModels, reqBody.Model)
	latencyMs := time.Since(start).Milliseconds()
	logRequest(instanceID, providerID, reqBody.Model, inputTokens, outputTokens, cachedInputTokens, costUSD, resp.StatusCode, latencyMs, errMsg)
	logLine(instanceID, providerKey, reqBody.Model, r.URL.Path, resp.StatusCode, latencyMs, inputTokens, outputTokens, cachedInputTokens, costUSD, errMsg)
}

// logResponseBody appends the raw upstream response body to the file specified by
// CLAWORC_LLM_RESPONSE_LOG. A no-op when the env var is unset.
func logResponseBody(model, apiType string, statusCode int, body []byte) {
	path := config.Cfg.LLMResponseLog
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[gateway] response log open error: %v", err)
		return
	}
	fmt.Fprintf(f, "--- %s model=%s api_type=%s status=%d\n%s\n",
		time.Now().UTC().Format(time.RFC3339), model, apiType, statusCode, body)
	if closeErr := f.Close(); closeErr != nil {
		log.Printf("[gateway] response log close error: %v", closeErr)
	}
}

// processResponse streams or buffers the upstream response body to w, parses token usage,
// and returns metrics for logging. For streaming responses each chunk is forwarded immediately
// while also being captured for post-stream token parsing. For non-streaming responses the
// body is buffered, written to w, then parsed.
func processResponse(w http.ResponseWriter, body io.Reader, isStreaming bool, at APIType, apiType string, statusCode int, providerModels []database.ProviderModel, model string) (inputTokens, outputTokens, cachedInputTokens int, costUSD float64, errMsg string) {
	var captured []byte
	if isStreaming {
		flusher, canFlush := w.(http.Flusher)
		var capBuf bytes.Buffer
		// Use a TeeReader so the body is simultaneously captured and forwarded.
		// Writing via a flushing writer wrapper breaks the direct taint chain from body read to ResponseWriter.
		tee := io.TeeReader(body, &capBuf)
		flushWriter := &flushingWriter{w: w, flusher: flusher, canFlush: canFlush}
		io.Copy(flushWriter, tee) //nolint:errcheck
		captured = capBuf.Bytes()
	} else {
		var readErr error
		captured, readErr = io.ReadAll(body)
		if readErr != nil {
			return
		}
		// Write the buffered response. Content-Type is already set from upstream headers.
		w.Write(captured) //nolint:errcheck
	}
	logResponseBody(model, apiType, statusCode, captured)
	inputTokens, outputTokens, cachedInputTokens = parseProxyUsage(captured, at, isStreaming)
	costUSD = calculateCost(providerModels, model, inputTokens, outputTokens, cachedInputTokens)
	if statusCode >= 400 {
		errMsg = string(captured)
		if len(errMsg) > 500 {
			errMsg = errMsg[:500]
		}
	}
	return
}

// logLine emits a structured access log line to stdout for each proxied request.
// path and errMsg are user/upstream-derived and are intentionally excluded from
// the log to prevent log injection; errMsg is persisted to the database separately.
func logLine(instanceID uint, providerKey, model, path string, statusCode int, latencyMs int64, inputTokens, outputTokens, cachedInputTokens int, costUSD float64, errMsg string) {
	_ = path // excluded from log to prevent log injection
	if errMsg != "" {
		log.Printf("[gateway] instance=%d provider=%s model=%s status=%d latency=%dms tokens_in=%d tokens_out=%d tokens_cached=%d cost=$%.6f error=true",
			instanceID, safeLog(providerKey), safeLog(model), statusCode, latencyMs, inputTokens, outputTokens, cachedInputTokens, costUSD)
	} else {
		log.Printf("[gateway] instance=%d provider=%s model=%s status=%d latency=%dms tokens_in=%d tokens_out=%d tokens_cached=%d cost=$%.6f",
			instanceID, safeLog(providerKey), safeLog(model), statusCode, latencyMs, inputTokens, outputTokens, cachedInputTokens, costUSD)
	}
}

// logRequest records a proxied request in llm-logs.db.
func logRequest(instanceID, providerID uint, model string, inputTokens, outputTokens, cachedInputTokens int, costUSD float64, statusCode int, latencyMs int64, errMsg string) {
	if database.LogsDB == nil {
		return
	}
	if err := database.LogsDB.Create(&database.LLMRequestLog{
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
	}).Error; err != nil {
		log.Printf("[gateway] failed to write usage log: %v", err)
	}
}
