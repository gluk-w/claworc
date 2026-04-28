package orchestrator

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestBuildBrowserDeployment_DownloadsMountAndAffinity(t *testing.T) {
	params := BrowserPodParams{
		Name:          "bot-foo",
		Image:         "glukw/claworc-browser-chromium:latest",
		StorageSize:   "10Gi",
		VNCResolution: "1920x1080",
	}
	dep := buildBrowserDeployment(params, "bot-foo-browser", "bot-foo-browser", "claworc", "bot-foo")

	spec := dep.Spec.Template.Spec
	if len(spec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(spec.Containers))
	}
	mounts := spec.Containers[0].VolumeMounts
	var found corev1.VolumeMount
	for _, m := range mounts {
		if m.Name == "agent-downloads" {
			found = m
			break
		}
	}
	if found.Name == "" {
		t.Fatalf("expected agent-downloads volume mount, got mounts=%+v", mounts)
	}
	if found.MountPath != "/home/claworc/Downloads" {
		t.Errorf("agent-downloads mountPath = %q, want /home/claworc/Downloads", found.MountPath)
	}
	if found.SubPath != "Downloads" {
		t.Errorf("agent-downloads subPath = %q, want Downloads", found.SubPath)
	}

	var foundVol corev1.Volume
	for _, v := range spec.Volumes {
		if v.Name == "agent-downloads" {
			foundVol = v
			break
		}
	}
	if foundVol.Name == "" {
		t.Fatalf("expected agent-downloads volume, got volumes=%+v", spec.Volumes)
	}
	if foundVol.PersistentVolumeClaim == nil || foundVol.PersistentVolumeClaim.ClaimName != "bot-foo-home" {
		t.Errorf("agent-downloads volume should reference PVC bot-foo-home, got %+v", foundVol.PersistentVolumeClaim)
	}

	if spec.Affinity == nil || spec.Affinity.PodAffinity == nil {
		t.Fatalf("expected pod affinity to be set")
	}
	terms := spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if len(terms) != 1 {
		t.Fatalf("expected 1 required affinity term, got %d", len(terms))
	}
	if terms[0].TopologyKey != "kubernetes.io/hostname" {
		t.Errorf("topologyKey = %q, want kubernetes.io/hostname", terms[0].TopologyKey)
	}
	if got := terms[0].LabelSelector.MatchLabels["app"]; got != "bot-foo" {
		t.Errorf("affinity match app = %q, want bot-foo", got)
	}
}

// TestKubernetesCloneBrowserVolume_NoOp pins the K8s implementation as a
// no-op. Callers (the clone task body) must tolerate the silent skip; if
// someone later wires in real PVC cloning they should also drop the
// "browser data not preserved on K8s clones" caveat in
// kubernetes_browser.go's docstring.
//
// We deliberately don't construct a fake clientset: the method's contract
// is "do nothing", and exercising it on a zero-value orchestrator with a
// nil clientset proves it doesn't reach for k8s state. If the
// implementation changes to issue API calls, this test crashes on the nil
// clientset — the explicit signal that the docstring needs updating too.
func TestKubernetesCloneBrowserVolume_NoOp(t *testing.T) {
	k := &KubernetesOrchestrator{}
	if err := k.CloneBrowserVolume(context.Background(), "bot-src", "bot-dst"); err != nil {
		t.Errorf("CloneBrowserVolume returned error %v, want nil", err)
	}
}
