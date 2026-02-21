package sshproxy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- SSH Server Hardening Verification Tests ---
//
// These tests verify the SSH server configuration in agent/rootfs/etc/ssh/sshd_config.d/claworc.conf
// and the startup script in agent/rootfs/etc/s6-overlay/s6-rc.d/svc-sshd/run to ensure
// security hardening directives are properly set.

// findRepoRoot walks up from the test file to find the repository root
// (identified by having a go.mod or .git directory).
func findRepoRoot(t *testing.T) string {
	t.Helper()
	// Start from the current source file location
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file path")
	}

	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("cannot find repository root")
		}
		dir = parent
	}
}

// loadSSHDConfig reads the hardened sshd config from the agent directory.
func loadSSHDConfig(t *testing.T) string {
	t.Helper()
	root := findRepoRoot(t)
	configPath := filepath.Join(root, "agent", "rootfs", "etc", "ssh", "sshd_config.d", "claworc.conf")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read sshd config: %v", err)
	}
	return string(data)
}

// loadSSHDStartupScript reads the sshd startup script.
func loadSSHDStartupScript(t *testing.T) string {
	t.Helper()
	root := findRepoRoot(t)
	scriptPath := filepath.Join(root, "agent", "rootfs", "etc", "s6-overlay", "s6-rc.d", "svc-sshd", "run")
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read sshd startup script: %v", err)
	}
	return string(data)
}

// getConfigDirective extracts the value of an SSH config directive.
// Returns the value and true if found, empty string and false otherwise.
func getConfigDirective(config, directive string) (string, bool) {
	lines := strings.Split(config, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 && strings.EqualFold(parts[0], directive) {
			return strings.Join(parts[1:], " "), true
		}
	}
	return "", false
}

// TestSecurity_PasswordAuthDisabled verifies that password authentication
// is disabled, requiring public key authentication only.
func TestSecurity_PasswordAuthDisabled(t *testing.T) {
	config := loadSSHDConfig(t)

	val, ok := getConfigDirective(config, "PasswordAuthentication")
	if !ok {
		t.Fatal("SECURITY: PasswordAuthentication directive not found")
	}
	if val != "no" {
		t.Errorf("SECURITY: PasswordAuthentication = %q, want 'no'", val)
	}
}

// TestSecurity_EmptyPasswordsDisabled verifies that empty passwords are not permitted.
func TestSecurity_EmptyPasswordsDisabled(t *testing.T) {
	config := loadSSHDConfig(t)

	val, ok := getConfigDirective(config, "PermitEmptyPasswords")
	if !ok {
		t.Fatal("SECURITY: PermitEmptyPasswords directive not found")
	}
	if val != "no" {
		t.Errorf("SECURITY: PermitEmptyPasswords = %q, want 'no'", val)
	}
}

// TestSecurity_PubkeyAuthEnabled verifies that public key authentication is enabled.
func TestSecurity_PubkeyAuthEnabled(t *testing.T) {
	config := loadSSHDConfig(t)

	val, ok := getConfigDirective(config, "PubkeyAuthentication")
	if !ok {
		t.Fatal("SECURITY: PubkeyAuthentication directive not found")
	}
	if val != "yes" {
		t.Errorf("SECURITY: PubkeyAuthentication = %q, want 'yes'", val)
	}
}

// TestSecurity_RootLoginRestrictedToKey verifies that root login is restricted
// to public key authentication only (no password).
func TestSecurity_RootLoginRestrictedToKey(t *testing.T) {
	config := loadSSHDConfig(t)

	val, ok := getConfigDirective(config, "PermitRootLogin")
	if !ok {
		t.Fatal("SECURITY: PermitRootLogin directive not found")
	}
	if val != "prohibit-password" {
		t.Errorf("SECURITY: PermitRootLogin = %q, want 'prohibit-password'", val)
	}
}

// TestSecurity_MaxAuthTriesLimited verifies that the maximum number of
// authentication attempts per connection is limited.
func TestSecurity_MaxAuthTriesLimited(t *testing.T) {
	config := loadSSHDConfig(t)

	val, ok := getConfigDirective(config, "MaxAuthTries")
	if !ok {
		t.Fatal("SECURITY: MaxAuthTries directive not found")
	}
	if val != "3" {
		t.Errorf("SECURITY: MaxAuthTries = %q, want '3'", val)
	}
}

// TestSecurity_StrictModesEnabled verifies that strict file permission checks
// are enabled for SSH key files.
func TestSecurity_StrictModesEnabled(t *testing.T) {
	config := loadSSHDConfig(t)

	val, ok := getConfigDirective(config, "StrictModes")
	if !ok {
		t.Fatal("SECURITY: StrictModes directive not found")
	}
	if val != "yes" {
		t.Errorf("SECURITY: StrictModes = %q, want 'yes'", val)
	}
}

// TestSecurity_X11ForwardingDisabled verifies that X11 forwarding is disabled.
func TestSecurity_X11ForwardingDisabled(t *testing.T) {
	config := loadSSHDConfig(t)

	val, ok := getConfigDirective(config, "X11Forwarding")
	if !ok {
		t.Fatal("SECURITY: X11Forwarding directive not found")
	}
	if val != "no" {
		t.Errorf("SECURITY: X11Forwarding = %q, want 'no'", val)
	}
}

// TestSecurity_AgentForwardingDisabled verifies that SSH agent forwarding is disabled.
func TestSecurity_AgentForwardingDisabled(t *testing.T) {
	config := loadSSHDConfig(t)

	val, ok := getConfigDirective(config, "AllowAgentForwarding")
	if !ok {
		t.Fatal("SECURITY: AllowAgentForwarding directive not found")
	}
	if val != "no" {
		t.Errorf("SECURITY: AllowAgentForwarding = %q, want 'no'", val)
	}
}

// TestSecurity_TCPForwardingRestrictedToLocal verifies that TCP forwarding
// is restricted to local connections only.
func TestSecurity_TCPForwardingRestrictedToLocal(t *testing.T) {
	config := loadSSHDConfig(t)

	val, ok := getConfigDirective(config, "AllowTcpForwarding")
	if !ok {
		t.Fatal("SECURITY: AllowTcpForwarding directive not found")
	}
	if val != "local" {
		t.Errorf("SECURITY: AllowTcpForwarding = %q, want 'local'", val)
	}
}

// TestSecurity_PermitOpenRestrictedToPorts verifies that port forwarding is
// restricted to specific required localhost ports only.
func TestSecurity_PermitOpenRestrictedToPorts(t *testing.T) {
	config := loadSSHDConfig(t)

	val, ok := getConfigDirective(config, "PermitOpen")
	if !ok {
		t.Fatal("SECURITY: PermitOpen directive not found")
	}

	// Should only allow localhost:3000 and localhost:8080
	if !strings.Contains(val, "localhost:3000") {
		t.Error("SECURITY: PermitOpen should include localhost:3000")
	}
	if !strings.Contains(val, "localhost:8080") {
		t.Error("SECURITY: PermitOpen should include localhost:8080")
	}

	// Should not have any wildcard or broad patterns
	if strings.Contains(val, "*") || strings.Contains(val, "any") || strings.Contains(val, "0.0.0.0") {
		t.Error("SECURITY: PermitOpen should not contain wildcards or broad patterns")
	}
}

// TestSecurity_LoginGraceTimeLimited verifies that the authentication
// grace period is limited to prevent slow-loris style attacks.
func TestSecurity_LoginGraceTimeLimited(t *testing.T) {
	config := loadSSHDConfig(t)

	val, ok := getConfigDirective(config, "LoginGraceTime")
	if !ok {
		t.Fatal("SECURITY: LoginGraceTime directive not found")
	}
	if val != "30" {
		t.Errorf("SECURITY: LoginGraceTime = %q, want '30'", val)
	}
}

// TestSecurity_MaxStartupsConfigured verifies that MaxStartups is configured
// to prevent SSH connection flooding (DoS protection).
func TestSecurity_MaxStartupsConfigured(t *testing.T) {
	config := loadSSHDConfig(t)

	val, ok := getConfigDirective(config, "MaxStartups")
	if !ok {
		t.Fatal("SECURITY: MaxStartups directive not found")
	}
	if val != "10:30:60" {
		t.Errorf("SECURITY: MaxStartups = %q, want '10:30:60'", val)
	}
}

// TestSecurity_SyslogEnabled verifies that SSH logging to syslog is enabled
// for centralized monitoring.
func TestSecurity_SyslogEnabled(t *testing.T) {
	config := loadSSHDConfig(t)

	val, ok := getConfigDirective(config, "SyslogFacility")
	if !ok {
		t.Fatal("SECURITY: SyslogFacility directive not found")
	}
	if val != "AUTH" {
		t.Errorf("SECURITY: SyslogFacility = %q, want 'AUTH'", val)
	}

	val, ok = getConfigDirective(config, "LogLevel")
	if !ok {
		t.Fatal("SECURITY: LogLevel directive not found")
	}
	if val != "INFO" {
		t.Errorf("SECURITY: LogLevel = %q, want 'INFO'", val)
	}
}

// TestSecurity_LegacyHostKeysRemovedOnStartup verifies that the startup script
// removes legacy DSA and ECDSA host keys, keeping only Ed25519 and RSA.
func TestSecurity_LegacyHostKeysRemovedOnStartup(t *testing.T) {
	script := loadSSHDStartupScript(t)

	// Should remove DSA keys
	if !strings.Contains(script, "rm -f /etc/ssh/ssh_host_dsa_key") {
		t.Error("SECURITY: startup script should remove DSA host key")
	}

	// Should remove ECDSA keys
	if !strings.Contains(script, "rm -f /etc/ssh/ssh_host_ecdsa_key") {
		t.Error("SECURITY: startup script should remove ECDSA host key")
	}

	// Should generate keys
	if !strings.Contains(script, "ssh-keygen -A") {
		t.Error("SECURITY: startup script should generate host keys")
	}

	// Should remove DSA/ECDSA again after keygen (ssh-keygen -A may recreate them)
	lines := strings.Split(script, "\n")
	keygenLine := -1
	postKeygenDSARemoval := false
	postKeygenECDSARemoval := false
	for i, line := range lines {
		if strings.Contains(line, "ssh-keygen -A") {
			keygenLine = i
		}
		if keygenLine > 0 && i > keygenLine {
			if strings.Contains(line, "ssh_host_dsa_key") {
				postKeygenDSARemoval = true
			}
			if strings.Contains(line, "ssh_host_ecdsa_key") {
				postKeygenECDSARemoval = true
			}
		}
	}
	if !postKeygenDSARemoval {
		t.Error("SECURITY: startup script should remove DSA keys AFTER ssh-keygen -A")
	}
	if !postKeygenECDSARemoval {
		t.Error("SECURITY: startup script should remove ECDSA keys AFTER ssh-keygen -A")
	}
}

// TestSecurity_SSHDirectoryPermissions verifies that the startup script sets
// correct permissions on the .ssh directory.
func TestSecurity_SSHDirectoryPermissions(t *testing.T) {
	script := loadSSHDStartupScript(t)

	if !strings.Contains(script, "chmod 700 /root/.ssh") {
		t.Error("SECURITY: startup script should set /root/.ssh to mode 700")
	}
}

// TestSecurity_ED25519KeyGeneration verifies that the SSH key pair generation
// uses ED25519 algorithm (not RSA, DSA, or ECDSA).
func TestSecurity_ED25519KeyGeneration(t *testing.T) {
	pubKey, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}

	// Public key should be ed25519 type
	pubKeyStr := string(pubKey)
	if !strings.HasPrefix(pubKeyStr, "ssh-ed25519") {
		t.Errorf("SECURITY: expected ssh-ed25519 key type, got prefix: %s", pubKeyStr[:min(30, len(pubKeyStr))])
	}

	// Private key should be valid PEM
	if !strings.Contains(string(privKey), "PRIVATE KEY") {
		t.Error("SECURITY: private key is not in PEM format")
	}

	// Key should be parseable
	signer, err := ParsePrivateKey(privKey)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	// Signer public key type should be ed25519
	if signer.PublicKey().Type() != "ssh-ed25519" {
		t.Errorf("SECURITY: signer key type = %q, want 'ssh-ed25519'", signer.PublicKey().Type())
	}
}

// TestSecurity_PrivateKeyFilePermissions verifies that SaveKeyPair creates
// the private key file with restrictive permissions (0600).
func TestSecurity_PrivateKeyFilePermissions(t *testing.T) {
	dir := t.TempDir()

	pubKey, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if err := SaveKeyPair(dir, privKey, pubKey); err != nil {
		t.Fatalf("save: %v", err)
	}

	privInfo, err := os.Stat(filepath.Join(dir, "ssh_key"))
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if privInfo.Mode().Perm() != 0600 {
		t.Errorf("SECURITY: private key permissions = %o, want 0600", privInfo.Mode().Perm())
	}

	pubInfo, err := os.Stat(filepath.Join(dir, "ssh_key.pub"))
	if err != nil {
		t.Fatalf("stat public key: %v", err)
	}
	if pubInfo.Mode().Perm() != 0644 {
		t.Errorf("SECURITY: public key permissions = %o, want 0644", pubInfo.Mode().Perm())
	}
}

