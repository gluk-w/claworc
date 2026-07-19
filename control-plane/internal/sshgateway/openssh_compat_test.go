package sshgateway

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

// TestOpenSSHClientCompat drives the gateway with the real `ssh` binary to
// catch protocol details the Go client tolerates but OpenSSH does not
// (banner handling, exit-status ordering, request semantics).
func TestOpenSSHClientCompat(t *testing.T) {
	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("ssh binary not available")
	}

	env := setupGateway(t)

	// Write the user's private key where the ssh CLI can use it. The DB
	// only has the registered public key; regenerate a matching PEM by
	// creating a fresh key for the same user instead.
	user, err := database.GetUserByUsername("stan")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	// Same format users download from the profile page.
	pubKey, privKeyPEM, err := sshproxy.GenerateOpenSSHKeyPair()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	parsed, _, _, _, _ := ssh.ParseAuthorizedKey(pubKey)
	if err := database.CreateUserSSHKey(&database.UserSSHKey{
		UserID:      user.ID,
		PublicKey:   string(pubKey),
		Fingerprint: ssh.FingerprintSHA256(parsed),
	}); err != nil {
		t.Fatalf("register key: %v", err)
	}

	keyPath := filepath.Join(t.TempDir(), "id_claworc")
	if err := os.WriteFile(keyPath, privKeyPEM, 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	host, port, err := net.SplitHostPort(env.gw.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}

	runSSH := func(loginUser, command string) (string, int) {
		t.Helper()
		cmd := exec.Command(sshBin,
			"-i", keyPath,
			"-p", port,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "IdentitiesOnly=yes",
			"-o", "BatchMode=yes",
			fmt.Sprintf("%s@%s", loginUser, host),
			command,
		)
		out, err := cmd.CombinedOutput()
		code := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else if err != nil {
			t.Fatalf("ssh: %v (output %q)", err, out)
		}
		return string(out), code
	}

	t.Run("exec with exit 0", func(t *testing.T) {
		out, code := runSSH("stan+my-agent", "hostname")
		if code != 0 {
			t.Errorf("exit code = %d, want 0 (output %q)", code, out)
		}
		if !strings.Contains(out, "ran:hostname") {
			t.Errorf("output %q missing exec result", out)
		}
	})

	t.Run("exit status forwarded", func(t *testing.T) {
		_, code := runSSH("stan+my-agent", "exit 7")
		if code != 7 {
			t.Errorf("exit code = %d, want 7", code)
		}
	})

	t.Run("missing instance help", func(t *testing.T) {
		out, code := runSSH("stan", "true")
		if code != 1 {
			t.Errorf("exit code = %d, want 1 (output %q)", code, out)
		}
		if !strings.Contains(out, "no instance specified") || !strings.Contains(out, "my-agent") {
			t.Errorf("output %q should explain and list instances", out)
		}
	})

	t.Run("revoked key refused", func(t *testing.T) {
		var key database.UserSSHKey
		if err := database.DB.Where("fingerprint = ?", ssh.FingerprintSHA256(parsed)).First(&key).Error; err != nil {
			t.Fatalf("find key: %v", err)
		}
		if err := database.DeleteUserSSHKey(user.ID, key.ID); err != nil && err != gorm.ErrRecordNotFound {
			t.Fatalf("revoke: %v", err)
		}
		out, code := runSSH("stan+my-agent", "hostname")
		if code == 0 {
			t.Errorf("expected failure after key revocation (output %q)", out)
		}
	})
}
