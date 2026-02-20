package orchestrator

import (
	"testing"

	"k8s.io/client-go/rest"
)

func TestBuildService_IncludesTunnelPort(t *testing.T) {
	svc := buildService("bot-test", "claworc")

	if svc.Name != "bot-test-vnc" {
		t.Errorf("service name = %q, want %q", svc.Name, "bot-test-vnc")
	}
	if svc.Namespace != "claworc" {
		t.Errorf("service namespace = %q, want %q", svc.Namespace, "claworc")
	}

	if len(svc.Spec.Ports) != 2 {
		t.Fatalf("expected 2 service ports, got %d", len(svc.Spec.Ports))
	}

	// Check HTTP port
	httpPort := svc.Spec.Ports[0]
	if httpPort.Name != "http" {
		t.Errorf("first port name = %q, want %q", httpPort.Name, "http")
	}
	if httpPort.Port != 3000 {
		t.Errorf("first port = %d, want %d", httpPort.Port, 3000)
	}

	// Check tunnel port
	tunnelPort := svc.Spec.Ports[1]
	if tunnelPort.Name != "tunnel" {
		t.Errorf("second port name = %q, want %q", tunnelPort.Name, "tunnel")
	}
	if tunnelPort.Port != 3001 {
		t.Errorf("second port = %d, want %d", tunnelPort.Port, 3001)
	}
	if tunnelPort.TargetPort.IntValue() != 3001 {
		t.Errorf("second port target = %d, want %d", tunnelPort.TargetPort.IntValue(), 3001)
	}
}

func TestBuildDeployment_IncludesTunnelContainerPort(t *testing.T) {
	params := CreateParams{
		Name:            "bot-test",
		CPURequest:      "500m",
		CPULimit:        "2000m",
		MemoryRequest:   "1Gi",
		MemoryLimit:     "4Gi",
		StorageHomebrew: "10Gi",
		StorageClawd:    "5Gi",
		StorageChrome:   "5Gi",
		ContainerImage:  "test-image:latest",
		VNCResolution:   "1920x1080",
		EnvVars:         map[string]string{},
	}

	dep := buildDeployment(params, "claworc")
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("deployment has no containers")
	}

	ports := containers[0].Ports
	if len(ports) != 2 {
		t.Fatalf("expected 2 container ports, got %d", len(ports))
	}

	var foundHTTP, foundTunnel bool
	for _, p := range ports {
		switch p.Name {
		case "http":
			foundHTTP = true
			if p.ContainerPort != 3000 {
				t.Errorf("http container port = %d, want %d", p.ContainerPort, 3000)
			}
		case "tunnel":
			foundTunnel = true
			if p.ContainerPort != 3001 {
				t.Errorf("tunnel container port = %d, want %d", p.ContainerPort, 3001)
			}
		}
	}

	if !foundHTTP {
		t.Error("http container port not found")
	}
	if !foundTunnel {
		t.Error("tunnel container port not found")
	}
}

func TestKubernetesOrchestrator_GetAgentTunnelAddr_InCluster(t *testing.T) {
	k := &KubernetesOrchestrator{
		inCluster: true,
	}

	// Use the config package's default namespace by setting it directly
	// Since ns() reads config.Cfg.K8sNamespace, we need that to be set.
	// In tests, the config may not be initialized, so we test the structure.
	// The ns() method reads from config.Cfg.K8sNamespace which defaults to "claworc".
	addr, err := k.GetAgentTunnelAddr(nil, "bot-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "bot-test-vnc." + k.ns() + ".svc.cluster.local:3001"
	if addr != expected {
		t.Errorf("addr = %q, want %q", addr, expected)
	}
}

func TestKubernetesOrchestrator_GetAgentTunnelAddr_OutOfCluster(t *testing.T) {
	k := &KubernetesOrchestrator{
		inCluster: false,
		restConfig: &rest.Config{
			Host: "https://127.0.0.1:6443",
		},
	}

	addr, err := k.GetAgentTunnelAddr(nil, "bot-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "https://127.0.0.1:6443/api/v1/namespaces/" + k.ns() + "/services/bot-test-vnc:3001/proxy"
	if addr != expected {
		t.Errorf("addr = %q, want %q", addr, expected)
	}
}

func TestKubernetesOrchestrator_GetAgentTunnelAddr_OutOfCluster_TrailingSlash(t *testing.T) {
	k := &KubernetesOrchestrator{
		inCluster: false,
		restConfig: &rest.Config{
			Host: "https://127.0.0.1:6443/",
		},
	}

	addr, err := k.GetAgentTunnelAddr(nil, "bot-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should strip trailing slash from host
	expected := "https://127.0.0.1:6443/api/v1/namespaces/" + k.ns() + "/services/bot-test-vnc:3001/proxy"
	if addr != expected {
		t.Errorf("addr = %q, want %q", addr, expected)
	}
}
