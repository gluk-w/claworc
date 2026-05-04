package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// desktopWebsockifyOverDialer proxies the noVNC websockify WebSocket through
// the per-instance browser-pod SSH tunnel, with an RFB stream filter that
// drops ClientSetDesktopSize messages from non-primary viewers. The first
// connected viewer per instance is primary; the X display geometry follows
// only that viewer's panel size. When the primary disconnects the next-oldest
// viewer is promoted and its last-attempted SetDesktopSize is replayed
// upstream so the X display snaps to the new primary's panel.
func desktopWebsockifyOverDialer(w http.ResponseWriter, r *http.Request, instanceID uint, transport *http.Transport, path string) {
	requestedProtocol := r.Header.Get("Sec-WebSocket-Protocol")
	var subprotocols []string
	if requestedProtocol != "" {
		subprotocols = strings.Split(requestedProtocol, ", ")
	}

	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       subprotocols,
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("Desktop websockify: accept error: %v", err)
		return
	}
	defer clientConn.CloseNow()

	wsURL := fmt.Sprintf("ws://browser-pod/%s", path)
	if r.URL.RawQuery != "" {
		wsURL += "?" + r.URL.RawQuery
	}

	ctx := r.Context()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	log.Printf("Desktop websockify: %s → %s", utils.SanitizeForLog(r.URL.Path), utils.SanitizeForLog(wsURL))
	upstreamConn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		Subprotocols: subprotocols,
		HTTPClient:   &http.Client{Transport: transport},
	})
	if err != nil {
		log.Printf("Desktop websockify: dial error %s: %v", utils.SanitizeForLog(wsURL), err)
		clientConn.Close(4502, "Cannot connect to browser pod")
		return
	}
	defer upstreamConn.CloseNow()

	relayWebsockifyWithFilter(ctx, instanceID, clientConn, upstreamConn)
}

// desktopWebsockifyToLocalPort is the legacy (combined-image) variant: it
// proxies websockify through the agent's reverse SSH tunnel local port. The
// RFB filter behaviour is identical.
func desktopWebsockifyToLocalPort(w http.ResponseWriter, r *http.Request, instanceID uint, port int, path string) {
	requestedProtocol := r.Header.Get("Sec-WebSocket-Protocol")
	var subprotocols []string
	if requestedProtocol != "" {
		subprotocols = strings.Split(requestedProtocol, ", ")
	}

	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       subprotocols,
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("Desktop websockify: accept error: %v", err)
		return
	}
	defer clientConn.CloseNow()

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/%s", port, path)
	if r.URL.RawQuery != "" {
		wsURL += "?" + r.URL.RawQuery
	}

	ctx := r.Context()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	log.Printf("Desktop websockify (legacy): %s → %s", utils.SanitizeForLog(r.URL.Path), utils.SanitizeForLog(wsURL))
	upstreamConn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		Subprotocols: subprotocols,
	})
	if err != nil {
		log.Printf("Desktop websockify (legacy): dial error %s: %v", utils.SanitizeForLog(wsURL), err)
		clientConn.Close(4502, "Cannot connect to service via tunnel")
		return
	}
	defer upstreamConn.CloseNow()

	relayWebsockifyWithFilter(ctx, instanceID, clientConn, upstreamConn)
}

// relayWebsockifyWithFilter pumps bytes between client and upstream noVNC.
// Client→server bytes pass through an RFB filter that drops SetDesktopSize
// from non-primary viewers; the filter also exposes an injection channel
// used to replay the last-attempted SetDesktopSize when this session is
// promoted to primary.
func relayWebsockifyWithFilter(ctx context.Context, instanceID uint, clientConn, upstreamConn *websocket.Conn) {
	clientConn.SetReadLimit(4 * 1024 * 1024)
	upstreamConn.SetReadLimit(4 * 1024 * 1024)

	session := viewers.Join(instanceID)
	log.Printf("Desktop websockify: instance=%d viewer joined primary=%v", instanceID, session.isPrimary())
	defer func() {
		viewers.Leave(instanceID, session)
		log.Printf("Desktop websockify: instance=%d viewer left primary=%v", instanceID, session.isPrimary())
	}()

	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	// Buffered injection channel: registry pushes replay bytes here when this
	// session is promoted, the writer goroutine forwards them upstream.
	inject := make(chan []byte, 4)
	session.inject = func(b []byte) {
		select {
		case inject <- b:
		default:
			// Channel full — replay slot already in flight.
		}
	}

	filter := newRFBClientFilter(session)

	// Client → Upstream (filtered).
	go func() {
		defer relayCancel()
		for {
			msgType, data, err := clientConn.Read(relayCtx)
			if err != nil {
				return
			}
			out := data
			if msgType == websocket.MessageBinary {
				out = filter.Process(data)
			}
			if len(out) == 0 {
				continue
			}
			if err := upstreamConn.Write(relayCtx, msgType, out); err != nil {
				return
			}
		}
	}()

	// Inject → Upstream. Replay bytes pushed by the registry when this
	// session is promoted to primary. coder/websocket Write is safe for
	// concurrent use, so this runs alongside the client→upstream goroutine.
	go func() {
		for {
			select {
			case <-relayCtx.Done():
				return
			case msg, ok := <-inject:
				if !ok {
					return
				}
				if err := upstreamConn.Write(relayCtx, websocket.MessageBinary, msg); err != nil {
					return
				}
			}
		}
	}()

	// Upstream → Client (raw).
	func() {
		defer relayCancel()
		for {
			msgType, data, err := upstreamConn.Read(relayCtx)
			if err != nil {
				return
			}
			if err := clientConn.Write(relayCtx, msgType, data); err != nil {
				return
			}
		}
	}()

	clientConn.Close(websocket.StatusNormalClosure, "")
	upstreamConn.Close(websocket.StatusNormalClosure, "")
}
