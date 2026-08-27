package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/go-chi/chi/v5"
)

const chatSessionKey = "browser"

// ChatProxy relays the browser chat WebSocket to the instance's agent via
// the agentshim. Browser→server frames are unchanged ({type:"chat",
// content}); server→browser frames are a {"type":"connected"} handshake
// followed by normalized agentshim events serialized verbatim — the shim
// event schema (docs/shim.md) is the browser chat protocol.
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

	client, err := agentshim.DefaultFactory().ForInstance(ctx, uint(id))
	if err != nil {
		log.Printf("[chat] No agent client for instance %d: %v", id, err)
		clientConn.Close(4500, truncate(err.Error(), 120))
		return
	}

	sess, err := client.OpenSession(ctx, chatSessionKey)
	if err != nil {
		var te *agentshim.TransportError
		if errors.As(err, &te) {
			log.Printf("[chat] No gateway tunnel for instance %d: %v", id, err)
			clientConn.Close(4500, truncate(err.Error(), 120))
			return
		}
		log.Printf("[chat] Session open failed for %s: %v", inst.Name, err)
		clientConn.Close(4502, truncate(err.Error(), 120))
		return
	}
	defer sess.Close()

	clientConn.SetReadLimit(4 * 1024 * 1024)

	// Notify browser that connection is established
	connectedMsg, _ := json.Marshal(map[string]string{"type": "connected"})
	clientConn.Write(ctx, websocket.MessageText, connectedMsg)

	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	// Browser → Agent (translate chat frames to session verbs)
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

			var verbErr error
			switch strings.TrimSpace(content) {
			case "/new", "/reset":
				verbErr = sess.Reset(relayCtx)
			case "/stop":
				verbErr = sess.Abort(relayCtx)
			default:
				verbErr = sess.Send(relayCtx, content)
			}
			if verbErr != nil {
				return
			}
		}
	}()

	// Agent → Browser (normalized events, serialized verbatim)
	func() {
		defer relayCancel()
		for {
			ev, err := sess.Recv(relayCtx)
			if err != nil {
				log.Printf("[chat] Session read error: %v", err)
				return
			}
			evJSON, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if err := clientConn.Write(relayCtx, websocket.MessageText, evJSON); err != nil {
				return
			}
		}
	}()

	clientConn.Close(websocket.StatusNormalClosure, "")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
