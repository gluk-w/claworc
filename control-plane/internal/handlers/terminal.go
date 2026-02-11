package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/go-chi/chi/v5"
)

type termResizeMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func TerminalWSProxy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid instance ID", http.StatusBadRequest)
		return
	}

	if !middleware.CanAccessInstance(r, uint(id)) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("Failed to accept terminal websocket: %v", err)
		return
	}
	defer clientConn.CloseNow()

	ctx := r.Context()

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		clientConn.Close(4004, "Instance not found")
		return
	}

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

	session, err := orch.ExecInteractive(ctx, inst.Name, []string{"su", "-", "claworc"})
	if err != nil {
		log.Printf("Failed to start exec session for %s: %v", inst.Name, err)
		clientConn.Close(4500, "Failed to start shell")
		return
	}
	defer session.Close()

	// Increase read limit for terminal traffic
	clientConn.SetReadLimit(1024 * 1024)

	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	// Shell stdout → Browser (binary WebSocket messages)
	go func() {
		defer relayCancel()
		buf := make([]byte, 32*1024)
		for {
			n, err := session.Stdout.Read(buf)
			if n > 0 {
				if err := clientConn.Write(relayCtx, websocket.MessageBinary, buf[:n]); err != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Browser → Shell stdin (binary = data, text = control JSON)
	func() {
		defer relayCancel()
		for {
			msgType, data, err := clientConn.Read(relayCtx)
			if err != nil {
				return
			}

			if msgType == websocket.MessageBinary {
				if _, err := session.Stdin.Write(data); err != nil {
					return
				}
			} else {
				// Text message: parse as JSON control
				var msg termResizeMsg
				if err := json.Unmarshal(data, &msg); err != nil {
					continue
				}
				if msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
					session.Resize(msg.Cols, msg.Rows)
				}
			}
		}
	}()

	clientConn.Close(websocket.StatusNormalClosure, "")
}
