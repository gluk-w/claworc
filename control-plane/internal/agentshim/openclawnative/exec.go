package openclawnative

import (
	"context"
	"fmt"
	"strings"

	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	gossh "golang.org/x/crypto/ssh"
)

// Instance runs openclaw CLI commands inside a legacy (pre-shim) OpenClaw
// container. Tests supply a mock.
type Instance interface {
	ExecOpenclaw(ctx context.Context, args ...string) (stdout, stderr string, code int, err error)
}

// SSHInstance implements Instance over a live SSH connection.
// All openclaw CLI calls are run as `su - claworc -c 'openclaw <args...>'`.
type SSHInstance struct{ client *gossh.Client }

// NewSSHInstance wraps an established SSH client as an Instance.
func NewSSHInstance(client *gossh.Client) *SSHInstance {
	return &SSHInstance{client: client}
}

// ExecOpenclaw runs `su - claworc -c 'openclaw <args...>'` over SSH.
// Each argument is shell-quoted to safely handle JSON and special characters.
func (i *SSHInstance) ExecOpenclaw(ctx context.Context, args ...string) (string, string, int, error) {
	// Guard against pathological input sizes that would overflow the slice length.
	const maxArgs = 1<<16 - 1
	if len(args) > maxArgs {
		return "", "", -1, fmt.Errorf("too many arguments: %d (max %d)", len(args), maxArgs)
	}
	parts := make([]string, len(args)+1)
	parts[0] = "openclaw"
	for j, a := range args {
		parts[j+1] = sshproxy.ShellQuote(a)
	}
	cmd := "su - claworc -c " + sshproxy.ShellQuote(strings.Join(parts, " "))
	return sshproxy.RunCommand(i.client, cmd)
}
