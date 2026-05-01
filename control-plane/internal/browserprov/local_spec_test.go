package browserprov

import (
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
)

// stubVolumeNamer is the minimum surface buildApplySpec touches on the
// orchestrator: a runtime-specific volume namer. Using a stub keeps the test
// hermetic — no docker/k8s clients required.
type stubOrch struct{ orchestrator.ContainerOrchestrator }

func (stubOrch) VolumeNameFor(name, suffix string) string { return name + "-" + suffix }
func (stubOrch) BackendName() string                      { return "stub" }

func TestBuildApplySpec_DownloadsSubpath_AffinityAndInitContainers(t *testing.T) {
	p := &LocalProvider{orch: stubOrch{}}
	spec := p.buildApplySpec("bot-foo", SessionParams{
		Image:         "glukw/claworc-browser-chromium:latest",
		StorageSize:   "10Gi",
		VNCResolution: "1920x1080",
	})

	if spec.Name != "bot-foo-browser" {
		t.Errorf("workload name = %q, want bot-foo-browser", spec.Name)
	}
	if got := spec.Hostname; got != "foo-browser" {
		t.Errorf("hostname = %q, want foo-browser (bot- stripped)", got)
	}

	var dataVol, homeVol *orchestrator.VolumeMount
	for i, v := range spec.Volumes {
		v := v
		switch v.Name {
		case "bot-foo-browser":
			dataVol = &spec.Volumes[i]
		case "bot-foo-home":
			homeVol = &spec.Volumes[i]
		}
	}
	if dataVol == nil || dataVol.MountPath != "/home/claworc/chrome-data" {
		t.Fatalf("expected browser data volume at /home/claworc/chrome-data, got %+v", spec.Volumes)
	}
	if dataVol.Size != "10Gi" {
		t.Errorf("browser data volume size = %q, want 10Gi", dataVol.Size)
	}
	if homeVol == nil || homeVol.MountPath != "/home/claworc/Downloads" || homeVol.SubPath != "Downloads" {
		t.Fatalf("expected agent home volume at /home/claworc/Downloads with SubPath=Downloads, got %+v", spec.Volumes)
	}

	if spec.Affinity == nil || len(spec.Affinity.RequiredCoLocation) != 1 || spec.Affinity.RequiredCoLocation[0] != "bot-foo" {
		t.Errorf("affinity should require co-location with bot-foo, got %+v", spec.Affinity)
	}

	if len(spec.InitContainers) != 2 {
		t.Fatalf("expected 2 init containers, got %d", len(spec.InitContainers))
	}
	if spec.InitContainers[0].Name != "prepare-downloads" {
		t.Errorf("first init container = %q, want prepare-downloads", spec.InitContainers[0].Name)
	}
	if spec.InitContainers[1].Name != "scrub-singletons" {
		t.Errorf("second init container = %q, want scrub-singletons", spec.InitContainers[1].Name)
	}

	if len(spec.Ports) != 1 || spec.Ports[0].ContainerPort != 22 {
		t.Errorf("expected only port 22 exposed, got %+v", spec.Ports)
	}
}
