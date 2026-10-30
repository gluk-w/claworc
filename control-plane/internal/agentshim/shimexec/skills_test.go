package shimexec

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
)

type tarEntry struct {
	name string
	typ  byte
	mode int64
	body string
}

func decodeTar(t *testing.T, raw string) []tarEntry {
	t.Helper()
	tr := tar.NewReader(strings.NewReader(raw))
	var out []tarEntry
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("decode tar: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read tar body: %v", err)
		}
		out = append(out, tarEntry{name: hdr.Name, typ: hdr.Typeflag, mode: hdr.Mode, body: string(body)})
	}
}

func lastCall(t *testing.T, fr *fakeRunner, verb string) fakeCall {
	t.Helper()
	fr.mu.Lock()
	defer fr.mu.Unlock()
	for i := len(fr.calls) - 1; i >= 0; i-- {
		if strings.HasSuffix(fr.calls[i].argv[0], "/"+verb) {
			return fr.calls[i]
		}
	}
	t.Fatalf("no %s call recorded", verb)
	return fakeCall{}
}

func TestDeploySkill_StreamsTarToSkillInstall(t *testing.T) {
	c, fr := newFakeClient(map[string]fakeResp{"meta": {stdout: validMetaDoc}})

	files := map[string][]byte{
		"SKILL.md":          []byte("# hi"),
		"scripts/run.sh":    []byte("#!/bin/sh\necho hi"),
		"scripts/util/x.py": []byte("pass"),
		"./notes/../ref.md": []byte("ref"), // cleaned to ref.md
	}
	if err := c.DeploySkill(context.Background(), "my-skill", files); err != nil {
		t.Fatalf("DeploySkill: %v", err)
	}

	call := lastCall(t, fr, "skill-install")
	want := []string{verbPath("skill-install"), "--name", "my-skill"}
	if !reflect.DeepEqual(call.argv, want) {
		t.Fatalf("argv = %v, want %v", call.argv, want)
	}

	got := decodeTar(t, call.stdin)
	wantEntries := []tarEntry{
		{"SKILL.md", tar.TypeReg, 0o644, "# hi"},
		{"ref.md", tar.TypeReg, 0o644, "ref"},
		{"scripts/", tar.TypeDir, 0o755, ""},
		{"scripts/run.sh", tar.TypeReg, 0o755, "#!/bin/sh\necho hi"},
		{"scripts/util/", tar.TypeDir, 0o755, ""},
		{"scripts/util/x.py", tar.TypeReg, 0o644, "pass"},
	}
	if !reflect.DeepEqual(got, wantEntries) {
		t.Fatalf("tar entries:\n got  %+v\n want %+v", got, wantEntries)
	}
}

func TestBuildSkillTar_Deterministic(t *testing.T) {
	files := map[string][]byte{"a/b/c.txt": []byte("1"), "z.md": []byte("2"), "a/d.txt": []byte("3")}
	first, err := buildSkillTar(files)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := buildSkillTar(files)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, again) {
			t.Fatal("archive differs between builds")
		}
	}
}

func TestSkills_UnsupportedWithoutCapability(t *testing.T) {
	// Meta without the skills capability (chat is mandatory).
	noSkills := `{"contract":1,"agent":{"name":"x"},"capabilities":["chat"]}`
	c, fr := newFakeClient(map[string]fakeResp{"meta": {stdout: noSkills}})

	err := c.DeploySkill(context.Background(), "s", map[string][]byte{"SKILL.md": nil})
	if !errors.Is(err, agentshim.ErrSkillsUnsupported) {
		t.Fatalf("DeploySkill: want ErrSkillsUnsupported, got %v", err)
	}
	if err := c.RemoveSkill(context.Background(), "s"); !errors.Is(err, agentshim.ErrSkillsUnsupported) {
		t.Fatalf("RemoveSkill: want ErrSkillsUnsupported, got %v", err)
	}
	if n := fr.verbCalls("skill-install") + fr.verbCalls("skill-remove"); n != 0 {
		t.Fatalf("no skill verb execs expected, got %d", n)
	}
}

func TestSkills_SkillsDirNotRequired(t *testing.T) {
	meta := `{"contract":1,"agent":{"name":"x"},"capabilities":["chat","skills"]}`
	c, fr := newFakeClient(map[string]fakeResp{"meta": {stdout: meta}})
	if err := c.DeploySkill(context.Background(), "s", map[string][]byte{"SKILL.md": []byte("x")}); err != nil {
		t.Fatalf("DeploySkill: %v", err)
	}
	if fr.verbCalls("skill-install") != 1 {
		t.Fatal("expected one skill-install exec")
	}
}

func TestDeploySkill_RejectsUnsafeInputBeforeExec(t *testing.T) {
	c, fr := newFakeClient(map[string]fakeResp{"meta": {stdout: validMetaDoc}})

	for _, name := range []string{"../evil", "", "a/b", "/abs"} {
		if err := c.DeploySkill(context.Background(), name, nil); err == nil {
			t.Errorf("want error for skill name %q", name)
		}
		if err := c.RemoveSkill(context.Background(), name); err == nil {
			t.Errorf("RemoveSkill: want error for skill name %q", name)
		}
	}
	for _, p := range []string{"../../etc/passwd", "/etc/passwd", "a/../../x", ""} {
		if err := c.DeploySkill(context.Background(), "ok", map[string][]byte{p: nil}); err == nil {
			t.Errorf("want error for file path %q", p)
		}
	}
	if n := fr.verbCalls("skill-install") + fr.verbCalls("skill-remove"); n != 0 {
		t.Fatalf("no skill verb execs expected, got %d", n)
	}
}

func TestDeploySkill_ValidationExit(t *testing.T) {
	c, _ := newFakeClient(map[string]fakeResp{
		"meta":          {stdout: validMetaDoc},
		"skill-install": {code: ExitValidation, stdout: `{"error":"archive contains a symlink"}`},
	})
	err := c.DeploySkill(context.Background(), "s", map[string][]byte{"SKILL.md": []byte("x")})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError, got %v", err)
	}
	if ve.Message != "archive contains a symlink" {
		t.Fatalf("message = %q", ve.Message)
	}
}

func TestSkills_UnsupportedExit(t *testing.T) {
	c, _ := newFakeClient(map[string]fakeResp{
		"meta":          {stdout: validMetaDoc},
		"skill-install": {code: ExitUnsupported},
		"skill-remove":  {code: ExitUnsupported},
	})
	err := c.DeploySkill(context.Background(), "s", map[string][]byte{"SKILL.md": []byte("x")})
	if !errors.Is(err, agentshim.ErrSkillsUnsupported) || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("DeploySkill: want ErrSkillsUnsupported+ErrUnsupported, got %v", err)
	}
	err = c.RemoveSkill(context.Background(), "s")
	if !errors.Is(err, agentshim.ErrSkillsUnsupported) || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("RemoveSkill: want ErrSkillsUnsupported+ErrUnsupported, got %v", err)
	}
}

func TestRemoveSkill_InvokesSkillRemove(t *testing.T) {
	c, fr := newFakeClient(map[string]fakeResp{"meta": {stdout: validMetaDoc}})
	if err := c.RemoveSkill(context.Background(), "claworc-gmail"); err != nil {
		t.Fatalf("RemoveSkill: %v", err)
	}
	call := lastCall(t, fr, "skill-remove")
	want := []string{verbPath("skill-remove"), "--name", "claworc-gmail"}
	if !reflect.DeepEqual(call.argv, want) {
		t.Fatalf("argv = %v, want %v", call.argv, want)
	}
	if call.stdin != "" {
		t.Fatalf("unexpected stdin %q", call.stdin)
	}
}
