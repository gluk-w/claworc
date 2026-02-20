package orchestrator

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"time"
)

const PathOpenClawConfig = "/config/.openclaw/openclaw.json"

var cmdGatewayStop = []string{"su", "-", "abc", "-c", "openclaw gateway stop"}

// ExecFunc matches the ExecInInstance method signature.
type ExecFunc func(ctx context.Context, name string, cmd []string) (string, string, int, error)

// WaitFunc waits for an instance to become ready before exec is possible.
// Returns the resolved image info (tag + SHA) and whether the instance is ready.
type WaitFunc func(ctx context.Context, name string, timeout time.Duration) (imageInfo string, ready bool)

func configureGatewayToken(ctx context.Context, execFn ExecFunc, name, token string, waitFn WaitFunc) {
	imageInfo, ready := waitFn(ctx, name, 120*time.Second)
	if !ready {
		log.Printf("Timed out waiting for %s to start; gateway token not configured", name)
		return
	}
	cmd := []string{"su", "-", "abc", "-c", fmt.Sprintf("openclaw config set gateway.auth.token %s", token)}
	_, stderr, code, err := execFn(ctx, name, cmd)
	if err != nil {
		log.Printf("Error configuring gateway token for %s: %v (image: %s)", name, err, imageInfo)
		return
	}
	if code != 0 {
		log.Printf("Failed to configure gateway token for %s: %s (image: %s)", name, stderr, imageInfo)
		return
	}
	_, stderr, code, err = execFn(ctx, name, cmdGatewayStop)
	if err != nil {
		log.Printf("Error restarting gateway for %s: %v (image: %s)", name, err, imageInfo)
		return
	}
	if code != 0 {
		log.Printf("Failed to restart gateway for %s: %s (image: %s)", name, stderr, imageInfo)
		return
	}
	log.Printf("Gateway token configured for %s (image: %s)", name, imageInfo)
}

func updateInstanceConfig(ctx context.Context, execFn ExecFunc, name string, configJSON string) error {
	b64 := base64.StdEncoding.EncodeToString([]byte(configJSON))
	cmd := []string{"sh", "-c", fmt.Sprintf("echo '%s' | base64 -d > %s", b64, PathOpenClawConfig)}
	_, stderr, code, err := execFn(ctx, name, cmd)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("write config: %s", stderr)
	}

	_, stderr, code, err = execFn(ctx, name, cmdGatewayStop)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("restart gateway: %s", stderr)
	}
	return nil
}

