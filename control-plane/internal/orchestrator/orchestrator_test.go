package orchestrator

import (
	"testing"

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
