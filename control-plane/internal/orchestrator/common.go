package orchestrator

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

const PathOpenClawConfig = "/home/claworc/.openclaw/openclaw.json"

// ExecFunc matches the ExecInInstance method signature.
type ExecFunc func(ctx context.Context, name string, cmd []string) (string, string, int, error)

func configureSSHAccess(ctx context.Context, execFn ExecFunc, name string, publicKey string) error {
	// Ensure /root/.ssh directory exists with correct permissions
	_, stderr, code, err := execFn(ctx, name, []string{"sh", "-c", "mkdir -p /root/.ssh && chmod 700 /root/.ssh"})
	if err != nil {
		return fmt.Errorf("create .ssh directory: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("create .ssh directory: %s", stderr)
	}

	// Write the public key to authorized_keys using base64 to safely pass content through exec
	b64 := base64.StdEncoding.EncodeToString([]byte(publicKey))
	cmd := []string{"sh", "-c", fmt.Sprintf("echo '%s' | base64 -d > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys", b64)}
	_, stderr, code, err = execFn(ctx, name, cmd)
	if err != nil {
		return fmt.Errorf("write authorized_keys: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("write authorized_keys: %s", stderr)
	}

	return nil
}

func updateInstanceConfig(ctx context.Context, execFn ExecFunc, factory sshproxy.InstanceFactory, name string, configJSON string) error {
	// Write config file via exec (not an openclaw CLI call)
	b64 := base64.StdEncoding.EncodeToString([]byte(configJSON))
	cmd := []string{"sh", "-c", fmt.Sprintf("echo '%s' | base64 -d > %s", b64, PathOpenClawConfig)}
	_, stderr, code, err := execFn(ctx, name, cmd)
	if err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("write config: %s", stderr)
	}

	inst, err := factory(ctx, name)
	if err != nil {
		return fmt.Errorf("get instance connection: %w", err)
	}
	if _, stderr, code, err := inst.ExecOpenclaw(ctx, "gateway", "stop"); err != nil || code != 0 {
		return fmt.Errorf("restart gateway: %v %s", err, stderr)
	}
	return nil
}
