package handlers

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/tunnel"
	"github.com/go-chi/chi/v5"
)

// Frame type markers matching the agent-side framing protocol.
const (
	frameBinary  byte = 0x01
	frameControl byte = 0x02
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

	tc := tunnel.Manager.Get(uint(id))
	if tc == nil {
		clientConn.Close(4500, "No tunnel available")
		return
	}

	stream, err := tc.OpenChannel(ctx, tunnel.ChannelTerminal)
	if err != nil {
		log.Printf("Failed to open terminal tunnel stream for %s: %v", inst.Name, err)
		clientConn.Close(4500, "Failed to open tunnel stream")
		return
	}
	defer stream.Close()

	// Send the init header with default size; the browser will send a resize
	// shortly after connecting with the actual terminal dimensions.
	initHeader, _ := json.Marshal(map[string]uint16{"cols": 80, "rows": 24})
	if _, err := stream.Write(append(initHeader, '\n')); err != nil {
		log.Printf("Failed to write terminal init header: %v", err)
		clientConn.Close(4500, "Failed to initialize terminal")
		return
	}

	// Increase read limit for terminal traffic.
	clientConn.SetReadLimit(1024 * 1024)

	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	// Tunnel stream -> Browser: read framed data, send binary frames as-is to the WebSocket.
	go func() {
		defer relayCancel()
		buf := make([]byte, 32*1024)
		for {
			n, err := stream.Read(buf)
			if n > 0 {
				// The agent wraps PTY output in [0x01][data] frames.
				// Strip the frame header and send raw PTY data to the browser.
				data := buf[:n]
				if len(data) > 0 && data[0] == frameBinary {
					data = data[1:]
				}
				if len(data) > 0 {
					if err := clientConn.Write(relayCtx, websocket.MessageBinary, data); err != nil {
						return
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Browser -> Tunnel stream: binary = PTY data, text = control JSON.
	func() {
		defer relayCancel()
		for {
			msgType, data, err := clientConn.Read(relayCtx)
			if err != nil {
				return
			}

			if msgType == websocket.MessageBinary {
				// Wrap as binary frame: [0x01][data]
				frame := make([]byte, 1+len(data))
				frame[0] = frameBinary
				copy(frame[1:], data)
				if _, err := stream.Write(frame); err != nil {
					return
				}
			} else {
				// Text message: parse as JSON control (e.g. resize).
				var msg termResizeMsg
				if err := json.Unmarshal(data, &msg); err != nil {
					continue
				}
				if msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
					// Build control frame: [0x02][varint len][json]
					jsonData, _ := json.Marshal(msg)
					var lenBuf [binary.MaxVarintLen64]byte
					n := binary.PutUvarint(lenBuf[:], uint64(len(jsonData)))
					frame := make([]byte, 1+n+len(jsonData))
					frame[0] = frameControl
					copy(frame[1:], lenBuf[:n])
					copy(frame[1+n:], jsonData)
					if _, err := stream.Write(frame); err != nil {
						return
					}
				}
			}
		}
	}()

	clientConn.Close(websocket.StatusNormalClosure, "")
}
