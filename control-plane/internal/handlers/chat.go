package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const chatSessionKey = "browser"

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

	// Get gateway tunnel port
	port, err := getTunnelPort(uint(id), "gateway")
	if err != nil {
		log.Printf("[chat] No gateway tunnel for instance %d: %v", id, err)
		clientConn.Close(4500, truncate(err.Error(), 120))
		return
	}

	// Decrypt gateway token
	var gatewayToken string
	if inst.GatewayToken != "" {
		if tok, err := utils.Decrypt(inst.GatewayToken); err == nil && tok != "" {
			gatewayToken = tok
		}
	}

	gwConn, err := sshproxy.DialGateway(ctx, port, gatewayToken)
	if err != nil {
		log.Printf("[chat] Gateway dial/handshake failed for %s: %v", inst.Name, err)
		clientConn.Close(4502, truncate(err.Error(), 120))
		return
	}
	defer gwConn.CloseNow()

	clientConn.SetReadLimit(4 * 1024 * 1024)

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

			trimmedContent := strings.TrimSpace(content)
			if trimmedContent == "/new" || trimmedContent == "/reset" {
				gwFrame = map[string]interface{}{
					"type":   "req",
					"id":     fmt.Sprintf("reset-%d", reqCounter),
					"method": "sessions.reset",
					"params": map[string]interface{}{
						"key": chatSessionKey,
					},
				}
			} else if trimmedContent == "/stop" {
				gwFrame = map[string]interface{}{
					"type":   "req",
					"id":     fmt.Sprintf("abort-%d", reqCounter),
					"method": "chat.abort",
					"params": map[string]interface{}{
						"sessionKey": chatSessionKey,
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
