package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/crypto"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/go-chi/chi/v5"
)

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
		log.Printf("Failed to accept websocket: %v", err)
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
		log.Printf("Failed to get gateway URL for %s: %v", inst.Name, err)
		clientConn.Close(4500, truncate(err.Error(), 120))
		return
	}

	// Decrypt gateway token
	var gatewayToken string
	if inst.GatewayToken != "" {
		gatewayToken, _ = crypto.Decrypt(inst.GatewayToken)
	}

	// Connect to gateway
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dialOpts := &websocket.DialOptions{}
	if t := orch.GetHTTPTransport(); t != nil {
		dialOpts.HTTPClient = &http.Client{Transport: t}
	}

	gwConn, _, err := websocket.Dial(dialCtx, gwURL, dialOpts)
	if err != nil {
		log.Printf("Failed to connect to gateway at %s: %v", gwURL, err)
		clientConn.Close(4502, "Cannot connect to gateway")
		return
	}
	defer gwConn.CloseNow()

	// Send connect handshake
	connectMsg := map[string]string{
		"type": "connect",
		"role": "operator",
	}
	if gatewayToken != "" {
		connectMsg["token"] = gatewayToken
	}
	connectJSON, _ := json.Marshal(connectMsg)
	if err := gwConn.Write(ctx, websocket.MessageText, connectJSON); err != nil {
		clientConn.Close(4502, "Failed to send handshake")
		return
	}

	// Wait for handshake response
	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, 10*time.Second)
	defer handshakeCancel()

	_, data, err := gwConn.Read(handshakeCtx)
	if err != nil {
		clientConn.Close(4504, "Gateway handshake timeout")
		return
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(data, &resp); err == nil {
		if resp["type"] == "error" {
			msg := "Gateway auth failed"
			if m, ok := resp["message"].(string); ok {
				msg = m
			}
			clientConn.Close(4401, msg)
			return
		}
	}

	// Notify browser
	connectedMsg, _ := json.Marshal(map[string]string{"type": "connected"})
	clientConn.Write(ctx, websocket.MessageText, connectedMsg)

	// Bidirectional relay
	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	// Browser → Gateway
	go func() {
		defer relayCancel()
		for {
			msgType, data, err := clientConn.Read(relayCtx)
			if err != nil {
				return
			}
			if err := gwConn.Write(relayCtx, msgType, data); err != nil {
				return
			}
		}
	}()

	// Gateway → Browser
	func() {
		defer relayCancel()
		for {
			msgType, data, err := gwConn.Read(relayCtx)
			if err != nil {
				return
			}
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
