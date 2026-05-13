package browserprov

import "testing"

func TestDeriveBrowserImage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		legacy string
		want   string
	}{
		{"glukw/openclaw-vnc-chromium:latest", "claworc/chromium-browser:latest"},
		{"glukw/openclaw-vnc-chrome:v1.2", "claworc/chrome-browser:v1.2"},
		{"glukw/openclaw-vnc-brave:latest", "claworc/brave-browser:latest"},
		{"docker.io/glukw/openclaw-vnc-chromium:dev", "claworc/chromium-browser:dev"},
		{"glukw/openclaw-vnc-chromium", "claworc/chromium-browser:latest"},
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
