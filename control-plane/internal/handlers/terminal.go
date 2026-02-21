// Package handlers implements HTTP and WebSocket handlers for the control plane API.
//
// terminal.go provides the WebSocket handler for interactive terminal sessions.
//
// ## Terminal Session Architecture
//
// The terminal system supports multiple concurrent sessions per instance with
// session persistence across WebSocket disconnects:
//
//	Browser ←WebSocket→ TerminalWSProxy ←→ SessionManager ←→ SSH Terminal ←→ Remote Shell
//
// ## Session Lifecycle
//
//  1. New session: Client connects without session_id query param.
//     A new SSH terminal session is created and managed by the SessionManager.
//     The session ID is sent to the client in the first message.
//
//  2. Reconnect: Client connects with ?session_id=<id>.
//     The scrollback buffer is replayed so the client sees output produced
//     while disconnected, then live streaming resumes.
//
//  3. Disconnect: When the WebSocket closes, the SSH session stays alive
//     (state=detached). The session can be reconnected later.
//
//  4. Close: Sessions are closed when explicitly terminated via the API,
//     when the SSH session ends, or when idle cleanup runs.
//
// ## Message Protocol
//
// Client → Server:
//   - Binary: raw terminal input (written to SSH stdin)
//   - JSON text: {type: "input", data: "..."} — alternative text input
//   - JSON text: {type: "resize", cols: N, rows: M} — resize terminal
//   - JSON text: {type: "ping"} — keepalive
//
// Server → Client:
//   - Binary: terminal output from SSH stdout
//   - JSON text: {type: "pong"} — keepalive response
//   - JSON text: {type: "session", session_id: "...", state: "..."} — session metadata (sent first)
//
// ## Limitations
//
//   - Session persistence relies on the SSH connection staying alive.
//     If the SSH connection drops, all sessions on that connection are lost.
//   - Scrollback buffer has a configurable max size (default 1MB).
//     Output beyond this limit is trimmed from the front.
//   - Recording (when enabled) captures all I/O with timestamps but has
//     no automatic disk persistence — data is in-memory only.
//   - Terminal resize only affects new output; existing scrollback content
//     retains its original formatting.
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
	"golang.org/x/crypto/ssh"
)

// termMsg is a generic JSON message exchanged over the terminal WebSocket.
// All messages have a "type" field; additional fields depend on the type.
type termMsg struct {
	Type      string `json:"type"`
	Data      string `json:"data,omitempty"`
	Cols      uint16 `json:"cols,omitempty"`
	Rows      uint16 `json:"rows,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	State     string `json:"state,omitempty"`
}

// TerminalWSProxy handles WebSocket connections for interactive terminal sessions.
//
// Query parameters:
//   - session_id: (optional) ID of an existing session to reconnect to.
//     If omitted, a new session is created.
//
// When a SessionManager is available (normal operation), sessions are tracked
// and persist across WebSocket disconnects. When no SessionManager is present
// (legacy/test mode), sessions use direct SSH relay and close on disconnect.
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

	// Get session manager (may be nil in legacy/test scenarios)
	sessMgr := sshtunnel.GetSessionManager()
	sessionID := r.URL.Query().Get("session_id")

	if sessMgr != nil {
		// Managed session mode — supports persistence and multi-session
		var userID uint
		if u := middleware.GetUser(r); u != nil {
			userID = u.ID
		}

		if sessionID != "" {
			handleReconnect(ctx, clientConn, sessMgr, sessionID, uint(id))
		} else {
			handleManagedSession(ctx, clientConn, sessMgr, sshClient, uint(id), userID, inst.Name)
		}
	} else {
		// Legacy mode: direct relay, no session persistence
		handleLegacySession(ctx, clientConn, sshClient, inst.Name)
	}

	clientConn.Close(websocket.StatusNormalClosure, "")
}

// handleReconnect attaches a WebSocket to an existing detached session,
// replays the scrollback buffer, then streams live output.
func handleReconnect(ctx context.Context, clientConn *websocket.Conn, sessMgr *sshterminal.SessionManager, sessionID string, instanceID uint) {
	ms := sessMgr.GetSession(sessionID)
	if ms == nil {
		clientConn.Close(4404, "Session not found")
		return
	}
	if ms.InstanceID != instanceID {
		clientConn.Close(4403, "Session belongs to different instance")
		return
	}
	if ms.State() == sshterminal.SessionClosed {
		clientConn.Close(4410, "Session is closed")
		return
	}
	ms.SetState(sshterminal.SessionActive)
	log.Printf("[terminal] reconnecting to session %s", sessionID)

	clientConn.SetReadLimit(1024 * 1024)

	// Send session metadata
	sendSessionMetadata(ctx, clientConn, ms)

	// Replay scrollback
	scrollbackData := ms.Scrollback.Snapshot()
	if len(scrollbackData) > 0 {
		if err := clientConn.Write(ctx, websocket.MessageBinary, scrollbackData); err != nil {
			return
		}
	}

	// Stream new data from where replay ended
	replayOffset := ms.Scrollback.Len()
	streamManagedSession(ctx, clientConn, ms, replayOffset)
}

// handleManagedSession creates a new session via the SessionManager and streams I/O.
func handleManagedSession(ctx context.Context, clientConn *websocket.Conn, sessMgr *sshterminal.SessionManager, sshClient *ssh.Client, instanceID, userID uint, instName string) {
	ms, err := sessMgr.CreateSession(ctx, sshClient, instanceID, userID, "")
	if err != nil {
		log.Printf("Failed to create terminal session for %s: %v", instName, err)
		clientConn.Close(4500, "Failed to start shell")
		return
	}

	clientConn.SetReadLimit(1024 * 1024)

	// Send session metadata
	sendSessionMetadata(ctx, clientConn, ms)

	// Stream all output from the beginning (offset 0 for new sessions)
	streamManagedSession(ctx, clientConn, ms, 0)
}

// streamManagedSession runs the bidirectional relay between WebSocket and
// a managed session's scrollback buffer / stdin. startOffset determines
// where to begin reading from the scrollback (0 for new sessions, or
// the replay end position for reconnections).
func streamManagedSession(ctx context.Context, clientConn *websocket.Conn, ms *sshterminal.ManagedSession, startOffset int) {
	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	// Scrollback → WebSocket
	// Uses a check-then-wait loop to avoid missing data that arrives
	// between snapshot reads and notify waits.
	go func() {
		defer relayCancel()
		offset := startOffset
		for {
			// First check if data is already available (no blocking)
			snapshot := ms.Scrollback.Snapshot()
			if len(snapshot) > offset {
				newData := snapshot[offset:]
				offset = len(snapshot)
				if err := clientConn.Write(relayCtx, websocket.MessageBinary, newData); err != nil {
					return
				}
			}
			if ms.Scrollback.IsClosed() {
				return
			}

			// Wait for new data or termination
			select {
			case <-relayCtx.Done():
				return
			case <-ms.Scrollback.Notify():
				// Loop back to check for data
			case <-ms.StdoutDone():
				// SSH session ended — send any remaining data
				snapshot := ms.Scrollback.Snapshot()
				if len(snapshot) > offset {
					clientConn.Write(relayCtx, websocket.MessageBinary, snapshot[offset:])
				}
				return
			}
		}
	}()

	// WebSocket → SSH stdin
	relayInput(relayCtx, clientConn, ms)

	// On disconnect, detach instead of close
	if ms.State() != sshterminal.SessionClosed {
		ms.SetState(sshterminal.SessionDetached)
		log.Printf("[terminal] session %s detached", ms.ID)
	}
}

// handleLegacySession provides backward-compatible direct relay without
// session management. Used when SessionManager is nil.
func handleLegacySession(ctx context.Context, clientConn *websocket.Conn, sshClient *ssh.Client, instName string) {
	session, err := sshterminal.CreateInteractiveSession(sshClient, "")
	if err != nil {
		log.Printf("Failed to start SSH terminal for %s: %v", instName, err)
		clientConn.Close(4500, "Failed to start shell")
		return
	}
	defer session.Close()

	clientConn.SetReadLimit(1024 * 1024)

	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	// SSH stdout → Browser
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

	// Browser → SSH stdin
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
}

// sendSessionMetadata sends session info to the WebSocket client.
func sendSessionMetadata(ctx context.Context, clientConn *websocket.Conn, ms *sshterminal.ManagedSession) {
	sessionMsg, _ := json.Marshal(termMsg{
		Type:      "session",
		SessionID: ms.ID,
		State:     string(ms.State()),
	})
	clientConn.Write(ctx, websocket.MessageText, sessionMsg)
}

// relayInput reads from the WebSocket and writes to the managed session's
// SSH stdin. It handles binary raw input, JSON input, resize, and ping messages.
func relayInput(ctx context.Context, clientConn *websocket.Conn, ms *sshterminal.ManagedSession) {
	for {
		msgType, data, err := clientConn.Read(ctx)
		if err != nil {
			return
		}

		if msgType == websocket.MessageBinary {
			if ms.Recording != nil {
				ms.Recording.RecordInput(data)
			}
			if _, err := ms.Terminal.Stdin.Write(data); err != nil {
				return
			}
		} else {
			var msg termMsg
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "input":
				if msg.Data != "" {
					inputBytes := []byte(msg.Data)
					if ms.Recording != nil {
						ms.Recording.RecordInput(inputBytes)
					}
					if _, err := ms.Terminal.Stdin.Write(inputBytes); err != nil {
						return
					}
				}
			case "resize":
				if msg.Cols > 0 && msg.Rows > 0 {
					ms.Terminal.Resize(msg.Cols, msg.Rows)
				}
			case "ping":
				pong, _ := json.Marshal(termMsg{Type: "pong"})
				if err := clientConn.Write(ctx, websocket.MessageText, pong); err != nil {
					return
				}
			}
		}
	}
}
