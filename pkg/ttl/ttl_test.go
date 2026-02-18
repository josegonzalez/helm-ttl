package ttl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
)

func setupTestRelease(t *testing.T, name, namespace string) (*action.Configuration, *storage.Storage) {
	t.Helper()

	mem := driver.NewMemory()
	store := storage.Init(mem)

	rel := &release.Release{
		Name:      name,
		Namespace: namespace,
		Version:   1,
		Info: &release.Info{
			Status: release.StatusDeployed,
		},
		Chart: &chart.Chart{
			Metadata: &chart.Metadata{
				Name:    "test-chart",
				Version: "1.0.0",
			},
		},
	}
	err := store.Create(rel)
	require.NoError(t, err)

	cfg := &action.Configuration{
		Releases:   store,
		KubeClient: &kubefake.PrintingKubeClient{Out: io.Discard},
		Log:        func(format string, v ...interface{}) {},
	}
	return cfg, store
}

func TestSetTTL(t *testing.T) {
	ctx := context.Background()

	t.Run("sets TTL with create-service-account", func(t *testing.T) {
		cfg, _ := setupTestRelease(t, "myapp", "default")
		client := fake.NewClientset()

		err := SetTTL(ctx, cfg, client, SetTTLOptions{
			ReleaseName:          "myapp",
			ReleaseNamespace:     "default",
			CronjobNamespace:     "default",
			Duration:             "24h",
			ServiceAccount:       "default",
			CreateServiceAccount: true,
		})
		require.NoError(t, err)

		// Verify CronJob was created
		cj, err := client.BatchV1().CronJobs("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, "myapp-default-ttl", cj.Name)
		assert.Equal(t, LabelManagedByValue, cj.Labels[LabelManagedBy])
	})

	t.Run("sets TTL with existing service account", func(t *testing.T) {
		cfg, _ := setupTestRelease(t, "myapp", "default")
		client := fake.NewClientset(&corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-sa",
				Namespace: "default",
			},
		})

		err := SetTTL(ctx, cfg, client, SetTTLOptions{
			ReleaseName:      "myapp",
			ReleaseNamespace: "default",
			CronjobNamespace: "default",
			Duration:         "2h",
			ServiceAccount:   "my-sa",
		})
		require.NoError(t, err)

		cj, err := client.BatchV1().CronJobs("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, "my-sa", cj.Spec.JobTemplate.Spec.Template.Spec.ServiceAccountName)
	})

	t.Run("fails when release not found", func(t *testing.T) {
		mem := driver.NewMemory()
		store := storage.Init(mem)
		cfg := &action.Configuration{Releases: store}
		client := fake.NewClientset()

		err := SetTTL(ctx, cfg, client, SetTTLOptions{
			ReleaseName:          "nonexistent",
			ReleaseNamespace:     "default",
			CronjobNamespace:     "default",
			Duration:             "1h",
			ServiceAccount:       "default",
			CreateServiceAccount: true,
		})

		var notFound *ReleaseNotFoundError
		assert.True(t, errors.As(err, &notFound))
	})

	t.Run("fails when service account not found", func(t *testing.T) {
		cfg, _ := setupTestRelease(t, "myapp", "default")
		client := fake.NewClientset()

		err := SetTTL(ctx, cfg, client, SetTTLOptions{
			ReleaseName:      "myapp",
			ReleaseNamespace: "default",
			CronjobNamespace: "default",
			Duration:         "1h",
			ServiceAccount:   "nonexistent-sa",
		})

		var notFound *ServiceAccountNotFoundError
		assert.True(t, errors.As(err, &notFound))
	})

	t.Run("rejects delete-namespace when same namespace", func(t *testing.T) {
		cfg, _ := setupTestRelease(t, "myapp", "default")
		client := fake.NewClientset()

		err := SetTTL(ctx, cfg, client, SetTTLOptions{
			ReleaseName:          "myapp",
			ReleaseNamespace:     "default",
			CronjobNamespace:     "default",
			Duration:             "1h",
			ServiceAccount:       "default",
			CreateServiceAccount: true,
			DeleteNamespace:      true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot use --delete-namespace")
	})

	t.Run("cross-namespace with delete-namespace", func(t *testing.T) {
		cfg, _ := setupTestRelease(t, "myapp", "staging")
		client := fake.NewClientset()

		err := SetTTL(ctx, cfg, client, SetTTLOptions{
			ReleaseName:          "myapp",
			ReleaseNamespace:     "staging",
			CronjobNamespace:     "ops",
			Duration:             "7d",
			ServiceAccount:       "default",
			CreateServiceAccount: true,
			DeleteNamespace:      true,
		})
		require.NoError(t, err)

		cj, err := client.BatchV1().CronJobs("ops").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, "true", cj.Labels[LabelDeleteNamespace])

		// Verify init containers include namespace deletion
		spec := cj.Spec.JobTemplate.Spec.Template.Spec
		assert.Len(t, spec.InitContainers, 2)
	})

	t.Run("updates existing CronJob", func(t *testing.T) {
		cfg, _ := setupTestRelease(t, "myapp", "default")
		client := fake.NewClientset()

		// Create initial TTL
		err := SetTTL(ctx, cfg, client, SetTTLOptions{
			ReleaseName:          "myapp",
			ReleaseNamespace:     "default",
			CronjobNamespace:     "default",
			Duration:             "1h",
			ServiceAccount:       "default",
			CreateServiceAccount: true,
		})
		require.NoError(t, err)

		// Get initial schedule
		cj1, err := client.BatchV1().CronJobs("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		require.NoError(t, err)
		schedule1 := cj1.Spec.Schedule

		// Update TTL
		err = SetTTL(ctx, cfg, client, SetTTLOptions{
			ReleaseName:          "myapp",
			ReleaseNamespace:     "default",
			CronjobNamespace:     "default",
			Duration:             "48h",
			ServiceAccount:       "default",
			CreateServiceAccount: true,
		})
		require.NoError(t, err)

		// Verify schedule changed
		cj2, err := client.BatchV1().CronJobs("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		require.NoError(t, err)
		assert.NotEqual(t, schedule1, cj2.Spec.Schedule)
	})

	t.Run("invalid duration", func(t *testing.T) {
		cfg, _ := setupTestRelease(t, "myapp", "default")
		client := fake.NewClientset()

		err := SetTTL(ctx, cfg, client, SetTTLOptions{
			ReleaseName:          "myapp",
			ReleaseNamespace:     "default",
			CronjobNamespace:     "default",
			Duration:             "invalid",
			ServiceAccount:       "default",
			CreateServiceAccount: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid duration")
	})
}

func TestGetTTL(t *testing.T) {
	ctx := context.Background()

	t.Run("gets existing TTL", func(t *testing.T) {
		client := fake.NewClientset(&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp-default-ttl",
				Namespace: "default",
				Labels: map[string]string{
					LabelManagedBy:        LabelManagedByValue,
					LabelRelease:          "myapp",
					LabelReleaseNamespace: "default",
					LabelCronjobNamespace: "default",
					LabelDeleteNamespace:  "false",
				},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "30 14 15 3 *",
			},
		})

		info, err := GetTTL(ctx, client, "myapp", "default", "default")
		require.NoError(t, err)
		assert.Equal(t, "myapp", info.ReleaseName)
		assert.Equal(t, "default", info.ReleaseNamespace)
		assert.Equal(t, "default", info.CronjobNamespace)
		assert.Equal(t, "30 14 15 3 *", info.CronSchedule)
		assert.False(t, info.DeleteNamespace)
	})

	t.Run("TTL not found", func(t *testing.T) {
		client := fake.NewClientset()

		_, err := GetTTL(ctx, client, "myapp", "default", "default")
		var notFound *TTLNotFoundError
		assert.True(t, errors.As(err, &notFound))
	})

	t.Run("cross-namespace TTL", func(t *testing.T) {
		client := fake.NewClientset(&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp-staging-ttl",
				Namespace: "ops",
				Labels: map[string]string{
					LabelManagedBy:        LabelManagedByValue,
					LabelRelease:          "myapp",
					LabelReleaseNamespace: "staging",
					LabelCronjobNamespace: "ops",
					LabelDeleteNamespace:  "true",
				},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "0 12 1 1 *",
			},
		})

		info, err := GetTTL(ctx, client, "myapp", "staging", "ops")
		require.NoError(t, err)
		assert.Equal(t, "staging", info.ReleaseNamespace)
		assert.Equal(t, "ops", info.CronjobNamespace)
		assert.True(t, info.DeleteNamespace)
	})
}

func TestUnsetTTL(t *testing.T) {
	ctx := context.Background()

	t.Run("unsets existing TTL", func(t *testing.T) {
		client := fake.NewClientset(&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp-default-ttl",
				Namespace: "default",
				Labels: map[string]string{
					LabelManagedBy:        LabelManagedByValue,
					LabelRelease:          "myapp",
					LabelReleaseNamespace: "default",
				},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "30 14 15 6 *",
			},
		})

		err := UnsetTTL(ctx, client, "myapp", "default", "default")
		require.NoError(t, err)

		// Verify CronJob is gone
		_, err = client.BatchV1().CronJobs("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		assert.Error(t, err)
	})

	t.Run("TTL not found", func(t *testing.T) {
		client := fake.NewClientset()

		err := UnsetTTL(ctx, client, "myapp", "default", "default")
		var notFound *TTLNotFoundError
		assert.True(t, errors.As(err, &notFound))
	})

	t.Run("cleans up RBAC on unset", func(t *testing.T) {
		client := fake.NewClientset()

		// Create RBAC and CronJob
		err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "default", "default", "myapp-default-ttl", false)
		require.NoError(t, err)

		_, err = client.BatchV1().CronJobs("default").Create(ctx, &batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp-default-ttl",
				Namespace: "default",
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "30 14 15 6 *",
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		// Unset
		err = UnsetTTL(ctx, client, "myapp", "default", "default")
		require.NoError(t, err)

		// Verify RBAC cleaned up
		_, err = client.CoreV1().ServiceAccounts("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		assert.Error(t, err)

		_, err = client.RbacV1().Roles("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		assert.Error(t, err)
	})
}

func TestReleaseNotFoundError(t *testing.T) {
	err := &ReleaseNotFoundError{Name: "myapp"}
	assert.Equal(t, `release "myapp" not found`, err.Error())
}

func TestTTLNotFoundError(t *testing.T) {
	err := &TTLNotFoundError{Name: "myapp"}
	assert.Equal(t, `no TTL set for release "myapp"`, err.Error())
}

func TestServiceAccountNotFoundError(t *testing.T) {
	err := &ServiceAccountNotFoundError{Name: "my-sa", Namespace: "default"}
	assert.Equal(t, `service account "my-sa" not found in namespace "default"`, err.Error())
}

func TestGetTTL_InvalidSchedule(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset(&batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp-default-ttl",
			Namespace: "default",
			Labels: map[string]string{
				LabelManagedBy: LabelManagedByValue,
			},
		},
		Spec: batchv1.CronJobSpec{
			Schedule: "invalid-schedule",
		},
	})

	_, err := GetTTL(ctx, client, "myapp", "default", "default")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse CronJob schedule")
}

func TestGetTTL_ResourceNameTooLong(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	_, err := GetTTL(ctx, client, "a-very-long-release-name-that-will-exceed", "a-long-namespace", "default")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum length")
}

func TestUnsetTTL_ResourceNameTooLong(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	err := UnsetTTL(ctx, client, "a-very-long-release-name-that-will-exceed", "a-long-namespace", "default")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum length")
}

func TestSetTTL_CustomServiceAccountName(t *testing.T) {
	ctx := context.Background()
	cfg, _ := setupTestRelease(t, "myapp", "default")
	client := fake.NewClientset()

	err := SetTTL(ctx, cfg, client, SetTTLOptions{
		ReleaseName:          "myapp",
		ReleaseNamespace:     "default",
		CronjobNamespace:     "default",
		Duration:             "24h",
		ServiceAccount:       "custom-sa",
		CreateServiceAccount: true,
	})
	require.NoError(t, err)

	// Verify the custom SA name was used (not the default resource name)
	cj, err := client.BatchV1().CronJobs("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "custom-sa", cj.Spec.JobTemplate.Spec.Template.Spec.ServiceAccountName)
}

func TestSetTTL_ResourceNameTooLong(t *testing.T) {
	ctx := context.Background()
	cfg, _ := setupTestRelease(t, "a-very-long-release-name-that-will-exceed", "default")
	client := fake.NewClientset()

	err := SetTTL(ctx, cfg, client, SetTTLOptions{
		ReleaseName:          "a-very-long-release-name-that-will-exceed",
		ReleaseNamespace:     "a-long-namespace",
		CronjobNamespace:     "default",
		Duration:             "1h",
		ServiceAccount:       "default",
		CreateServiceAccount: true,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum length")
}

func TestSetTTL_CreateServiceAccountError(t *testing.T) {
	ctx := context.Background()
	cfg, _ := setupTestRelease(t, "myapp", "default")
	client := fake.NewClientset()
	client.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated SA error")
	})

	err := SetTTL(ctx, cfg, client, SetTTLOptions{
		ReleaseName:          "myapp",
		ReleaseNamespace:     "default",
		CronjobNamespace:     "default",
		Duration:             "1h",
		ServiceAccount:       "default",
		CreateServiceAccount: true,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create service account and RBAC")
}

func TestSetTTL_SACheckError(t *testing.T) {
	ctx := context.Background()
	cfg, _ := setupTestRelease(t, "myapp", "default")
	client := fake.NewClientset()
	client.PrependReactor("get", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated API error")
	})

	err := SetTTL(ctx, cfg, client, SetTTLOptions{
		ReleaseName:      "myapp",
		ReleaseNamespace: "default",
		CronjobNamespace: "default",
		Duration:         "1h",
		ServiceAccount:   "my-sa",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check service account")
}

func TestSetTTL_CreateCronJobError(t *testing.T) {
	ctx := context.Background()
	cfg, _ := setupTestRelease(t, "myapp", "default")
	client := fake.NewClientset()
	client.PrependReactor("create", "cronjobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated CronJob create error")
	})

	err := SetTTL(ctx, cfg, client, SetTTLOptions{
		ReleaseName:          "myapp",
		ReleaseNamespace:     "default",
		CronjobNamespace:     "default",
		Duration:             "1h",
		ServiceAccount:       "default",
		CreateServiceAccount: true,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create CronJob")
}

func TestSetTTL_GetCronJobError(t *testing.T) {
	ctx := context.Background()
	cfg, _ := setupTestRelease(t, "myapp", "default")
	client := fake.NewClientset()
	client.PrependReactor("get", "cronjobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated API error")
	})

	err := SetTTL(ctx, cfg, client, SetTTLOptions{
		ReleaseName:          "myapp",
		ReleaseNamespace:     "default",
		CronjobNamespace:     "default",
		Duration:             "1h",
		ServiceAccount:       "default",
		CreateServiceAccount: true,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check existing CronJob")
}

func TestSetTTL_UpdateCronJobError(t *testing.T) {
	ctx := context.Background()
	cfg, _ := setupTestRelease(t, "myapp", "default")
	client := fake.NewClientset(&batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp-default-ttl",
			Namespace: "default",
		},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 0 1 1 *",
		},
	})
	client.PrependReactor("update", "cronjobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated update error")
	})

	err := SetTTL(ctx, cfg, client, SetTTLOptions{
		ReleaseName:          "myapp",
		ReleaseNamespace:     "default",
		CronjobNamespace:     "default",
		Duration:             "1h",
		ServiceAccount:       "default",
		CreateServiceAccount: true,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update CronJob")
}

func TestGetTTL_APIError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("get", "cronjobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated API error")
	})

	_, err := GetTTL(ctx, client, "myapp", "default", "default")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get CronJob")
}

func TestUnsetTTL_APIError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("delete", "cronjobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated API error")
	})

	err := UnsetTTL(ctx, client, "myapp", "default", "default")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete CronJob")
}

// testLogFetcher returns a LogFetcher that returns canned log output.
func testLogFetcher(logs string) LogFetcher {
	return func(_ context.Context, _, _, _ string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(logs)), nil
	}
}

// buildTestCronJob creates a CronJob with a proper job template for testing RunTTL.
func buildTestCronJob(t *testing.T, releaseName, releaseNamespace, cronjobNamespace string, deleteNamespace bool) *batchv1.CronJob {
	t.Helper()
	cj, err := BuildCronJob(CronJobOptions{
		ReleaseName:      releaseName,
		ReleaseNamespace: releaseNamespace,
		CronjobNamespace: cronjobNamespace,
		Schedule:         "30 14 15 3 *",
		ServiceAccount:   "default",
		HelmImage:        "alpine/helm:3.14",
		KubectlImage:     "alpine/k8s:1.29",
		DeleteNamespace:  deleteNamespace,
	})
	require.NoError(t, err)
	return cj
}

// buildCompletedPod creates a Pod that looks like a completed Job pod.
func buildCompletedPod(namespace, jobName string, initContainerNames []string, containerNames []string, exitCodes map[string]int32) *corev1.Pod {
	var initStatuses []corev1.ContainerStatus
	for _, name := range initContainerNames {
		code := exitCodes[name]
		initStatuses = append(initStatuses, corev1.ContainerStatus{
			Name: name,
			State: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{
					ExitCode: code,
				},
			},
		})
	}

	var containerStatuses []corev1.ContainerStatus
	for _, name := range containerNames {
		code := exitCodes[name]
		containerStatuses = append(containerStatuses, corev1.ContainerStatus{
			Name: name,
			State: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{
					ExitCode: code,
				},
			},
		})
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName + "-pod",
			Namespace: namespace,
			Labels:    map[string]string{"job-name": jobName},
		},
		Status: corev1.PodStatus{
			InitContainerStatuses: initStatuses,
			ContainerStatuses:     containerStatuses,
		},
	}
}

func TestRunTTL(t *testing.T) {
	ctx := context.Background()

	t.Run("happy path same namespace", func(t *testing.T) {
		cj := buildTestCronJob(t, "myapp", "default", "default", false)
		pod := buildCompletedPod("default", "myapp-default-ttl-run",
			[]string{"helm-uninstall"}, []string{"self-cleanup"},
			map[string]int32{"helm-uninstall": 0, "self-cleanup": 0})

		client := fake.NewClientset(cj, pod)
		var buf bytes.Buffer

		result, err := RunTTL(ctx, client, &buf, testLogFetcher("ok\n"), "myapp", "default", "default")
		require.NoError(t, err)
		assert.Equal(t, "myapp", result.ReleaseName)
		assert.Equal(t, "default", result.ReleaseNamespace)
		assert.False(t, result.DeletedNamespace)
		assert.False(t, result.JobFailed)
		assert.Len(t, result.ContainerResults, 2)
		assert.Equal(t, int32(0), result.ContainerResults[0].ExitCode)
		assert.Equal(t, int32(0), result.ContainerResults[1].ExitCode)

		// Verify CronJob is gone
		_, err = client.BatchV1().CronJobs("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		assert.Error(t, err)

		// Verify logs were streamed
		assert.Contains(t, buf.String(), "==> Container: helm-uninstall <==")
		assert.Contains(t, buf.String(), "==> Container: self-cleanup <==")
	})

	t.Run("container failure", func(t *testing.T) {
		cj := buildTestCronJob(t, "myapp", "default", "default", false)
		pod := buildCompletedPod("default", "myapp-default-ttl-run",
			[]string{"helm-uninstall"}, []string{"self-cleanup"},
			map[string]int32{"helm-uninstall": 1, "self-cleanup": 0})

		client := fake.NewClientset(cj, pod)
		var buf bytes.Buffer

		result, err := RunTTL(ctx, client, &buf, testLogFetcher("error\n"), "myapp", "default", "default")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "job failed")
		require.NotNil(t, result)
		assert.True(t, result.JobFailed)
		assert.Equal(t, int32(1), result.ContainerResults[0].ExitCode)

		// CronJob should still be cleaned up even on failure
		_, err = client.BatchV1().CronJobs("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		assert.Error(t, err)
	})

	t.Run("TTL not found", func(t *testing.T) {
		client := fake.NewClientset()
		var buf bytes.Buffer

		_, err := RunTTL(ctx, client, &buf, testLogFetcher(""), "myapp", "default", "default")
		var notFound *TTLNotFoundError
		assert.True(t, errors.As(err, &notFound))
	})

	t.Run("Job creation failure", func(t *testing.T) {
		cj := buildTestCronJob(t, "myapp", "default", "default", false)
		client := fake.NewClientset(cj)
		client.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("simulated Job create error")
		})

		var buf bytes.Buffer
		_, err := RunTTL(ctx, client, &buf, testLogFetcher(""), "myapp", "default", "default")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create Job")
	})

	t.Run("cross-namespace with delete-namespace", func(t *testing.T) {
		cj := buildTestCronJob(t, "myapp", "staging", "ops", true)
		pod := buildCompletedPod("ops", "myapp-staging-ttl-run",
			[]string{"helm-uninstall", "delete-namespace"}, []string{"self-cleanup"},
			map[string]int32{"helm-uninstall": 0, "delete-namespace": 0, "self-cleanup": 0})
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "staging"},
		}

		client := fake.NewClientset(cj, pod, ns)
		var buf bytes.Buffer

		result, err := RunTTL(ctx, client, &buf, testLogFetcher("ok\n"), "myapp", "staging", "ops")
		require.NoError(t, err)
		assert.True(t, result.DeletedNamespace)
		assert.Len(t, result.ContainerResults, 3)

		// Verify CronJob is gone
		_, err = client.BatchV1().CronJobs("ops").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
		assert.Error(t, err)

		// Verify namespace is gone
		_, err = client.CoreV1().Namespaces().Get(ctx, "staging", metav1.GetOptions{})
		assert.Error(t, err)
	})

	t.Run("resource name too long", func(t *testing.T) {
		client := fake.NewClientset()
		var buf bytes.Buffer

		_, err := RunTTL(ctx, client, &buf, testLogFetcher(""), "a-very-long-release-name-that-will-exceed", "a-long-namespace", "default")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds maximum length")
	})

	t.Run("CronJob get API error", func(t *testing.T) {
		client := fake.NewClientset()
		client.PrependReactor("get", "cronjobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("simulated API error")
		})

		var buf bytes.Buffer
		_, err := RunTTL(ctx, client, &buf, testLogFetcher(""), "myapp", "default", "default")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get CronJob")
	})

	t.Run("CronJob delete API error", func(t *testing.T) {
		cj := buildTestCronJob(t, "myapp", "default", "default", false)
		pod := buildCompletedPod("default", "myapp-default-ttl-run",
			[]string{"helm-uninstall"}, []string{"self-cleanup"},
			map[string]int32{"helm-uninstall": 0, "self-cleanup": 0})

		client := fake.NewClientset(cj, pod)
		client.PrependReactor("delete", "cronjobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("simulated delete error")
		})

		var buf bytes.Buffer
		_, err := RunTTL(ctx, client, &buf, testLogFetcher("ok\n"), "myapp", "default", "default")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete CronJob")
	})

	t.Run("pod timeout still cleans up", func(t *testing.T) {
		cj := buildTestCronJob(t, "myapp", "default", "default", false)
		// No pod - will timeout
		client := fake.NewClientset(cj)
		var buf bytes.Buffer

		shortCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()

		result, err := RunTTL(shortCtx, client, &buf, testLogFetcher(""), "myapp", "default", "default")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "timed out waiting for pod")
		require.NotNil(t, result)

		// CronJob should still be cleaned up
		_, err = client.BatchV1().CronJobs("default").Get(context.Background(), "myapp-default-ttl", metav1.GetOptions{})
		assert.Error(t, err)
	})
}
