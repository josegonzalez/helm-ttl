package ttl

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestWaitForPod(t *testing.T) {
	t.Run("pod found immediately", func(t *testing.T) {
		client := fake.NewClientset(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				Labels:    map[string]string{"job-name": "test-job"},
			},
		})

		ctx := context.Background()
		podName, err := waitForPod(ctx, client, "default", "test-job")
		require.NoError(t, err)
		assert.Equal(t, "test-pod", podName)
	})

	t.Run("context cancelled", func(t *testing.T) {
		client := fake.NewClientset()

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, err := waitForPod(ctx, client, "default", "test-job")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "timed out waiting for pod")
	})
}

func TestWaitForContainerTermination(t *testing.T) {
	t.Run("normal exit", func(t *testing.T) {
		client := fake.NewClientset(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "test-container",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 0,
							},
						},
					},
				},
			},
		})

		ctx := context.Background()
		exitCode, err := waitForContainerTermination(ctx, client, "default", "test-pod", "test-container")
		require.NoError(t, err)
		assert.Equal(t, int32(0), exitCode)
	})

	t.Run("non-zero exit", func(t *testing.T) {
		client := fake.NewClientset(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "test-container",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 1,
							},
						},
					},
				},
			},
		})

		ctx := context.Background()
		exitCode, err := waitForContainerTermination(ctx, client, "default", "test-pod", "test-container")
		require.NoError(t, err)
		assert.Equal(t, int32(1), exitCode)
	})

	t.Run("init container termination", func(t *testing.T) {
		client := fake.NewClientset(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
			},
			Status: corev1.PodStatus{
				InitContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "init-container",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 0,
							},
						},
					},
				},
			},
		})

		ctx := context.Background()
		exitCode, err := waitForContainerTermination(ctx, client, "default", "test-pod", "init-container")
		require.NoError(t, err)
		assert.Equal(t, int32(0), exitCode)
	})

	t.Run("timeout waiting for termination", func(t *testing.T) {
		client := fake.NewClientset(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "test-container",
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{},
						},
					},
				},
			},
		})

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, err := waitForContainerTermination(ctx, client, "default", "test-pod", "test-container")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "timed out waiting for container")
	})
}

func TestStreamContainerLogs(t *testing.T) {
	t.Run("writes header and log content", func(t *testing.T) {
		logContent := "line 1\nline 2\n"
		fetcher := func(_ context.Context, _, _, _ string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(logContent)), nil
		}

		var buf bytes.Buffer
		ctx := context.Background()
		err := streamContainerLogs(ctx, fetcher, &buf, "default", "test-pod", "helm-uninstall")
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, "==> Container: helm-uninstall <==")
		assert.Contains(t, output, "line 1\nline 2\n")
	})

	t.Run("log fetch error", func(t *testing.T) {
		fetcher := func(_ context.Context, _, _, _ string) (io.ReadCloser, error) {
			return nil, assert.AnError
		}

		var buf bytes.Buffer
		ctx := context.Background()
		err := streamContainerLogs(ctx, fetcher, &buf, "default", "test-pod", "test-container")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get logs for container")
	})
}
