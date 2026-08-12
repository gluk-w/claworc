package shimexec

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
)

// --- scripted fake Runner ---

type fakeResp struct {
	stdout string
	stderr string
	code   int
	err    error
}

type fakeCall struct {
	argv  []string
	stdin string
}

// fakeRunner is a Runner test double scripted with canned exit codes and
// output per verb (keyed by the basename of argv[0]).
type fakeRunner struct {
	mu        sync.Mutex
	responses map[string]fakeResp
	files     map[string][]byte
	calls     []fakeCall
}

func (f *fakeRunner) record(argv []string, stdin io.Reader) fakeResp {
	var in []byte
	if stdin != nil {
		in, _ = io.ReadAll(stdin)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{argv: argv, stdin: string(in)})
	return f.responses[path.Base(argv[0])]
}

func (f *fakeRunner) Run(_ context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	resp := f.record(argv, stdin)
	if stdout != nil {
		io.WriteString(stdout, resp.stdout)
	}
	if stderr != nil {
		io.WriteString(stderr, resp.stderr)
	}
	return resp.code, resp.err
}

func (f *fakeRunner) Start(_ context.Context, argv []string, stdin io.Reader) (StreamHandle, error) {
	resp := f.record(argv, stdin)
	if resp.err != nil {
		return nil, resp.err
	}
	return &fakeStream{r: strings.NewReader(resp.stdout), tail: resp.stderr, code: resp.code}, nil
}

func (f *fakeRunner) ReadFile(_ context.Context, p string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.files[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return b, nil
}

func (f *fakeRunner) verbCalls(verb string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if path.Base(c.argv[0]) == verb {
			n++
		}
	}
	return n
}

func (f *fakeRunner) lastStdin(verb string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if path.Base(f.calls[i].argv[0]) == verb {
			return f.calls[i].stdin
		}
	}
	return ""
}

type fakeStream struct {
	r    *strings.Reader
	tail string
	code int
}

func (h *fakeStream) Stdout() io.Reader  { return h.r }
func (h *fakeStream) StderrTail() string { return h.tail }
func (h *fakeStream) Terminate() error   { return nil }
func (h *fakeStream) Wait() (int, error) { return h.code, nil }

const validMetaDoc = `{
  "contract": 1,
  "shim_version": "0.1.0",
  "agent": {"name": "fakeagent", "version": "9.9.9"},
  "capabilities": ["chat", "chat.abort", "session.reset", "config", "configure-llm", "restart", "control-ui", "skills"],
  "config_files": [
    {"id": "main", "path": "/etc/agent.json", "language": "json", "label": "agent.json", "restart_required": true}
  ],
  "workspace_dir": "/home/claworc/workspace",
  "skills_dir": "/home/claworc/skills",
  "log_files": [{"path": "/var/log/claworc/agent.log", "label": "Agent"}],
  "llm": {"styles": ["openai", "anthropic"]},
  "session_persistence": "native",
  "unknown_future_field": 42
}`

func newFakeClient(responses map[string]fakeResp) (*Client, *fakeRunner) {
	fr := &fakeRunner{responses: responses}
	return New(fr), fr
}

func TestMetaParsingAndCapabilities(t *testing.T) {
	c, fr := newFakeClient(map[string]fakeResp{"meta": {stdout: validMetaDoc}})
	caps, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.Chat || !caps.ChatAbort || !caps.SessionReset || !caps.Config ||
		!caps.ConfigureLLM || !caps.Restart || !caps.ControlUI || !caps.Skills {
		t.Errorf("capability flags not all set: %+v", caps)
	}
	if len(caps.ConfigFiles) != 1 || caps.ConfigFiles[0].ID != "main" ||
		caps.ConfigFiles[0].Language != "json" || !caps.ConfigFiles[0].RestartRequired {
		t.Errorf("config files: %+v", caps.ConfigFiles)
	}
	if caps.WorkspaceDir != "/home/claworc/workspace" || caps.SkillsDir != "/home/claworc/skills" {
		t.Errorf("dirs: %q %q", caps.WorkspaceDir, caps.SkillsDir)
	}
	if len(caps.LLMStyles) != 2 || caps.LLMStyles[0] != "openai" {
		t.Errorf("llm styles: %v", caps.LLMStyles)
	}
	if caps.SessionPersistence != "native" {
		t.Errorf("session persistence: %q", caps.SessionPersistence)
	}

	m, err := c.Meta(context.Background())
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	if m.Agent.Name != "fakeagent" || m.Agent.Version != "9.9.9" || m.ShimVersion != "0.1.0" {
		t.Errorf("meta identity: %+v", m)
	}

	// Cached: three reads, one probe.
	if _, err := c.Capabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fr.verbCalls("meta"); got != 1 {
		t.Errorf("meta probes = %d, want 1 (cached)", got)
	}
	c.InvalidateCache()
	if _, err := c.Capabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fr.verbCalls("meta"); got != 2 {
		t.Errorf("meta probes after invalidate = %d, want 2", got)
	}
}

func TestMetaValidation(t *testing.T) {
	cases := []struct {
		name, doc, wantSub string
	}{
		{"bad contract", `{"contract": 2, "capabilities": ["chat"]}`, "unsupported shim contract"},
		{"missing chat", `{"contract": 1, "capabilities": ["config"]}`, `required "chat" capability`},
		{"invalid json", `{nope`, "invalid JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newFakeClient(map[string]fakeResp{"meta": {stdout: tc.doc}})
			_, err := c.Capabilities(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("err = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

func TestMetaUnsupportedVerb(t *testing.T) {
	c, _ := newFakeClient(map[string]fakeResp{"meta": {code: 127, stderr: "not found"}})
	if _, err := c.Capabilities(context.Background()); err == nil {
		t.Fatal("want error for exit 127 meta")
	}
}

func TestExitCodeMapping(t *testing.T) {
	t.Run("3 unsupported", func(t *testing.T) {
		c, _ := newFakeClient(map[string]fakeResp{"restart": {code: ExitUnsupported}})
		err := c.Restart(context.Background())
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("err = %v, want ErrUnsupported", err)
		}
	})
	t.Run("4 booting", func(t *testing.T) {
		c, _ := newFakeClient(map[string]fakeResp{"health": {code: ExitNotReady}})
		err := c.Health(context.Background())
		if !errors.Is(err, ErrBooting) {
			t.Errorf("err = %v, want ErrBooting", err)
		}
	})
	t.Run("6 validation with payload", func(t *testing.T) {
		c, _ := newFakeClient(map[string]fakeResp{
			"meta":       {stdout: validMetaDoc},
			"config-set": {code: ExitValidation, stdout: `{"error":"bad json at line 3"}`},
		})
		err := c.SetConfig(context.Background(), "main", "{}")
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("err = %v, want *ValidationError", err)
		}
		if ve.Message != "bad json at line 3" {
			t.Errorf("message = %q", ve.Message)
		}
	})
	t.Run("generic failure carries stderr tail", func(t *testing.T) {
		c, _ := newFakeClient(map[string]fakeResp{"health": {code: 1, stderr: "gateway crashed hard"}})
		err := c.Health(context.Background())
		if err == nil || !strings.Contains(err.Error(), "gateway crashed hard") || !strings.Contains(err.Error(), "exit 1") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("stderr tail capped", func(t *testing.T) {
		big := strings.Repeat("x", 10*stderrTailCap) + "TAIL-END"
		c, _ := newFakeClient(map[string]fakeResp{"health": {code: 1, stderr: big}})
		err := c.Health(context.Background())
		if err == nil {
			t.Fatal("want error")
		}
		if len(err.Error()) > stderrTailCap+100 {
			t.Errorf("error not capped: %d bytes", len(err.Error()))
		}
		if !strings.Contains(err.Error(), "TAIL-END") {
			t.Errorf("tail should keep the end of stderr: %v", err.Error()[:80])
		}
	})
	t.Run("health ok", func(t *testing.T) {
		c, _ := newFakeClient(map[string]fakeResp{"health": {stdout: `{"status":"ok"}`}})
		if err := c.Health(context.Background()); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})
}

func TestGetConfig(t *testing.T) {
	c, fr := newFakeClient(map[string]fakeResp{
		"meta":       {stdout: validMetaDoc},
		"config-get": {stdout: `{"model": "x"}`},
	})
	content, lang, err := c.GetConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if content != `{"model": "x"}` || lang != "json" {
		t.Errorf("content=%q lang=%q", content, lang)
	}
	// "" resolves to the first declared file's id.
	fr.mu.Lock()
	last := fr.calls[len(fr.calls)-1].argv
	fr.mu.Unlock()
	if want := []string{ShimDir + "/config-get", "--id", "main"}; strings.Join(last, " ") != strings.Join(want, " ") {
		t.Errorf("argv = %v", last)
	}

	if _, _, err := c.GetConfig(context.Background(), "nope"); err == nil || !strings.Contains(err.Error(), `unknown config file "nope"`) {
		t.Errorf("unknown id err = %v", err)
	}
}

func TestSetConfigSendsContentOnStdin(t *testing.T) {
	c, fr := newFakeClient(map[string]fakeResp{
		"meta":       {stdout: validMetaDoc},
		"config-set": {},
	})
	if err := c.SetConfig(context.Background(), "main", "new content"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if got := fr.lastStdin("config-set"); got != "new content" {
		t.Errorf("stdin = %q", got)
	}
}

func TestConfigureLLMWireFormat(t *testing.T) {
	c, fr := newFakeClient(map[string]fakeResp{"configure-llm": {}})
	routing := agentshim.LLMRouting{
		ProxyURL:     "http://127.0.0.1:40001",
		Style:        "openai",
		DefaultModel: "anthropic/claude-sonnet-4-5",
		// FallbackModels deliberately nil: must marshal as [].
		Providers: []agentshim.ProviderRoute{{
			Key:    "anthropic",
			APIKey: "claworc-vk-abc123",
			Models: []agentshim.ModelRef{{ID: "anthropic/claude-sonnet-4-5", Default: true}},
		}},
	}
	if err := c.ConfigureLLM(context.Background(), routing); err != nil {
		t.Fatalf("ConfigureLLM: %v", err)
	}
	raw := fr.lastStdin("configure-llm")

	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("routing doc not JSON: %v (%s)", err, raw)
	}
	if doc["proxy_url"] != "http://127.0.0.1:40001" || doc["style"] != "openai" ||
		doc["default_model"] != "anthropic/claude-sonnet-4-5" {
		t.Errorf("doc = %v", doc)
	}
	fb, ok := doc["fallback_models"].([]any)
	if !ok || len(fb) != 0 {
		t.Errorf("fallback_models = %#v, want []", doc["fallback_models"])
	}
	providers := doc["providers"].([]any)
	p0 := providers[0].(map[string]any)
	if p0["key"] != "anthropic" || p0["api_key"] != "claworc-vk-abc123" {
		t.Errorf("provider = %v", p0)
	}
	m0 := p0["models"].([]any)[0].(map[string]any)
	if m0["id"] != "anthropic/claude-sonnet-4-5" {
		t.Errorf("model = %v", m0)
	}
	// The documented wire schema carries only "id" per model.
	if _, has := m0["default"]; has {
		t.Errorf("model entry should carry only id: %v", m0)
	}
}

func TestConfigureLLMValidationFailure(t *testing.T) {
	c, _ := newFakeClient(map[string]fakeResp{
		"configure-llm": {code: ExitValidation, stdout: `{"error":"routing not expressible"}`},
	})
	err := c.ConfigureLLM(context.Background(), agentshim.LLMRouting{})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Message != "routing not expressible" {
		t.Errorf("err = %v", err)
	}
}

func TestIdentity(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		fr := &fakeRunner{files: map[string][]byte{
			ShimDir + "/agent.txt": []byte("  OpenClaw  \nextra junk\n"),
			ShimDir + "/agent.svg": []byte("<svg/>"),
		}}
		name, svg, err := New(fr).Identity(context.Background())
		if err != nil {
			t.Fatalf("Identity: %v", err)
		}
		if name != "OpenClaw" {
			t.Errorf("name = %q", name)
		}
		if string(svg) != "<svg/>" {
			t.Errorf("svg = %q", svg)
		}
	})
	t.Run("missing svg tolerated", func(t *testing.T) {
		fr := &fakeRunner{files: map[string][]byte{
			ShimDir + "/agent.txt": []byte("Hermes\n"),
		}}
		name, svg, err := New(fr).Identity(context.Background())
		if err != nil || name != "Hermes" || svg != nil {
			t.Errorf("name=%q svg=%v err=%v", name, svg, err)
		}
	})
	t.Run("missing txt fails", func(t *testing.T) {
		fr := &fakeRunner{files: map[string][]byte{}}
		if _, _, err := New(fr).Identity(context.Background()); err == nil {
			t.Error("want error for missing agent.txt")
		}
	})
}

func TestOpenSessionRequiresChat(t *testing.T) {
	c, _ := newFakeClient(map[string]fakeResp{
		"meta": {stdout: `{"contract":1,"capabilities":["config"]}`},
	})
	if _, err := c.OpenSession(context.Background(), "browser"); err == nil {
		t.Fatal("want error when meta lacks chat capability")
	}
}

func TestTailBuffer(t *testing.T) {
	tb := newTailBuffer(8)
	io.WriteString(tb, "0123456789abcdef")
	if got := tb.String(); got != "89abcdef" {
		t.Errorf("tail = %q", got)
	}
}
