package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshterminal"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
	"github.com/go-chi/chi/v5"
)

// termMsg is a generic JSON message exchanged over the terminal WebSocket.
// All messages have a "type" field; additional fields depend on the type.
type termMsg struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
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

	// Get SSH client for instance from SSHManager
	sm := sshtunnel.GetSSHManager()
	if sm == nil {
		clientConn.Close(4500, "SSH manager not initialized")
		return
	}

	sshClient, err := sm.GetClient(inst.Name)
	if err != nil {
		log.Printf("No SSH connection for terminal %s: %v", inst.Name, err)
		clientConn.Close(4500, fmt.Sprintf("No SSH connection: %v", err))
		return
	}

	// Create interactive SSH terminal session
	session, err := sshterminal.CreateInteractiveSession(sshClient, "")
	if err != nil {
		log.Printf("Failed to start SSH terminal for %s: %v", inst.Name, err)
		clientConn.Close(4500, "Failed to start shell")
		return
	}
	defer session.Close()

	// Increase read limit for terminal traffic
	clientConn.SetReadLimit(1024 * 1024)

	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	// SSH stdout → Browser (binary WebSocket messages)
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

	// Browser → SSH stdin (binary = raw data, text = JSON control messages)
	func() {
		defer relayCancel()
		for {
			msgType, data, err := clientConn.Read(relayCtx)
			if err != nil {
				return
			}

			if msgType == websocket.MessageBinary {
				// Raw terminal input
				if _, err := session.Stdin.Write(data); err != nil {
					return
				}
			} else {
				// Text message: parse as JSON control
				var msg termMsg
				if err := json.Unmarshal(data, &msg); err != nil {
					continue
				}
				switch msg.Type {
				case "input":
					if msg.Data != "" {
						if _, err := session.Stdin.Write([]byte(msg.Data)); err != nil {
							return
						}
					}
				case "resize":
					if msg.Cols > 0 && msg.Rows > 0 {
						session.Resize(msg.Cols, msg.Rows)
					}
				case "ping":
					pong, _ := json.Marshal(termMsg{Type: "pong"})
					if err := clientConn.Write(relayCtx, websocket.MessageText, pong); err != nil {
						return
					}
				}
			}
		}
	}()

	clientConn.Close(websocket.StatusNormalClosure, "")
}
