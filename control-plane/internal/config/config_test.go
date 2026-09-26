package config

import "testing"

// TestLoad_InternalProxyPort covers the env var precedence: the new
// CLAWORC_INTERNAL_PROXY_PORT wins, the legacy CLAWORC_LLM_GATEWAY_PORT is
// honored as a fallback, and the default applies when neither is set.
func TestLoad_InternalProxyPort(t *testing.T) {
	cases := []struct {
		name      string
		newVar    string
		legacyVar string
		want      int
	}{
		{"default", "", "", 40001},
		{"legacy fallback", "", "40002", 40002},
		{"new var wins", "40003", "40002", 40003},
		{"invalid legacy ignored", "", "not-a-port", 40001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.newVar != "" {
				t.Setenv("CLAWORC_INTERNAL_PROXY_PORT", tc.newVar)
			}
			if tc.legacyVar != "" {
				t.Setenv("CLAWORC_LLM_GATEWAY_PORT", tc.legacyVar)
			}
			Cfg = Settings{}
			Load()
			if Cfg.InternalProxyPort != tc.want {
				t.Errorf("InternalProxyPort = %d, want %d", Cfg.InternalProxyPort, tc.want)
			}
		})
	}
}
