// gateway.go implements the internal LLM proxy gateway.
//
// The gateway listens on 127.0.0.1:40001 (internal only — reachable from containers
// only via SSH agent-listener tunnel). It accepts OpenAI-compatible requests with
// a Bearer token (sk-gw-*), looks up the real provider URL and API key, and proxies
// the request to the actual LLM provider.
//
// All providers use the OpenAI wire format (OpenAI-compat base URLs), so the gateway
// never needs to translate between formats.

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

	"github.com/gluk-w/claworc/control-plane/internal/crypto"
	"github.com/gluk-w/claworc/control-plane/internal/database"
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

// authAndResolve validates the Bearer token and returns the provider info and real API key.
func authAndResolve(r *http.Request) (instanceID, providerID uint, baseURL, apiKey string, err error) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		err = fmt.Errorf("missing or invalid Authorization header")
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")

	var key database.LLMGatewayKey
	if dbErr := database.DB.Preload("Provider").Where("gateway_key = ?", token).First(&key).Error; dbErr != nil {
		err = fmt.Errorf("invalid gateway key")
		return
	}

	instanceID = key.InstanceID
	providerID = key.ProviderID
	baseURL = strings.TrimRight(key.Provider.BaseURL, "/")
	apiKey = resolveRealAPIKey(instanceID, key.Provider.Key)
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
		if decrypted, err := crypto.Decrypt(instKey.KeyValue); err == nil {
			return decrypted
		}
	}

	// Global setting
	if val, err := database.GetSetting("api_key:" + keyName); err == nil && val != "" {
		if decrypted, err := crypto.Decrypt(val); err == nil {
			return decrypted
		}
	}

	return ""
}

// handleProxy is the single handler for all gateway requests.
func handleProxy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	instanceID, providerID, baseURL, apiKey, err := authAndResolve(r)
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
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, `{"error":{"message":"failed to build upstream request"}}`, http.StatusInternalServerError)
		return
	}

	// Copy headers, replacing Authorization with the real API key
	for k, vs := range r.Header {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Host") {
			continue
		}
		for _, v := range vs {
			upstreamReq.Header.Add(k, v)
		}
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+apiKey)
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
			var usage struct {
				Usage struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal(respBody, &usage) == nil {
				inputTokens = usage.Usage.PromptTokens
				outputTokens = usage.Usage.CompletionTokens
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
