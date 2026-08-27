package handlers

import (
	"strings"
	"testing"
)

func TestLogSafe(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain value is untouched", "gmail", "gmail"},
		{"newline cannot start a forged entry", "gmail\n2026/01/01 fake entry", `gmail\n2026/01/01 fake entry`},
		{"carriage return is escaped too", "a\r\nb", `a\r\nb`},
		{"empty stays empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := logSafe(tc.in); got != tc.want {
				t.Errorf("logSafe(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestLogSafeTruncates(t *testing.T) {
	got := logSafe(strings.Repeat("x", 500))
	if want := strings.Repeat("x", 200) + "…"; got != want {
		t.Errorf("logSafe did not truncate to 200 runes: len=%d", len([]rune(got)))
	}
}

func TestLogSafeTruncatesOnRuneBoundary(t *testing.T) {
	// 500 multi-byte runes: a byte-wise cut would leave a mangled character.
	got := logSafe(strings.Repeat("é", 500))
	if r := []rune(got); len(r) != 201 { // 200 runes + the ellipsis
		t.Fatalf("expected 201 runes, got %d", len(r))
	}
	if !strings.HasSuffix(got, "é…") {
		t.Errorf("truncation split a rune: %q", got[len(got)-8:])
	}
}
