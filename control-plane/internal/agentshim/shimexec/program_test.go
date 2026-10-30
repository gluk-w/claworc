package shimexec

import "testing"

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

func TestShellJoinRejectsNonShimProgram(t *testing.T) {
	if _, err := shellJoin([]string{"bash", "-c", "id"}); err == nil {
		t.Fatal("shellJoin accepted a non-shim program")
	}
	got, err := shellJoin([]string{verbPath("meta"), "a b"})
	if err != nil || got != "'/opt/claworc/shim/meta' 'a b'" {
		t.Fatalf("shellJoin = %q, %v", got, err)
	}
}
