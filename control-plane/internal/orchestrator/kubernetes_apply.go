package orchestrator

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// controlPlaneSelector is the label every claworc-managed workload must
// accept ingress from. Applied by the default NetworkPolicy created in Apply.
var controlPlaneSelector = map[string]string{"app.kubernetes.io/name": "claworc"}

// Apply creates or updates the workload described by spec. It is idempotent:
// PVCs that already exist are reused (size honoured only on first creation),
// the Deployment / Service / NetworkPolicy are upserted to match the desired
// spec on every call.
func (k *KubernetesOrchestrator) Apply(ctx context.Context, spec WorkloadSpec) error {
	ns := k.ns()

	if err := k.applyVolumes(ctx, ns, spec.Volumes); err != nil {
		return err
	}
	if err := k.applyDeployment(ctx, ns, spec); err != nil {
		return err
	}
	if err := k.applyService(ctx, ns, spec); err != nil {
		return err
	}
	if err := k.applyNetworkPolicy(ctx, ns, spec); err != nil {
		return err
	}
	return nil
}

// DeleteWorkload removes the Deployment, Service, NetworkPolicy and the
// volumes named in spec.Volumes for the workload. Volumes not listed in the
// spec (e.g. shared folders managed elsewhere) are left untouched.
func (k *KubernetesOrchestrator) DeleteWorkload(ctx context.Context, spec WorkloadSpec) error {
	ns := k.ns()
	name := spec.Name

	if err := k.clientset.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete deployment %s: %w", name, err)
	}
	if err := k.clientset.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete service %s: %w", name, err)
	}
	if err := k.clientset.NetworkingV1().NetworkPolicies(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete networkpolicy %s: %w", name, err)
	}
	for _, vol := range spec.Volumes {
		if vol.Shared {
			// Shared volumes are managed independently and may back other workloads.
			continue
		}
		if err := k.clientset.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, vol.Name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete pvc %s: %w", vol.Name, err)
		}
	}
	return nil
}

// EnsureSSHAccess writes publicKey into the workload's authorized_keys file.
// Reuses the existing helper that already writes via ExecInInstance and
// routes by the `app=<name>` label, so it works for both agent and browser
// workloads without further changes.
func (k *KubernetesOrchestrator) EnsureSSHAccess(ctx context.Context, name string, publicKey string) error {
	return configureSSHAccess(ctx, k.ExecInInstance, name, publicKey)
}

// WorkloadSSHAddress returns the pod IP and port 22 for the named workload.
func (k *KubernetesOrchestrator) WorkloadSSHAddress(ctx context.Context, name string) (string, int, error) {
	pods, err := k.clientset.CoreV1().Pods(k.ns()).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", name),
	})
	if err != nil {
		return "", 0, fmt.Errorf("list pods for %s: %w", name, err)
	}
	if len(pods.Items) == 0 {
		return "", 0, fmt.Errorf("no pods found for %s", name)
	}
	pod := pods.Items[0]
	if pod.Status.PodIP == "" {
		return "", 0, fmt.Errorf("pod %s has no IP assigned", pod.Name)
	}
	return pod.Status.PodIP, 22, nil
}

// --- internals ---

func (k *KubernetesOrchestrator) applyVolumes(ctx context.Context, ns string, vols []VolumeMount) error {
	seen := map[string]bool{}
	for _, vol := range vols {
		if seen[vol.Name] {
			continue
		}
		seen[vol.Name] = true
		if _, err := k.clientset.CoreV1().PersistentVolumeClaims(ns).Get(ctx, vol.Name, metav1.GetOptions{}); err == nil {
			continue
		} else if !errors.IsNotFound(err) {
			return fmt.Errorf("get pvc %s: %w", vol.Name, err)
		}
		size := vol.Size
		if size == "" {
			size = "1Gi"
		}
		access := corev1.ReadWriteOnce
		if vol.Shared {
			access = corev1.ReadWriteMany
		}
		labels := map[string]string{"managed-by": "claworc"}
		if vol.Shared {
			labels["type"] = "shared-folder"
		}
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: vol.Name, Namespace: ns, Labels: labels},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{access},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
				},
			},
		}
		if _, err := k.clientset.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create pvc %s: %w", vol.Name, err)
		}
	}
	return nil
}

func (k *KubernetesOrchestrator) applyDeployment(ctx context.Context, ns string, spec WorkloadSpec) error {
	desired := buildDeploymentFromSpec(ns, spec)
	existing, err := k.clientset.AppsV1().Deployments(ns).Get(ctx, spec.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, err := k.clientset.AppsV1().Deployments(ns).Create(ctx, desired, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("create deployment %s: %w", spec.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get deployment %s: %w", spec.Name, err)
	}
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	if _, err := k.clientset.AppsV1().Deployments(ns).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update deployment %s: %w", spec.Name, err)
	}
	return nil
}

func (k *KubernetesOrchestrator) applyService(ctx context.Context, ns string, spec WorkloadSpec) error {
	if len(spec.Ports) == 0 {
		// Tear down any stale Service if a previous spec had ports.
		_ = k.clientset.CoreV1().Services(ns).Delete(ctx, spec.Name, metav1.DeleteOptions{})
		return nil
	}
	ports := make([]corev1.ServicePort, 0, len(spec.Ports))
	for _, p := range spec.Ports {
		svcPort := p.ServicePort
		if svcPort == 0 {
			svcPort = p.ContainerPort
		}
		proto := corev1.ProtocolTCP
		if strings.EqualFold(p.Protocol, "UDP") {
			proto = corev1.ProtocolUDP
		}
		ports = append(ports, corev1.ServicePort{
			Name:       p.Name,
			Port:       int32(svcPort),
			TargetPort: intstr.FromInt32(int32(p.ContainerPort)),
			Protocol:   proto,
		})
	}
	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: ns,
			Labels:    mergeLabels(spec.Labels, map[string]string{"app": spec.Name, "managed-by": "claworc"}),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"app": spec.Name},
			Ports:    ports,
		},
	}
	existing, err := k.clientset.CoreV1().Services(ns).Get(ctx, spec.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		if _, err := k.clientset.CoreV1().Services(ns).Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create service %s: %w", spec.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get service %s: %w", spec.Name, err)
	}
	// Preserve ClusterIP and resourceVersion; replace selector + ports.
	existing.Spec.Selector = desired.Spec.Selector
	existing.Spec.Ports = desired.Spec.Ports
	existing.Labels = desired.Labels
	if _, err := k.clientset.CoreV1().Services(ns).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update service %s: %w", spec.Name, err)
	}
	return nil
}

// applyNetworkPolicy creates a default-deny ingress policy that allows traffic
// only from control-plane pods (label `app.kubernetes.io/name=claworc`) on the
// ports the workload exposes. Workloads with no ports get no NetworkPolicy.
func (k *KubernetesOrchestrator) applyNetworkPolicy(ctx context.Context, ns string, spec WorkloadSpec) error {
	if len(spec.Ports) == 0 {
		_ = k.clientset.NetworkingV1().NetworkPolicies(ns).Delete(ctx, spec.Name, metav1.DeleteOptions{})
		return nil
	}
	tcp := corev1.ProtocolTCP
	policyPorts := make([]networkingv1.NetworkPolicyPort, 0, len(spec.Ports))
	for _, p := range spec.Ports {
		port := intstr.FromInt32(int32(p.ContainerPort))
		policyPorts = append(policyPorts, networkingv1.NetworkPolicyPort{Protocol: &tcp, Port: &port})
	}
	desired := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: ns,
			Labels:    mergeLabels(spec.Labels, map[string]string{"app": spec.Name, "managed-by": "claworc"}),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": spec.Name}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{{
					PodSelector: &metav1.LabelSelector{MatchLabels: controlPlaneSelector},
				}},
				Ports: policyPorts,
			}},
		},
	}
	existing, err := k.clientset.NetworkingV1().NetworkPolicies(ns).Get(ctx, spec.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		if _, err := k.clientset.NetworkingV1().NetworkPolicies(ns).Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create networkpolicy %s: %w", spec.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get networkpolicy %s: %w", spec.Name, err)
	}
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	if _, err := k.clientset.NetworkingV1().NetworkPolicies(ns).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update networkpolicy %s: %w", spec.Name, err)
	}
	return nil
}

// buildDeploymentFromSpec converts a WorkloadSpec into a K8s Deployment object.
// All workload-specific decisions are encoded in the spec; this function only
// translates between representations.
func buildDeploymentFromSpec(ns string, spec WorkloadSpec) *appsv1.Deployment {
	replicas := int32(1)
	labels := mergeLabels(spec.Labels, map[string]string{"app": spec.Name, "managed-by": "claworc"})

	envVars := make([]corev1.EnvVar, 0, len(spec.Env))
	for k, v := range spec.Env {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	containerPorts := make([]corev1.ContainerPort, 0, len(spec.Ports))
	for _, p := range spec.Ports {
		containerPorts = append(containerPorts, corev1.ContainerPort{
			Name:          p.Name,
			ContainerPort: int32(p.ContainerPort),
		})
	}

	mainMounts := make([]corev1.VolumeMount, 0, len(spec.Volumes)+len(spec.EmptyDirs))
	for _, v := range spec.Volumes {
		mainMounts = append(mainMounts, corev1.VolumeMount{
			Name:      v.Name,
			MountPath: v.MountPath,
			SubPath:   v.SubPath,
			ReadOnly:  v.ReadOnly,
		})
	}
	for _, ed := range spec.EmptyDirs {
		mainMounts = append(mainMounts, corev1.VolumeMount{Name: ed.Name, MountPath: ed.MountPath})
	}

	volumes := make([]corev1.Volume, 0, len(spec.Volumes)+len(spec.EmptyDirs))
	added := map[string]bool{}
	for _, v := range spec.Volumes {
		if added[v.Name] {
			continue
		}
		added[v.Name] = true
		volumes = append(volumes, corev1.Volume{
			Name: v.Name,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: v.Name},
			},
		})
	}
	for _, ed := range spec.EmptyDirs {
		src := corev1.EmptyDirVolumeSource{}
		if strings.EqualFold(ed.Medium, "Memory") {
			src.Medium = corev1.StorageMediumMemory
		}
		if ed.SizeLimit != "" {
			q := resource.MustParse(ed.SizeLimit)
			src.SizeLimit = &q
		}
		volumes = append(volumes, corev1.Volume{Name: ed.Name, VolumeSource: corev1.VolumeSource{EmptyDir: &src}})
	}

	pullPolicy := corev1.PullAlways
	switch spec.Pull {
	case PullIfNotPresent:
		pullPolicy = corev1.PullIfNotPresent
	case PullNever:
		pullPolicy = corev1.PullNever
	}

	privileged := spec.Security.Privileged
	allowPrivEsc := spec.Security.AllowPrivilegeEscalation
	secCtx := &corev1.SecurityContext{
		Privileged:               &privileged,
		AllowPrivilegeEscalation: &allowPrivEsc,
	}
	if len(spec.Security.DropCapabilities) > 0 || len(spec.Security.AddCapabilities) > 0 {
		caps := &corev1.Capabilities{}
		for _, c := range spec.Security.DropCapabilities {
			caps.Drop = append(caps.Drop, corev1.Capability(c))
		}
		for _, c := range spec.Security.AddCapabilities {
			caps.Add = append(caps.Add, corev1.Capability(c))
		}
		secCtx.Capabilities = caps
	}
	if spec.Security.SeccompDefault {
		secCtx.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}
	}

	resources := corev1.ResourceRequirements{}
	if spec.Resources.CPURequest != "" || spec.Resources.MemoryRequest != "" {
		resources.Requests = corev1.ResourceList{}
		if spec.Resources.CPURequest != "" {
			resources.Requests[corev1.ResourceCPU] = resource.MustParse(spec.Resources.CPURequest)
		}
		if spec.Resources.MemoryRequest != "" {
			resources.Requests[corev1.ResourceMemory] = resource.MustParse(spec.Resources.MemoryRequest)
		}
	}
	if spec.Resources.CPULimit != "" || spec.Resources.MemoryLimit != "" {
		resources.Limits = corev1.ResourceList{}
		if spec.Resources.CPULimit != "" {
			resources.Limits[corev1.ResourceCPU] = resource.MustParse(spec.Resources.CPULimit)
		}
		if spec.Resources.MemoryLimit != "" {
			resources.Limits[corev1.ResourceMemory] = resource.MustParse(spec.Resources.MemoryLimit)
		}
	}

	mainContainer := corev1.Container{
		Name:            "main",
		Image:           spec.Image,
		Command:         spec.Command,
		ImagePullPolicy: pullPolicy,
		SecurityContext: secCtx,
		Env:             envVars,
		Resources:       resources,
		Ports:           containerPorts,
		VolumeMounts:    mainMounts,
	}
	if spec.Probes.Readiness != nil {
		mainContainer.ReadinessProbe = tcpProbe(spec.Probes.Readiness)
	}
	if spec.Probes.Liveness != nil {
		mainContainer.LivenessProbe = tcpProbe(spec.Probes.Liveness)
	}

	initContainers := make([]corev1.Container, 0, len(spec.InitContainers))
	for _, ic := range spec.InitContainers {
		mounts := make([]corev1.VolumeMount, 0, len(ic.Mounts))
		for _, m := range ic.Mounts {
			mounts = append(mounts, corev1.VolumeMount{Name: m.Name, MountPath: m.MountPath, SubPath: m.SubPath, ReadOnly: m.ReadOnly})
		}
		initContainers = append(initContainers, corev1.Container{
			Name:         ic.Name,
			Image:        ic.Image,
			Command:      ic.Command,
			VolumeMounts: mounts,
		})
	}

	podSpec := corev1.PodSpec{
		Hostname:         spec.Hostname,
		Containers:       []corev1.Container{mainContainer},
		InitContainers:   initContainers,
		Volumes:          volumes,
		ImagePullSecrets: []corev1.LocalObjectReference{{Name: "ghcr-secret"}},
	}
	if spec.Affinity != nil && len(spec.Affinity.RequiredCoLocation) > 0 {
		terms := make([]corev1.PodAffinityTerm, 0, len(spec.Affinity.RequiredCoLocation))
		for _, target := range spec.Affinity.RequiredCoLocation {
			terms = append(terms, corev1.PodAffinityTerm{
				LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": target}},
				TopologyKey:   "kubernetes.io/hostname",
			})
		}
		podSpec.Affinity = &corev1.Affinity{
			PodAffinity: &corev1.PodAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: terms,
			},
		}
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: ns, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": spec.Name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
		},
	}
}

func tcpProbe(p *TCPProbe) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(int32(p.Port))}},
		InitialDelaySeconds: int32(p.InitialDelay.Seconds()),
		PeriodSeconds:       int32(p.Period.Seconds()),
	}
}

func mergeLabels(in ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range in {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}
