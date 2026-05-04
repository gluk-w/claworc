package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/homedir"
)

type KubernetesOrchestrator struct {
	clientset       kubernetes.Interface
	restConfig      *rest.Config
	available       bool
	inCluster       bool
	InstanceFactory sshproxy.InstanceFactory
}

func (k *KubernetesOrchestrator) Initialize(ctx context.Context) error {
	cfg, err := rest.InClusterConfig()
	if err == nil {
		k.inCluster = true
	} else {
		kubeconfig := clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
		if home := homedir.HomeDir(); home != "" && kubeconfig == "" {
			kubeconfig = home + "/.kube/config"
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return fmt.Errorf("k8s config: %w", err)
		}
	}

	k.restConfig = cfg
	k.clientset, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("k8s clientset: %w", err)
	}

	_, err = k.clientset.CoreV1().Namespaces().Get(ctx, config.Cfg.K8sNamespace, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("k8s namespace check: %w", err)
	}

	k.available = true
	return nil
}

func (k *KubernetesOrchestrator) IsAvailable(_ context.Context) bool {
	return k.available
}

func (k *KubernetesOrchestrator) BackendName() string {
	return "kubernetes"
}

// VolumeNameFor returns the canonical PVC name for a workload's per-suffix
// data volume. K8s PVCs are scoped per-namespace; the convention is plain
// "<workload>-<suffix>" with no managed-by prefix.
func (k *KubernetesOrchestrator) VolumeNameFor(name, suffix string) string {
	return fmt.Sprintf("%s-%s", name, suffix)
}

// CloneVolume on Kubernetes is intentionally a no-op for now: cloning a PVC
// in-band requires either a CSI clone capability or an attach-detach helper
// pod, both of which complicate the instance-clone path. Callers should treat
// per-workload data as not preserved on K8s clones.
func (k *KubernetesOrchestrator) CloneVolume(_ context.Context, _, _ string) error { return nil }

func (k *KubernetesOrchestrator) ns() string {
	return config.Cfg.K8sNamespace
}

func (k *KubernetesOrchestrator) CreateInstance(ctx context.Context, params CreateParams) error {
	progress := params.OnProgress
	if progress == nil {
		progress = func(string) {}
	}

	ns := k.ns()

	progress("Creating storage...")
	pvcs := []struct {
		suffix  string
		storage string
	}{
		{"homebrew", params.StorageHomebrew},
		{"home", params.StorageHome},
	}
	for _, p := range pvcs {
		pvc := buildPVC(fmt.Sprintf("%s-%s", params.Name, p.suffix), ns, p.storage)
		if _, err := k.clientset.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create PVC %s: %w", p.suffix, err)
		}
	}

	// Create shared folder PVCs (ReadWriteMany for multi-pod access)
	for _, sfm := range params.SharedFolderMounts {
		pvcName := fmt.Sprintf("shared-folder-%d", sfm.VolumeID)
		_, err := k.clientset.CoreV1().PersistentVolumeClaims(ns).Get(ctx, pvcName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			pvc := buildSharedPVC(pvcName, ns, "1Gi")
			if _, err := k.clientset.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("create shared PVC %s: %w", pvcName, err)
			}
		}
	}

	progress("Creating deployment...")
	dep := buildDeployment(params, ns)
	if _, err := k.clientset.AppsV1().Deployments(ns).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create deployment: %w", err)
	}

	return nil
}

func (k *KubernetesOrchestrator) CloneVolumes(ctx context.Context, srcName, dstName string) error {
	// Scale both deployments to 0 to release PVCs (RWO constraint)
	_ = k.scaleDeployment(ctx, srcName, 0)
	_ = k.scaleDeployment(ctx, dstName, 0)
	k.waitForPodTermination(ctx, srcName, 60*time.Second)
	k.waitForPodTermination(ctx, dstName, 60*time.Second)

	// Copy each PVC pair
	for _, suffix := range []string{"homebrew", "home"} {
		srcPVC := fmt.Sprintf("%s-%s", srcName, suffix)
		dstPVC := fmt.Sprintf("%s-%s", dstName, suffix)
		if err := k.copyPVC(ctx, srcPVC, dstPVC); err != nil {
			// Best-effort: restart both even on error
			k.scaleDeployment(ctx, srcName, 1)
			k.scaleDeployment(ctx, dstName, 1)
			return fmt.Errorf("copy PVC %s: %w", suffix, err)
		}
	}

	// Restart both
	_ = k.scaleDeployment(ctx, srcName, 1)
	_ = k.scaleDeployment(ctx, dstName, 1)
	return nil
}

func (k *KubernetesOrchestrator) waitForPodTermination(ctx context.Context, name string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pods, err := k.clientset.CoreV1().Pods(k.ns()).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", name),
		})
		if err != nil || len(pods.Items) == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (k *KubernetesOrchestrator) copyPVC(ctx context.Context, srcPVC, dstPVC string) error {
	ns := k.ns()
	podName := fmt.Sprintf("claworc-copy-%d", time.Now().UnixNano()%1000000)
	if len(podName) > 63 {
		podName = podName[:63]
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
			Labels:    map[string]string{"managed-by": "claworc"},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "copy",
				Image:   "alpine:latest",
				Command: []string{"sh", "-c", "cp -a /src/. /dst/"},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "src", MountPath: "/src", ReadOnly: true},
					{Name: "dst", MountPath: "/dst"},
				},
			}},
			Volumes: []corev1.Volume{
				{Name: "src", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: srcPVC, ReadOnly: true}}},
				{Name: "dst", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: dstPVC}}},
			},
		},
	}

	if _, err := k.clientset.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create copy pod: %w", err)
	}
	defer k.clientset.CoreV1().Pods(ns).Delete(context.Background(), podName, metav1.DeleteOptions{})

	// Wait for pod to complete (up to 10 minutes)
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		p, err := k.clientset.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get copy pod: %w", err)
		}
		if p.Status.Phase == corev1.PodSucceeded {
			return nil
		}
		if p.Status.Phase == corev1.PodFailed {
			return fmt.Errorf("copy pod failed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("copy pod timed out")
}

func (k *KubernetesOrchestrator) DeleteInstance(ctx context.Context, name string) error {
	ns := k.ns()

	if err := k.clientset.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete deployment: %w", err)
	}
	for _, suffix := range []string{"homebrew", "home"} {
		pvcName := fmt.Sprintf("%s-%s", name, suffix)
		if err := k.clientset.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, pvcName, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete PVC %s: %w", suffix, err)
		}
	}
	return nil
}

func (k *KubernetesOrchestrator) DeleteSharedVolume(ctx context.Context, folderID uint) error {
	ns := k.ns()
	pvcName := fmt.Sprintf("shared-folder-%d", folderID)
	if err := k.clientset.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, pvcName, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete shared PVC %s: %w", pvcName, err)
	}
	return nil
}

func (k *KubernetesOrchestrator) StartInstance(ctx context.Context, name string) error {
	return k.scaleDeployment(ctx, name, 1)
}

func (k *KubernetesOrchestrator) StopInstance(ctx context.Context, name string) error {
	return k.scaleDeployment(ctx, name, 0)
}

func (k *KubernetesOrchestrator) RestartInstance(ctx context.Context, name string, params CreateParams) error {
	ns := k.ns()

	// Ensure shared folder PVCs exist
	for _, sfm := range params.SharedFolderMounts {
		pvcName := fmt.Sprintf("shared-folder-%d", sfm.VolumeID)
		_, err := k.clientset.CoreV1().PersistentVolumeClaims(ns).Get(ctx, pvcName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			pvc := buildSharedPVC(pvcName, ns, "1Gi")
			if _, err := k.clientset.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("create shared PVC %s: %w", pvcName, err)
			}
		}
	}

	// Fetch existing deployment so Update carries a valid resourceVersion;
	// without it the K8s API rejects the write and the pod template is never rolled out.
	existing, err := k.clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		dep := buildDeployment(params, ns)
		if _, err := k.clientset.AppsV1().Deployments(ns).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create deployment: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get deployment: %w", err)
	}

	desired := buildDeployment(params, ns)
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	_, err = k.clientset.AppsV1().Deployments(ns).Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

func (k *KubernetesOrchestrator) UpdateImage(ctx context.Context, name string, params CreateParams) error {
	// With ImagePullPolicy: Always, a rollout restart pulls the latest image
	return k.RestartInstance(ctx, name, params)
}

func (k *KubernetesOrchestrator) GetInstanceStatus(ctx context.Context, name string) (string, error) {
	pods, err := k.clientset.CoreV1().Pods(k.ns()).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", name),
	})
	if err != nil {
		return "error", nil
	}
	if len(pods.Items) == 0 {
		return "stopped", nil
	}

	pod := pods.Items[0]
	switch pod.Status.Phase {
	case corev1.PodRunning:
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil {
				return "creating", nil
			}
			if cs.Ready {
				return "running", nil
			}
		}
		return "creating", nil
	case corev1.PodPending:
		return "creating", nil
	case corev1.PodFailed, corev1.PodUnknown:
		return "error", nil
	default:
		return "creating", nil
	}
}

func (k *KubernetesOrchestrator) GetInstanceImageInfo(ctx context.Context, name string) (string, error) {
	pods, err := k.clientset.CoreV1().Pods(k.ns()).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", name),
	})
	if err != nil {
		return "", fmt.Errorf("list pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return "", nil
	}
	pod := pods.Items[0]
	if len(pod.Spec.Containers) == 0 {
		return "", nil
	}
	tag := pod.Spec.Containers[0].Image
	for _, cs := range pod.Status.ContainerStatuses {
		sha := cs.ImageID
		if idx := strings.Index(sha, "sha256:"); idx >= 0 {
			sha = sha[idx:]
			if len(sha) > 19 { // "sha256:" (7) + 12 chars
				sha = sha[:19]
			}
			return fmt.Sprintf("%s (%s)", tag, sha), nil
		}
	}
	return tag, nil
}

func (k *KubernetesOrchestrator) ConfigureSSHAccess(ctx context.Context, instanceID uint, publicKey string) error {
	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return fmt.Errorf("instance %d not found: %w", instanceID, err)
	}
	return configureSSHAccess(ctx, k.ExecInInstance, inst.Name, publicKey)
}

func (k *KubernetesOrchestrator) GetSSHAddress(ctx context.Context, instanceID uint) (string, int, error) {
	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return "", 0, fmt.Errorf("instance %d not found: %w", instanceID, err)
	}
	pods, err := k.clientset.CoreV1().Pods(k.ns()).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", inst.Name),
	})
	if err != nil {
		return "", 0, fmt.Errorf("list pods for instance %d: %w", instanceID, err)
	}
	if len(pods.Items) == 0 {
		return "", 0, fmt.Errorf("no pods found for instance %d", instanceID)
	}
	pod := pods.Items[0]
	if pod.Status.PodIP == "" {
		return "", 0, fmt.Errorf("pod %s has no IP assigned (instance %d)", pod.Name, instanceID)
	}
	return pod.Status.PodIP, 22, nil
}

func (k *KubernetesOrchestrator) UpdateResources(ctx context.Context, name string, params UpdateResourcesParams) error {
	dep, err := k.clientset.AppsV1().Deployments(k.ns()).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get deployment: %w", err)
	}

	if len(dep.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("deployment %s has no containers", name)
	}

	c := &dep.Spec.Template.Spec.Containers[0]
	c.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(params.CPURequest),
			corev1.ResourceMemory: resource.MustParse(params.MemoryRequest),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(params.CPULimit),
			corev1.ResourceMemory: resource.MustParse(params.MemoryLimit),
		},
	}

	_, err = k.clientset.AppsV1().Deployments(k.ns()).Update(ctx, dep, metav1.UpdateOptions{})
	return err
}

func (k *KubernetesOrchestrator) GetContainerStats(ctx context.Context, name string) (*ContainerStats, error) {
	// Use the metrics API (requires metrics-server)
	podName, err := k.getPodName(ctx, name)
	if err != nil || podName == "" {
		return nil, fmt.Errorf("no running pod for %s", name)
	}

	// Call metrics API via REST client
	result := k.clientset.CoreV1().RESTClient().Get().
		AbsPath(fmt.Sprintf("/apis/metrics.k8s.io/v1beta1/namespaces/%s/pods/%s", k.ns(), podName)).
		Do(ctx)

	if err := result.Error(); err != nil {
		return nil, fmt.Errorf("metrics API: %w", err)
	}

	raw, err := result.Raw()
	if err != nil {
		return nil, fmt.Errorf("read metrics: %w", err)
	}

	var podMetrics struct {
		Containers []struct {
			Usage struct {
				CPU    string `json:"cpu"`
				Memory string `json:"memory"`
			} `json:"usage"`
		} `json:"containers"`
	}
	if err := json.Unmarshal(raw, &podMetrics); err != nil {
		return nil, fmt.Errorf("parse metrics: %w", err)
	}

	if len(podMetrics.Containers) == 0 {
		return nil, fmt.Errorf("no container metrics for %s", name)
	}

	cpuQuantity := resource.MustParse(podMetrics.Containers[0].Usage.CPU)
	memQuantity := resource.MustParse(podMetrics.Containers[0].Usage.Memory)

	cpuMillicores := cpuQuantity.MilliValue()
	memBytes := memQuantity.Value()

	// Get limit from deployment for percent calculation
	dep, err := k.clientset.AppsV1().Deployments(k.ns()).Get(ctx, name, metav1.GetOptions{})
	var cpuPercent float64
	var memLimit int64
	if err == nil && len(dep.Spec.Template.Spec.Containers) > 0 {
		c := dep.Spec.Template.Spec.Containers[0]
		if cpuLim, ok := c.Resources.Limits[corev1.ResourceCPU]; ok {
			cpuPercent = float64(cpuMillicores) / float64(cpuLim.MilliValue()) * 100
		}
		if memLim, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
			memLimit = memLim.Value()
		}
	}

	return &ContainerStats{
		CPUUsageMillicores: cpuMillicores,
		CPUUsagePercent:    cpuPercent,
		MemoryUsageBytes:   memBytes,
		MemoryLimitBytes:   memLimit,
	}, nil
}

func (k *KubernetesOrchestrator) UpdateInstanceConfig(ctx context.Context, name string, configJSON string) error {
	return updateInstanceConfig(ctx, k.ExecInInstance, k.InstanceFactory, name, configJSON)
}

func (k *KubernetesOrchestrator) ExecInInstance(ctx context.Context, name string, cmd []string) (string, string, int, error) {
	podName, err := k.getPodName(ctx, name)
	if err != nil {
		return "", "", -1, err
	}
	if podName == "" {
		return "", "", -1, fmt.Errorf("no running pod found for instance %s", name)
	}
	return k.execInPod(ctx, podName, cmd)
}

// --- Helpers ---

func (k *KubernetesOrchestrator) scaleDeployment(ctx context.Context, name string, replicas int32) error {
	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	_, err := k.clientset.AppsV1().Deployments(k.ns()).Patch(
		ctx, name, "application/strategic-merge-patch+json", []byte(patch), metav1.PatchOptions{},
	)
	return err
}

func (k *KubernetesOrchestrator) getPodName(ctx context.Context, name string) (string, error) {
	pods, err := k.clientset.CoreV1().Pods(k.ns()).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", name),
	})
	if err != nil {
		return "", err
	}
	if len(pods.Items) == 0 {
		return "", nil
	}
	return pods.Items[0].Name, nil
}

func (k *KubernetesOrchestrator) execInPod(ctx context.Context, podName string, command []string) (string, string, int, error) {
	req := k.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(k.ns()).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: command,
			Stdout:  true,
			Stderr:  true,
			Stdin:   false,
			TTY:     false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(k.restConfig, "POST", req.URL())
	if err != nil {
		return "", "", -1, fmt.Errorf("create executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(interface{ ExitStatus() int }); ok {
			exitCode = exitErr.ExitStatus()
		} else {
			log.Printf("exec error (treating as exit code 1): %v", err)
			exitCode = 1
		}
	}

	return stdout.String(), stderr.String(), exitCode, nil
}

func (k *KubernetesOrchestrator) StreamExecInInstance(ctx context.Context, name string, cmd []string, stdout io.Writer) (string, int, error) {
	podName, err := k.getPodName(ctx, name)
	if err != nil {
		return "", -1, err
	}
	if podName == "" {
		return "", -1, fmt.Errorf("no running pod found for instance %s", name)
	}
	return k.streamExecInPod(ctx, podName, cmd, stdout)
}

func (k *KubernetesOrchestrator) streamExecInPod(ctx context.Context, podName string, command []string, stdout io.Writer) (string, int, error) {
	req := k.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(k.ns()).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: command,
			Stdout:  true,
			Stderr:  true,
			Stdin:   false,
			TTY:     false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(k.restConfig, "POST", req.URL())
	if err != nil {
		return "", -1, fmt.Errorf("create executor: %w", err)
	}

	var stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: stdout,
		Stderr: &stderr,
	})

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(interface{ ExitStatus() int }); ok {
			exitCode = exitErr.ExitStatus()
		} else {
			log.Printf("stream exec error (treating as exit code 1): %v", err)
			exitCode = 1
		}
	}

	return stderr.String(), exitCode, nil
}

// --- Resource builders ---

func buildPVC(name, ns, storage string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(storage),
				},
			},
		},
	}
}

func buildSharedPVC(name, ns, storage string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{"managed-by": "claworc", "type": "shared-folder"},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(storage),
				},
			},
		},
	}
}

func buildDeployment(params CreateParams, ns string) *appsv1.Deployment {
	replicas := int32(1)
	privileged := false
	allowPrivEsc := false
	initPrivileged := true
	fsGroup := int64(1000)
	fsGroupPolicy := corev1.FSGroupChangeOnRootMismatch

	var envVars []corev1.EnvVar
	if parts := strings.SplitN(params.VNCResolution, "x", 2); len(parts) == 2 {
		envVars = append(envVars,
			corev1.EnvVar{Name: "DISPLAY_WIDTH", Value: parts[0]},
			corev1.EnvVar{Name: "DISPLAY_HEIGHT", Value: parts[1]},
		)
	}
	if token, ok := params.EnvVars["OPENCLAW_GATEWAY_TOKEN"]; ok && token != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "OPENCLAW_GATEWAY_TOKEN", Value: token})
	}
	if params.Timezone != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "TZ", Value: params.Timezone})
	}
	if params.UserAgent != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "CHROMIUM_USER_AGENT", Value: params.UserAgent})
	}

	shmSize := resource.MustParse("2Gi")

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.Name,
			Namespace: ns,
			Labels:    map[string]string{"app": params.Name, "managed-by": "claworc"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": params.Name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": params.Name, "managed-by": "claworc"}},
				Spec: corev1.PodSpec{
					Hostname: strings.TrimPrefix(params.Name, "bot-"),
					SecurityContext: &corev1.PodSecurityContext{
						// Pin the SELinux MCS level so every pod incarnation
						// can read what its predecessors wrote to the home
						// PVC. Without this, the runtime hands out random
						// MCS categories per pod and Bottlerocket-style
						// SELinux policies deny access to the agent's own
						// home dir on restart. Ignored on non-SELinux nodes.
						SELinuxOptions: &corev1.SELinuxOptions{
							Level: "s0:c0,c0",
						},
						FSGroup:             &fsGroup,
						FSGroupChangePolicy: &fsGroupPolicy,
					},
					Containers: []corev1.Container{{
						Name:            "claworc-instance",
						Image:           params.ContainerImage,
						ImagePullPolicy: corev1.PullAlways,
						SecurityContext: &corev1.SecurityContext{Privileged: &privileged, AllowPrivilegeEscalation: &allowPrivEsc},
						Env:             envVars,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse(params.CPURequest),
								corev1.ResourceMemory: resource.MustParse(params.MemoryRequest),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse(params.CPULimit),
								corev1.ResourceMemory: resource.MustParse(params.MemoryLimit),
							},
						},
						VolumeMounts: appendSharedVolumeMounts([]corev1.VolumeMount{
							{Name: "home-data", MountPath: "/home/claworc"},
							{Name: "homebrew-data", MountPath: "/home/linuxbrew/.linuxbrew"},
							{Name: "dshm", MountPath: "/dev/shm"},
						}, params.SharedFolderMounts),
						LivenessProbe: &corev1.Probe{
							ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(22)}},
							InitialDelaySeconds: 60,
							PeriodSeconds:       30,
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(22)}},
							InitialDelaySeconds: 30,
							PeriodSeconds:       10,
						},
					}},
					InitContainers: buildInitContainers(params.SharedFolderMounts, initPrivileged),
					Volumes: appendSharedVolumes([]corev1.Volume{
						{Name: "homebrew-data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: params.Name + "-homebrew"}}},
						{Name: "home-data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: params.Name + "-home"}}},
						{Name: "dshm", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: &shmSize}}},
					}, params.SharedFolderMounts),
					ImagePullSecrets: []corev1.LocalObjectReference{{Name: "ghcr-secret"}},
				},
			},
		},
	}
}

// buildInitContainers builds the pod's init containers. It always includes
// fix-home-selinux which relabels the home/homebrew PVCs to a fixed MCS
// level on Bottlerocket/RHCOS-style SELinux nodes (no-op elsewhere). When
// shared folder mounts are configured, it also includes fix-shared-permissions
// to chown those mount paths to claworc:claworc (1000:1000).
func buildInitContainers(sfMounts []SharedFolderMount, privileged bool) []corev1.Container {
	containers := []corev1.Container{{
		Name:  "fix-home-selinux",
		Image: "busybox:latest",
		// chcon may fail on non-SELinux filesystems or nodes — that's fine,
		// the `|| true` keeps the pod startable on plain Ubuntu/COS clusters.
		Command: []string{"sh", "-c",
			"chcon -R -l s0:c0,c0 /home/claworc /home/linuxbrew/.linuxbrew 2>/dev/null || true"},
		SecurityContext: &corev1.SecurityContext{Privileged: &privileged},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "home-data", MountPath: "/home/claworc"},
			{Name: "homebrew-data", MountPath: "/home/linuxbrew/.linuxbrew"},
		},
	}}
	if len(sfMounts) > 0 {
		var chownCmds []string
		var volumeMounts []corev1.VolumeMount
		for _, sfm := range sfMounts {
			chownCmds = append(chownCmds, fmt.Sprintf("chown 1000:1000 %s", sfm.MountPath))
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      fmt.Sprintf("shared-%d", sfm.VolumeID),
				MountPath: sfm.MountPath,
			})
		}
		containers = append(containers, corev1.Container{
			Name:         "fix-shared-permissions",
			Image:        "busybox:latest",
			Command:      []string{"sh", "-c", strings.Join(chownCmds, " && ")},
			VolumeMounts: volumeMounts,
		})
	}
	return containers
}

func appendSharedVolumeMounts(base []corev1.VolumeMount, sfMounts []SharedFolderMount) []corev1.VolumeMount {
	for _, sfm := range sfMounts {
		base = append(base, corev1.VolumeMount{
			Name:      fmt.Sprintf("shared-%d", sfm.VolumeID),
			MountPath: sfm.MountPath,
		})
	}
	return base
}

func appendSharedVolumes(base []corev1.Volume, sfMounts []SharedFolderMount) []corev1.Volume {
	for _, sfm := range sfMounts {
		base = append(base, corev1.Volume{
			Name: fmt.Sprintf("shared-%d", sfm.VolumeID),
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: fmt.Sprintf("shared-folder-%d", sfm.VolumeID),
				},
			},
		})
	}
	return base
}
