package browserprov

import (
	"testing"
	"time"
)

func TestParsePositiveInt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in    string
		want  int
		valid bool
	}{
		{"15", 15, true},
		{"60", 60, true},
		{"0", 0, false},
		{"-5", 0, false},
		{"", 0, false},
		{"abc", 0, false},
		{"15s", 0, false}, // we expect plain integers; suffix is not allowed
	}
	for _, tc := range cases {
		got, ok := parsePositiveInt(tc.in)
		if ok != tc.valid {
			t.Errorf("parsePositiveInt(%q) ok=%v want %v", tc.in, ok, tc.valid)
		}
		if ok && got != tc.want {
			t.Errorf("parsePositiveInt(%q) = %d want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseMinutesAndSeconds(t *testing.T) {
	t.Parallel()
	if d, ok := parseMinutes("15"); !ok || d != 15*time.Minute {
		t.Errorf("parseMinutes(15) = %v ok=%v", d, ok)
	}
	if d, ok := parseSeconds("60"); !ok || d != 60*time.Second {
		t.Errorf("parseSeconds(60) = %v ok=%v", d, ok)
	}
	if _, ok := parseMinutes("nope"); ok {
		t.Errorf("parseMinutes(nope) accepted invalid input")
	}
}
