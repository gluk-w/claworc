package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshterminal"
	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"
)

// terminalRateLimit defines the maximum number of messages allowed per second
// per WebSocket connection. Messages beyond this rate are dropped.
const terminalRateLimit = 200

// terminalRateBurst is the token bucket burst size, allowing short bursts
// of rapid input (e.g., paste operations) before rate limiting kicks in.
const terminalRateBurst = 200

// TermSessionMgr is set from main.go during init. When non-nil, terminal
// sessions persist after WebSocket disconnect and support reconnection.
var TermSessionMgr *sshterminal.SessionManager

type termResizeMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// TerminalWSProxy handles WebSocket connections for interactive terminal sessions.
//
// Query parameters:
//   - session_id: (optional) reconnect to an existing detached session. If omitted
//     or the referenced session doesn't exist, a new session is created.
//
// When TermSessionMgr is set, sessions persist after WebSocket disconnect and
// output is buffered in a scrollback history. On reconnect the scrollback is
// replayed so the client sees missed output. When TermSessionMgr is nil, each
// WebSocket gets a fresh ephemeral session destroyed on disconnect (legacy mode).
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

	if SSHMgr == nil {
		clientConn.Close(4500, "SSH manager not initialized")
		return
	}

	sshClient, err := SSHMgr.EnsureConnected(ctx, inst.ID, orch)
	if err != nil {
		log.Printf("Failed to get SSH connection for instance %d: %v", inst.ID, err)
		clientConn.Close(4500, "Failed to establish SSH connection")
		return
	}

	if TermSessionMgr != nil {
		handleManagedTerminal(ctx, clientConn, r, sshClient, inst.ID)
	} else {
		handleLegacyTerminal(ctx, clientConn, sshClient)
	}
}

// tokenBucket implements a simple token bucket rate limiter for terminal messages.
type tokenBucket struct {
	tokens     int
	maxTokens  int
	refillRate int // tokens added per second
	lastRefill time.Time
}

func newTokenBucket(maxTokens, refillRate int) *tokenBucket {
	return &tokenBucket{
		tokens:     maxTokens,
		maxTokens:  maxTokens,
		refillRate: refillRate,
		lastRefill: time.Now(),
	}
}

// allow checks if a message is allowed and consumes a token.
func (tb *tokenBucket) allow() bool {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill)
	tb.lastRefill = now

	// Refill tokens based on elapsed time
	tb.tokens += int(elapsed.Seconds() * float64(tb.refillRate))
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}

	if tb.tokens <= 0 {
		return false
	}
	tb.tokens--
	return true
}

// handleManagedTerminal uses SessionManager for session persistence, multiple
// concurrent sessions, history replay, and optional recording.
func handleManagedTerminal(ctx context.Context, clientConn *websocket.Conn, r *http.Request, sshClient *ssh.Client, instanceID uint) {
	sessionID := r.URL.Query().Get("session_id")

	var ms *sshterminal.ManagedSession

	// Try to reconnect to an existing session
	if sessionID != "" {
		ms = TermSessionMgr.GetSession(sessionID)
		if ms != nil && ms.InstanceID != instanceID {
			ms = nil // wrong instance
		}
		if ms != nil && ms.IsAttached() {
			clientConn.Close(4409, "Session already attached")
			return
		}
	}

	// Create a new session if needed
	if ms == nil {
		var createErr error
		ms, createErr = TermSessionMgr.CreateSession(sshClient, instanceID, "su - abc")
		if createErr != nil {
			log.Printf("Terminal session creation failed for instance %d: %v", instanceID, createErr)
			clientConn.Close(4500, "Failed to start shell")
			return
		}
		log.Printf("Terminal session created: session=%s instance=%d", ms.ID, instanceID)
	} else {
		log.Printf("Terminal session reconnected: session=%s instance=%d", ms.ID, instanceID)
	}

	clientConn.SetReadLimit(1024 * 1024)

	// Send session ID to client so it can reconnect later
	sessionInfo, _ := json.Marshal(map[string]string{
		"type":       "session_info",
		"session_id": ms.ID,
	})
	if err := clientConn.Write(ctx, websocket.MessageText, sessionInfo); err != nil {
		return
	}

	// Attach and replay history
	wsWriter := &wsOutputWriter{conn: clientConn, ctx: ctx}
	history := ms.Attach(wsWriter)
	defer func() {
		ms.Detach()
		log.Printf("Terminal session detached: session=%s instance=%d", ms.ID, instanceID)
	}()

	if len(history) > 0 {
		if err := clientConn.Write(ctx, websocket.MessageBinary, history); err != nil {
			return
		}
	}

	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	// Watch for session SSH process termination
	go func() {
		select {
		case <-ms.Done():
			relayCancel()
		case <-relayCtx.Done():
		}
	}()

	// Rate limiter for this connection
	limiter := newTokenBucket(terminalRateBurst, terminalRateLimit)

	// Browser -> Shell stdin
	func() {
		defer relayCancel()
		for {
			msgType, data, err := clientConn.Read(relayCtx)
			if err != nil {
				return
			}

			// Rate limit: drop messages that exceed the allowed rate
			if !limiter.allow() {
				continue
			}

			if msgType == websocket.MessageBinary {
				// Enforce per-message input size limit
				if len(data) > sshterminal.MaxInputMessageSize {
					log.Printf("Terminal input message too large: session=%s size=%d limit=%d", ms.ID, len(data), sshterminal.MaxInputMessageSize)
					continue
				}
				if _, err := ms.WriteInput(data); err != nil {
					return
				}
			} else {
				var msg termResizeMsg
				if err := json.Unmarshal(data, &msg); err != nil {
					continue
				}
				if msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
					// Clamp resize dimensions to safe upper bounds
					cols := msg.Cols
					rows := msg.Rows
					if cols > sshterminal.MaxResizeCols {
						cols = sshterminal.MaxResizeCols
					}
					if rows > sshterminal.MaxResizeRows {
						rows = sshterminal.MaxResizeRows
					}
					ms.Resize(cols, rows)
				}
			}
		}
	}()

	clientConn.Close(websocket.StatusNormalClosure, "")
}

// handleLegacyTerminal creates an ephemeral session destroyed on disconnect.
func handleLegacyTerminal(ctx context.Context, clientConn *websocket.Conn, sshClient *ssh.Client) {
	session, err := sshterminal.CreateInteractiveSession(sshClient, "su - abc")
	if err != nil {
		log.Printf("Legacy terminal session creation failed: %v", err)
		clientConn.Close(4500, "Failed to start shell")
		return
	}
	defer session.Close()

	log.Printf("Legacy terminal session started")

	clientConn.SetReadLimit(1024 * 1024)

	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	// Shell stdout -> Browser
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

	// Rate limiter for this connection
	limiter := newTokenBucket(terminalRateBurst, terminalRateLimit)

	// Browser -> Shell stdin
	func() {
		defer relayCancel()
		for {
			msgType, data, err := clientConn.Read(relayCtx)
			if err != nil {
				return
			}

			// Rate limit: drop messages that exceed the allowed rate
			if !limiter.allow() {
				continue
			}

			if msgType == websocket.MessageBinary {
				// Enforce per-message input size limit
				if len(data) > sshterminal.MaxInputMessageSize {
					continue
				}
				if _, err := session.Stdin.Write(data); err != nil {
					return
				}
			} else {
				var msg termResizeMsg
				if err := json.Unmarshal(data, &msg); err != nil {
					continue
				}
				if msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
					cols := msg.Cols
					rows := msg.Rows
					if cols > sshterminal.MaxResizeCols {
						cols = sshterminal.MaxResizeCols
					}
					if rows > sshterminal.MaxResizeRows {
						rows = sshterminal.MaxResizeRows
					}
					session.Resize(cols, rows)
				}
			}
		}
	}()

	log.Printf("Legacy terminal session ended")
	clientConn.Close(websocket.StatusNormalClosure, "")
}

// wsOutputWriter wraps a WebSocket connection to implement io.Writer.
type wsOutputWriter struct {
	conn *websocket.Conn
	ctx  context.Context
}

func (w *wsOutputWriter) Write(p []byte) (int, error) {
	if err := w.conn.Write(w.ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// ListTerminalSessions returns the active terminal sessions for an instance.
func ListTerminalSessions(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	if TermSessionMgr == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"sessions": []interface{}{},
		})
		return
	}

	sessions := TermSessionMgr.ListSessions(uint(id))

	type sessionResponse struct {
		ID        string `json:"id"`
		Shell     string `json:"shell"`
		Attached  bool   `json:"attached"`
		CreatedAt string `json:"created_at"`
	}

	resp := make([]sessionResponse, len(sessions))
	for i, s := range sessions {
		resp[i] = sessionResponse{
			ID:        s.ID,
			Shell:     s.Shell,
			Attached:  s.IsAttached(),
			CreatedAt: s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sessions": resp,
	})
}

// CloseTerminalSession terminates a specific terminal session.
func CloseTerminalSession(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	sessionID := chi.URLParam(r, "sessionId")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "Session ID required")
		return
	}

	if TermSessionMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "Session manager not initialized")
		return
	}

	ms := TermSessionMgr.GetSession(sessionID)
	if ms == nil || ms.InstanceID != uint(id) {
		writeError(w, http.StatusNotFound, "Session not found")
		return
	}

	if err := TermSessionMgr.CloseSession(sessionID); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to close session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "closed"})
}
