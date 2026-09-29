package shimexec

// Conformance run against the REAL copy-me shim implementation shipped in
// agent/template/shim. The template's verbs honor CLAWORC_SHIM_ENV_FILE /
// CLAWORC_SHIM_STATE_DIR / CLAWORC_SHIM_RUN_DIR overrides, so every verb is
// driven against temp files without a container and without touching the
// repo copies. Verbs whose implementation shells out to python3 (chat-send's
// JSON escaping, configure-llm) are skipped when python3 is absent.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
)

func templateDir(t *testing.T) string {
	t.Helper()
	requireSh(t)
	return shimDir(t, filepath.Join("..", "..", "..", "..", "agent", "template", "shim"))
}

func requirePython3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available (template verb needs it)")
	}
}

// templateEnvFile writes a temp agent.env and returns its path.
func templateEnvFile(t *testing.T, chatCmd string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "agent.env")
	if err := os.WriteFile(p, []byte("CHAT_CMD="+chatCmd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newTemplateClient(t *testing.T, env ...string) *Client {
	t.Helper()
	return New(&LocalRunner{Dir: templateDir(t), Env: env})
}

func TestTemplateMetaAndIdentity(t *testing.T) {
	c := newTemplateClient(t)
	caps, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.Chat || !caps.ChatAbort || !caps.SessionReset || !caps.Config || !caps.ConfigureLLM || !caps.Restart {
		t.Errorf("caps = %+v", caps)
	}
	if len(caps.ConfigFiles) != 1 || caps.ConfigFiles[0].Language != "shell" || caps.ConfigFiles[0].RestartRequired {
		t.Errorf("config files = %+v", caps.ConfigFiles)
	}
	if caps.SessionPersistence != "none" {
		t.Errorf("persistence = %q", caps.SessionPersistence)
	}

	name, svg, err := c.Identity(context.Background())
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}
	if name != "Custom Agent" {
		t.Errorf("name = %q", name)
	}
	if !strings.Contains(string(svg), "<svg") {
		t.Errorf("svg missing: %q", svg)
	}
}

func TestTemplateHealth(t *testing.T) {
	ok := newTemplateClient(t, "CLAWORC_SHIM_ENV_FILE="+templateEnvFile(t, `"cat"`))
	if err := ok.Health(context.Background()); err != nil {
		t.Errorf("healthy: %v", err)
	}

	broken := newTemplateClient(t, "CLAWORC_SHIM_ENV_FILE="+templateEnvFile(t, `"/no/such/agent-binary"`))
	err := broken.Health(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("broken err = %v", err)
	}

	missing := newTemplateClient(t, "CLAWORC_SHIM_ENV_FILE=/nonexistent/agent.env")
	if err := missing.Health(context.Background()); err == nil {
		t.Error("want error for missing agent.env")
	}
}

func TestTemplateConfigRoundTrip(t *testing.T) {
	envFile := templateEnvFile(t, `"cat"`)
	c := newTemplateClient(t, "CLAWORC_SHIM_ENV_FILE="+envFile)

	content, lang, err := c.GetConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if content != "CHAT_CMD=\"cat\"\n" || lang != "shell" {
		t.Errorf("content=%q lang=%q", content, lang)
	}

	if err := c.SetConfig(context.Background(), "main", "CHAT_CMD=\"cat\"\nEXTRA=1\n"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	b, _ := os.ReadFile(envFile)
	if string(b) != "CHAT_CMD=\"cat\"\nEXTRA=1\n" {
		t.Errorf("file after set = %q", b)
	}
}

func TestTemplateConfigSetWritesVerbatim(t *testing.T) {
	// config-set does not validate (the frontend validates by the meta
	// `language`): even syntactically broken shell is stored byte for byte.
	envFile := templateEnvFile(t, `"cat"`)
	c := newTemplateClient(t, "CLAWORC_SHIM_ENV_FILE="+envFile)

	if err := c.SetConfig(context.Background(), "main", "if [ broken\n"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	b, _ := os.ReadFile(envFile)
	if string(b) != "if [ broken\n" {
		t.Errorf("file after set = %q", b)
	}
}

// runTemplateSkillLib drives the template's shared skill_install /
// skill_remove helpers (the bodies of the skill-install / skill-remove verbs)
// against a temp skills dir. The verbs themselves hardcode the in-image
// skills_dir, so the library is sourced directly.
func runTemplateSkillLib(t *testing.T, fn, skillsDir, name string, stdin []byte) (int, string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := templateDir(t)
	for _, verb := range []string{"skill-install", "skill-remove"} {
		if _, err := os.Stat(filepath.Join(dir, verb)); err != nil {
			t.Fatalf("template shim is missing the %s verb: %v", verb, err)
		}
	}
	cmd := exec.Command("bash", "-c", `set -euo pipefail; source "$1/lib/shimlib.sh"; "$2" "$3" "$4"`,
		"_", dir, fn, skillsDir, name)
	cmd.Stdin = bytes.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run %s: %v", fn, err)
		}
		code = ee.ExitCode()
	}
	return code, string(out)
}

func TestTemplateSkillInstallAcceptsClientArchive(t *testing.T) {
	archive, err := buildSkillTar(map[string][]byte{
		"SKILL.md":          []byte("# skill"),
		"scripts/run.sh":    []byte("#!/bin/sh\necho hi\n"),
		"scripts/util/x.py": []byte("pass\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	skills := t.TempDir()
	if code, out := runTemplateSkillLib(t, "skill_install", skills, "demo", archive); code != 0 {
		t.Fatalf("skill_install exit %d: %s", code, out)
	}
	base := filepath.Join(skills, "demo")
	for rel, want := range map[string]string{
		"SKILL.md":          "# skill",
		"scripts/run.sh":    "#!/bin/sh\necho hi\n",
		"scripts/util/x.py": "pass\n",
	} {
		b, err := os.ReadFile(filepath.Join(base, rel))
		if err != nil || string(b) != want {
			t.Errorf("%s = %q, %v", rel, b, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(base, "scripts/run.sh")); err != nil || fi.Mode()&0o100 == 0 {
		t.Errorf("run.sh should be executable: %v %v", fi, err)
	}

	// Reinstall replaces the skill wholesale.
	archive, _ = buildSkillTar(map[string][]byte{"SKILL.md": []byte("v2")})
	if code, out := runTemplateSkillLib(t, "skill_install", skills, "demo", archive); code != 0 {
		t.Fatalf("reinstall exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(base, "scripts")); !os.IsNotExist(err) {
		t.Errorf("stale files survived reinstall (err=%v)", err)
	}

	// Remove is idempotent.
	for i := 0; i < 2; i++ {
		if code, out := runTemplateSkillLib(t, "skill_remove", skills, "demo", nil); code != 0 {
			t.Fatalf("skill_remove #%d exit %d: %s", i+1, code, out)
		}
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Errorf("skill dir still present (err=%v)", err)
	}
}

func TestTemplateChatSend(t *testing.T) {
	requirePython3(t)
	c := newTemplateClient(t,
		"CLAWORC_SHIM_ENV_FILE="+templateEnvFile(t, `"cat"`),
		"CLAWORC_SHIM_STATE_DIR="+t.TempDir(),
		"CLAWORC_SHIM_RUN_DIR="+t.TempDir(),
	)
	sess := openLocalSession(t, c, "browser")

	if err := sess.Send(context.Background(), "hello template"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	ev := recvEvent(t, sess)
	if ev.Kind != agentshim.EventStart || ev.Session != "browser" {
		t.Fatalf("start = %+v", ev)
	}
	ev = recvEvent(t, sess)
	if ev.Kind != agentshim.EventAssistant || ev.Text != "hello template" {
		t.Fatalf("assistant = %+v", ev)
	}
	ev = recvEvent(t, sess)
	if ev.Kind != agentshim.EventEnd || ev.StopReason != agentshim.StopComplete || ev.Text != "hello template" {
		t.Fatalf("end = %+v", ev)
	}
}

func TestTemplateChatAbort(t *testing.T) {
	requirePython3(t)
	runDir := t.TempDir()
	c := newTemplateClient(t,
		"CLAWORC_SHIM_ENV_FILE="+templateEnvFile(t, `"sleep 30"`),
		"CLAWORC_SHIM_STATE_DIR="+t.TempDir(),
		"CLAWORC_SHIM_RUN_DIR="+runDir,
	)
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
	if ev.Kind != agentshim.EventEnd || ev.StopReason != agentshim.StopAborted {
		t.Fatalf("end = %+v", ev)
	}
}

func TestTemplateSessionReset(t *testing.T) {
	stateDir := t.TempDir()
	c := newTemplateClient(t, "CLAWORC_SHIM_STATE_DIR="+stateDir)

	// Seed a transcript like a previous chat-send would have.
	sessions := filepath.Join(stateDir, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(sessions, "browser.log")
	if err := os.WriteFile(transcript, []byte("history"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess := openLocalSession(t, c, "browser")
	if err := sess.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := os.Stat(transcript); !os.IsNotExist(err) {
		t.Errorf("transcript still present (err=%v)", err)
	}
	// Idempotent: resetting again succeeds.
	if err := sess.Reset(context.Background()); err != nil {
		t.Errorf("second Reset: %v", err)
	}
}

func TestTemplateConfigureLLM(t *testing.T) {
	requirePython3(t)
	envFile := templateEnvFile(t, `"cat"`)
	c := newTemplateClient(t, "CLAWORC_SHIM_ENV_FILE="+envFile)

	routing := agentshim.LLMRouting{
		ProxyURL:     "http://127.0.0.1:40001",
		Style:        "openai",
		DefaultModel: "anthropic/claude-sonnet-4-5",
		Providers: []agentshim.ProviderRoute{{
			Key:    "anthropic",
			APIKey: "claworc-vk-test",
			Models: []agentshim.ModelRef{{ID: "anthropic/claude-sonnet-4-5"}},
		}},
	}
	for i := 0; i < 2; i++ { // MUST be idempotent
		if err := c.ConfigureLLM(context.Background(), routing); err != nil {
			t.Fatalf("ConfigureLLM #%d: %v", i+1, err)
		}
	}
	b, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	if !strings.Contains(content, "CHAT_CMD=\"cat\"") {
		t.Errorf("user content lost: %q", content)
	}
	if !strings.Contains(content, "OPENAI_BASE_URL=http://127.0.0.1:40001") ||
		!strings.Contains(content, "OPENAI_API_KEY=claworc-vk-test") {
		t.Errorf("managed block missing: %q", content)
	}
	if got := strings.Count(content, ">>> claworc-llm >>>"); got != 1 {
		t.Errorf("managed block appears %d times, want 1 (idempotency): %q", got, content)
	}
}

func TestTemplateRestart(t *testing.T) {
	if err := newTemplateClient(t).Restart(context.Background()); err != nil {
		t.Errorf("Restart: %v", err)
	}
}

func TestTemplateHealthTimeBudget(t *testing.T) {
	// meta + health are probed on every SSH reconnect; keep them snappy.
	c := newTemplateClient(t, "CLAWORC_SHIM_ENV_FILE="+templateEnvFile(t, `"cat"`))
	start := time.Now()
	if _, err := c.Capabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("probe took %s", elapsed)
	}
}
