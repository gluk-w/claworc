package shimexec

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCheckProgram(t *testing.T) {
	for _, ok := range [][]string{
		{verbPath("chat-send"), "--session", "x; rm -rf /"},
		{verbPath("meta")},
		{"cat", "/etc/hostname"},
	} {
		if err := checkProgram(ok); err != nil {
			t.Errorf("checkProgram(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range [][]string{
		nil,
		{"sh", "-c", "id"},
		{verbPath("../../bin/sh")},
		{verbPath("meta; id")},
		{"/opt/claworc/shim/"},
	} {
		if err := checkProgram(bad); err == nil {
			t.Errorf("checkProgram(%q) = nil, want error", bad)
		}
	}
}

// TestExecScriptRoundTrip runs the real execScript under /bin/sh and checks
// that hostile arguments reach the program byte-for-byte (nothing is
// shell-evaluated) and that the verb's own stdin follows the header intact.
func TestExecScriptRoundTrip(t *testing.T) {
	args := []string{
		"plain",
		"with space",
		"",
		"$(touch /tmp/pwned) `id` ; rm -rf / && echo hi",
		"'single' \"double\" \\backslash",
		"line1\nline2",
		"trailing newline\n\n",
		"ünïcødé ✓",
		"-n",
	}
	argv := append([]string{"/usr/bin/printf", "[%s]"}, args...)
	cmd := exec.Command("/bin/sh", "-c", execScript)
	cmd.Stdin = framedStdin(argv, nil)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("execScript: %v", err)
	}
	var want strings.Builder
	for _, a := range args {
		want.WriteString("[" + a + "]")
	}
	if string(out) != want.String() {
		t.Errorf("args mangled:\n got %q\nwant %q", out, want.String())
	}

	// The remainder of stdin belongs to the program.
	cmd = exec.Command("/bin/sh", "-c", execScript)
	cmd.Stdin = framedStdin([]string{"cat"}, strings.NewReader("payload\nmore\n"))
	out, err = cmd.Output()
	if err != nil || string(out) != "payload\nmore\n" {
		t.Errorf("stdin passthrough = %q, %v", out, err)
	}

	// A malformed header is rejected instead of executing anything.
	cmd = exec.Command("/bin/sh", "-c", execScript)
	cmd.Stdin = strings.NewReader("1; id\n")
	if err := cmd.Run(); err == nil {
		t.Error("malformed header accepted")
	} else if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 125 {
		t.Errorf("malformed header exit = %v, want 125", err)
	}
}
