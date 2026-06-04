package orchestrator

import (
	"context"
	"fmt"
	"log"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// StartPodWatch watches for pod restarts in the claworc namespace and calls
// onRestart(instanceName) when a bot pod is deleted or replaced. It is a no-op
// if the orchestrator is not a KubernetesOrchestrator. The goroutine runs until
// ctx is cancelled and automatically reconnects on watch errors.
//
// Backward compatible: if the K8s watch cannot be established (RBAC, network),
// it logs a warning and the caller falls back to the existing manual-restart
// recovery path.
func StartPodWatch(ctx context.Context, orch ContainerOrchestrator, onRestart func(instanceName string)) {
	k8s, ok := orch.(*KubernetesOrchestrator)
	if !ok {
		return
	}
	go k8s.watchPodRestarts(ctx, onRestart)
}

func (k *KubernetesOrchestrator) watchPodRestarts(ctx context.Context, onRestart func(string)) {
	// podUIDs tracks the last seen Kubernetes pod UID per instance (app label).
	// When a pod is replaced its UID changes, letting us detect restarts even
	// when we missed the intermediate DELETE event.
	podUIDs := make(map[string]string)

	log.Printf("[pod-watch] Starting pod watch in namespace %s", k.ns())

	for {
		if err := k.runPodWatch(ctx, podUIDs, onRestart); err != nil {
			if ctx.Err() != nil {
				log.Printf("[pod-watch] Stopped (context cancelled)")
				return
			}
			log.Printf("[pod-watch] Watch error: %v; reconnecting in 15s", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
			}
		}
	}
}

func (k *KubernetesOrchestrator) runPodWatch(ctx context.Context, podUIDs map[string]string, onRestart func(string)) error {
	watcher, err := k.clientset.CoreV1().Pods(k.ns()).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("create watch: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("watch channel closed")
			}
			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			instanceName := pod.Labels["app"]
			if instanceName == "" {
				continue
			}
			uid := string(pod.UID)

			switch event.Type {
			case watch.Deleted:
				// Pod is gone — clear SSH state immediately so the reconnect
				// loop doesn't block on a stale rate-limit while waiting for
				// the new pod. Keep the old UID in the map so we can also
				// detect the case where Added fires with a new UID (pod
				// recreated so fast we got both events in the same watch).
				log.Printf("[pod-watch] Pod deleted for instance %s, resetting SSH state", instanceName)
				onRestart(instanceName)
				delete(podUIDs, instanceName)

			case watch.Added:
				prevUID, known := podUIDs[instanceName]
				podUIDs[instanceName] = uid
				// If we already knew this instance but the UID changed, the
				// pod was replaced (we may have missed the DELETE event).
				if known && prevUID != uid {
					log.Printf("[pod-watch] Pod UID changed for instance %s, resetting SSH state", instanceName)
					onRestart(instanceName)
				}
			}
		}
	}
}
