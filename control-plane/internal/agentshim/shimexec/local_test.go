package shimexec

// Local-exec conformance harness: drives the adapter (Client + session)
// against real shell scripts in testdata/shim through the LocalRunner, so
// the exact code paths used over SSH — argv building, JSONL parsing, exit
// code mapping, abort/terminate semantics — are exercised end to end.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
)

func requireSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shim scripts need a POSIX sh")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
}

func shimDir(t *testing.T, rel string) string {
	t.Helper()
	dir, err := filepath.Abs(rel)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("shim dir %s not available: %v", dir, err)
	}
	// Ensure the verbs are executable regardless of how the tree was
	// checked out.
	for _, e := range entries {
		if e.IsDir() || strings.Contains(e.Name(), ".") {
			continue
		}
		_ = os.Chmod(filepath.Join(dir, e.Name()), 0o755)
	}
	return dir
}

func newLocalClient(t *testing.T, env ...string) *Client {
	t.Helper()
	requireSh(t)
	return New(&LocalRunner{Dir: shimDir(t, filepath.Join("testdata", "shim")), Env: env})
}

func recvEvent(t *testing.T, s agentshim.Session) agentshim.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ev, err := s.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	return ev
}

func openLocalSession(t *testing.T, c *Client, key string) agentshim.Session {
	t.Helper()
	sess, err := c.OpenSession(context.Background(), key)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

func TestLocalMetaCapabilities(t *testing.T) {
	c := newLocalClient(t)
	caps, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.Chat || !caps.ChatAbort || !caps.SessionReset || !caps.Config || !caps.ConfigureLLM || !caps.Restart {
		t.Errorf("caps = %+v", caps)
	}
	if caps.ControlUI || caps.Skills {
		t.Errorf("control-ui/skills should be off: %+v", caps)
	}
	if len(caps.ConfigFiles) != 1 || caps.ConfigFiles[0].Language != "ini" {
		t.Errorf("config files = %+v", caps.ConfigFiles)
	}
	if caps.SessionPersistence != "emulated" {
		t.Errorf("persistence = %q", caps.SessionPersistence)
	}
	m, err := c.Meta(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if m.Agent.Name != "fakeagent" || m.Agent.Version != "1.2.3" {
		t.Errorf("agent = %+v", m.Agent)
	}
}

func TestLocalHealth(t *testing.T) {
	if err := newLocalClient(t).Health(context.Background()); err != nil {
		t.Errorf("healthy: %v", err)
	}
	if err := newLocalClient(t, "HEALTH_EXIT=4").Health(context.Background()); !errors.Is(err, ErrBooting) {
		t.Errorf("booting err = %v", err)
	}
	err := newLocalClient(t, "HEALTH_EXIT=1").Health(context.Background())
	if err == nil || !strings.Contains(err.Error(), "health detail on stderr") {
		t.Errorf("broken err = %v", err)
	}
}

func TestLocalIdentity(t *testing.T) {
	name, svg, err := newLocalClient(t).Identity(context.Background())
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}
	if name != "Fake Agent" {
		t.Errorf("name = %q", name)
	}
	if !strings.Contains(string(svg), "<svg") {
		t.Errorf("svg = %q", svg)
	}
}

func TestLocalChatHappyPath(t *testing.T) {
	c := newLocalClient(t, "CLAWORC_SHIM_RUN_DIR="+t.TempDir())
	sess := openLocalSession(t, c, "browser")

	if err := sess.Send(context.Background(), "hello world"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	ev := recvEvent(t, sess)
	if ev.Kind != agentshim.EventStart || ev.Session != "browser" || ev.Turn == "" {
		t.Fatalf("start = %+v", ev)
	}
	turn := ev.Turn

	ev = recvEvent(t, sess)
	if ev.Kind != agentshim.EventAssistant || ev.Text != "echo:" || ev.MessageID != "m1" {
		t.Fatalf("first snapshot = %+v", ev)
	}

	// The malformed line and unknown event kind between the snapshots must
	// be skipped: the next event is the tool event.
	ev = recvEvent(t, sess)
	if ev.Kind != agentshim.EventTool || ev.Name != "exec" || ev.Phase != "start" {
		t.Fatalf("tool = %+v", ev)
	}
	var detail map[string]any
	if err := json.Unmarshal(ev.Detail, &detail); err != nil || detail["command"] != "true" {
		t.Fatalf("tool detail = %s (%v)", ev.Detail, err)
	}

	ev = recvEvent(t, sess)
	if ev.Kind != agentshim.EventAssistant || ev.Text != "echo: hello world" {
		t.Fatalf("cumulative snapshot = %+v", ev)
	}

	ev = recvEvent(t, sess)
	if ev.Kind != agentshim.EventEnd || ev.StopReason != agentshim.StopComplete ||
		ev.Text != "echo: hello world" || ev.Turn != turn {
		t.Fatalf("end = %+v", ev)
	}
}

func TestLocalChatNoEndSynthesized(t *testing.T) {
	c := newLocalClient(t, "CHAT_MODE=fail")
	sess := openLocalSession(t, c, "browser")

	if err := sess.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ev := recvEvent(t, sess); ev.Kind != agentshim.EventStart {
		t.Fatalf("start = %+v", ev)
	}
	ev := recvEvent(t, sess)
	if ev.Kind != agentshim.EventError || !ev.Fatal {
		t.Fatalf("error = %+v", ev)
	}
	if !strings.Contains(ev.Text, "boom: agent exploded") || !strings.Contains(ev.Text, "code 1") {
		t.Errorf("error text = %q", ev.Text)
	}
	ev = recvEvent(t, sess)
	if ev.Kind != agentshim.EventEnd || ev.StopReason != agentshim.StopError {
		t.Fatalf("end = %+v", ev)
	}
}

func TestLocalChatAbort(t *testing.T) {
	runDir := t.TempDir()
	c := newLocalClient(t, "CHAT_MODE=hang", "CLAWORC_SHIM_RUN_DIR="+runDir)
	sess := openLocalSession(t, c, "browser")

	if err := sess.Send(context.Background(), "block forever"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ev := recvEvent(t, sess); ev.Kind != agentshim.EventStart {
		t.Fatalf("start = %+v", ev)
	}
	if err := sess.Abort(context.Background()); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	ev := recvEvent(t, sess)
	if ev.Kind != agentshim.EventEnd || ev.StopReason != agentshim.StopAborted {
		t.Fatalf("end = %+v", ev)
	}
}

func TestLocalAbortSafetyKill(t *testing.T) {
	// chat-abort is a no-op and chat-send has no TERM handling: the adapter
	// must hard-terminate the exec after AbortGrace and synthesize the end.
	c := newLocalClient(t, "CHAT_MODE=stubborn", "ABORT_NOOP=1")
	c.AbortGrace = 200 * time.Millisecond
	sess := openLocalSession(t, c, "browser")

	if err := sess.Send(context.Background(), "never answered"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ev := recvEvent(t, sess); ev.Kind != agentshim.EventStart {
		t.Fatalf("start = %+v", ev)
	}
	if err := sess.Abort(context.Background()); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	ev := recvEvent(t, sess)
	if ev.Kind != agentshim.EventError || !ev.Fatal {
		t.Fatalf("error = %+v", ev)
	}
	ev = recvEvent(t, sess)
	if ev.Kind != agentshim.EventEnd || ev.StopReason != agentshim.StopError {
		t.Fatalf("end = %+v", ev)
	}
}

func TestLocalQueuedSends(t *testing.T) {
	// A Send while a turn is in flight is queued, not rejected: both turns
	// complete, in order.
	c := newLocalClient(t, "CHAT_DELAY=0.3")
	sess := openLocalSession(t, c, "browser")

	if err := sess.Send(context.Background(), "one"); err != nil {
		t.Fatalf("Send one: %v", err)
	}
	if err := sess.Send(context.Background(), "two"); err != nil {
		t.Fatalf("Send two: %v", err)
	}

	var ends []string
	deadline := time.After(20 * time.Second)
	for len(ends) < 2 {
		select {
		case <-deadline:
			t.Fatalf("timed out; ends = %v", ends)
		default:
		}
		ev := recvEvent(t, sess)
		if ev.Kind == agentshim.EventEnd {
			ends = append(ends, ev.Text)
		}
	}
	if ends[0] != "echo: one" || ends[1] != "echo: two" {
		t.Errorf("ends = %v", ends)
	}
}

func TestLocalSessionReset(t *testing.T) {
	stateDir := t.TempDir()
	c := newLocalClient(t, "CLAWORC_SHIM_STATE_DIR="+stateDir)
	sess := openLocalSession(t, c, "browser")

	if err := sess.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(stateDir, "reset.log"))
	if err != nil || strings.TrimSpace(string(b)) != "browser" {
		t.Errorf("reset.log = %q err = %v", b, err)
	}
}

func TestLocalConfigRoundTrip(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "agent.conf")
	if err := os.WriteFile(cfg, []byte("[agent]\nname=one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := newLocalClient(t, "TEST_CONFIG_FILE="+cfg)

	content, lang, err := c.GetConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if content != "[agent]\nname=one\n" || lang != "ini" {
		t.Errorf("content=%q lang=%q", content, lang)
	}

	if err := c.SetConfig(context.Background(), "main", "[agent]\nname=two\n"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	b, _ := os.ReadFile(cfg)
	if string(b) != "[agent]\nname=two\n" {
		t.Errorf("file after set = %q", b)
	}

	err = c.SetConfig(context.Background(), "main", "something INVALID here")
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(ve.Message, "INVALID marker") {
		t.Errorf("validation err = %v", err)
	}
	// A failed set must not clobber the file.
	b, _ = os.ReadFile(cfg)
	if string(b) != "[agent]\nname=two\n" {
		t.Errorf("file after failed set = %q", b)
	}
}

func TestLocalConfigureLLM(t *testing.T) {
	out := filepath.Join(t.TempDir(), "llm.json")
	c := newLocalClient(t, "TEST_LLM_FILE="+out)

	routing := agentshim.LLMRouting{
		ProxyURL:     "http://127.0.0.1:40001",
		Style:        "openai",
		DefaultModel: "anthropic/claude-sonnet-4-5",
		Providers: []agentshim.ProviderRoute{{
			Key:    "anthropic",
			APIKey: "claworc-vk-x",
			Models: []agentshim.ModelRef{{ID: "anthropic/claude-sonnet-4-5"}},
		}},
	}
	if err := c.ConfigureLLM(context.Background(), routing); err != nil {
		t.Fatalf("ConfigureLLM: %v", err)
	}
	var doc struct {
		ProxyURL       string   `json:"proxy_url"`
		Style          string   `json:"style"`
		DefaultModel   string   `json:"default_model"`
		FallbackModels []string `json:"fallback_models"`
		Providers      []struct {
			Key    string `json:"key"`
			APIKey string `json:"api_key"`
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"providers"`
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("routing doc: %v (%s)", err, b)
	}
	if doc.ProxyURL != routing.ProxyURL || doc.Style != "openai" ||
		doc.DefaultModel != routing.DefaultModel ||
		doc.FallbackModels == nil || len(doc.FallbackModels) != 0 ||
		len(doc.Providers) != 1 || doc.Providers[0].Key != "anthropic" ||
		doc.Providers[0].Models[0].ID != "anthropic/claude-sonnet-4-5" {
		t.Errorf("doc = %+v", doc)
	}
}

func TestLocalRestart(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "restarted")
	c := newLocalClient(t, "TEST_RESTART_MARKER="+marker)
	if err := c.Restart(context.Background()); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker: %v", err)
	}
}

func TestLocalSessionClose(t *testing.T) {
	c := newLocalClient(t)
	sess := openLocalSession(t, c, "browser")
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := sess.Recv(ctx); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("Recv after close = %v", err)
	}
	if err := sess.Send(ctx, "hi"); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("Send after close = %v", err)
	}
}

func TestLocalCloseTerminatesInflight(t *testing.T) {
	c := newLocalClient(t, "CHAT_MODE=stubborn")
	sess := openLocalSession(t, c, "browser")
	if err := sess.Send(context.Background(), "hang"); err != nil {
		t.Fatal(err)
	}
	if ev := recvEvent(t, sess); ev.Kind != agentshim.EventStart {
		t.Fatalf("start = %+v", ev)
	}
	start := time.Now()
	sess.Close()
	// Close must not leave the chat-send process running: the SIGTERM path
	// kills the trap-less script promptly. Give it a moment and make sure
	// the session goroutines wound down without hanging the test.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Close took %s", elapsed)
	}
}
