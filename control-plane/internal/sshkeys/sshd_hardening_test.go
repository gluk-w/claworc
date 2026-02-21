package sshkeys

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// sshdConfigPath returns the absolute path to the agent's sshd_config file
// relative to this test file's location in the repository.
func sshdConfigPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	// Navigate from control-plane/internal/sshkeys/ up to repo root, then into agent/
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	cfgPath := filepath.Join(repoRoot, "agent", "rootfs", "etc", "ssh", "sshd_config.d", "claworc.conf")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("sshd config not found at %s: %v", cfgPath, err)
	}
	return cfgPath
}

// sshdRunScriptPath returns the absolute path to the agent's sshd run script.
func sshdRunScriptPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	scriptPath := filepath.Join(repoRoot, "agent", "rootfs", "etc", "s6-overlay", "s6-rc.d", "svc-sshd", "run")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("sshd run script not found at %s: %v", scriptPath, err)
	}
	return scriptPath
}

// parseSSHDConfig reads the sshd_config file and returns a map of directive -> value.
// For directives that appear multiple times, only the last value is kept.
func parseSSHDConfig(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open sshd config: %v", err)
	}
	defer f.Close()

	cfg := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			cfg[strings.ToLower(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("reading sshd config: %v", err)
	}
	return cfg
}

func TestSSHDConfig_PasswordAuthDisabled(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	if v := cfg["passwordauthentication"]; v != "no" {
		t.Errorf("PasswordAuthentication should be 'no', got %q", v)
	}
}

func TestSSHDConfig_EmptyPasswordsDisabled(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	if v := cfg["permitemptypasswords"]; v != "no" {
		t.Errorf("PermitEmptyPasswords should be 'no', got %q", v)
	}
}

func TestSSHDConfig_PubkeyAuthEnabled(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	if v := cfg["pubkeyauthentication"]; v != "yes" {
		t.Errorf("PubkeyAuthentication should be 'yes', got %q", v)
	}
}

func TestSSHDConfig_RootLoginProhibitPassword(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	v := cfg["permitrootlogin"]
	if v != "prohibit-password" {
		t.Errorf("PermitRootLogin should be 'prohibit-password', got %q", v)
	}
}

func TestSSHDConfig_StrictModes(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	if v := cfg["strictmodes"]; v != "yes" {
		t.Errorf("StrictModes should be 'yes', got %q", v)
	}
}

func TestSSHDConfig_MaxAuthTries(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	if v := cfg["maxauthtries"]; v != "3" {
		t.Errorf("MaxAuthTries should be '3', got %q", v)
	}
}

func TestSSHDConfig_LoginGraceTime(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	if v := cfg["logingracetime"]; v != "30" {
		t.Errorf("LoginGraceTime should be '30', got %q", v)
	}
}

func TestSSHDConfig_X11ForwardingDisabled(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	if v := cfg["x11forwarding"]; v != "no" {
		t.Errorf("X11Forwarding should be 'no', got %q", v)
	}
}

func TestSSHDConfig_AgentForwardingDisabled(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	if v := cfg["allowagentforwarding"]; v != "no" {
		t.Errorf("AllowAgentForwarding should be 'no', got %q", v)
	}
}

func TestSSHDConfig_TCPForwardingRestricted(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	v := cfg["allowtcpforwarding"]
	if v != "local" {
		t.Errorf("AllowTcpForwarding should be 'local', got %q", v)
	}
}

func TestSSHDConfig_PermitOpenRestricted(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	v := cfg["permitopen"]
	if v == "" {
		t.Fatal("PermitOpen directive is missing; should restrict forwarding to known ports")
	}
	if !strings.Contains(v, "localhost:3000") {
		t.Errorf("PermitOpen should include localhost:3000 (VNC), got %q", v)
	}
	if !strings.Contains(v, "localhost:8080") {
		t.Errorf("PermitOpen should include localhost:8080 (Gateway), got %q", v)
	}
}

func TestSSHDConfig_StreamLocalForwardingDisabled(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	if v := cfg["allowstreamlocalforwarding"]; v != "no" {
		t.Errorf("AllowStreamLocalForwarding should be 'no', got %q", v)
	}
}

func TestSSHDConfig_KbdInteractiveDisabled(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	if v := cfg["kbdinteractiveauthentication"]; v != "no" {
		t.Errorf("KbdInteractiveAuthentication should be 'no', got %q", v)
	}
}

func TestSSHDConfig_HostbasedAuthDisabled(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	if v := cfg["hostbasedauthentication"]; v != "no" {
		t.Errorf("HostbasedAuthentication should be 'no', got %q", v)
	}
}

func TestSSHDConfig_PAMDisabled(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	if v := cfg["usepam"]; v != "no" {
		t.Errorf("UsePAM should be 'no', got %q", v)
	}
}

func TestSSHDConfig_SyslogFacility(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	if v := cfg["syslogfacility"]; v != "AUTH" {
		t.Errorf("SyslogFacility should be 'AUTH', got %q", v)
	}
}

func TestSSHDConfig_LogLevel(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	if v := cfg["loglevel"]; v != "INFO" {
		t.Errorf("LogLevel should be 'INFO', got %q", v)
	}
}

func TestSSHDConfig_MaxStartups(t *testing.T) {
	cfg := parseSSHDConfig(t, sshdConfigPath(t))
	v := cfg["maxstartups"]
	if v == "" {
		t.Fatal("MaxStartups directive is missing; should limit unauthenticated connections")
	}
	// Expect rate-limiting format like "10:30:60"
	if !strings.Contains(v, ":") {
		t.Errorf("MaxStartups should use rate-limiting format (e.g. 10:30:60), got %q", v)
	}
}

func TestSSHDConfig_NoInsecureDefaults(t *testing.T) {
	raw, err := os.ReadFile(sshdConfigPath(t))
	if err != nil {
		t.Fatalf("read sshd config: %v", err)
	}
	content := strings.ToLower(string(raw))

	insecure := []string{
		"protocol 1",            // SSH protocol 1 is insecure
		"permitrootlogin yes",   // Should be prohibit-password, not yes
		"permitemptypasswords yes",
		"passwordauthentication yes",
	}
	for _, pattern := range insecure {
		if strings.Contains(content, pattern) {
			t.Errorf("sshd config contains insecure setting: %q", pattern)
		}
	}
}

func TestSSHDRunScript_HostKeyCleanup(t *testing.T) {
	raw, err := os.ReadFile(sshdRunScriptPath(t))
	if err != nil {
		t.Fatalf("read sshd run script: %v", err)
	}
	content := string(raw)

	// Verify legacy key types are removed
	if !strings.Contains(content, "ssh_host_dsa_key") {
		t.Error("run script should remove DSA host keys")
	}
	if !strings.Contains(content, "ssh_host_ecdsa_key") {
		t.Error("run script should remove ECDSA host keys")
	}
	// Verify it removes them (rm -f pattern)
	if !strings.Contains(content, "rm -f") {
		t.Error("run script should use rm -f to remove legacy host keys")
	}
}

func TestSSHDRunScript_ForegroundMode(t *testing.T) {
	raw, err := os.ReadFile(sshdRunScriptPath(t))
	if err != nil {
		t.Fatalf("read sshd run script: %v", err)
	}
	content := string(raw)

	// sshd must run in foreground (-D) for s6 supervision
	if !strings.Contains(content, "sshd -D") {
		t.Error("sshd should run in foreground mode (-D) for s6 supervision")
	}
}

func TestSSHDRunScript_LogsToFile(t *testing.T) {
	raw, err := os.ReadFile(sshdRunScriptPath(t))
	if err != nil {
		t.Fatalf("read sshd run script: %v", err)
	}
	content := string(raw)

	// Verify logs are redirected to the standard log file
	if !strings.Contains(content, "/var/log/sshd.log") {
		t.Error("sshd should log to /var/log/sshd.log for control plane log streaming")
	}
}
