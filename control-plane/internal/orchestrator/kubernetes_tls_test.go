package orchestrator

import (
	"testing"
)

func TestBuildTLSSecret(t *testing.T) {
	secret := buildTLSSecret("bot-test", "claworc", "CERT_PEM", "KEY_PEM", "")

	if secret.Name != "bot-test-tls" {
		t.Errorf("secret name = %q, want %q", secret.Name, "bot-test-tls")
	}
	if secret.Namespace != "claworc" {
		t.Errorf("secret namespace = %q, want %q", secret.Namespace, "claworc")
	}
	if string(secret.Data["agent-tls-cert"]) != "CERT_PEM" {
		t.Errorf("secret cert = %q, want %q", string(secret.Data["agent-tls-cert"]), "CERT_PEM")
	}
	if string(secret.Data["agent-tls-key"]) != "KEY_PEM" {
		t.Errorf("secret key = %q, want %q", string(secret.Data["agent-tls-key"]), "KEY_PEM")
	}
	if secret.Labels["managed-by"] != "claworc" {
		t.Errorf("secret label managed-by = %q, want %q", secret.Labels["managed-by"], "claworc")
	}
	if secret.Labels["app"] != "bot-test" {
		t.Errorf("secret label app = %q, want %q", secret.Labels["app"], "bot-test")
	}
	// cp-ca.crt should not be present when ControlPlaneCA is empty
	if _, ok := secret.Data["cp-ca.crt"]; ok {
		t.Error("cp-ca.crt should not be present when ControlPlaneCA is empty")
	}
}

func TestBuildTLSSecret_WithControlPlaneCA(t *testing.T) {
	secret := buildTLSSecret("bot-test", "claworc", "CERT_PEM", "KEY_PEM", "CP_CA_PEM")

	if string(secret.Data["cp-ca.crt"]) != "CP_CA_PEM" {
		t.Errorf("secret cp-ca.crt = %q, want %q", string(secret.Data["cp-ca.crt"]), "CP_CA_PEM")
	}
	if string(secret.Data["agent-tls-cert"]) != "CERT_PEM" {
		t.Errorf("secret cert = %q, want %q", string(secret.Data["agent-tls-cert"]), "CERT_PEM")
	}
}

func TestBuildDeployment_WithTLS(t *testing.T) {
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
		AgentTLSCert:    "CERT_PEM",
		AgentTLSKey:     "KEY_PEM",
	}

	dep := buildDeployment(params, "claworc")
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("deployment has no containers")
	}

	// Check for agent-tls volume mount
	var foundMount bool
	for _, vm := range containers[0].VolumeMounts {
		if vm.Name == "agent-tls" {
			foundMount = true
			if vm.MountPath != "/config/ssl" {
				t.Errorf("agent-tls mount path = %q, want %q", vm.MountPath, "/config/ssl")
			}
			if !vm.ReadOnly {
				t.Error("agent-tls mount should be read-only")
			}
		}
	}
	if !foundMount {
		t.Error("agent-tls volume mount not found in container")
	}

	// Check for agent-tls volume
	var foundVolume bool
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Name == "agent-tls" {
			foundVolume = true
			if v.Secret == nil {
				t.Fatal("agent-tls volume has no secret source")
			}
			if v.Secret.SecretName != "bot-test-tls" {
				t.Errorf("agent-tls secret name = %q, want %q", v.Secret.SecretName, "bot-test-tls")
			}
			if v.Secret.DefaultMode == nil || *v.Secret.DefaultMode != 0400 {
				t.Error("agent-tls secret default mode should be 0400")
			}
		}
	}
	if !foundVolume {
		t.Error("agent-tls volume not found in deployment")
	}
}

func TestBuildDeployment_WithoutTLS(t *testing.T) {
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

	// Check that agent-tls volume mount is NOT present
	for _, vm := range containers[0].VolumeMounts {
		if vm.Name == "agent-tls" {
			t.Error("agent-tls volume mount should not be present when no TLS cert/key provided")
		}
	}

	// Check that agent-tls volume is NOT present
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Name == "agent-tls" {
			t.Error("agent-tls volume should not be present when no TLS cert/key provided")
		}
	}

	// Verify base volumes are still present (4: homebrew, openclaw, chrome, dshm)
	if len(dep.Spec.Template.Spec.Volumes) != 4 {
		t.Errorf("expected 4 volumes without TLS, got %d", len(dep.Spec.Template.Spec.Volumes))
	}
}
