package shimexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
)

// maxEventLine bounds a single JSONL event line (cumulative assistant
// snapshots can be large). Matches the browser websocket read limit.
const maxEventLine = 4 * 1024 * 1024

// sendQueueDepth bounds messages queued behind an in-flight turn. Sends
// beyond it block until the queue drains (or the Send ctx is done).
const sendQueueDepth = 16

// eventBufferDepth bounds events buffered between the turn reader and Recv.
// A full buffer applies backpressure to the chat-send stdout reader.
const eventBufferDepth = 256

// session implements agentshim.Session over per-turn `chat-send` streaming
// execs.
//
// Concurrency model:
//   - Send enqueues the message onto sendCh; a single manager goroutine
//     (loop) drains the queue and runs exactly one chat-send exec at a time,
//     so a message sent while a turn is in flight is queued, not rejected —
//     the /stop + new-message flow relies on this.
//   - The exec's lifetime is bound to the session's own context (derived
//     from context.Background), NOT the Send ctx: chat.go's relay ctx may
//     cancel independently of the turn.
//   - Recv drains the events channel; Abort/Reset run their verbs through
//     the Runner directly and are safe to call while Recv blocks.
//   - Close cancels the session context (terminating any in-flight exec via
//     the Runner's ctx binding) and makes Recv/Send return ErrSessionClosed.
type session struct {
	c   *Client
	key string

	ctx    context.Context
	cancel context.CancelFunc

	sendCh chan string
	events chan agentshim.Event

	closeOnce sync.Once

	mu       sync.Mutex
	inflight StreamHandle
}

var _ agentshim.Session = (*session)(nil)

func newSession(c *Client, key string) *session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{
		c:      c,
		key:    key,
		ctx:    ctx,
		cancel: cancel,
		sendCh: make(chan string, sendQueueDepth),
		events: make(chan agentshim.Event, eventBufferDepth),
	}
	go s.loop()
	return s
}

// Send implements agentshim.Session: it queues the message for the manager
// goroutine. Only one chat-send exec runs at a time; queued messages start
// after the in-flight turn ends (or is aborted).
func (s *session) Send(ctx context.Context, message string) error {
	// Checked first: a buffered sendCh could otherwise win the select below
	// even after Close.
	if s.ctx.Err() != nil {
		return ErrSessionClosed
	}
	select {
	case s.sendCh <- message:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ctx.Done():
		return ErrSessionClosed
	}
}

// Recv implements agentshim.Session: it blocks for the next event across
// all turns. Buffered events are still delivered after Close; once drained,
// Recv returns ErrSessionClosed.
func (s *session) Recv(ctx context.Context) (agentshim.Event, error) {
	// Prefer already-buffered events over the closed/cancelled signals.
	select {
	case ev := <-s.events:
		return ev, nil
	default:
	}
	select {
	case ev := <-s.events:
		return ev, nil
	case <-ctx.Done():
		return agentshim.Event{}, ctx.Err()
	case <-s.ctx.Done():
		return agentshim.Event{}, ErrSessionClosed
	}
}

// Abort implements agentshim.Session: `chat-abort --session <key>`. The
// in-flight chat-send then emits end/aborted and exits on its own; as a
// safety net, if the streaming exec is still alive after AbortGrace it is
// hard-terminated (which synthesizes an error end).
func (s *session) Abort(ctx context.Context) error {
	_, err := s.c.run(ctx, nil, "chat-abort", "--session", s.key)

	if h := s.currentHandle(); h != nil {
		grace := s.c.AbortGrace
		if grace <= 0 {
			grace = 5 * time.Second
		}
		go func() {
			t := time.NewTimer(grace)
			defer t.Stop()
			select {
			case <-t.C:
				if s.currentHandle() == h {
					log.Printf("[shimexec] session %s: chat-send still running %s after abort, terminating", s.key, grace)
					_ = h.Terminate()
				}
			case <-s.ctx.Done():
			}
		}()
	}
	return err
}

// Reset implements agentshim.Session: `session-reset --session <key>`.
func (s *session) Reset(ctx context.Context) error {
	_, err := s.c.run(ctx, nil, "session-reset", "--session", s.key)
	return err
}

// Close implements agentshim.Session: it terminates any in-flight exec and
// releases resources. Recv returns ErrSessionClosed once buffered events are
// drained; Send fails immediately.
func (s *session) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		if h := s.currentHandle(); h != nil {
			_ = h.Terminate()
		}
	})
	return nil
}

func (s *session) currentHandle() StreamHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inflight
}

func (s *session) setHandle(h StreamHandle) {
	s.mu.Lock()
	s.inflight = h
	s.mu.Unlock()
}

// emit delivers one event to Recv, blocking (backpressure) unless the
// session is closed.
func (s *session) emit(ev agentshim.Event) {
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	}
}

// loop is the session manager goroutine: it serializes turns.
func (s *session) loop() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg := <-s.sendCh:
			s.runTurn(msg)
		}
	}
}

// runTurn spawns one `chat-send` streaming exec for a queued message,
// forwards its JSONL events, and synthesizes an error end when the exec dies
// without emitting one.
func (s *session) runTurn(message string) {
	argv := []string{verbPath("chat-send"), "--session", s.key}
	h, err := s.c.runner.Start(s.ctx, argv, strings.NewReader(message))
	if err != nil {
		s.emit(agentshim.Event{
			V: 1, Kind: agentshim.EventError, Code: "shim_exec_failed",
			Text: "chat-send: " + err.Error(), Fatal: true,
		})
		s.emit(agentshim.Event{V: 1, Kind: agentshim.EventEnd, StopReason: agentshim.StopError})
		return
	}
	s.setHandle(h)
	defer s.setHandle(nil)

	var (
		sawEnd   bool
		lastTurn string
	)
	sc := bufio.NewScanner(h.Stdout())
	sc.Buffer(make([]byte, 64*1024), maxEventLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev agentshim.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			log.Printf("[shimexec] session %s: skipping malformed event line: %v", s.key, err)
			continue
		}
		switch ev.Kind {
		case agentshim.EventStart, agentshim.EventAssistant, agentshim.EventTool,
			agentshim.EventError, agentshim.EventEnd:
		default:
			// Unknown event kinds MUST be ignored (contract v1 forward compat).
			continue
		}
		if ev.Turn != "" {
			lastTurn = ev.Turn
		}
		s.emit(ev)
		if ev.Kind == agentshim.EventEnd {
			sawEnd = true
			break
		}
	}
	if serr := sc.Err(); serr != nil {
		log.Printf("[shimexec] session %s: chat-send stdout read error: %v", s.key, serr)
	}
	if sawEnd {
		// end is contractually the last line; drain any trailing output in
		// the background so a misbehaving shim cannot stall Wait on a full
		// stdout pipe.
		go func() { _, _ = io.Copy(io.Discard, h.Stdout()) }()
	}
	code, werr := h.Wait()

	if sawEnd {
		if code != 0 || werr != nil {
			log.Printf("[shimexec] session %s: chat-send exited code=%d err=%v after end event", s.key, code, werr)
		}
		return
	}

	// The exec ended without emitting an end event: the shim/transport
	// itself failed. Synthesize a fatal error plus an error end so consumers
	// always see a terminated turn.
	text := fmt.Sprintf("chat-send exited (code %d) without an end event", code)
	if werr != nil {
		text = fmt.Sprintf("chat-send failed: %v", werr)
	}
	if detail := strings.TrimSpace(h.StderrTail()); detail != "" {
		text += ": " + capString(detail, stderrTailCap)
	}
	s.emit(agentshim.Event{
		V: 1, Kind: agentshim.EventError, Turn: lastTurn,
		Code: "shim_exec_failed", Text: text, Fatal: true,
	})
	s.emit(agentshim.Event{
		V: 1, Kind: agentshim.EventEnd, Turn: lastTurn,
		StopReason: agentshim.StopError,
	})
}
