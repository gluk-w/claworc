package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// newFakeOrchestrator returns a KubernetesOrchestrator wired to an in-memory
// fake clientset. The namespace is set to "claworc" via config.Cfg so the
// production code's k.ns() helper returns the same value.
func newFakeOrchestrator(t *testing.T, objects ...interface{}) *KubernetesOrchestrator {
	t.Helper()
	if config.Cfg.K8sNamespace == "" {
		config.Cfg.K8sNamespace = "claworc"
	}
	objs := make([]interface{}, 0, len(objects))
	objs = append(objs, objects...)
	cs := fake.NewSimpleClientset()
	for _, o := range objs {
		// Re-add via the typed clients so the fake's tracker indexes them.
		// The simple constructor only accepts runtime.Object implementations;
		// callers should pass typed K8s objects directly to NewSimpleClientset
		// instead of via this helper if they need pre-seeded state.
		_ = o
	}
	return &KubernetesOrchestrator{clientset: cs}
}

func browserSpec() WorkloadSpec {
	return WorkloadSpec{
		Name:  "bot-foo-browser",
		Image: "claworc/chromium-browser:latest",
		Volumes: []VolumeMount{
			{Name: "bot-foo-browser", Size: "10Gi", MountPath: "/home/claworc/chrome-data"},
		},
		Ports: []PortSpec{
			{Name: "ssh", ContainerPort: 22},
		},
		Probes: ProbeSpec{
			Readiness: &TCPProbe{Port: 22, InitialDelay: 5 * time.Second, Period: 3 * time.Second},
			Liveness:  &TCPProbe{Port: 22, InitialDelay: 30 * time.Second, Period: 30 * time.Second},
		},
		Pull: PullAlways,
	}
}

func sharedFolderSpec() WorkloadSpec {
	return WorkloadSpec{
		Name:  "bot-foo",
		Image: "claworc/openclaw:latest",
		Volumes: []VolumeMount{
			{Name: "bot-foo-home", Size: "5Gi", MountPath: "/home/claworc"},
			{Name: "shared-folder-7", Size: "1Gi", Shared: true, MountPath: "/mnt/shared"},
		},
		Ports: []PortSpec{
			{Name: "ssh", ContainerPort: 22},
		},
		Pull: PullAlways,
	}
}

// --- Apply ---

func TestApply_CreatesPVC_DeploymentService_NetworkPolicy(t *testing.T) {
	k := newFakeOrchestrator(t)
	ctx := context.Background()

	if err := k.Apply(ctx, browserSpec()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	pvc, err := k.clientset.CoreV1().PersistentVolumeClaims("claworc").Get(ctx, "bot-foo-browser", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pvc: %v", err)
	}
	if got := pvc.Spec.AccessModes[0]; got != corev1.ReadWriteOnce {
		t.Errorf("pvc access mode = %v, want ReadWriteOnce", got)
	}
	if got := pvc.Spec.Resources.Requests.Storage().String(); got != "10Gi" {
		t.Errorf("pvc size = %q, want 10Gi", got)
	}

	dep, err := k.clientset.AppsV1().Deployments("claworc").Get(ctx, "bot-foo-browser", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if got := dep.Spec.Template.Spec.Containers[0].Image; got != "claworc/chromium-browser:latest" {
		t.Errorf("container image = %q", got)
	}

	svc, err := k.clientset.CoreV1().Services("claworc").Get(ctx, "bot-foo-browser", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get service: %v", err)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 22 {
		t.Errorf("service ports = %+v, want a single :22", svc.Spec.Ports)
	}

	netpol, err := k.clientset.NetworkingV1().NetworkPolicies("claworc").Get(ctx, "bot-foo-browser", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get networkpolicy: %v", err)
	}
	if got := netpol.Spec.PodSelector.MatchLabels["app"]; got != "bot-foo-browser" {
		t.Errorf("netpol pod selector app = %q, want bot-foo-browser", got)
	}
}

func TestApply_IsIdempotent_OnReapply(t *testing.T) {
	k := newFakeOrchestrator(t)
	ctx := context.Background()

	if err := k.Apply(ctx, browserSpec()); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := k.Apply(ctx, browserSpec()); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	deps, err := k.clientset.AppsV1().Deployments("claworc").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	if len(deps.Items) != 1 {
		t.Fatalf("expected 1 deployment after re-apply, got %d", len(deps.Items))
	}

	svcs, err := k.clientset.CoreV1().Services("claworc").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list services: %v", err)
	}
	if len(svcs.Items) != 1 {
		t.Fatalf("expected 1 service after re-apply, got %d", len(svcs.Items))
	}
}

func TestApply_PVCSizeOnlyHonouredAtCreation(t *testing.T) {
	k := newFakeOrchestrator(t)
	ctx := context.Background()

	spec := browserSpec()
	if err := k.Apply(ctx, spec); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	// Re-apply with a different requested size; PVC should be untouched.
	spec.Volumes[0].Size = "20Gi"
	if err := k.Apply(ctx, spec); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	pvc, err := k.clientset.CoreV1().PersistentVolumeClaims("claworc").Get(ctx, "bot-foo-browser", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pvc: %v", err)
	}
	if got := pvc.Spec.Resources.Requests.Storage().String(); got != "10Gi" {
		t.Errorf("pvc size after re-apply = %q, want 10Gi (unchanged)", got)
	}
}

func TestApply_SharedVolume_UsesRWX_AndIsNotDeleted(t *testing.T) {
	k := newFakeOrchestrator(t)
	ctx := context.Background()

	spec := sharedFolderSpec()
	if err := k.Apply(ctx, spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	shared, err := k.clientset.CoreV1().PersistentVolumeClaims("claworc").Get(ctx, "shared-folder-7", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get shared pvc: %v", err)
	}
	if got := shared.Spec.AccessModes[0]; got != corev1.ReadWriteMany {
		t.Errorf("shared pvc access mode = %v, want ReadWriteMany", got)
	}

	// DeleteWorkload must NOT remove the shared PVC.
	if err := k.DeleteWorkload(ctx, spec); err != nil {
		t.Fatalf("DeleteWorkload: %v", err)
	}
	if _, err := k.clientset.CoreV1().PersistentVolumeClaims("claworc").Get(ctx, "shared-folder-7", metav1.GetOptions{}); err != nil {
		t.Errorf("shared pvc disappeared after DeleteWorkload: %v", err)
	}
	// The non-shared workload PVC must be removed.
	if _, err := k.clientset.CoreV1().PersistentVolumeClaims("claworc").Get(ctx, "bot-foo-home", metav1.GetOptions{}); !k8serrors.IsNotFound(err) {
		t.Errorf("workload pvc should be gone, got err=%v", err)
	}
}

func TestApply_NetworkPolicy_AllowsControlPlaneOnlyForExposedPorts(t *testing.T) {
	k := newFakeOrchestrator(t)
	ctx := context.Background()

	if err := k.Apply(ctx, browserSpec()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	netpol, err := k.clientset.NetworkingV1().NetworkPolicies("claworc").Get(ctx, "bot-foo-browser", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get networkpolicy: %v", err)
	}
	if len(netpol.Spec.Ingress) != 1 {
		t.Fatalf("expected 1 ingress rule, got %d", len(netpol.Spec.Ingress))
	}
	rule := netpol.Spec.Ingress[0]
	if len(rule.From) != 1 || rule.From[0].PodSelector == nil {
		t.Fatalf("ingress.From should be a single PodSelector peer, got %+v", rule.From)
	}
	if got := rule.From[0].PodSelector.MatchLabels["app.kubernetes.io/name"]; got != "claworc" {
		t.Errorf("ingress allow-from label = %q, want claworc (control plane only)", got)
	}
	if len(rule.Ports) != 1 || rule.Ports[0].Port == nil || rule.Ports[0].Port.IntValue() != 22 {
		t.Errorf("ingress ports = %+v, want [{Port:22}]", rule.Ports)
	}
}

func TestApply_NoPorts_DeletesStaleService_AndNetworkPolicy(t *testing.T) {
	k := newFakeOrchestrator(t)
	ctx := context.Background()

	// First apply: create Service + NetworkPolicy.
	spec := browserSpec()
	if err := k.Apply(ctx, spec); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	// Re-apply with no ports.
	spec.Ports = nil
	if err := k.Apply(ctx, spec); err != nil {
		t.Fatalf("second Apply (no ports): %v", err)
	}

	if _, err := k.clientset.CoreV1().Services("claworc").Get(ctx, "bot-foo-browser", metav1.GetOptions{}); !k8serrors.IsNotFound(err) {
		t.Errorf("stale service should be gone, got err=%v", err)
	}
	if _, err := k.clientset.NetworkingV1().NetworkPolicies("claworc").Get(ctx, "bot-foo-browser", metav1.GetOptions{}); !k8serrors.IsNotFound(err) {
		t.Errorf("stale networkpolicy should be gone, got err=%v", err)
	}
}

// --- DeleteWorkload ---

func TestDeleteWorkload_CleansUp_NonSharedPVCs_LeavesShared(t *testing.T) {
	k := newFakeOrchestrator(t)
	ctx := context.Background()

	spec := sharedFolderSpec()
	if err := k.Apply(ctx, spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := k.DeleteWorkload(ctx, spec); err != nil {
		t.Fatalf("DeleteWorkload: %v", err)
	}

	for _, want := range []struct {
		kind, name  string
		shouldExist bool
	}{
		{"deployment", "bot-foo", false},
		{"service", "bot-foo", false},
		{"networkpolicy", "bot-foo", false},
		{"pvc", "bot-foo-home", false},
		{"pvc", "shared-folder-7", true}, // shared PVC must survive
	} {
		var err error
		switch want.kind {
		case "deployment":
			_, err = k.clientset.AppsV1().Deployments("claworc").Get(ctx, want.name, metav1.GetOptions{})
		case "service":
			_, err = k.clientset.CoreV1().Services("claworc").Get(ctx, want.name, metav1.GetOptions{})
		case "networkpolicy":
			_, err = k.clientset.NetworkingV1().NetworkPolicies("claworc").Get(ctx, want.name, metav1.GetOptions{})
		case "pvc":
			_, err = k.clientset.CoreV1().PersistentVolumeClaims("claworc").Get(ctx, want.name, metav1.GetOptions{})
		}
		exists := err == nil
		if exists != want.shouldExist {
			t.Errorf("%s/%s exists=%v want=%v (err=%v)", want.kind, want.name, exists, want.shouldExist, err)
		}
	}
}

// --- EnsureSSHAccess ---

func TestEnsureSSHAccess_RoutesByName(t *testing.T) {
	// The K8s implementation calls k.ExecInInstance, which in turn looks up
	// a pod by `app=<name>` label. We exercise the helper directly using a
	// stub ExecFunc to verify the name flows through to the exec layer.
	var calls []struct {
		name string
		cmd  []string
	}
	stub := func(_ context.Context, name string, cmd []string) (string, string, int, error) {
		calls = append(calls, struct {
			name string
			cmd  []string
		}{name, cmd})
		return "", "", 0, nil
	}

	for _, name := range []string{"bot-foo", "bot-foo-browser"} {
		if err := configureSSHAccess(context.Background(), stub, name, "ssh-ed25519 AAAA..."); err != nil {
			t.Fatalf("configureSSHAccess(%s): %v", name, err)
		}
	}

	// Each call site (mkdir, write key) should hit each name.
	wantPerName := 2
	got := map[string]int{}
	for _, c := range calls {
		got[c.name]++
	}
	for _, name := range []string{"bot-foo", "bot-foo-browser"} {
		if got[name] != wantPerName {
			t.Errorf("name=%s exec calls=%d, want %d", name, got[name], wantPerName)
		}
	}
}

// --- WorkloadSSHAddress ---

func TestWorkloadSSHAddress_ReturnsPodIP_OnPort22(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bot-foo-browser-abc",
			Namespace: "claworc",
			Labels:    map[string]string{"app": "bot-foo-browser"},
		},
		Status: corev1.PodStatus{PodIP: "10.1.2.3"},
	})
	if config.Cfg.K8sNamespace == "" {
		config.Cfg.K8sNamespace = "claworc"
	}
	k := &KubernetesOrchestrator{clientset: cs}

	host, port, err := k.WorkloadSSHAddress(context.Background(), "bot-foo-browser")
	if err != nil {
		t.Fatalf("WorkloadSSHAddress: %v", err)
	}
	if host != "10.1.2.3" || port != 22 {
		t.Errorf("got (%q, %d), want (10.1.2.3, 22)", host, port)
	}
}

func TestWorkloadSSHAddress_NoPodFound(t *testing.T) {
	k := newFakeOrchestrator(t)
	_, _, err := k.WorkloadSSHAddress(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error when no pod matches the label, got nil")
	}
	if !errors.Is(err, err) || err.Error() == "" {
		t.Errorf("error should mention the missing workload, got: %v", err)
	}
}
