package openclawnative

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/google/uuid"
)

// OpenSession implements agentshim.Client: it dials the OpenClaw gateway
// over the instance's SSH tunnel, completes the connect handshake, and
// returns a Session speaking the normalized chat event schema.
func (c *Client) OpenSession(ctx context.Context, sessionKey string) (agentshim.Session, error) {
	port, err := c.tunnelPort()
	if err != nil {
		return nil, &agentshim.TransportError{Err: err}
	}
	conn, err := sshproxy.DialGateway(ctx, port, c.deps.GatewayToken)
	if err != nil {
		return nil, err
	}
	return newSession(conn, sessionKey), nil
}

// session speaks the OpenClaw gateway WebSocket protocol on one side and the
// normalized agentshim event schema on the other.
type session struct {
	conn *websocket.Conn
	key  string

	mu         sync.Mutex
	reqCounter int
	// lastText tracks the latest cumulative assistant snapshot per gateway
	// runId, so the synthesized "end" event can carry the final text (OpenClaw
	// lifecycle/end frames don't repeat it). lastAnyText is the fallback when
	// the end frame's runId doesn't match any assistant frame's.
	lastText    map[string]string
	lastAnyText string
}

var _ agentshim.Session = (*session)(nil)

func newSession(conn *websocket.Conn, key string) *session {
	return &session{conn: conn, key: key, lastText: make(map[string]string)}
}

func (s *session) nextID(prefix string) string {
	s.mu.Lock()
	s.reqCounter++
	n := s.reqCounter
	s.mu.Unlock()
	return fmt.Sprintf("%s-%d", prefix, n)
}

func (s *session) writeFrame(ctx context.Context, frame map[string]any) error {
	b, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return s.conn.Write(ctx, websocket.MessageText, b)
}

// Send implements agentshim.Session via a chat.send gateway frame.
func (s *session) Send(ctx context.Context, message string) error {
	return s.writeFrame(ctx, map[string]any{
		"type":   "req",
		"id":     s.nextID("chat"),
		"method": "chat.send",
		"params": map[string]any{
			"sessionKey":     s.key,
			"message":        message,
			"idempotencyKey": uuid.New().String(),
		},
	})
}

// Abort implements agentshim.Session via a chat.abort gateway frame.
func (s *session) Abort(ctx context.Context) error {
	return s.writeFrame(ctx, map[string]any{
		"type":   "req",
		"id":     s.nextID("abort"),
		"method": "chat.abort",
		"params": map[string]any{
			"sessionKey": s.key,
		},
	})
}

// Reset implements agentshim.Session via a sessions.reset gateway frame.
func (s *session) Reset(ctx context.Context) error {
	return s.writeFrame(ctx, map[string]any{
		"type":   "req",
		"id":     s.nextID("reset"),
		"method": "sessions.reset",
		"params": map[string]any{
			"key": s.key,
		},
	})
}

// Recv implements agentshim.Session: it reads gateway frames, skipping the
// ones that don't translate (tick/presence/health, successful res acks), and
// returns the next normalized event.
func (s *session) Recv(ctx context.Context) (agentshim.Event, error) {
	for {
		_, data, err := s.conn.Read(ctx)
		if err != nil {
			return agentshim.Event{}, err
		}
		if ev, ok := s.translate(data); ok {
			return ev, nil
		}
	}
}

// Close implements agentshim.Session.
func (s *session) Close() error {
	return s.conn.CloseNow()
}

// translate converts one raw OpenClaw gateway frame into a normalized event.
// The second return value is false for frames that must be skipped.
//
// Mapping:
//
//	payload.stream=="lifecycle" data.phase=="start" → start
//	payload.stream=="assistant" data.text           → assistant (Text is the
//	    CUMULATIVE SNAPSHOT for that runId, which doubles as message_id/turn)
//	payload.stream=="tool"                          → tool, Detail = raw data
//	payload.stream=="lifecycle" data.phase=="end"   → end, stop_reason
//	    "complete", Text = last assistant snapshot for that run
//	type=="res" ok==false                           → error (non-fatal)
//	tick / presence / health / res-ok               → skipped
func (s *session) translate(raw []byte) (agentshim.Event, bool) {
	var msg map[string]any
	if err := json.Unmarshal(raw, &msg); err != nil {
		return agentshim.Event{}, false
	}

	switch msg["type"] {
	case "res":
		if ok, _ := msg["ok"].(bool); ok {
			return agentshim.Event{}, false
		}
		text := "gateway request failed"
		if errObj, _ := msg["error"].(map[string]any); errObj != nil {
			if m, _ := errObj["message"].(string); m != "" {
				text = m
			}
		}
		return agentshim.Event{
			V:     1,
			Kind:  agentshim.EventError,
			Text:  text,
			Fatal: false,
		}, true

	case "event":
		payload, _ := msg["payload"].(map[string]any)
		if payload == nil {
			return agentshim.Event{}, false
		}
		stream, _ := payload["stream"].(string)
		runID, _ := payload["runId"].(string)
		data, _ := payload["data"].(map[string]any)

		switch stream {
		case "assistant":
			text, _ := data["text"].(string)
			if text != "" {
				s.mu.Lock()
				s.lastText[runID] = text
				s.lastAnyText = text
				s.mu.Unlock()
			}
			return agentshim.Event{
				V:         1,
				Kind:      agentshim.EventAssistant,
				Turn:      runID,
				MessageID: runID,
				Text:      text,
			}, true

		case "tool":
			var detail json.RawMessage
			if data != nil {
				if b, err := json.Marshal(data); err == nil {
					detail = b
				}
			}
			name, _ := data["name"].(string)
			phase, _ := data["phase"].(string)
			return agentshim.Event{
				V:      1,
				Kind:   agentshim.EventTool,
				Turn:   runID,
				Name:   name,
				Phase:  phase,
				Detail: detail,
			}, true

		case "lifecycle":
			phase, _ := data["phase"].(string)
			switch phase {
			case "start":
				return agentshim.Event{
					V:       1,
					Kind:    agentshim.EventStart,
					Session: s.key,
					Turn:    runID,
				}, true
			case "end":
				s.mu.Lock()
				text := s.lastText[runID]
				if text == "" {
					text = s.lastAnyText
				}
				s.mu.Unlock()
				return agentshim.Event{
					V:          1,
					Kind:       agentshim.EventEnd,
					Turn:       runID,
					StopReason: agentshim.StopComplete,
					Text:       text,
				}, true
			}
			return agentshim.Event{}, false
		}
		// tick, presence, health, and any unknown stream: skip.
		return agentshim.Event{}, false
	}
	return agentshim.Event{}, false
}
