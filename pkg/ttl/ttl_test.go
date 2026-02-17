package ttl

import (
	"context"
	"errors"
	"fmt"
	"testing"

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

	cfg := &action.Configuration{Releases: store}
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

func TestReleaseExists(t *testing.T) {
	t.Run("release exists", func(t *testing.T) {
		cfg, _ := setupTestRelease(t, "myapp", "default")
		exists, err := releaseExists(cfg, "myapp")
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("release does not exist", func(t *testing.T) {
		mem := driver.NewMemory()
		store := storage.Init(mem)
		cfg := &action.Configuration{Releases: store}

		exists, err := releaseExists(cfg, "nonexistent")
		require.NoError(t, err)
		assert.False(t, exists)
	})
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
