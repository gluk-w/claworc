package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// browserPodName returns the K8s deployment / service / PVC base name for an
// instance's browser pod. We append "-browser" to the agent name; the agent
// name is K8s-safe (≤63 chars) and the suffix keeps it under that limit
// because agent names already reserve the budget for it (max 55 chars in
// practice).
func browserPodName(instanceName string) string {
	return instanceName + "-browser"
}

// browserPVCName mirrors browserPodName so PVCs and Deployments use the same
// base.
func browserPVCName(instanceName string) string {
	return instanceName + "-browser"
}

func (k *KubernetesOrchestrator) lookupInstanceName(instanceID uint) (string, error) {
	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return "", fmt.Errorf("instance %d not found: %w", instanceID, err)
	}
	return inst.Name, nil
}

// EnsureBrowserPod creates (or updates) the Deployment + PVC + Service +
// NetworkPolicy for a per-instance on-demand browser pod and waits for
// pod readiness up to ~60 s. Idempotent: repeated calls with the same params
// no-op once the deployment is already running.
func (k *KubernetesOrchestrator) EnsureBrowserPod(ctx context.Context, instanceID uint, params BrowserPodParams) (BrowserPodEndpoint, error) {
	ns := k.ns()
	name := params.Name
	if name == "" {
		var err error
		name, err = k.lookupInstanceName(instanceID)
		if err != nil {
			return BrowserPodEndpoint{}, err
		}
	}
	podName := browserPodName(name)

	// Storage
	storage := params.StorageSize
	if storage == "" {
		storage = "10Gi"
	}
	pvcName := browserPVCName(name)
	if _, err := k.clientset.CoreV1().PersistentVolumeClaims(ns).Get(ctx, pvcName, metav1.GetOptions{}); errors.IsNotFound(err) {
		pvc := buildPVC(pvcName, ns, storage)
		pvc.Labels = map[string]string{"managed-by": "claworc", "claworc-role": "browser", "app": podName}
		if _, err := k.clientset.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
			return BrowserPodEndpoint{}, fmt.Errorf("create browser PVC: %w", err)
		}
	} else if err != nil {
		return BrowserPodEndpoint{}, fmt.Errorf("get browser PVC: %w", err)
	}

	// Service (ClusterIP) — exposes 9222 + 3000, control-plane reachable via DNS.
	if err := k.upsertBrowserService(ctx, ns, podName); err != nil {
		return BrowserPodEndpoint{}, fmt.Errorf("upsert browser service: %w", err)
	}

	// NetworkPolicy — control plane only, ports 9222 + 3000.
	if err := k.upsertBrowserNetworkPolicy(ctx, ns, podName); err != nil {
		return BrowserPodEndpoint{}, fmt.Errorf("upsert browser network policy: %w", err)
	}

	// Deployment
	dep := buildBrowserDeployment(params, podName, pvcName, ns, name)
	existing, err := k.clientset.AppsV1().Deployments(ns).Get(ctx, podName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		if _, err := k.clientset.AppsV1().Deployments(ns).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
			return BrowserPodEndpoint{}, fmt.Errorf("create browser deployment: %w", err)
		}
	} else if err != nil {
		return BrowserPodEndpoint{}, fmt.Errorf("get browser deployment: %w", err)
	} else {
		// Re-apply spec so image/env updates roll out on respawn.
		existing.Spec = dep.Spec
		existing.Labels = dep.Labels
		if _, err := k.clientset.AppsV1().Deployments(ns).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
			return BrowserPodEndpoint{}, fmt.Errorf("update browser deployment: %w", err)
		}
	}

	// Wait for at least one pod to report ready (TCP probe on 9222 backs the
	// readiness check below); cap at 60 s so callers don't block forever.
	if err := k.waitForBrowserPodReady(ctx, ns, podName, 60*time.Second); err != nil {
		return BrowserPodEndpoint{}, err
	}

	return BrowserPodEndpoint{
		Host:    fmt.Sprintf("%s.%s.svc.cluster.local", podName, ns),
		CDPPort: 9222,
		VNCPort: 3000,
	}, nil
}

func (k *KubernetesOrchestrator) StopBrowserPod(ctx context.Context, instanceID uint) error {
	name, err := k.lookupInstanceName(instanceID)
	if err != nil {
		return err
	}
	return k.scaleDeployment(ctx, browserPodName(name), 0)
}

func (k *KubernetesOrchestrator) DeleteBrowserPod(ctx context.Context, instanceID uint) error {
	name, err := k.lookupInstanceName(instanceID)
	if err != nil {
		return err
	}
	ns := k.ns()
	podName := browserPodName(name)

	if err := k.clientset.AppsV1().Deployments(ns).Delete(ctx, podName, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete browser deployment: %w", err)
	}
	if err := k.clientset.CoreV1().Services(ns).Delete(ctx, podName, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete browser service: %w", err)
	}
	if err := k.clientset.NetworkingV1().NetworkPolicies(ns).Delete(ctx, podName, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete browser network policy: %w", err)
	}
	if err := k.clientset.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, browserPVCName(name), metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete browser PVC: %w", err)
	}
	return nil
}

func (k *KubernetesOrchestrator) GetBrowserPodStatus(ctx context.Context, instanceID uint) (string, error) {
	name, err := k.lookupInstanceName(instanceID)
	if err != nil {
		return "error", err
	}
	pods, err := k.clientset.CoreV1().Pods(k.ns()).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", browserPodName(name)),
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
			if cs.Ready {
				return "running", nil
			}
		}
		return "starting", nil
	case corev1.PodPending:
		return "starting", nil
	case corev1.PodFailed, corev1.PodUnknown:
		return "error", nil
	default:
		return "starting", nil
	}
}

func (k *KubernetesOrchestrator) GetBrowserPodEndpoint(ctx context.Context, instanceID uint) (BrowserPodEndpoint, error) {
	name, err := k.lookupInstanceName(instanceID)
	if err != nil {
		return BrowserPodEndpoint{}, err
	}
	podName := browserPodName(name)
	if _, err := k.clientset.CoreV1().Services(k.ns()).Get(ctx, podName, metav1.GetOptions{}); err != nil {
		return BrowserPodEndpoint{}, fmt.Errorf("browser service for %s: %w", name, err)
	}
	return BrowserPodEndpoint{
		Host:    fmt.Sprintf("%s.%s.svc.cluster.local", podName, k.ns()),
		CDPPort: 9222,
		VNCPort: 3000,
	}, nil
}

// CloneBrowserVolume is intentionally a no-op on Kubernetes for now: cloning
// PVC contents requires either a CSI snapshot/clone capability or running a
// helper pod that mounts both PVCs simultaneously, which complicates the
// instance clone path. Callers should treat browser data as not preserved
// on K8s clones; the new instance will get a fresh profile on first launch.
func (k *KubernetesOrchestrator) CloneBrowserVolume(_ context.Context, _, _ string) error {
	return nil
}

// --- helpers ---

func (k *KubernetesOrchestrator) waitForBrowserPodReady(ctx context.Context, ns, podName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pods, err := k.clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", podName),
		})
		if err == nil {
			for _, pod := range pods.Items {
				if pod.Status.Phase == corev1.PodRunning {
					for _, cs := range pod.Status.ContainerStatuses {
						if cs.Ready {
							return nil
						}
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("browser pod %s did not become ready within %s", podName, timeout)
}

func (k *KubernetesOrchestrator) upsertBrowserService(ctx context.Context, ns, podName string) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
			Labels: map[string]string{
				"managed-by":   "claworc",
				"claworc-role": "browser",
				"app":          podName,
			},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"app": podName},
			Ports: []corev1.ServicePort{
				{Name: "cdp", Port: 9222, TargetPort: intstr.FromInt32(9222)},
				{Name: "novnc", Port: 3000, TargetPort: intstr.FromInt32(3000)},
			},
		},
	}
	existing, err := k.clientset.CoreV1().Services(ns).Get(ctx, podName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, err := k.clientset.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	// Preserve cluster IP and resourceVersion; replace selector + ports.
	existing.Spec.Selector = svc.Spec.Selector
	existing.Spec.Ports = svc.Spec.Ports
	_, err = k.clientset.CoreV1().Services(ns).Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

func (k *KubernetesOrchestrator) upsertBrowserNetworkPolicy(ctx context.Context, ns, podName string) error {
	tcp := corev1.ProtocolTCP
	cdpPort := intstr.FromInt32(9222)
	novncPort := intstr.FromInt32(3000)

	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
			Labels: map[string]string{
				"managed-by":   "claworc",
				"claworc-role": "browser",
				"app":          podName,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": podName},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app.kubernetes.io/name": "claworc"},
							},
						},
					},
					Ports: []networkingv1.NetworkPolicyPort{
						{Protocol: &tcp, Port: &cdpPort},
						{Protocol: &tcp, Port: &novncPort},
					},
				},
			},
		},
	}
	existing, err := k.clientset.NetworkingV1().NetworkPolicies(ns).Get(ctx, podName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, err := k.clientset.NetworkingV1().NetworkPolicies(ns).Create(ctx, policy, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	existing.Spec = policy.Spec
	_, err = k.clientset.NetworkingV1().NetworkPolicies(ns).Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

func buildBrowserDeployment(params BrowserPodParams, podName, pvcName, ns, agentName string) *appsv1.Deployment {
	replicas := int32(1)
	allowPriv := false

	envVars := []corev1.EnvVar{}
	if parts := strings.SplitN(params.VNCResolution, "x", 2); len(parts) == 2 {
		envVars = append(envVars,
			corev1.EnvVar{Name: "DISPLAY_WIDTH", Value: parts[0]},
			corev1.EnvVar{Name: "DISPLAY_HEIGHT", Value: parts[1]},
		)
	}
	if params.Timezone != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "TZ", Value: params.Timezone})
	}
	if params.UserAgent != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "CHROMIUM_USER_AGENT", Value: params.UserAgent})
	}
	for k, v := range params.EnvVars {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	shmSize := resource.MustParse("2Gi")

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
			Labels: map[string]string{
				"app":          podName,
				"managed-by":   "claworc",
				"claworc-role": "browser",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": podName}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":          podName,
						"managed-by":   "claworc",
						"claworc-role": "browser",
					},
				},
				Spec: corev1.PodSpec{
					Hostname: strings.TrimPrefix(podName, "bot-"),
					// RWO PVC sharing requires same-node placement: the
					// agent-home PVC is mounted into the browser pod (subPath
					// Downloads) so files Chromium downloads are visible to
					// OpenClaw and the agent terminal. Required (not preferred)
					// because scheduling on a different node would fail to
					// mount the agent's RWO PVC.
					Affinity: &corev1.Affinity{
						PodAffinity: &corev1.PodAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{"app": agentName},
								},
								TopologyKey: "kubernetes.io/hostname",
							}},
						},
					},
					Containers: []corev1.Container{{
						Name:            "browser",
						Image:           params.Image,
						ImagePullPolicy: corev1.PullAlways,
						SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: &allowPriv},
						Env:             envVars,
						Ports: []corev1.ContainerPort{
							{Name: "cdp", ContainerPort: 9222},
							{Name: "novnc", ContainerPort: 3000},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "browser-data", MountPath: "/home/claworc/chrome-data"},
							{Name: "agent-downloads", MountPath: "/home/claworc/Downloads", SubPath: "Downloads"},
							{Name: "dshm", MountPath: "/dev/shm"},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(9222)}},
							InitialDelaySeconds: 5,
							PeriodSeconds:       3,
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(9222)}},
							InitialDelaySeconds: 30,
							PeriodSeconds:       30,
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "browser-data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName}}},
						{Name: "agent-downloads", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: agentName + "-home"}}},
						{Name: "dshm", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: &shmSize}}},
					},
					ImagePullSecrets: []corev1.LocalObjectReference{{Name: "ghcr-secret"}},
				},
			},
		},
	}
}
