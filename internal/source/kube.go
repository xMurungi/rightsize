package source

import (
	"context"
	"fmt"
	"rightsize/internal/model"
	// "runtime/metrics"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

// LoadKubeConfig uses the pod's service account when running inside a cluster,
// and otherwise your kubeconfig (KUBECONFIG, or ~/.kube/config) and its current context.
func LoadKubeConfig() (*rest.Config, error) {
	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}

	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
}

// Kube reads real usage and requests from a cluster. The fields are interfaces,
// so tests can plug in the fake clients that client-go provides.
type Kube struct {
	core    kubernetes.Interface
	metrics metricsclient.Interface
}

func NewKube(config *rest.Config) (*Kube, error) {

	core, err := kubernetes.NewForConfig(config)

	if err != nil {
		return nil, fmt.Errorf("kubernetes client: %w", err)
	}

	metrics, err := metricsclient.NewForConfig(config)

	if err != nil {
		return nil, fmt.Errorf("metrics client: %w", err)
	}

	return &Kube{core: core, metrics: metrics}, nil
}

func(kube *Kube) Snapshot(ctx context.Context) (model.Snapshot, error) {
	pods, err := kube.core.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return model.Snapshot{}, fmt.Errorf("list pods: %w", err)
	}

	podMetrics, err := kube.metrics.MetricsV1beta1().PodMetricses(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return model.Snapshot{}, fmt.Errorf("list pod metrics (is metrics-server running?): %w", err)
	}

	var snapshot model.Snapshot
	workloadByPod := map[string]string{}        	// "namespace/pod" -> workload name
	specsByKey := map[model.Key]*model.Spec{}         // one entry per workload + container

	for index := range pods.Items {
		pod := &pods.Items[index]
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}

		workload := workloadName(pod)
		workloadByPod[pod.Namespace + "/" + pod.Name] = workload

		for _, container := range pod.Spec.Containers {
			key := model.Key{Namespace: pod.Namespace, Workload: workload, Container: container.Name}
			spec := specsByKey[key]

			if spec == nil {
				spec = &model.Spec{
					Key: key,
					CPURequestMilli: container.Resources.Requests.Cpu().MilliValue(),
					MemRequestBytes: container.Resources.Requests.Memory().Value(),
					CPULimitMilli:   container.Resources.Limits.Cpu().MilliValue(),
					MemLimitBytes:   container.Resources.Limits.Memory().Value(),
				}
				specsByKey[key] = spec
			}
			addStatus(spec, pod, container.Name)
		}
	}

	for _, spec := range specsByKey {
		snapshot.Specs = append(snapshot.Specs, *spec)
	}

	for _, podMetric := range podMetrics.Items {
		workload, found := workloadByPod[podMetric.Namespace + "/" + podMetric.Name]

		if !found {
			continue    // a pod that finished or appeared between the two calls
		}

		for _, container := range podMetric.Containers {
			snapshot.Usages = append(snapshot.Usages, model.Usage{
				Key: model.Key{Namespace: podMetric.Namespace, Workload: workload, Container: container.Name},
				CPUMilli: container.Usage.Cpu().MilliValue(),
				MemBytes: container.Usage.Memory().Value(),
			})
		}
	}
	return snapshot, nil
}

// addStatus folds one pod's restart count and OOMKill history into the spec.
func addStatus(spec *model.Spec, pod *corev1.Pod, containerName string) {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name != containerName {
			continue
		}

		spec.Restarts = max(spec.Restarts, int64(status.RestartCount))
		terminations := []*corev1.ContainerStateTerminated{status.State.Terminated, status.LastTerminationState.Terminated}

		for _, terminated := range terminations {
			if terminated != nil && terminated.Reason == "OOMKilled" && terminated.FinishedAt.Time.After(spec.LastOOM) {
				spec.LastOOM = terminated.FinishedAt.Time
			}
		}
	}
}

// workloadName: see the label trick above.
func workloadName(pod *corev1.Pod) string {
	owner := metav1.GetControllerOf(pod)
	if owner == nil {
		return pod.Name
	}

	if owner.Kind == "ReplicaSet" {
		if hash := pod.Labels["pod-template-hash"]; hash != "" {
			return strings.TrimSuffix(owner.Name, "-" + hash)
		}
	}
	return owner.Name
}
