package browserauthn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// DialFunc opens a raw byte stream to the browser's CDP port (9222). It must
// NOT spawn a browser session — the manager only ever talks to sessions that
// are already running.
type DialFunc func(ctx context.Context, instanceID uint) (io.ReadWriteCloser, error)

// cdpMessage is the union of CDP command responses and events.
type cdpMessage struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cdpError) Error() string { return fmt.Sprintf("cdp error %d: %s", e.Code, e.Message) }

// cdpEvent is an event delivered to the client's event channel.
type cdpEvent struct {
	Method    string
	Params    json.RawMessage
	SessionID string
}

// cdpConn is a minimal flattened-session CDP JSON-RPC client.
type cdpConn struct {
	ws     *websocket.Conn
	nextID atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan cdpMessage

	// Events are queued without bound so the read loop never blocks on a
	// slow consumer (a blocked read loop would also stall command responses
	// the consumer may be waiting for).
	qmu    sync.Mutex
	queue  []cdpEvent
	notify chan struct{}

	events chan cdpEvent
	done   chan struct{}
	err    error
}

// rwcConn adapts an io.ReadWriteCloser to net.Conn for providers whose dial
// result is not already a net.Conn.
type rwcConn struct{ io.ReadWriteCloser }

func (rwcConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (rwcConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (rwcConn) SetDeadline(time.Time) error      { return nil }
func (rwcConn) SetReadDeadline(time.Time) error  { return nil }
func (rwcConn) SetWriteDeadline(time.Time) error { return nil }

// httpClientFor returns an HTTP client whose every connection goes through
// dial (so "127.0.0.1:9222" resolves inside the browser pod).
func httpClientFor(dial DialFunc, instanceID uint) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				c, err := dial(ctx, instanceID)
				if err != nil {
					return nil, err
				}
				if nc, ok := c.(net.Conn); ok {
					return nc, nil
				}
				return rwcConn{c}, nil
			},
			DisableKeepAlives: true,
		},
	}
}

// dialCDP discovers the browser websocket URL via /json/version and opens a
// browser-level CDP connection.
func dialCDP(ctx context.Context, dial DialFunc, instanceID uint) (*cdpConn, error) {
	hc := httpClientFor(dial, instanceID)

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://127.0.0.1:9222/json/version", nil)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cdp /json/version: %w", err)
	}
	var ver struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	err = json.NewDecoder(resp.Body).Decode(&ver)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("decode /json/version: %w", err)
	}
	if ver.WebSocketDebuggerURL == "" {
		return nil, errors.New("cdp /json/version: no webSocketDebuggerUrl")
	}

	ws, _, err := websocket.Dial(reqCtx, ver.WebSocketDebuggerURL, &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		return nil, fmt.Errorf("cdp websocket dial: %w", err)
	}
	ws.SetReadLimit(64 << 20)
	return newCDPConn(ws), nil
}

func newCDPConn(ws *websocket.Conn) *cdpConn {
	c := &cdpConn{
		ws:      ws,
		pending: make(map[int64]chan cdpMessage),
		notify:  make(chan struct{}, 1),
		events:  make(chan cdpEvent),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	go c.pump()
	return c
}

// pump delivers queued events in order; closes events once the connection
// is gone and the queue is drained.
func (c *cdpConn) pump() {
	defer close(c.events)
	for {
		c.qmu.Lock()
		if len(c.queue) == 0 {
			c.qmu.Unlock()
			select {
			case <-c.notify:
				continue
			case <-c.done:
				c.qmu.Lock()
				empty := len(c.queue) == 0
				c.qmu.Unlock()
				if empty {
					return
				}
				continue
			}
		}
		ev := c.queue[0]
		c.queue = c.queue[1:]
		c.qmu.Unlock()
		c.events <- ev
	}
}

func (c *cdpConn) readLoop() {
	ctx := context.Background()
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			c.mu.Lock()
			c.err = err
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			close(c.done)
			return
		}
		var msg cdpMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.ID != 0 {
			c.mu.Lock()
			ch := c.pending[msg.ID]
			delete(c.pending, msg.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		if msg.Method != "" {
			c.qmu.Lock()
			c.queue = append(c.queue, cdpEvent{Method: msg.Method, Params: msg.Params, SessionID: msg.SessionID})
			c.qmu.Unlock()
			select {
			case c.notify <- struct{}{}:
			default:
			}
		}
	}
}

// Call sends a command (optionally on a flattened child session) and waits
// for the response. result may be nil.
func (c *cdpConn) Call(ctx context.Context, sessionID, method string, params, result interface{}) error {
	id := c.nextID.Add(1)
	msg := map[string]interface{}{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if sessionID != "" {
		msg["sessionId"] = sessionID
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	ch := make(chan cdpMessage, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return fmt.Errorf("cdp connection closed: %w", c.err)
	}
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.ws.Write(ctx, websocket.MessageText, data); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}

	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case resp, ok := <-ch:
		if !ok {
			return errors.New("cdp connection closed")
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	case <-callCtx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("cdp %s: %w", method, callCtx.Err())
	}
}

// Events returns the event stream; it is closed after the connection drops
// and all queued events were delivered.
func (c *cdpConn) Events() <-chan cdpEvent { return c.events }

// Done is closed when the connection drops.
func (c *cdpConn) Done() <-chan struct{} { return c.done }

func (c *cdpConn) Close() error { return c.ws.Close(websocket.StatusNormalClosure, "") }
