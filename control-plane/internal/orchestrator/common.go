package orchestrator

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/logutil"
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
		log.Printf("Timed out waiting for %s to start; gateway token not configured", logutil.SanitizeForLog(name))
		return
	}
	cmd := []string{"su", "-", "abc", "-c", fmt.Sprintf("openclaw config set gateway.auth.token %s", token)}
	_, stderr, code, err := execFn(ctx, name, cmd)
	if err != nil {
		log.Printf("Error configuring gateway token for %s: %v (image: %s)", logutil.SanitizeForLog(name), err, logutil.SanitizeForLog(imageInfo))
		return
	}
	if code != 0 {
		log.Printf("Failed to configure gateway token for %s: %s (image: %s)", logutil.SanitizeForLog(name), logutil.SanitizeForLog(stderr), logutil.SanitizeForLog(imageInfo))
		return
	}
	_, stderr, code, err = execFn(ctx, name, cmdGatewayStop)
	if err != nil {
		log.Printf("Error restarting gateway for %s: %v (image: %s)", logutil.SanitizeForLog(name), err, logutil.SanitizeForLog(imageInfo))
		return
	}
	if code != 0 {
		log.Printf("Failed to restart gateway for %s: %s (image: %s)", logutil.SanitizeForLog(name), logutil.SanitizeForLog(stderr), logutil.SanitizeForLog(imageInfo))
		return
	}
	log.Printf("Gateway token configured for %s (image: %s)", logutil.SanitizeForLog(name), logutil.SanitizeForLog(imageInfo))
}

// configureSSHAuthorizedKey waits for the container to be running, then writes
// the given SSH public key to /root/.ssh/authorized_keys with proper permissions.
func configureSSHAuthorizedKey(ctx context.Context, execFn ExecFunc, name, publicKey string, waitFn WaitFunc) {
	_, ready := waitFn(ctx, name, 120*time.Second)
	if !ready {
		log.Printf("Timed out waiting for %s to start; SSH public key not configured", logutil.SanitizeForLog(name))
		return
	}

	// Create /root/.ssh directory with 700 permissions
	_, stderr, code, err := execFn(ctx, name, []string{"sh", "-c", "mkdir -p /root/.ssh && chmod 700 /root/.ssh"})
	if err != nil {
		log.Printf("Error creating .ssh dir for %s: %v", logutil.SanitizeForLog(name), err)
		return
	}
	if code != 0 {
		log.Printf("Failed to create .ssh dir for %s: %s", logutil.SanitizeForLog(name), logutil.SanitizeForLog(stderr))
		return
	}

	// Write the authorized_keys file using base64 to avoid shell escaping issues
	b64 := base64.StdEncoding.EncodeToString([]byte(publicKey))
	cmd := []string{"sh", "-c", fmt.Sprintf("echo '%s' | base64 -d > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys", b64)}
	_, stderr, code, err = execFn(ctx, name, cmd)
	if err != nil {
		log.Printf("Error writing authorized_keys for %s: %v", logutil.SanitizeForLog(name), err)
		return
	}
	if code != 0 {
		log.Printf("Failed to write authorized_keys for %s: %s", logutil.SanitizeForLog(name), logutil.SanitizeForLog(stderr))
		return
	}

	log.Printf("SSH public key configured for %s", logutil.SanitizeForLog(name))
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

func listDirectory(ctx context.Context, execFn ExecFunc, name string, path string) ([]FileEntry, error) {
	stdout, stderr, code, err := execFn(ctx, name, []string{"ls", "-la", "--color=never", path})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("list directory: %s", stderr)
	}
	return ParseLsOutput(stdout), nil
}

func readFile(ctx context.Context, execFn ExecFunc, name string, path string) ([]byte, error) {
	stdout, stderr, code, err := execFn(ctx, name, []string{"cat", path})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("read file: %s", stderr)
	}
	return []byte(stdout), nil
}

func createFile(ctx context.Context, execFn ExecFunc, name string, path string, content string) error {
	escaped := strings.ReplaceAll(content, "'", "'\\''")
	cmd := []string{"sh", "-c", fmt.Sprintf("echo -n '%s' > '%s'", escaped, path)}
	_, stderr, code, err := execFn(ctx, name, cmd)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("create file: %s", stderr)
	}
	return nil
}

func createDirectory(ctx context.Context, execFn ExecFunc, name string, path string) error {
	_, stderr, code, err := execFn(ctx, name, []string{"mkdir", "-p", path})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("create directory: %s", stderr)
	}
	return nil
}

func writeFile(ctx context.Context, execFn ExecFunc, name string, path string, data []byte) error {
	// Write in chunks to avoid "argument list too long" for large files.
	// 48KB raw → ~64KB base64, well under the typical 128KB–2MB arg limit.
	const chunkSize = 48000

	// Truncate / create the target file
	_, stderr, code, err := execFn(ctx, name, []string{"sh", "-c", fmt.Sprintf("> '%s'", path)})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("write file: %s", stderr)
	}

	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		b64 := base64.StdEncoding.EncodeToString(data[i:end])
		cmd := []string{"sh", "-c", fmt.Sprintf("echo '%s' | base64 -d >> '%s'", b64, path)}
		_, stderr, code, err = execFn(ctx, name, cmd)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("write file: %s", stderr)
		}
	}

	return nil
}

// ParseLsOutput parses the output of `ls -la` into FileEntry slices.
func ParseLsOutput(output string) []FileEntry {
	var entries []FileEntry
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) <= 1 {
		return entries
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 9 {
			continue
		}
		permissions := parts[0]
		size := parts[4]
		entryName := strings.Join(parts[8:], " ")

		if entryName == "." || entryName == ".." {
			continue
		}

		isDir := strings.HasPrefix(permissions, "d")
		isLink := strings.HasPrefix(permissions, "l")

		entryType := "file"
		if isDir {
			entryType = "directory"
		} else if isLink {
			entryType = "symlink"
		}

		var sizePtr *string
		if !isDir {
			sizePtr = &size
		}

		entries = append(entries, FileEntry{
			Name:        entryName,
			Type:        entryType,
			Size:        sizePtr,
			Permissions: permissions,
		})
	}
	return entries
}
