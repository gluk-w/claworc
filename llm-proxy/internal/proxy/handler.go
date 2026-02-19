package proxy

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gluk-w/claworc/llm-proxy/internal/database"
	"github.com/gluk-w/claworc/llm-proxy/internal/providers"
	"github.com/go-chi/chi/v5"
)

// ProxyHandler handles reverse proxying to LLM providers.
func ProxyHandler(w http.ResponseWriter, r *http.Request) {
	providerName := chi.URLParam(r, "provider")
	provider, ok := providers.Get(providerName)
	if !ok {
		http.Error(w, `{"error":"unknown_provider","message":"Unknown provider"}`, http.StatusBadRequest)
		return
	}

	instanceName := GetInstanceName(r.Context())

	// Store provider name in context for rate limiter
	r = r.WithContext(context.WithValue(r.Context(), providerKey, providerName))

	// Resolve the real API key
	apiKey := resolveAPIKey(providerName, instanceName)
	if apiKey == "" {
		http.Error(w, `{"error":"no_api_key","message":"No API key configured for this provider"}`, http.StatusBadGateway)
		return
	}

	// Build upstream URL: strip /v1/{provider} prefix from path
	pathPrefix := "/v1/" + providerName
	upstreamPath := strings.TrimPrefix(r.URL.Path, pathPrefix)
	if upstreamPath == "" {
		upstreamPath = "/"
	}
	upstreamURL := provider.UpstreamURL + upstreamPath
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	// Read request body
	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"read_body","message":"Failed to read request body"}`, http.StatusBadRequest)
		return
	}

	// Detect if streaming is requested
	isStreaming := detectStreaming(reqBody, providerName)

	// Create upstream request
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(reqBody))
	if err != nil {
		http.Error(w, `{"error":"upstream_request","message":"Failed to create upstream request"}`, http.StatusInternalServerError)
		return
	}

	// Copy headers (except auth-related)
	for key, vals := range r.Header {
		lower := strings.ToLower(key)
		if lower == "authorization" || lower == "x-api-key" || lower == "x-goog-api-key" || lower == "host" {
			continue
		}
		for _, v := range vals {
			upstreamReq.Header.Add(key, v)
		}
	}

	// Set the real API key
	headerName, headerValue := provider.SetAuthHeader(apiKey)
	upstreamReq.Header.Set(headerName, headerValue)

	// Make the upstream request
	startTime := time.Now()
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		log.Printf("Upstream request to %s failed: %v", providerName, err)
		http.Error(w, `{"error":"upstream_error","message":"Failed to reach upstream provider"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}

	if isStreaming && resp.StatusCode == http.StatusOK {
		handleStreamingResponse(w, resp, provider, instanceName, startTime)
	} else {
		handleNonStreamingResponse(w, resp, provider, instanceName, startTime)
	}
}

func handleStreamingResponse(w http.ResponseWriter, resp *http.Response, provider providers.Provider, instanceName string, startTime time.Time) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)

	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("ResponseWriter does not support Flusher")
		io.Copy(w, resp.Body)
		return
	}

	parser := &StreamingParser{ParserType: provider.ParserType}
	flushWriter := &flushingWriter{w: w, f: flusher}
	if err := parser.ParseSSEStream(resp.Body, flushWriter); err != nil {
		log.Printf("SSE parse error for %s: %v", provider.Name, err)
	}

	// Record usage
	duration := time.Since(startTime)
	recordUsage(instanceName, provider.Name, parser.Result, resp.StatusCode, duration)
}

func handleNonStreamingResponse(w http.ResponseWriter, resp *http.Response, provider providers.Provider, instanceName string, startTime time.Time) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read upstream response: %v", err)
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	w.WriteHeader(resp.StatusCode)
	w.Write(body)

	// Extract usage from response body
	if resp.StatusCode == http.StatusOK {
		result := ParseNonStreamingBody(provider.ParserType, body)
		duration := time.Since(startTime)
		recordUsage(instanceName, provider.Name, result, resp.StatusCode, duration)
	}
}

func resolveAPIKey(providerName, instanceName string) string {
	// Check instance-specific key first
	var instKey database.ProviderKey
	if err := database.DB.Where("provider_name = ? AND scope = ?", providerName, instanceName).First(&instKey).Error; err == nil {
		return instKey.KeyValue
	}

	// Fall back to global key
	var globalKey database.ProviderKey
	if err := database.DB.Where("provider_name = ? AND scope = ?", providerName, "global").First(&globalKey).Error; err == nil {
		return globalKey.KeyValue
	}

	return ""
}

func detectStreaming(body []byte, providerName string) bool {
	// Check for "stream": true in the request body
	return bytes.Contains(body, []byte(`"stream":true`)) ||
		bytes.Contains(body, []byte(`"stream": true`))
}

func recordUsage(instanceName, providerName string, result UsageResult, statusCode int, duration time.Duration) {
	cost := estimateCost(providerName, result.Model, result.InputTokens, result.OutputTokens)

	record := database.UsageRecord{
		InstanceName:       instanceName,
		Provider:           providerName,
		Model:              result.Model,
		InputTokens:        result.InputTokens,
		OutputTokens:       result.OutputTokens,
		EstimatedCostMicro: cost,
		StatusCode:         statusCode,
		DurationMs:         duration.Milliseconds(),
	}

	if err := database.DB.Create(&record).Error; err != nil {
		log.Printf("Failed to record usage: %v", err)
	}

	// Invalidate budget cache since spend changed
	InvalidateBudgetCache(instanceName)
}

func estimateCost(providerName, model string, inputTokens, outputTokens int64) int64 {
	if model == "" {
		return 0
	}

	var pricing []database.ModelPricing
	database.DB.Where("provider = ?", providerName).Find(&pricing)

	for _, p := range pricing {
		if strings.Contains(model, p.ModelPattern) {
			inputCost := (inputTokens * p.InputPriceMicro) / 1_000_000
			outputCost := (outputTokens * p.OutputPriceMicro) / 1_000_000
			return inputCost + outputCost
		}
	}

	return 0
}

type flushingWriter struct {
	w io.Writer
	f http.Flusher
}

func (fw *flushingWriter) Write(p []byte) (n int, err error) {
	n, err = fw.w.Write(p)
	if fw.f != nil {
		fw.f.Flush()
	}
	return
}

func (fw *flushingWriter) Flush() {
	if fw.f != nil {
		fw.f.Flush()
	}
}
