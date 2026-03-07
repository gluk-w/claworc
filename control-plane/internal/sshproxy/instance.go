package sshproxy

import (
	"context"
	"strings"

	gossh "golang.org/x/crypto/ssh"
)

// Instance represents an active connection to a running OpenClaw agent.
// All remote openclaw CLI calls go through this interface; tests supply a mock.
type Instance interface {
	ExecOpenclaw(ctx context.Context, args ...string) (stdout, stderr string, code int, err error)
}

// InstanceFactory resolves an Instance for the given instance name,
// blocking until SSH is available.
type InstanceFactory func(ctx context.Context, instanceName string) (Instance, error)

// ShellQuote wraps s in single quotes, escaping embedded single quotes.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
	parts := make([]string, len(args)+1)
	parts[0] = "openclaw"
	for j, a := range args {
		parts[j+1] = ShellQuote(a)
	}
	cmd := "su - claworc -c " + ShellQuote(strings.Join(parts, " "))
	return RunCommand(i.client, cmd)
}
