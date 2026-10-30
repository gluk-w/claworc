package shimexec

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
)

const streamMetaDoc = `{
  "contract": 1,
  "agent": {"name": "fakeagent"},
  "capabilities": ["chat", "chat.stream", "chat.abort", "session.reset"],
  "llm": {"styles": ["openai"]}
}`

// fakeShim plays the image side of one chat-stream exec: it reads commands
// from cmds and writes JSONL to out. Its return value is the exit code.
type fakeShim func(argv []string, cmds *bufio.Scanner, out io.Writer) int

// streamRunner is a Runner whose chat-stream execs are driven by a fakeShim
// over real pipes; one-shot verbs return canned responses.
type streamRunner struct {
	shim fakeShim

	mu     sync.Mutex
	starts int
	runs   []string
	resps  map[string]fakeResp
}

func newStreamRunner(shim fakeShim) *streamRunner {
	return &streamRunner{shim: shim, resps: map[string]fakeResp{"meta": {stdout: streamMetaDoc}}}
}

func (r *streamRunner) Run(_ context.Context, argv []string, _ io.Reader, stdout, _ io.Writer) (int, error) {
	verb := path.Base(argv[0])
	r.mu.Lock()
	r.runs = append(r.runs, verb)
	resp := r.resps[verb]
	r.mu.Unlock()
	if stdout != nil {
		io.WriteString(stdout, resp.stdout)
	}
	return resp.code, resp.err
}

func (r *streamRunner) Start(_ context.Context, argv []string, stdin io.Reader) (StreamHandle, error) {
	if stdin != nil {
		return nil, errors.New("streamRunner only supports interactive starts")
	}
	r.mu.Lock()
	r.starts++
	r.mu.Unlock()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h := &pipeStream{inR: inR, inW: inW, outR: outR, outW: outW, done: make(chan struct{})}
	go func() {
		code := r.shim(argv, bufio.NewScanner(inR), outW)
		h.mu.Lock()
		if !h.terminated {
			h.code = code
		}
		h.mu.Unlock()
		outW.Close()
		inR.Close()
		close(h.done)
	}()
	return h, nil
}

func (r *streamRunner) ReadFile(context.Context, string) ([]byte, error) { return nil, os.ErrNotExist }

func (r *streamRunner) startCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts
}

func (r *streamRunner) ranVerb(verb string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.runs {
		if v == verb {
			return true
		}
	}
	return false
}

type pipeStream struct {
	inR        *io.PipeReader
	inW        *io.PipeWriter
	outR       *io.PipeReader
	outW       *io.PipeWriter
	done       chan struct{}
	mu         sync.Mutex
	code       int
	terminated bool
}

func (h *pipeStream) Stdin() io.WriteCloser { return h.inW }
func (h *pipeStream) Stdout() io.Reader     { return h.outR }
func (h *pipeStream) StderrTail() string    { return "" }
func (h *pipeStream) Terminate() error {
	h.mu.Lock()
	h.terminated, h.code = true, -1
	h.mu.Unlock()
	h.inR.CloseWithError(errors.New("terminated"))
	h.outW.CloseWithError(errors.New("terminated"))
	return nil
}
func (h *pipeStream) Wait() (int, error) {
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.code, nil
}

func jsonl(out io.Writer, ev map[string]any) {
	ev["v"] = 1
	b, _ := json.Marshal(ev)
	fmt.Fprintf(out, "%s\n", b)
}

// echoShim answers every send with start/assistant(<decoded message>)/end
// and every abort with nothing; it exits 0 on stdin EOF.
func echoShim(_ []string, cmds *bufio.Scanner, out io.Writer) int {
	jsonl(out, map[string]any{"event": "ready"})
	for cmds.Scan() {
		f := strings.Fields(cmds.Text())
		if len(f) == 3 && f[0] == "send" {
			msg, _ := base64.StdEncoding.DecodeString(f[2])
			jsonl(out, map[string]any{"event": "start", "turn": f[1]})
			jsonl(out, map[string]any{"event": "assistant", "turn": f[1], "message_id": "m1", "text": string(msg)})
			jsonl(out, map[string]any{"event": "end", "turn": f[1], "stop_reason": "complete", "text": string(msg)})
		}
	}
	return 0
}

func openStream(t *testing.T, r Runner) agentshim.Session {
	t.Helper()
	c := New(r)
	c.AbortGrace = 200 * time.Millisecond
	sess, err := c.OpenSession(context.Background(), "browser")
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if _, ok := sess.(*streamSession); !ok {
		t.Fatalf("OpenSession returned %T, want *streamSession", sess)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

func mustSend(t *testing.T, s agentshim.Session, msg string) {
	t.Helper()
	if err := s.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

// recvUntilEnd collects events up to and including the next end.
func recvUntilEnd(t *testing.T, s agentshim.Session) []agentshim.Event {
	t.Helper()
	var evs []agentshim.Event
	for {
		ev := recvEvent(t, s)
		evs = append(evs, ev)
		if ev.Kind == agentshim.EventEnd {
			return evs
		}
	}
}

func TestStreamSessionFramingRoundTrip(t *testing.T) {
	r := newStreamRunner(echoShim)
	sess := openStream(t, r)

	msgs := []string{"line one\nline two\n\ttabbed", "héllo 🌍 — «ünïcode» send abort reset"}
	for _, msg := range msgs {
		mustSend(t, sess, msg)
		evs := recvUntilEnd(t, sess)
		if len(evs) != 3 || evs[0].Kind != "start" || evs[1].Kind != "assistant" {
			t.Fatalf("events = %+v", evs)
		}
		if evs[1].Text != msg || evs[2].Text != msg {
			t.Fatalf("round trip: got %q want %q", evs[1].Text, msg)
		}
		if !strings.HasPrefix(evs[0].Turn, "t-") || evs[2].Turn != evs[0].Turn {
			t.Fatalf("turn ids: %+v", evs)
		}
	}
	if n := r.startCount(); n != 1 {
		t.Fatalf("chat-stream started %d times, want 1 (one exec for the session)", n)
	}
}

func TestStreamSessionLazyStart(t *testing.T) {
	r := newStreamRunner(echoShim)
	openStream(t, r)
	if n := r.startCount(); n != 0 {
		t.Fatalf("OpenSession started chat-stream %d times; want lazy start", n)
	}
}

func TestStreamSessionForwardsUnsolicitedTurns(t *testing.T) {
	r := newStreamRunner(func(argv []string, cmds *bufio.Scanner, out io.Writer) int {
		jsonl(out, map[string]any{"event": "ready"})
		// An agent-initiated turn (cron / heartbeat) before any send.
		jsonl(out, map[string]any{"event": "start", "turn": "cron-1"})
		jsonl(out, map[string]any{"event": "assistant", "turn": "cron-1", "message_id": "c1", "text": "reminder"})
		jsonl(out, map[string]any{"event": "end", "turn": "cron-1", "stop_reason": "complete", "text": "reminder"})
		return echoShim(argv, cmds, io.Discard)
	})
	sess := openStream(t, r)
	mustSend(t, sess, "hi")
	evs := recvUntilEnd(t, sess)
	if evs[0].Turn != "cron-1" || evs[1].Text != "reminder" {
		t.Fatalf("unsolicited turn not forwarded: %+v", evs)
	}
}

func TestStreamSessionDropsReadyAndUnknownEvents(t *testing.T) {
	r := newStreamRunner(func(argv []string, cmds *bufio.Scanner, out io.Writer) int {
		jsonl(out, map[string]any{"event": "ready"})
		for cmds.Scan() {
			f := strings.Fields(cmds.Text())
			jsonl(out, map[string]any{"event": "ready"})
			jsonl(out, map[string]any{"event": "future-kind"})
			fmt.Fprintln(out, "not json")
			jsonl(out, map[string]any{"event": "end", "turn": f[1], "stop_reason": "complete"})
		}
		return 0
	})
	sess := openStream(t, r)
	mustSend(t, sess, "x")
	if ev := recvEvent(t, sess); ev.Kind != agentshim.EventEnd {
		t.Fatalf("first forwarded event = %+v, want end", ev)
	}
}

func TestStreamSessionDeathFailsOpenTurnsAndRestarts(t *testing.T) {
	oldMin := streamBackoffMin
	streamBackoffMin = 10 * time.Millisecond
	t.Cleanup(func() { streamBackoffMin = oldMin })

	var mu sync.Mutex
	execs := 0
	r := newStreamRunner(func(argv []string, cmds *bufio.Scanner, out io.Writer) int {
		mu.Lock()
		execs++
		first := execs == 1
		mu.Unlock()
		if !first {
			return echoShim(argv, cmds, out)
		}
		jsonl(out, map[string]any{"event": "ready"})
		cmds.Scan()
		f := strings.Fields(cmds.Text())
		jsonl(out, map[string]any{"event": "start", "turn": f[1]})
		return 1 // dies mid-turn
	})
	sess := openStream(t, r)

	mustSend(t, sess, "doomed")
	evs := recvUntilEnd(t, sess)
	if len(evs) != 3 || evs[1].Kind != "error" || evs[1].Code != "shim_exec_failed" || !evs[1].Fatal {
		t.Fatalf("events = %+v", evs)
	}
	if evs[2].StopReason != agentshim.StopError || evs[2].Turn != evs[0].Turn {
		t.Fatalf("synthetic end = %+v", evs[2])
	}

	mustSend(t, sess, "again")
	evs = recvUntilEnd(t, sess)
	if evs[len(evs)-1].Text != "again" {
		t.Fatalf("after restart: %+v", evs)
	}
	if n := r.startCount(); n != 2 {
		t.Fatalf("starts = %d, want 2", n)
	}
}

func TestStreamSessionNotReadyReportsFailedTurn(t *testing.T) {
	r := newStreamRunner(func([]string, *bufio.Scanner, io.Writer) int { return ExitNotReady })
	sess := openStream(t, r)
	mustSend(t, sess, "hi")
	evs := recvUntilEnd(t, sess)
	if len(evs) != 2 || evs[0].Kind != "error" || !strings.Contains(evs[0].Text, "booting") {
		t.Fatalf("events = %+v", evs)
	}
}

func TestStreamSessionAbortWritesCommand(t *testing.T) {
	r := newStreamRunner(func(_ []string, cmds *bufio.Scanner, out io.Writer) int {
		jsonl(out, map[string]any{"event": "ready"})
		var turn string
		for cmds.Scan() {
			f := strings.Fields(cmds.Text())
			switch f[0] {
			case "send":
				turn = f[1]
				jsonl(out, map[string]any{"event": "start", "turn": turn})
			case "abort":
				jsonl(out, map[string]any{"event": "end", "turn": turn, "stop_reason": "aborted"})
			}
		}
		return 0
	})
	sess := openStream(t, r)
	mustSend(t, sess, "long task")
	if ev := recvEvent(t, sess); ev.Kind != "start" {
		t.Fatalf("got %+v", ev)
	}
	if err := sess.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ev := recvEvent(t, sess); ev.StopReason != agentshim.StopAborted {
		t.Fatalf("got %+v", ev)
	}
	if r.ranVerb("chat-abort") {
		t.Fatal("abort ran the one-shot verb despite a live stream")
	}
}

func TestStreamSessionAbortGraceTerminates(t *testing.T) {
	r := newStreamRunner(func(_ []string, cmds *bufio.Scanner, out io.Writer) int {
		jsonl(out, map[string]any{"event": "ready"})
		for cmds.Scan() {
			if f := strings.Fields(cmds.Text()); f[0] == "send" {
				jsonl(out, map[string]any{"event": "start", "turn": f[1]})
			}
			// abort is ignored: the turn never ends
		}
		return 0
	})
	sess := openStream(t, r)
	mustSend(t, sess, "stuck")
	recvEvent(t, sess)
	if err := sess.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	evs := recvUntilEnd(t, sess)
	if evs[len(evs)-1].StopReason != agentshim.StopError {
		t.Fatalf("expected terminated turn to end with error: %+v", evs)
	}
}

func TestStreamSessionAbortAndResetWithoutStreamUseVerbs(t *testing.T) {
	r := newStreamRunner(echoShim)
	sess := openStream(t, r)
	if err := sess.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := sess.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.ranVerb("chat-abort") || !r.ranVerb("session-reset") {
		t.Fatalf("runs = %v", r.runs)
	}
	if r.startCount() != 0 {
		t.Fatal("abort/reset must not start a stream")
	}
}

func TestStreamSessionResetWritesCommand(t *testing.T) {
	gotReset := make(chan struct{}, 1)
	r := newStreamRunner(func(argv []string, cmds *bufio.Scanner, out io.Writer) int {
		jsonl(out, map[string]any{"event": "ready"})
		for cmds.Scan() {
			f := strings.Fields(cmds.Text())
			switch f[0] {
			case "send":
				jsonl(out, map[string]any{"event": "end", "turn": f[1], "stop_reason": "complete"})
			case "reset":
				gotReset <- struct{}{}
			}
		}
		return 0
	})
	sess := openStream(t, r)
	mustSend(t, sess, "x")
	recvEvent(t, sess)
	if err := sess.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gotReset:
	case <-time.After(5 * time.Second):
		t.Fatal("reset command not delivered")
	}
	if r.ranVerb("session-reset") {
		t.Fatal("reset ran the one-shot verb despite a live stream")
	}
}

func TestStreamSessionCloseSendsEOF(t *testing.T) {
	sawEOF := make(chan struct{})
	r := newStreamRunner(func(argv []string, cmds *bufio.Scanner, out io.Writer) int {
		code := echoShim(argv, cmds, out)
		close(sawEOF)
		return code
	})
	sess := openStream(t, r)
	mustSend(t, sess, "x")
	recvUntilEnd(t, sess)
	sess.Close()
	select {
	case <-sawEOF:
	case <-time.After(5 * time.Second):
		t.Fatal("chat-stream did not see stdin EOF after Close")
	}
	if _, err := sess.Recv(context.Background()); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Recv after Close = %v", err)
	}
	if err := sess.Send(context.Background(), "y"); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Send after Close = %v", err)
	}
}

func TestOpenSessionFallsBackToPerTurn(t *testing.T) {
	c := New(&fakeRunner{responses: map[string]fakeResp{"meta": {stdout: validMetaDoc}}})
	sess, err := c.OpenSession(context.Background(), "browser")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if _, ok := sess.(*session); !ok {
		t.Fatalf("OpenSession = %T, want per-turn *session without chat.stream", sess)
	}
}

// TestLocalChatStream drives a real bash chat-stream over OS pipes through
// the LocalRunner (the same StdinPipe/StdoutPipe wiring the SSH runner uses).
func TestLocalChatStream(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("meta", "#!/bin/sh\ncat <<'EOF'\n"+streamMetaDoc+"\nEOF\n")
	write("chat-stream", `#!/usr/bin/env bash
set -euo pipefail
printf '{"v":1,"event":"ready"}\n'
while read -r cmd turn b64; do
    case "$cmd" in
        send)
            n=$(printf %s "$b64" | base64 -d | wc -c | tr -d ' ')
            printf '{"v":1,"event":"start","turn":"%s"}\n' "$turn"
            printf '{"v":1,"event":"end","turn":"%s","stop_reason":"complete","text":"%s bytes"}\n' "$turn" "$n"
            ;;
    esac
done
`)
	sess := openStream(t, &LocalRunner{Dir: dir})
	msg := "multi\nline ünïcode"
	mustSend(t, sess, msg)
	evs := recvUntilEnd(t, sess)
	if want := fmt.Sprintf("%d bytes", len(msg)); evs[len(evs)-1].Text != want {
		t.Fatalf("end = %+v, want text %q", evs[len(evs)-1], want)
	}
	mustSend(t, sess, "second")
	evs = recvUntilEnd(t, sess)
	if evs[len(evs)-1].Text != "6 bytes" {
		t.Fatalf("second turn: %+v", evs)
	}
}
