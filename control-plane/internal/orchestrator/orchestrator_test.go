package orchestrator

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestBuildServiceIncludesSSHPort(t *testing.T) {
	svc := buildService("bot-test", "claworc")

	if svc.Name != "bot-test-vnc" {
		t.Errorf("expected service name bot-test-vnc, got %s", svc.Name)
	}
	if svc.Namespace != "claworc" {
		t.Errorf("expected namespace claworc, got %s", svc.Namespace)
	}

	// Verify both HTTP and SSH ports exist
	foundHTTP := false
	foundSSH := false
	for _, p := range svc.Spec.Ports {
		if p.Name == "http" && p.Port == 3000 && p.TargetPort == intstr.FromInt32(3000) {
			foundHTTP = true
		}
		if p.Name == "ssh" && p.Port == 22 && p.TargetPort == intstr.FromInt32(22) {
			foundSSH = true
		}
	}
	if !foundHTTP {
		t.Error("expected HTTP port (3000) in service spec")
	}
	if !foundSSH {
		t.Error("expected SSH port (22) in service spec")
	}
}

func TestBuildServiceSelector(t *testing.T) {
	svc := buildService("bot-myinstance", "default")

	sel, ok := svc.Spec.Selector["app"]
	if !ok || sel != "bot-myinstance" {
		t.Errorf("expected selector app=bot-myinstance, got %v", svc.Spec.Selector)
	}
}

func TestBuildDeploymentContainerPorts(t *testing.T) {
	params := CreateParams{
		Name:            "bot-test",
		CPURequest:      "100m",
		CPULimit:        "1",
		MemoryRequest:   "256Mi",
		MemoryLimit:     "1Gi",
		StorageHomebrew: "1Gi",
		StorageClawd:    "1Gi",
		StorageChrome:   "1Gi",
		ContainerImage:  "test:latest",
		VNCResolution:   "1920x1080",
		EnvVars:         map[string]string{},
	}

	dep := buildDeployment(params, "claworc")

	containers := dep.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}

	foundHTTP := false
	for _, p := range containers[0].Ports {
		if p.Name == "http" && p.ContainerPort == 3000 {
			foundHTTP = true
		}
	}
	if !foundHTTP {
		t.Error("expected HTTP container port (3000)")
	}
}

func TestContainerOrchestratorInterfaceHasSSHEndpoint(t *testing.T) {
	// Compile-time check that both implementations satisfy the interface.
	// These are already checked by var _ lines but this documents the SSH endpoint requirement.
	var _ ContainerOrchestrator = (*DockerOrchestrator)(nil)
	// KubernetesOrchestrator doesn't have a var _ check, so verify it compiles:
	var _ ContainerOrchestrator = (*KubernetesOrchestrator)(nil)
}

func TestCreateParamsHasSSHPublicKey(t *testing.T) {
	params := CreateParams{
		Name:         "bot-test",
		SSHPublicKey: "ssh-ed25519 AAAA... test@host\n",
	}
	if params.SSHPublicKey == "" {
		t.Error("expected SSHPublicKey to be set on CreateParams")
	}
}

func TestConfigureSSHAuthorizedKey_Success(t *testing.T) {
	publicKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest claworc@test\n"
	expectedB64 := base64.StdEncoding.EncodeToString([]byte(publicKey))

	var mu sync.Mutex
	var execCalls [][]string

	mockExec := func(ctx context.Context, name string, cmd []string) (string, string, int, error) {
		mu.Lock()
		defer mu.Unlock()
		execCalls = append(execCalls, cmd)
		return "", "", 0, nil
	}

	mockWait := func(ctx context.Context, name string, timeout time.Duration) (string, bool) {
		return "test:latest (sha256:abc123)", true
	}

	configureSSHAuthorizedKey(context.Background(), mockExec, "bot-test", publicKey, mockWait)

	mu.Lock()
	defer mu.Unlock()

	if len(execCalls) != 2 {
		t.Fatalf("expected 2 exec calls, got %d", len(execCalls))
	}

	// First call: create .ssh directory
	mkdirCmd := strings.Join(execCalls[0], " ")
	if !strings.Contains(mkdirCmd, "mkdir -p /root/.ssh") {
		t.Errorf("expected mkdir command, got: %s", mkdirCmd)
	}
	if !strings.Contains(mkdirCmd, "chmod 700 /root/.ssh") {
		t.Errorf("expected chmod 700 in mkdir command, got: %s", mkdirCmd)
	}

	// Second call: write authorized_keys with base64
	writeCmd := strings.Join(execCalls[1], " ")
	if !strings.Contains(writeCmd, expectedB64) {
		t.Errorf("expected base64-encoded key in write command, got: %s", writeCmd)
	}
	if !strings.Contains(writeCmd, "/root/.ssh/authorized_keys") {
		t.Errorf("expected authorized_keys path in write command, got: %s", writeCmd)
	}
	if !strings.Contains(writeCmd, "chmod 600 /root/.ssh/authorized_keys") {
		t.Errorf("expected chmod 600 in write command, got: %s", writeCmd)
	}
}

func TestConfigureSSHAuthorizedKey_WaitTimeout(t *testing.T) {
	execCalled := false
	mockExec := func(ctx context.Context, name string, cmd []string) (string, string, int, error) {
		execCalled = true
		return "", "", 0, nil
	}

	mockWait := func(ctx context.Context, name string, timeout time.Duration) (string, bool) {
		return "", false // Not ready
	}

	configureSSHAuthorizedKey(context.Background(), mockExec, "bot-test", "ssh-ed25519 AAA...\n", mockWait)

	if execCalled {
		t.Error("exec should not have been called when wait timed out")
	}
}

func TestConfigureSSHAuthorizedKey_MkdirFailure(t *testing.T) {
	callCount := 0
	mockExec := func(ctx context.Context, name string, cmd []string) (string, string, int, error) {
		callCount++
		if callCount == 1 {
			return "", "permission denied", 1, nil // mkdir fails
		}
		return "", "", 0, nil
	}

	mockWait := func(ctx context.Context, name string, timeout time.Duration) (string, bool) {
		return "test:latest", true
	}

	configureSSHAuthorizedKey(context.Background(), mockExec, "bot-test", "ssh-ed25519 AAA...\n", mockWait)

	if callCount != 1 {
		t.Errorf("expected only 1 exec call (mkdir), got %d", callCount)
	}
}

func TestConfigureSSHAuthorizedKey_WriteFailure(t *testing.T) {
	callCount := 0
	mockExec := func(ctx context.Context, name string, cmd []string) (string, string, int, error) {
		callCount++
		if callCount == 2 {
			return "", "disk full", 1, nil // write fails
		}
		return "", "", 0, nil
	}

	mockWait := func(ctx context.Context, name string, timeout time.Duration) (string, bool) {
		return "test:latest", true
	}

	configureSSHAuthorizedKey(context.Background(), mockExec, "bot-test", "ssh-ed25519 AAA...\n", mockWait)

	if callCount != 2 {
		t.Errorf("expected 2 exec calls (mkdir + write), got %d", callCount)
	}
}

func TestConfigureSSHAuthorizedKey_EmptyKey(t *testing.T) {
	// When SSHPublicKey is empty, configureSSHAuthorizedKey shouldn't typically
	// be called (the caller checks), but verify it handles it gracefully
	callCount := 0
	mockExec := func(ctx context.Context, name string, cmd []string) (string, string, int, error) {
		callCount++
		return "", "", 0, nil
	}

	mockWait := func(ctx context.Context, name string, timeout time.Duration) (string, bool) {
		return "test:latest", true
	}

	configureSSHAuthorizedKey(context.Background(), mockExec, "bot-test", "", mockWait)

	// It should still attempt to write (the empty key case is caught by the caller)
	if callCount != 2 {
		t.Errorf("expected 2 exec calls, got %d", callCount)
	}
}
