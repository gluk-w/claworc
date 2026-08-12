package handlers

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// scriptStep is one Recv result of a fakeSession: after delay, ev is
// returned. Steps past the end of the script block until ctx is done
// (simulating a silent agent).
type scriptStep struct {
	delay time.Duration
	ev    agentshim.Event
}

// fakeSession is an in-memory agentshim.Session: Send records messages,
// Recv replays a script of events.
type fakeSession struct {
	mu     sync.Mutex
	idx    int
	script []scriptStep
	sent   []string
	closed bool
}

func (f *fakeSession) Send(_ context.Context, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, message)
	return nil
}

func (f *fakeSession) Recv(ctx context.Context) (agentshim.Event, error) {
	f.mu.Lock()
	if f.idx >= len(f.script) {
		f.mu.Unlock()
		// Deliberate silence: block until the caller's deadline fires.
		<-ctx.Done()
		return agentshim.Event{}, ctx.Err()
	}
	step := f.script[f.idx]
	f.idx++
	f.mu.Unlock()

	if step.delay > 0 {
		select {
		case <-ctx.Done():
			return agentshim.Event{}, ctx.Err()
		case <-time.After(step.delay):
		}
	}
	return step.ev, nil
}

func (f *fakeSession) Abort(context.Context) error { return nil }
func (f *fakeSession) Reset(context.Context) error { return nil }
func (f *fakeSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func assistantSnapshot(text string) agentshim.Event {
	return agentshim.Event{V: 1, Kind: agentshim.EventAssistant, Text: text}
}

func endEvent(text string) agentshim.Event {
	return agentshim.Event{V: 1, Kind: agentshim.EventEnd, StopReason: agentshim.StopComplete, Text: text}
}

// pointSessionTo overrides webhookOpenSession to hand out sess and records
// the session key the bridge requested.
func pointSessionTo(t *testing.T, sess agentshim.Session) (sessionKey *string) {
	t.Helper()
	var key string
	orig := webhookOpenSession
	webhookOpenSession = func(_ context.Context, _ uint, sessionKey string) (agentshim.Session, error) {
		key = sessionKey
		return sess, nil
	}
	t.Cleanup(func() { webhookOpenSession = orig })
	return &key
}

// setBridgeIdleTimeout sets the webhook idle timeout for the test and restores
// the previous value on cleanup.
func setBridgeIdleTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	orig := config.Cfg.WebhookIdleTimeout
	config.Cfg.WebhookIdleTimeout = d
	t.Cleanup(func() { config.Cfg.WebhookIdleTimeout = orig })
}

// newBridgeInstance creates a running instance row for bridge tests.
func newBridgeInstance(t *testing.T, uuid string) database.Instance {
	t.Helper()
	if err := database.DB.AutoMigrate(&database.WebhookApiKey{}, &database.WebhookLog{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	inst := database.Instance{
		UUID:        uuid,
		Name:        "bot-" + uuid,
		DisplayName: uuid,
		Status:      "running",
	}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	return inst
}

func TestRunWebhookBridge_SessionKeyHasPrefix(t *testing.T) {
	setupTestDB(t)
	inst := newBridgeInstance(t, "bridge-prefix-test")

	sess := &fakeSession{script: []scriptStep{{ev: endEvent("")}}}
	key := pointSessionTo(t, sess)

	_, bridgeErr := RunWebhookBridge(context.Background(), inst.ID, "my-task", "hello", nil)
	if bridgeErr != nil {
		t.Fatalf("RunWebhookBridge: %v", bridgeErr)
	}

	if *key != "claworc-webhook-my-task" {
		t.Fatalf("sessionKey = %q, want %q", *key, "claworc-webhook-my-task")
	}
	if len(sess.sent) != 1 || sess.sent[0] != "hello" {
		t.Fatalf("sent = %v, want exactly [hello]", sess.sent)
	}
	if !sess.closed {
		t.Fatal("session was not closed")
	}
}

// TestRunWebhookBridge_IdleTimeout: a session that yields one event then goes
// silent must trip the idle timeout rather than blocking forever.
func TestRunWebhookBridge_IdleTimeout(t *testing.T) {
	setupTestDB(t)
	inst := newBridgeInstance(t, "bridge-idle-test")
	setBridgeIdleTimeout(t, 200*time.Millisecond)

	// One assistant event, then deliberate silence (never sends "end").
	sess := &fakeSession{script: []scriptStep{{ev: assistantSnapshot("working...")}}}
	pointSessionTo(t, sess)

	_, err := RunWebhookBridge(context.Background(), inst.ID, "idle-task", "hello", nil)
	if err == nil {
		t.Fatalf("expected idle timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "agent idle timeout") {
		t.Fatalf("error = %q, want idle timeout", err.Error())
	}
}

// TestRunWebhookBridge_HeartbeatKeepsAlive: a session streaming events at an
// interval shorter than the idle window — but for far longer than that window
// in total — must NOT be cut off, proving the deadline re-arms per event.
func TestRunWebhookBridge_HeartbeatKeepsAlive(t *testing.T) {
	setupTestDB(t)
	inst := newBridgeInstance(t, "bridge-heartbeat-test")
	setBridgeIdleTimeout(t, 200*time.Millisecond)

	// 12 events @ 50ms = ~600ms total, well past the 200ms idle window,
	// but each gap (50ms) stays under it. Last snapshot is the reply.
	var script []scriptStep
	for i := 0; i < 12; i++ {
		script = append(script, scriptStep{delay: 50 * time.Millisecond, ev: assistantSnapshot("chunk-" + strconv.Itoa(i))})
	}
	script = append(script, scriptStep{ev: endEvent("")})
	sess := &fakeSession{script: script}
	pointSessionTo(t, sess)

	reply, err := RunWebhookBridge(context.Background(), inst.ID, "hb-task", "hello", nil)
	if err != nil {
		t.Fatalf("RunWebhookBridge: %v", err)
	}
	if reply != "chunk-11" {
		t.Fatalf("reply = %q, want last snapshot %q", reply, "chunk-11")
	}
}

// TestRunWebhookBridge_EndTextWins: when the end event carries text (the
// normalized schema's end.text), it is the authoritative reply.
func TestRunWebhookBridge_EndTextWins(t *testing.T) {
	setupTestDB(t)
	inst := newBridgeInstance(t, "bridge-endtext-test")

	sess := &fakeSession{script: []scriptStep{
		{ev: assistantSnapshot("partial")},
		{ev: endEvent("final answer")},
	}}
	pointSessionTo(t, sess)

	reply, err := RunWebhookBridge(context.Background(), inst.ID, "end-task", "hello", nil)
	if err != nil {
		t.Fatalf("RunWebhookBridge: %v", err)
	}
	if reply != "final answer" {
		t.Fatalf("reply = %q, want %q", reply, "final answer")
	}
}

// TestRunWebhookBridge_ClientDisconnect: cancelling the request context mid-
// stream returns context.Canceled, not the idle-timeout error.
func TestRunWebhookBridge_ClientDisconnect(t *testing.T) {
	setupTestDB(t)
	inst := newBridgeInstance(t, "bridge-disconnect-test")
	setBridgeIdleTimeout(t, 5*time.Second) // generous, so idle never fires first

	// One assistant event, then silence until the caller cancels.
	sess := &fakeSession{script: []scriptStep{{ev: assistantSnapshot("working...")}}}
	pointSessionTo(t, sess)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		_, err := RunWebhookBridge(ctx, inst.ID, "disc-task", "hello", nil)
		resCh <- result{err}
	}()

	// Wait until the first event has been consumed, then cancel.
	deadline := time.Now().Add(2 * time.Second)
	for {
		sess.mu.Lock()
		consumed := sess.idx >= 1
		sess.mu.Unlock()
		if consumed || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	select {
	case res := <-resCh:
		if !errors.Is(res.err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", res.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunWebhookBridge did not return after cancel")
	}
}
