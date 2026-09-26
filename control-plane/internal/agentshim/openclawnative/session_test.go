package openclawnative

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
)

// gwFrame builds a raw OpenClaw gateway event frame.
func gwFrame(stream, runID string, data map[string]any) []byte {
	payload := map[string]any{"stream": stream, "data": data}
	if runID != "" {
		payload["runId"] = runID
	}
	b, _ := json.Marshal(map[string]any{"type": "event", "payload": payload})
	return b
}

func resFrame(ok bool, errMsg string) []byte {
	frame := map[string]any{"type": "res", "ok": ok}
	if errMsg != "" {
		frame["error"] = map[string]any{"message": errMsg}
	}
	b, _ := json.Marshal(frame)
	return b
}

// --- translate golden tests (no transport involved) ---

// TestTranslate_GoldenSequence feeds a recorded OpenClaw gateway frame
// sequence through the translator and asserts the exact normalized event
// sequence, guarding the cumulative-text and lifecycle-end semantics.
func TestTranslate_GoldenSequence(t *testing.T) {
	s := newSession(nil, "browser")

	frames := [][]byte{
		gwFrame("lifecycle", "r1", map[string]any{"phase": "start"}),
		gwFrame("health", "", map[string]any{"ok": true}), // skipped
		resFrame(true, ""), // ack: skipped
		gwFrame("assistant", "r1", map[string]any{"text": "Looking into it"}),        // snapshot 1
		gwFrame("assistant", "r1", map[string]any{"text": "Looking into it. Done."}), // snapshot 2 (cumulative)
		gwFrame("tool", "r1", map[string]any{"name": "exec", "phase": "start", "command": "ls /tmp"}),
		gwFrame("tick", "", nil),        // skipped
		resFrame(false, "rate limited"), // res error → non-fatal error event
		gwFrame("presence", "", nil),    // skipped
		gwFrame("lifecycle", "r1", map[string]any{"phase": "end"}),
	}

	var got []agentshim.Event
	for _, f := range frames {
		if ev, ok := s.translate(f); ok {
			got = append(got, ev)
		}
	}

	want := []agentshim.Event{
		{V: 1, Kind: "start", Session: "browser", Turn: "r1"},
		{V: 1, Kind: "assistant", Turn: "r1", MessageID: "r1", Text: "Looking into it"},
		{V: 1, Kind: "assistant", Turn: "r1", MessageID: "r1", Text: "Looking into it. Done."},
		{V: 1, Kind: "tool", Turn: "r1", Name: "exec", Phase: "start"},
		{V: 1, Kind: "error", Text: "rate limited"},
		{V: 1, Kind: "end", Turn: "r1", StopReason: "complete", Text: "Looking into it. Done."},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		g := got[i]
		// Compare Detail separately (JSON object key order is irrelevant).
		detail := g.Detail
		g.Detail = nil
		if !reflect.DeepEqual(g, want[i]) {
			t.Errorf("event %d = %+v, want %+v", i, g, want[i])
		}
		if want[i].Kind == "tool" {
			var d map[string]any
			if err := json.Unmarshal(detail, &d); err != nil {
				t.Fatalf("tool detail not JSON: %v", err)
			}
			if d["command"] != "ls /tmp" {
				t.Errorf("tool detail = %v, want command 'ls /tmp'", d)
			}
		}
	}
}

// TestTranslate_EndTextFallsBackAcrossRuns: when the end frame's runId does
// not match the assistant frames' (or both are absent), end.text still
// carries the last assistant snapshot.
func TestTranslate_EndTextFallsBackAcrossRuns(t *testing.T) {
	s := newSession(nil, "k")

	if _, ok := s.translate(gwFrame("assistant", "run-a", map[string]any{"text": "the reply"})); !ok {
		t.Fatal("assistant frame not translated")
	}
	ev, ok := s.translate(gwFrame("lifecycle", "run-b", map[string]any{"phase": "end"}))
	if !ok {
		t.Fatal("end frame not translated")
	}
	if ev.Text != "the reply" {
		t.Fatalf("end.Text = %q, want fallback to last snapshot", ev.Text)
	}
}

// TestTranslate_MultipleMessageIDsPerTurn: each snapshot replaces only its
// own message; end carries the LAST assistant snapshot.
func TestTranslate_LastSnapshotPerRunWins(t *testing.T) {
	s := newSession(nil, "k")
	s.translate(gwFrame("assistant", "r9", map[string]any{"text": "one"}))
	s.translate(gwFrame("assistant", "r9", map[string]any{"text": "one two"}))
	s.translate(gwFrame("assistant", "r9", map[string]any{"text": "one two three"}))
	ev, _ := s.translate(gwFrame("lifecycle", "r9", map[string]any{"phase": "end"}))
	if ev.Text != "one two three" {
		t.Fatalf("end.Text = %q, want final cumulative snapshot", ev.Text)
	}
}

// TestTranslate_SkipsUnknownAndMalformed: unknown streams, malformed JSON,
// and successful res acks are all skipped.
func TestTranslate_SkipsUnknownAndMalformed(t *testing.T) {
	s := newSession(nil, "k")
	for _, raw := range [][]byte{
		[]byte("not json"),
		[]byte(`{"type":"event"}`),
		[]byte(`{"type":"event","payload":{"stream":"weird-new-stream"}}`),
		resFrame(true, ""),
		gwFrame("lifecycle", "r1", map[string]any{"phase": "compacting"}),
	} {
		if ev, ok := s.translate(raw); ok {
			t.Errorf("frame %s translated to %+v, want skip", raw, ev)
		}
	}
}

func TestTranslate_ResErrorDefaultsMessage(t *testing.T) {
	s := newSession(nil, "k")
	ev, ok := s.translate([]byte(`{"type":"res","ok":false}`))
	if !ok {
		t.Fatal("res error not translated")
	}
	if ev.Kind != agentshim.EventError || ev.Fatal {
		t.Fatalf("event = %+v, want non-fatal error", ev)
	}
	if ev.Text == "" {
		t.Fatal("error event has empty text")
	}
}

// --- end-to-end session tests over a fake gateway WebSocket ---

// fakeGateway runs a minimal OpenClaw gateway: it completes the DialGateway
// handshake, then hands the connection to serve.
func fakeGateway(t *testing.T, serve func(ctx context.Context, conn *websocket.Conn)) (port int, cleanup func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Logf("ws accept: %v", err)
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()

		// Phase 1: connect.challenge
		challenge, _ := json.Marshal(map[string]any{"type": "event", "payload": map[string]any{"stream": "connect.challenge"}})
		if err := conn.Write(ctx, websocket.MessageText, challenge); err != nil {
			return
		}
		// Phase 2: read connect frame (discard)
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
		// Phase 3: hello-ok
		helloOK, _ := json.Marshal(map[string]any{"type": "res", "ok": true})
		if err := conn.Write(ctx, websocket.MessageText, helloOK); err != nil {
			return
		}
		serve(ctx, conn)
	}))
	addr := srv.Listener.Addr().String()
	portStr := addr[strings.LastIndex(addr, ":")+1:]
	p, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	return p, srv.Close
}

func testClient(port int) *Client {
	return New(agentshim.InstanceDeps{
		TunnelPort: func(service string) (int, error) { return port, nil },
	})
}

// readReq reads the next req frame from the session side of the gateway.
func readReq(ctx context.Context, t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Logf("gateway read: %v", err)
		return nil
	}
	var frame map[string]any
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Logf("gateway unmarshal: %v", err)
		return nil
	}
	return frame
}

// TestSession_SendAbortResetFrames asserts the exact gateway frames the
// session verbs produce.
func TestSession_SendAbortResetFrames(t *testing.T) {
	frames := make(chan map[string]any, 3)
	port, cleanup := fakeGateway(t, func(ctx context.Context, conn *websocket.Conn) {
		for i := 0; i < 3; i++ {
			f := readReq(ctx, t, conn)
			if f == nil {
				return
			}
			frames <- f
		}
	})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := testClient(port).OpenSession(ctx, "claworc-webhook-my-task")
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer sess.Close()

	if err := sess.Send(ctx, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := sess.Abort(ctx); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if err := sess.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	send := <-frames
	if send["method"] != "chat.send" {
		t.Fatalf("frame 1 method = %v, want chat.send", send["method"])
	}
	params, _ := send["params"].(map[string]any)
	if params["sessionKey"] != "claworc-webhook-my-task" {
		t.Errorf("sessionKey = %v", params["sessionKey"])
	}
	if params["message"] != "hello" {
		t.Errorf("message = %v", params["message"])
	}
	if ik, _ := params["idempotencyKey"].(string); ik == "" {
		t.Error("idempotencyKey is empty")
	}

	abort := <-frames
	if abort["method"] != "chat.abort" {
		t.Fatalf("frame 2 method = %v, want chat.abort", abort["method"])
	}
	if p, _ := abort["params"].(map[string]any); p["sessionKey"] != "claworc-webhook-my-task" {
		t.Errorf("abort sessionKey = %v", p["sessionKey"])
	}

	reset := <-frames
	if reset["method"] != "sessions.reset" {
		t.Fatalf("frame 3 method = %v, want sessions.reset", reset["method"])
	}
	if p, _ := reset["params"].(map[string]any); p["key"] != "claworc-webhook-my-task" {
		t.Errorf("reset key = %v", p["key"])
	}
}

// TestSession_RecvStream drives a full chat turn through a fake gateway and
// asserts the normalized event stream the session yields, including frame
// skipping over the wire.
func TestSession_RecvStream(t *testing.T) {
	port, cleanup := fakeGateway(t, func(ctx context.Context, conn *websocket.Conn) {
		if f := readReq(ctx, t, conn); f == nil {
			return
		}
		for _, frame := range [][]byte{
			resFrame(true, ""), // chat.send ack: skipped
			gwFrame("lifecycle", "r1", map[string]any{"phase": "start"}),
			gwFrame("tick", "", nil),
			gwFrame("assistant", "r1", map[string]any{"text": "chunk"}),
			gwFrame("assistant", "r1", map[string]any{"text": "chunk chunk"}),
			gwFrame("lifecycle", "r1", map[string]any{"phase": "end"}),
		} {
			if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
				return
			}
		}
		<-ctx.Done()
	})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := testClient(port).OpenSession(ctx, "browser")
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer sess.Close()

	if err := sess.Send(ctx, "do the thing"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	var kinds []string
	var lastEnd agentshim.Event
	for {
		ev, err := sess.Recv(ctx)
		if err != nil {
			t.Fatalf("Recv: %v (got kinds %v)", err, kinds)
		}
		kinds = append(kinds, ev.Kind)
		if ev.Kind == agentshim.EventEnd {
			lastEnd = ev
			break
		}
	}

	wantKinds := []string{"start", "assistant", "assistant", "end"}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("kinds = %v, want %v", kinds, wantKinds)
	}
	if lastEnd.StopReason != agentshim.StopComplete {
		t.Errorf("end stop_reason = %q, want complete", lastEnd.StopReason)
	}
	if lastEnd.Text != "chunk chunk" {
		t.Errorf("end text = %q, want final cumulative snapshot", lastEnd.Text)
	}
}

// TestOpenSession_NoTunnel: a missing tunnel is a transport error.
func TestOpenSession_NoTunnel(t *testing.T) {
	c := New(agentshim.InstanceDeps{})
	_, err := c.OpenSession(context.Background(), "browser")
	if err == nil {
		t.Fatal("expected error")
	}
	var te *agentshim.TransportError
	if !errors.As(err, &te) {
		t.Fatalf("error %v is not a TransportError", err)
	}
}
