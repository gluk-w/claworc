package browserprov

import "testing"

func TestDeriveBrowserImage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		legacy string
		want   string
	}{
		{"glukw/openclaw-vnc-chromium:latest", "glukw/claworc-browser-chromium:latest"},
		{"glukw/openclaw-vnc-chrome:v1.2", "glukw/claworc-browser-chrome:v1.2"},
		{"glukw/openclaw-vnc-brave:latest", "glukw/claworc-browser-brave:latest"},
		{"docker.io/glukw/openclaw-vnc-chromium:dev", "docker.io/glukw/claworc-browser-chromium:dev"},
		{"glukw/openclaw-vnc-chromium", "glukw/claworc-browser-chromium:latest"},
		{"random/image:tag", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := deriveBrowserImage(tc.legacy)
		if got != tc.want {
			t.Errorf("deriveBrowserImage(%q) = %q, want %q", tc.legacy, got, tc.want)
		}
	}
}
