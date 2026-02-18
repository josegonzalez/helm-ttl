package ttl

import (
	"context"
	"fmt"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// LogFetcher abstracts pod log retrieval for testability.
type LogFetcher func(ctx context.Context, namespace, podName, containerName string) (io.ReadCloser, error)

// NewKubeLogFetcher returns a LogFetcher that uses the Kubernetes API.
func NewKubeLogFetcher(client kubernetes.Interface) LogFetcher {
	return func(ctx context.Context, namespace, podName, containerName string) (io.ReadCloser, error) {
		opts := &corev1.PodLogOptions{
			Container: containerName,
		}
		return client.CoreV1().Pods(namespace).GetLogs(podName, opts).Stream(ctx)
	}
}

// waitForPod polls until a pod owned by the given job appears.
func waitForPod(ctx context.Context, client kubernetes.Interface, namespace, jobName string) (string, error) {
	labelSelector := fmt.Sprintf("job-name=%s", jobName)
	for {
		pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return "", fmt.Errorf("failed to list pods: %w", err)
		}

		if len(pods.Items) > 0 {
			return pods.Items[0].Name, nil
		}

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timed out waiting for pod (job %s): %w", jobName, ctx.Err())
		case <-time.After(1 * time.Second):
		}
	}
}

// waitForContainerTermination polls until the named container has terminated.
func waitForContainerTermination(ctx context.Context, client kubernetes.Interface, namespace, podName, containerName string) (int32, error) {
	for {
		pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return -1, fmt.Errorf("failed to get pod %s: %w", podName, err)
		}

		allStatuses := append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...)
		for _, cs := range allStatuses {
			if cs.Name == containerName && cs.State.Terminated != nil {
				return cs.State.Terminated.ExitCode, nil
			}
		}

		select {
		case <-ctx.Done():
			return -1, fmt.Errorf("timed out waiting for container %s in pod %s: %w", containerName, podName, ctx.Err())
		case <-time.After(1 * time.Second):
		}
	}
}

// streamContainerLogs fetches and writes container logs to w with a header.
func streamContainerLogs(ctx context.Context, logFetcher LogFetcher, w io.Writer, namespace, podName, containerName string) error {
	_, _ = fmt.Fprintf(w, "==> Container: %s <==\n", containerName)

	rc, err := logFetcher(ctx, namespace, podName, containerName)
	if err != nil {
		return fmt.Errorf("failed to get logs for container %s: %w", containerName, err)
	}
	defer func() { _ = rc.Close() }()

	if _, err := io.Copy(w, rc); err != nil {
		return fmt.Errorf("failed to stream logs for container %s: %w", containerName, err)
	}

	return nil
}
