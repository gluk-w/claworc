package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/crypto"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const chatSessionKey = "agent:main:main"

func ChatProxy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid instance ID", http.StatusBadRequest)
		return
	}

	if !middleware.CanAccessInstance(r, uint(id)) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// Accept client WebSocket
	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("[chat] Failed to accept websocket: %v", err)
		return
	}
	defer clientConn.CloseNow()

	ctx := r.Context()

	// Look up instance
	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		clientConn.Close(4004, "Instance not found")
		return
	}

	// Check instance is running
	orch := orchestrator.Get()
	if orch == nil {
		clientConn.Close(4500, "No orchestrator available")
		return
	}

	status, _ := orch.GetInstanceStatus(ctx, inst.Name)
	if status != "running" {
		clientConn.Close(4003, "Instance not running")
		return
	}

	// Get gateway URL
	gwURL, err := orch.GetGatewayWSURL(ctx, inst.Name)
	if err != nil {
		log.Printf("[chat] Failed to get gateway URL for %s: %v", inst.Name, err)
		clientConn.Close(4500, truncate(err.Error(), 120))
		return
	}

	// Decrypt gateway token
	var gatewayToken string
	if inst.GatewayToken != "" {
		if tok, err := crypto.Decrypt(inst.GatewayToken); err == nil && tok != "" {
			gatewayToken = tok
		}
	}

	// Append token to URL (matching controlWSProxy pattern)
	if gatewayToken != "" {
		if strings.Contains(gwURL, "?") {
			gwURL += "&token=" + gatewayToken
		} else {
			gwURL += "?token=" + gatewayToken
		}
	}

	// Connect to gateway
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	origin, host := gatewayHost(gwURL)
	dialOpts := &websocket.DialOptions{
		Host:       host,
		HTTPHeader: http.Header{},
	}
	if origin != "" {
		dialOpts.HTTPHeader.Set("Origin", origin)
	}
	if t := orch.GetHTTPTransport(); t != nil {
		dialOpts.HTTPClient = &http.Client{Transport: t}
	}

	log.Printf("[chat] Connecting to gateway: %s", gwURL)
	gwConn, _, err := websocket.Dial(dialCtx, gwURL, dialOpts)
	if err != nil {
		log.Printf("[chat] Failed to connect to gateway at %s: %v", gwURL, err)
		clientConn.Close(4502, "Cannot connect to gateway")
		return
	}
	defer gwConn.CloseNow()

	clientConn.SetReadLimit(4 * 1024 * 1024)
	gwConn.SetReadLimit(4 * 1024 * 1024)

	// Phase 1: Read connect.challenge from gateway
	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, 10*time.Second)
	defer handshakeCancel()

	_, challengeData, err := gwConn.Read(handshakeCtx)
	if err != nil {
		log.Printf("[chat] Failed to read connect.challenge for %s: %v", inst.Name, err)
		clientConn.Close(4504, "Gateway handshake timeout")
		return
	}
	log.Printf("[chat] Received challenge: %s", string(challengeData))

	// Phase 2: Send connect request
	connectFrame := map[string]interface{}{
		"type":   "req",
		"id":     fmt.Sprintf("connect-%d", time.Now().UnixNano()),
		"method": "connect",
		"params": map[string]interface{}{
			"minProtocol": 3,
			"maxProtocol": 3,
			"client": map[string]interface{}{
				"id":       "openclaw-control-ui",
				"version":  "1.0.0",
				"platform": "linux",
				"mode":     "webchat",
			},
			"role":   "operator",
			"scopes": []string{"operator.admin"},
			"auth": map[string]interface{}{
				"token": gatewayToken,
			},
		},
	}
	connectJSON, _ := json.Marshal(connectFrame)
	log.Printf("[chat] Sending connect: %s", string(connectJSON))
	if err := gwConn.Write(ctx, websocket.MessageText, connectJSON); err != nil {
		log.Printf("[chat] Failed to send connect for %s: %v", inst.Name, err)
		clientConn.Close(4502, "Failed to send handshake")
		return
	}

	// Phase 3: Read hello-ok response (skip event frames)
	for {
		_, data, err := gwConn.Read(handshakeCtx)
		if err != nil {
			log.Printf("[chat] Handshake read error for %s: %v", inst.Name, err)
			clientConn.Close(4504, "Gateway handshake timeout")
			return
		}
		log.Printf("[chat] Handshake frame: %s", string(data))

		var resp map[string]interface{}
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}

		// Skip event frames
		if resp["type"] == "event" {
			continue
		}

		if resp["type"] == "res" {
			if ok, _ := resp["ok"].(bool); !ok {
				errObj, _ := resp["error"].(map[string]interface{})
				msg := "Gateway auth failed"
				if m, _ := errObj["message"].(string); m != "" {
					msg = m
				}
				log.Printf("[chat] Handshake error for %s: %s (full: %s)", inst.Name, msg, string(data))
				clientConn.Close(4401, truncate(msg, 120))
				return
			}
			log.Printf("[chat] Handshake OK for %s", inst.Name)
			break
		}
	}

	// Notify browser that connection is established
	connectedMsg, _ := json.Marshal(map[string]string{"type": "connected"})
	clientConn.Write(ctx, websocket.MessageText, connectedMsg)

	// Bidirectional relay with message translation
	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	var reqCounter int

	// Browser → Gateway (translate chat messages to gateway protocol)
	go func() {
		defer relayCancel()
		for {
			_, data, err := clientConn.Read(relayCtx)
			if err != nil {
				return
			}

			var browserMsg map[string]interface{}
			if err := json.Unmarshal(data, &browserMsg); err != nil {
				log.Printf("[chat] Invalid JSON from browser: %v", err)
				continue
			}

			msgType, _ := browserMsg["type"].(string)
			content, _ := browserMsg["content"].(string)

			if msgType != "chat" || content == "" {
				log.Printf("[chat] Ignoring non-chat frame from browser: %s", string(data))
				continue
			}

			// Translate to gateway protocol
			reqCounter++
			var gwFrame map[string]interface{}

			if strings.TrimSpace(content) == "/new" || strings.TrimSpace(content) == "/reset" {
				gwFrame = map[string]interface{}{
					"type":   "req",
					"id":     fmt.Sprintf("reset-%d", reqCounter),
					"method": "sessions.reset",
					"params": map[string]interface{}{
						"key": chatSessionKey,
					},
				}
			} else {
				gwFrame = map[string]interface{}{
					"type":   "req",
					"id":     fmt.Sprintf("chat-%d", reqCounter),
					"method": "chat.send",
					"params": map[string]interface{}{
						"sessionKey":     chatSessionKey,
						"message":        content,
						"idempotencyKey": uuid.New().String(),
					},
				}
			}

			gwJSON, _ := json.Marshal(gwFrame)
			log.Printf("[chat] Browser→Gateway: %s", string(gwJSON))
			if err := gwConn.Write(relayCtx, websocket.MessageText, gwJSON); err != nil {
				return
			}
		}
	}()

	// Gateway → Browser (forward all frames, log for debugging)
	func() {
		defer relayCancel()
		for {
			msgType, data, err := gwConn.Read(relayCtx)
			if err != nil {
				log.Printf("[chat] Gateway read error: %v", err)
				return
			}
			log.Printf("[chat] Gateway→Browser: %s", string(data))
			if err := clientConn.Write(relayCtx, msgType, data); err != nil {
				return
			}
		}
	}()

	clientConn.Close(websocket.StatusNormalClosure, "")
	gwConn.Close(websocket.StatusNormalClosure, "")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// gatewayHost derives the gateway's internal host:port from the WS URL.
// For K8s API proxy URLs it reconstructs the cluster DNS name;
// for direct URLs it returns scheme+host as-is.
func gatewayHost(gwURL string) (origin, host string) {
	u, err := url.Parse(gwURL)
	if err != nil {
		return "", ""
	}

	// K8s API proxy: .../api/v1/namespaces/{ns}/services/{svc}:{port}/proxy
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	var ns, svc, port string
	for i := 0; i < len(parts)-1; i++ {
		switch parts[i] {
		case "namespaces":
			ns = parts[i+1]
		case "services":
			sp := strings.SplitN(parts[i+1], ":", 2)
			svc = sp[0]
			if len(sp) > 1 {
				port = sp[1]
			}
		}
	}
	if ns != "" && svc != "" && port != "" {
		h := fmt.Sprintf("%s.%s.svc.cluster.local:%s", svc, ns, port)
		return "http://" + h, h
	}

	// Direct URL (Docker / in-cluster)
	scheme := "http"
	if u.Scheme == "wss" || u.Scheme == "https" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, u.Host), u.Host
}
