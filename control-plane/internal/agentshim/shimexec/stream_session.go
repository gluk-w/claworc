package shimexec

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// streamReadyTimeout bounds how long a freshly started chat-stream may take
// to print its `ready` line. A var so tests can shorten it.
var streamReadyTimeout = 30 * time.Second

// Restart backoff after a chat-stream exec failed to start or died.
var (
	streamBackoffMin = 1 * time.Second
	streamBackoffMax = 30 * time.Second
)

// streamSession implements agentshim.Session over one long-lived
// `chat-stream --session <key>` exec (docs/shim.md, "chat-stream").
//
// Commands go to the exec's stdin, one per line:
//
//	send <turn-id> <base64(message)>
//	abort
//	reset
//
// and its stdout carries the chat event JSONL, starting with a `ready` line.
// Events are forwarded as they arrive — including unsolicited turns the agent
// starts on its own (cron, heartbeats).
//
// Concurrency model:
//   - The exec is started lazily by the first Send and replaced (after a
//     backoff) when it dies. mu guards the current stream and the turns sent
//     on it that have not ended yet. A turn is registered under mu before its
//     command is written, so the reader always sees it if the exec dies.
//   - stdin writes are serialized by the stream's writeMu and never happen
//     under mu: a write can block while the shim is busy writing events, and
//     the reader needs mu to track them.
//   - One reader goroutine per exec forwards events. When the exec dies it
//     synthesizes error+end for every turn still open on it, so consumers
//     (webhooks, moderator) always see their turn terminate.
//   - The exec's lifetime is bound to the session context, not to any
//     caller ctx; Close ends it gracefully (stdin EOF) and then hard.
type streamSession struct {
	c      *Client
	key    string
	logKey string

	ctx    context.Context
	cancel context.CancelFunc
	closed chan struct{}

	events chan agentshim.Event

	startMu sync.Mutex // serializes (re)starts

	mu        sync.Mutex
	cur       *chatStream
	failures  int
	nextStart time.Time

	closeOnce sync.Once
}

// chatStream is one running chat-stream exec.
type chatStream struct {
	h       StreamHandle
	stdin   io.WriteCloser
	writeMu sync.Mutex
	// open lists the turns sent on (or started by) this exec that have not
	// ended, oldest first. Guarded by streamSession.mu.
	open []string
	done chan struct{}
}

// write sends one command line to the exec's stdin.
func (st *chatStream) write(line string) error {
	st.writeMu.Lock()
	defer st.writeMu.Unlock()
	_, err := io.WriteString(st.stdin, line)
	return err
}

var _ agentshim.Session = (*streamSession)(nil)

func newStreamSession(c *Client, key string) *streamSession {
	ctx, cancel := context.WithCancel(context.Background())
	return &streamSession{
		c:      c,
		key:    key,
		logKey: utils.SanitizeForLog(key),
		ctx:    ctx,
		cancel: cancel,
		closed: make(chan struct{}),
		events: make(chan agentshim.Event, eventBufferDepth),
	}
}

func (s *streamSession) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

// Send implements agentshim.Session: it writes a `send` command, starting
// the chat-stream exec first when none is running. A stream that cannot be
// started is reported as a failed turn (error + end events), matching the
// per-turn session, so consumers waiting for an end are never left hanging.
func (s *streamSession) Send(ctx context.Context, message string) error {
	if s.isClosed() {
		return ErrSessionClosed
	}
	turn := newTurnID()
	line := "send " + turn + " " + base64.StdEncoding.EncodeToString([]byte(message)) + "\n"
	for {
		st, err := s.ensureStream(ctx)
		if err != nil {
			if errors.Is(err, ErrSessionClosed) || ctx.Err() != nil {
				return err
			}
			for _, ev := range execFailureEvents(turn, err.Error()) {
				s.emit(ev)
			}
			return nil
		}
		s.mu.Lock()
		if s.cur != st {
			// The stream died between ensureStream and now; retry on its
			// replacement.
			s.mu.Unlock()
			continue
		}
		st.open = append(st.open, turn)
		s.mu.Unlock()
		if werr := st.write(line); werr != nil {
			// The reader goroutine reports the registered turn as failed
			// once the exec is gone.
			log.Printf("[shimexec] stream %s: write send: %s", s.logKey, utils.SanitizeForLog(werr.Error()))
			_ = st.h.Terminate()
		}
		return nil
	}
}

// Recv implements agentshim.Session.
func (s *streamSession) Recv(ctx context.Context) (agentshim.Event, error) {
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
	case <-s.closed:
		return agentshim.Event{}, ErrSessionClosed
	}
}

// Abort implements agentshim.Session. With a live stream it writes `abort`;
// otherwise it runs the one-shot chat-abort verb (a turn may still be
// running inside the agent). If the oldest open turn has not ended after
// AbortGrace, the exec is terminated, which reports the turn as failed.
func (s *streamSession) Abort(ctx context.Context) error {
	s.mu.Lock()
	st := s.cur
	var victim string
	if st != nil && len(st.open) > 0 {
		victim = st.open[0]
	}
	s.mu.Unlock()

	if st == nil {
		_, err := s.c.run(ctx, nil, "chat-abort", "--session", s.key)
		return err
	}
	if werr := st.write("abort\n"); werr != nil {
		_ = st.h.Terminate()
		return nil
	}
	if victim == "" {
		return nil
	}
	grace := s.c.AbortGrace
	if grace <= 0 {
		grace = 5 * time.Second
	}
	go func() {
		t := time.NewTimer(grace)
		defer t.Stop()
		select {
		case <-t.C:
		case <-st.done:
			return
		case <-s.ctx.Done():
			return
		}
		s.mu.Lock()
		still := s.cur == st && len(st.open) > 0 && st.open[0] == victim
		s.mu.Unlock()
		if still {
			log.Printf("[shimexec] stream %s: turn still open %s after abort, terminating chat-stream", s.logKey, grace)
			_ = st.h.Terminate()
		}
	}()
	return nil
}

// Reset implements agentshim.Session. With a live stream it writes `reset`
// (queued by the shim behind any in-flight turn; failures arrive as a
// non-fatal error event); otherwise it runs the one-shot session-reset verb.
func (s *streamSession) Reset(ctx context.Context) error {
	s.mu.Lock()
	st := s.cur
	s.mu.Unlock()
	if st != nil && st.write("reset\n") == nil {
		return nil
	}
	_, err := s.c.run(ctx, nil, "session-reset", "--session", s.key)
	return err
}

// Close implements agentshim.Session: it closes the exec's stdin (the
// graceful shutdown signal), waits up to AbortGrace for it to exit, then
// cancels the session context, which terminates it. Close does not block.
func (s *streamSession) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		s.mu.Lock()
		st := s.cur
		s.mu.Unlock()
		if st == nil {
			s.cancel()
			return
		}
		_ = st.stdin.Close()
		grace := s.c.AbortGrace
		if grace <= 0 {
			grace = 5 * time.Second
		}
		go func() {
			t := time.NewTimer(grace)
			defer t.Stop()
			select {
			case <-st.done:
			case <-t.C:
				_ = st.h.Terminate()
			}
			s.cancel()
		}()
	})
	return nil
}

// emit delivers one event to Recv, blocking (backpressure) unless the
// session context is gone.
func (s *streamSession) emit(ev agentshim.Event) {
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	}
}

// ensureStream returns the running chat-stream exec, starting one (after any
// pending restart backoff) when none is running.
func (s *streamSession) ensureStream(ctx context.Context) (*chatStream, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	s.mu.Lock()
	st, wait := s.cur, time.Until(s.nextStart)
	s.mu.Unlock()
	if st != nil {
		return st, nil
	}
	if s.isClosed() {
		return nil, ErrSessionClosed
	}
	if wait > 0 {
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-s.closed:
			t.Stop()
			return nil, ErrSessionClosed
		}
	}

	st, err := s.start()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.noteFailureLocked()
		return nil, err
	}
	s.cur = st
	return st, nil
}

// noteFailureLocked schedules the next restart with exponential backoff.
func (s *streamSession) noteFailureLocked() {
	s.failures++
	d := streamBackoffMin << (s.failures - 1)
	if d > streamBackoffMax || d <= 0 {
		d = streamBackoffMax
	}
	s.nextStart = time.Now().Add(d)
}

// start launches the exec and waits for its ready line.
func (s *streamSession) start() (*chatStream, error) {
	argv := []string{verbPath("chat-stream"), "--session", s.key}
	h, err := s.c.runner.Start(s.ctx, argv, nil)
	if err != nil {
		return nil, fmt.Errorf("chat-stream: %w", err)
	}
	stdin := h.Stdin()
	if stdin == nil {
		_ = h.Terminate()
		return nil, errors.New("chat-stream: runner returned no stdin")
	}

	sc := bufio.NewScanner(h.Stdout())
	sc.Buffer(make([]byte, 64*1024), maxEventLine)

	readyCh := make(chan bool, 1)
	go func() {
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			var ev agentshim.Event
			if json.Unmarshal(line, &ev) == nil && ev.Kind == agentshim.EventReady {
				readyCh <- true
				return
			}
			log.Printf("[shimexec] stream %s: ignoring output before ready", s.logKey)
		}
		readyCh <- false
	}()

	t := time.NewTimer(streamReadyTimeout)
	defer t.Stop()
	select {
	case ok := <-readyCh:
		if ok {
			st := &chatStream{h: h, stdin: stdin, done: make(chan struct{})}
			go s.read(st, sc)
			return st, nil
		}
	case <-t.C:
		_ = h.Terminate()
		<-readyCh
		return nil, fmt.Errorf("chat-stream: no ready line within %s", streamReadyTimeout)
	}
	// The exec exited before ready: map its exit code (4 = booting).
	go func() { _, _ = io.Copy(io.Discard, h.Stdout()) }()
	code, werr := h.Wait()
	if werr != nil {
		return nil, fmt.Errorf("chat-stream: %w", werr)
	}
	if err := mapExit("chat-stream", code, nil, h.StderrTail()); err != nil {
		return nil, err
	}
	return nil, errors.New("chat-stream exited before ready")
}

// read forwards one exec's events until it exits, then reports every turn
// still open on it as failed.
func (s *streamSession) read(st *chatStream, sc *bufio.Scanner) {
	defer close(st.done)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev agentshim.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			log.Printf("[shimexec] stream %s: skipping malformed event line: %s", s.logKey, utils.SanitizeForLog(err.Error()))
			continue
		}
		switch ev.Kind {
		case agentshim.EventStart:
			s.trackStart(st, ev.Turn)
		case agentshim.EventEnd:
			s.trackEnd(st, ev.Turn)
		case agentshim.EventAssistant, agentshim.EventTool, agentshim.EventError:
		default:
			// ready (internal) and unknown kinds are not forwarded.
			continue
		}
		s.emit(ev)
	}
	if serr := sc.Err(); serr != nil {
		log.Printf("[shimexec] stream %s: chat-stream stdout read error: %s", s.logKey, utils.SanitizeForLog(serr.Error()))
		go func() { _, _ = io.Copy(io.Discard, st.h.Stdout()) }()
	}
	code, werr := st.h.Wait()

	s.mu.Lock()
	open := st.open
	st.open = nil
	if s.cur == st {
		s.cur = nil
		if !s.isClosed() {
			s.noteFailureLocked()
		}
	}
	s.mu.Unlock()

	if len(open) == 0 {
		if !s.isClosed() {
			log.Printf("[shimexec] stream %s: chat-stream exited (code %d)", s.logKey, code)
		}
		return
	}
	text := exitFailureText("chat-stream", code, werr, st.h.StderrTail())
	for _, turn := range open {
		for _, ev := range execFailureEvents(turn, text) {
			s.emit(ev)
		}
	}
}

// trackStart registers an unsolicited turn (one not started by Send).
func (s *streamSession) trackStart(st *chatStream, turn string) {
	if turn == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range st.open {
		if t == turn {
			return
		}
	}
	st.open = append(st.open, turn)
}

// trackEnd retires an ended turn (the oldest one when the end carries no
// turn id) and resets the restart backoff: the stream is healthy.
func (s *streamSession) trackEnd(st *chatStream, turn string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = 0
	s.nextStart = time.Time{}
	if len(st.open) == 0 {
		return
	}
	idx := 0
	if turn != "" {
		idx = -1
		for i, t := range st.open {
			if t == turn {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
	}
	st.open = append(st.open[:idx:idx], st.open[idx+1:]...)
}

// newTurnID returns a random turn id for a send command.
func newTurnID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "t-" + hex.EncodeToString(b[:])
}
