package orchestrator

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
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
	clientset  *kubernetes.Clientset
	restConfig *rest.Config
	available  bool
	inCluster  bool
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

func (k *KubernetesOrchestrator) ns() string {
	return config.Cfg.K8sNamespace
}

func (k *KubernetesOrchestrator) CreateInstance(ctx context.Context, params CreateParams) error {
	ns := k.ns()

	pvcs := []struct {
		suffix  string
		storage string
	}{
		{"homebrew", params.StorageHomebrew},
		{"openclaw", params.StorageClawd},
		{"chrome", params.StorageChrome},
	}
	for _, p := range pvcs {
		pvc := buildPVC(fmt.Sprintf("%s-%s", params.Name, p.suffix), ns, p.storage)
		if _, err := k.clientset.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create PVC %s: %w", p.suffix, err)
		}
	}

	dep := buildDeployment(params, ns)
	if _, err := k.clientset.AppsV1().Deployments(ns).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create deployment: %w", err)
	}

	svc := buildService(params.Name, ns)
	if _, err := k.clientset.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create service: %w", err)
	}

	if token, ok := params.EnvVars["OPENCLAW_GATEWAY_TOKEN"]; ok && token != "" {
		go k.configureGatewayToken(context.Background(), params.Name, token)
	}

	return nil
}

func (k *KubernetesOrchestrator) waitForPodRunning(ctx context.Context, name string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pods, err := k.clientset.CoreV1().Pods(k.ns()).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", name),
		})
		if err == nil && len(pods.Items) > 0 {
			pod := pods.Items[0]
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Running != nil {
					tag := pod.Spec.Containers[0].Image
					sha := cs.ImageID
					if idx := strings.Index(sha, "sha256:"); idx >= 0 {
						sha = sha[idx:]
						if len(sha) > 19 { // "sha256:" (7) + 12 chars
							sha = sha[:19]
						}
					}
					return fmt.Sprintf("%s (%s)", tag, sha), true
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(2 * time.Second):
		}
	}
	return "", false
}

func (k *KubernetesOrchestrator) configureGatewayToken(ctx context.Context, name, token string) {
	configureGatewayToken(ctx, k.ExecInInstance, name, token, k.waitForPodRunning)
}

func (k *KubernetesOrchestrator) CloneVolumes(ctx context.Context, srcName, dstName string) error {
	// Scale both deployments to 0 to release PVCs (RWO constraint)
	_ = k.scaleDeployment(ctx, srcName, 0)
	_ = k.scaleDeployment(ctx, dstName, 0)
	k.waitForPodTermination(ctx, srcName, 60*time.Second)
	k.waitForPodTermination(ctx, dstName, 60*time.Second)

	// Copy each PVC pair
	for _, suffix := range []string{"homebrew", "openclaw", "chrome"} {
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
	if err := k.clientset.CoreV1().Services(ns).Delete(ctx, name+"-vnc", metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete service: %w", err)
	}
	for _, suffix := range []string{"homebrew", "openclaw", "chrome"} {
		pvcName := fmt.Sprintf("%s-%s", name, suffix)
		if err := k.clientset.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, pvcName, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete PVC %s: %w", suffix, err)
		}
	}
	return nil
}

func (k *KubernetesOrchestrator) StartInstance(ctx context.Context, name string) error {
	return k.scaleDeployment(ctx, name, 1)
}

func (k *KubernetesOrchestrator) StopInstance(ctx context.Context, name string) error {
	return k.scaleDeployment(ctx, name, 0)
}

func (k *KubernetesOrchestrator) RestartInstance(ctx context.Context, name string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"%s"}}}}}`, now)
	_, err := k.clientset.AppsV1().Deployments(k.ns()).Patch(
		ctx, name, "application/strategic-merge-patch+json", []byte(patch), metav1.PatchOptions{},
	)
	return err
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

func (k *KubernetesOrchestrator) UpdateInstanceConfig(ctx context.Context, name string, configJSON string) error {
	return updateInstanceConfig(ctx, k.ExecInInstance, name, configJSON)
}

func (k *KubernetesOrchestrator) StreamInstanceLogs(ctx context.Context, name string, tail int, follow bool) (<-chan string, error) {
	podName, err := k.getPodName(ctx, name)
	if err != nil {
		return nil, err
	}
	if podName == "" {
		ch := make(chan string, 1)
		ch <- "No pods found"
		close(ch)
		return ch, nil
	}

	cmd := fmt.Sprintf("openclaw logs --plain --limit %d", tail)
	if follow {
		cmd += " --follow"
	}
	cmdSlice := []string{"su", "-", "abc", "-c", cmd}

	req := k.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(k.ns()).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: cmdSlice,
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(k.restConfig, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("create executor: %w", err)
	}

	stdoutR, stdoutW := io.Pipe()

	ch := make(chan string, 100)
	go func() {
		defer stdoutW.Close()
		err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdout: stdoutW,
			Stderr: stdoutW,
		})
		if err != nil {
			log.Printf("k8s log exec stream ended: %v", err)
		}
	}()

	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(stdoutR)
		for scanner.Scan() {
			select {
			case ch <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
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

// termSizeQueue implements remotecommand.TerminalSizeQueue via a channel.
type termSizeQueue struct {
	ch chan remotecommand.TerminalSize
}

func (q *termSizeQueue) Next() *remotecommand.TerminalSize {
	size, ok := <-q.ch
	if !ok {
		return nil
	}
	return &size
}

func (k *KubernetesOrchestrator) ExecInteractive(ctx context.Context, name string, cmd []string) (*ExecSession, error) {
	podName, err := k.getPodName(ctx, name)
	if err != nil {
		return nil, err
	}
	if podName == "" {
		return nil, fmt.Errorf("no running pod found for instance %s", name)
	}

	req := k.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(k.ns()).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: cmd,
			Stdin:   true,
			Stdout:  true,
			Stderr:  false,
			TTY:     true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(k.restConfig, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("create executor: %w", err)
	}

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	sizeCh := make(chan remotecommand.TerminalSize, 1)
	sizeQueue := &termSizeQueue{ch: sizeCh}

	// Send initial size
	sizeCh <- remotecommand.TerminalSize{Width: 80, Height: 24}

	done := make(chan struct{})

	go func() {
		defer close(done)
		defer stdoutW.Close()
		err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdin:             stdinR,
			Stdout:            stdoutW,
			Tty:               true,
			TerminalSizeQueue: sizeQueue,
		})
		if err != nil {
			log.Printf("k8s exec stream ended: %v", err)
		}
	}()

	return &ExecSession{
		Stdin:  stdinW,
		Stdout: stdoutR,
		Resize: func(cols, rows uint16) error {
			// Drain any pending size so the new one is always delivered
			select {
			case <-sizeCh:
			default:
			}
			sizeCh <- remotecommand.TerminalSize{Width: cols, Height: rows}
			return nil
		},
		Close: func() error {
			close(sizeCh)
			stdinW.Close()
			stdinR.Close()
			stdoutR.Close()
			return nil
		},
	}, nil
}

func (k *KubernetesOrchestrator) ListDirectory(ctx context.Context, name string, path string) ([]FileEntry, error) {
	return listDirectory(ctx, k.ExecInInstance, name, path)
}

func (k *KubernetesOrchestrator) ReadFile(ctx context.Context, name string, path string) ([]byte, error) {
	return readFile(ctx, k.ExecInInstance, name, path)
}

func (k *KubernetesOrchestrator) CreateFile(ctx context.Context, name string, path string, content string) error {
	return createFile(ctx, k.ExecInInstance, name, path, content)
}

func (k *KubernetesOrchestrator) CreateDirectory(ctx context.Context, name string, path string) error {
	return createDirectory(ctx, k.ExecInInstance, name, path)
}

func (k *KubernetesOrchestrator) WriteFile(ctx context.Context, name string, path string, data []byte) error {
	return writeFile(ctx, k.ExecInInstance, name, path, data)
}

func (k *KubernetesOrchestrator) GetVNCBaseURL(_ context.Context, name string, display string) (string, error) {
	if display != "chrome" {
		return "", fmt.Errorf("unsupported display type: %s", display)
	}
	port := 3000
	if !k.inCluster {
		host := strings.TrimRight(k.restConfig.Host, "/")
		return fmt.Sprintf("%s/api/v1/namespaces/%s/services/%s-vnc:%d/proxy", host, k.ns(), name, port), nil
	}
	return fmt.Sprintf("http://%s-vnc.%s.svc.cluster.local:%d", name, k.ns(), port), nil
}

func (k *KubernetesOrchestrator) GetGatewayWSURL(_ context.Context, name string) (string, error) {
	if !k.inCluster {
		host := strings.TrimRight(k.restConfig.Host, "/")
		// Convert https:// to wss:// for WebSocket through API server proxy
		wsHost := strings.Replace(host, "https://", "wss://", 1)
		wsHost = strings.Replace(wsHost, "http://", "ws://", 1)
		return fmt.Sprintf("%s/api/v1/namespaces/%s/services/%s-vnc:3000/proxy/gateway", wsHost, k.ns(), name), nil
	}
	return fmt.Sprintf("ws://%s-vnc.%s.svc.cluster.local:3000/gateway", name, k.ns()), nil
}

func (k *KubernetesOrchestrator) GetInstanceSSHEndpoint(ctx context.Context, name string) (string, int, error) {
	svcName := name + "-vnc"
	svc, err := k.clientset.CoreV1().Services(k.ns()).Get(ctx, svcName, metav1.GetOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("get service %s: %w", svcName, err)
	}

	if svc.Spec.Type == corev1.ServiceTypeNodePort {
		for _, p := range svc.Spec.Ports {
			if p.Port == 22 {
				// For NodePort, use the first node address we can find
				nodes, err := k.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				if err != nil {
					return "", 0, fmt.Errorf("list nodes: %w", err)
				}
				if len(nodes.Items) == 0 {
					return "", 0, fmt.Errorf("no nodes found")
				}
				nodeHost := ""
				for _, addr := range nodes.Items[0].Status.Addresses {
					if addr.Type == corev1.NodeInternalIP {
						nodeHost = addr.Address
						break
					}
				}
				if nodeHost == "" {
					return "", 0, fmt.Errorf("no internal IP found for node %s", nodes.Items[0].Name)
				}
				return nodeHost, int(p.NodePort), nil
			}
		}
		return "", 0, fmt.Errorf("SSH port 22 not found in service %s", svcName)
	}

	// ClusterIP service - return ClusterIP and port 22
	return svc.Spec.ClusterIP, 22, nil
}

func (k *KubernetesOrchestrator) GetHTTPTransport() http.RoundTripper {
	if !k.inCluster {
		transport, err := rest.TransportFor(k.restConfig)
		if err != nil {
			log.Printf("Failed to create K8s transport: %v", err)
			return nil
		}
		return transport
	}
	return nil
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

func buildDeployment(params CreateParams, ns string) *appsv1.Deployment {
	replicas := int32(1)
	privileged := true

	envVars := []corev1.EnvVar{
		{Name: "PUID", Value: "1000"},
		{Name: "PGID", Value: "1000"},
		{Name: "START_DOCKER", Value: "false"},
	}
	if parts := strings.SplitN(params.VNCResolution, "x", 2); len(parts) == 2 {
		envVars = append(envVars,
			corev1.EnvVar{Name: "SELKIES_MANUAL_WIDTH", Value: parts[0]},
			corev1.EnvVar{Name: "SELKIES_MANUAL_HEIGHT", Value: parts[1]},
		)
	}
	if token, ok := params.EnvVars["OPENCLAW_GATEWAY_TOKEN"]; ok && token != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "OPENCLAW_GATEWAY_TOKEN", Value: token})
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
					Containers: []corev1.Container{{
						Name:            "moltbot",
						Image:           params.ContainerImage,
						ImagePullPolicy: corev1.PullAlways,
						SecurityContext: &corev1.SecurityContext{Privileged: &privileged},
						Ports: []corev1.ContainerPort{
							{Name: "http", ContainerPort: 3000},
						},
						Env: envVars,
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
						VolumeMounts: []corev1.VolumeMount{
							{Name: "chrome-data", MountPath: "/config/chrome-data"},
							{Name: "homebrew-data", MountPath: "/home/linuxbrew/.linuxbrew"},
							{Name: "openclaw-data", MountPath: "/config/.openclaw"},
							{Name: "dshm", MountPath: "/dev/shm"},
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(3000)}},
							InitialDelaySeconds: 60,
							PeriodSeconds:       30,
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/", Port: intstr.FromInt32(3000)}},
							InitialDelaySeconds: 30,
							PeriodSeconds:       10,
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "homebrew-data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: params.Name + "-homebrew"}}},
						{Name: "openclaw-data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: params.Name + "-openclaw"}}},
						{Name: "chrome-data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: params.Name + "-chrome"}}},
						{Name: "dshm", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: &shmSize}}},
					},
					ImagePullSecrets: []corev1.LocalObjectReference{{Name: "ghcr-secret"}},
				},
			},
		},
	}
}

func buildService(name, ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-vnc", Namespace: ns},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"app": name},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 3000, TargetPort: intstr.FromInt32(3000), Protocol: corev1.ProtocolTCP},
				{Name: "ssh", Port: 22, TargetPort: intstr.FromInt32(22), Protocol: corev1.ProtocolTCP},
			},
		},
	}
}
