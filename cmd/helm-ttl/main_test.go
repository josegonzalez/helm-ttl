package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/josegonzalez/helm-ttl/pkg/ttl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	helmrelease "helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
)

func setupTestStore(t *testing.T, releaseName, namespace string) *storage.Storage {
	t.Helper()

	mem := driver.NewMemory()
	store := storage.Init(mem)

	rel := &helmrelease.Release{
		Name:      releaseName,
		Namespace: namespace,
		Version:   1,
		Info: &helmrelease.Info{
			Status: helmrelease.StatusDeployed,
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

	return store
}

func testConfigFactory(store *storage.Storage) configFactory {
	return func(_ string) (*action.Configuration, error) {
		return &action.Configuration{
			Releases:   store,
			KubeClient: &kubefake.PrintingKubeClient{Out: io.Discard},
			Log:        func(format string, v ...interface{}) {},
		}, nil
	}
}

func testKubeFactoryWithClient(client kubernetes.Interface) kubeClientFactory {
	return func() (kubernetes.Interface, error) {
		return client, nil
	}
}

func errorConfigFactory() configFactory {
	return func(_ string) (*action.Configuration, error) {
		return nil, errors.New("config error")
	}
}

func errorKubeFactory() kubeClientFactory {
	return func() (kubernetes.Interface, error) {
		return nil, errors.New("kube error")
	}
}

func TestNewRootCmd(t *testing.T) {
	cmd := newRootCmd(defaultConfigFactory, defaultKubeClientFactory)
	assert.Equal(t, "helm-ttl", cmd.Use)
	assert.Equal(t, version, cmd.Version)

	// Should have 5 subcommands
	assert.Len(t, cmd.Commands(), 5)

	names := make([]string, 0, len(cmd.Commands()))
	for _, c := range cmd.Commands() {
		names = append(names, c.Name())
	}
	assert.Contains(t, names, "set")
	assert.Contains(t, names, "get")
	assert.Contains(t, names, "unset")
	assert.Contains(t, names, "run")
	assert.Contains(t, names, "cleanup-rbac")

	// Should have --release-namespace persistent flag
	f := cmd.PersistentFlags().Lookup("release-namespace")
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestSetCmd(t *testing.T) {
	origNs := os.Getenv("HELM_NAMESPACE")
	defer func() { _ = os.Setenv("HELM_NAMESPACE", origNs) }()
	_ = os.Setenv("HELM_NAMESPACE", "default")

	t.Run("set TTL with create-service-account", func(t *testing.T) {
		store := setupTestStore(t, "myapp", "default")
		client := fake.NewClientset()

		cmd := newRootCmd(testConfigFactory(store), testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"set", "myapp", "24h", "--create-service-account"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "TTL set")
		assert.Contains(t, buf.String(), "myapp")

		// Verify CronJob was created
		ctx := context.Background()
		cj, err := client.BatchV1().CronJobs("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, "myapp-default-ttl", cj.Name)
	})

	t.Run("set TTL with existing service account", func(t *testing.T) {
		store := setupTestStore(t, "myapp", "default")
		client := fake.NewClientset(&corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "my-sa", Namespace: "default"},
		})

		cmd := newRootCmd(testConfigFactory(store), testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"set", "myapp", "2h", "--service-account", "my-sa"})

		err := cmd.Execute()
		require.NoError(t, err)
	})

	t.Run("release not found", func(t *testing.T) {
		mem := driver.NewMemory()
		store := storage.Init(mem)
		client := fake.NewClientset()

		cmd := newRootCmd(testConfigFactory(store), testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"set", "nonexistent", "1h", "--create-service-account"})

		err := cmd.Execute()
		assert.Error(t, err)
	})

	t.Run("service account not found", func(t *testing.T) {
		store := setupTestStore(t, "myapp", "default")
		client := fake.NewClientset()

		cmd := newRootCmd(testConfigFactory(store), testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"set", "myapp", "1h", "--service-account", "nonexistent"})

		err := cmd.Execute()
		assert.Error(t, err)
	})

	t.Run("config error", func(t *testing.T) {
		client := fake.NewClientset()

		cmd := newRootCmd(errorConfigFactory(), testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"set", "myapp", "1h"})

		err := cmd.Execute()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "configuration")
	})

	t.Run("kube client error", func(t *testing.T) {
		store := setupTestStore(t, "myapp", "default")

		cmd := newRootCmd(testConfigFactory(store), errorKubeFactory())
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"set", "myapp", "1h"})

		err := cmd.Execute()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "kubernetes client")
	})

	t.Run("too few args", func(t *testing.T) {
		cmd := newRootCmd(defaultConfigFactory, defaultKubeClientFactory)
		cmd.SetArgs([]string{"set", "myapp"})
		err := cmd.Execute()
		assert.Error(t, err)
	})

	t.Run("delete-namespace flag", func(t *testing.T) {
		_ = os.Setenv("HELM_NAMESPACE", "staging")
		defer func() { _ = os.Setenv("HELM_NAMESPACE", "default") }()

		store := setupTestStore(t, "myapp", "staging")
		client := fake.NewClientset()

		cmd := newRootCmd(testConfigFactory(store), testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"set", "myapp", "7d", "--create-service-account", "--cronjob-namespace", "ops", "--delete-namespace"})

		err := cmd.Execute()
		require.NoError(t, err)

		ctx := context.Background()
		cj, err := client.BatchV1().CronJobs("ops").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, "true", cj.Labels[ttl.LabelDeleteNamespace])
	})

	t.Run("custom images", func(t *testing.T) {
		store := setupTestStore(t, "myapp", "default")
		client := fake.NewClientset()

		cmd := newRootCmd(testConfigFactory(store), testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"set", "myapp", "1h", "--create-service-account", "--helm-image", "custom/helm:v3", "--kubectl-image", "custom/kubectl:v1"})

		err := cmd.Execute()
		require.NoError(t, err)

		ctx := context.Background()
		cj, err := client.BatchV1().CronJobs("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, "custom/helm:v3", cj.Spec.JobTemplate.Spec.Template.Spec.InitContainers[0].Image)
		assert.Equal(t, "custom/kubectl:v1", cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Image)
	})

	t.Run("release-namespace flag overrides env", func(t *testing.T) {
		store := setupTestStore(t, "myapp", "staging")
		client := fake.NewClientset()

		cmd := newRootCmd(testConfigFactory(store), testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"set", "myapp", "24h", "--create-service-account", "--release-namespace", "staging"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "staging")

		ctx := context.Background()
		cj, err := client.BatchV1().CronJobs("staging").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, "myapp-staging-ttl", cj.Name)
	})
}

func TestGetCmd(t *testing.T) {
	origNs := os.Getenv("HELM_NAMESPACE")
	defer func() { _ = os.Setenv("HELM_NAMESPACE", origNs) }()
	_ = os.Setenv("HELM_NAMESPACE", "default")

	t.Run("get TTL - text output", func(t *testing.T) {
		client := fake.NewClientset(&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp-default-ttl",
				Namespace: "default",
				Labels: map[string]string{
					ttl.LabelManagedBy:        ttl.LabelManagedByValue,
					ttl.LabelRelease:          "myapp",
					ttl.LabelReleaseNamespace: "default",
					ttl.LabelCronjobNamespace: "default",
					ttl.LabelDeleteNamespace:  "false",
				},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "30 14 15 3 *",
			},
		})

		cmd := newRootCmd(defaultConfigFactory, testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"get", "myapp"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "myapp")
		assert.Contains(t, buf.String(), "30 14 15 3 *")
	})

	t.Run("get TTL - json output", func(t *testing.T) {
		client := fake.NewClientset(&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp-default-ttl",
				Namespace: "default",
				Labels: map[string]string{
					ttl.LabelManagedBy:        ttl.LabelManagedByValue,
					ttl.LabelRelease:          "myapp",
					ttl.LabelReleaseNamespace: "default",
					ttl.LabelCronjobNamespace: "default",
					ttl.LabelDeleteNamespace:  "false",
				},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "30 14 15 3 *",
			},
		})

		cmd := newRootCmd(defaultConfigFactory, testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"get", "myapp", "-o", "json"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), `"release_name": "myapp"`)
	})

	t.Run("get TTL - yaml output", func(t *testing.T) {
		client := fake.NewClientset(&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp-default-ttl",
				Namespace: "default",
				Labels: map[string]string{
					ttl.LabelManagedBy:        ttl.LabelManagedByValue,
					ttl.LabelRelease:          "myapp",
					ttl.LabelReleaseNamespace: "default",
					ttl.LabelCronjobNamespace: "default",
					ttl.LabelDeleteNamespace:  "false",
				},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "30 14 15 3 *",
			},
		})

		cmd := newRootCmd(defaultConfigFactory, testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"get", "myapp", "-o", "yaml"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "release_name: myapp")
	})

	t.Run("TTL not found", func(t *testing.T) {
		client := fake.NewClientset()

		cmd := newRootCmd(defaultConfigFactory, testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"get", "myapp"})

		err := cmd.Execute()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no TTL set")
	})

	t.Run("kube client error", func(t *testing.T) {
		cmd := newRootCmd(defaultConfigFactory, errorKubeFactory())
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"get", "myapp"})

		err := cmd.Execute()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "kubernetes client")
	})

	t.Run("cross-namespace get", func(t *testing.T) {
		_ = os.Setenv("HELM_NAMESPACE", "staging")
		defer func() { _ = os.Setenv("HELM_NAMESPACE", "default") }()

		client := fake.NewClientset(&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp-staging-ttl",
				Namespace: "ops",
				Labels: map[string]string{
					ttl.LabelManagedBy:        ttl.LabelManagedByValue,
					ttl.LabelRelease:          "myapp",
					ttl.LabelReleaseNamespace: "staging",
					ttl.LabelCronjobNamespace: "ops",
					ttl.LabelDeleteNamespace:  "true",
				},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "0 12 1 1 *",
			},
		})

		cmd := newRootCmd(defaultConfigFactory, testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"get", "myapp", "--cronjob-namespace", "ops"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "staging")
		assert.Contains(t, buf.String(), "ops")
	})

	t.Run("release-namespace flag overrides env", func(t *testing.T) {
		client := fake.NewClientset(&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp-staging-ttl",
				Namespace: "staging",
				Labels: map[string]string{
					ttl.LabelManagedBy:        ttl.LabelManagedByValue,
					ttl.LabelRelease:          "myapp",
					ttl.LabelReleaseNamespace: "staging",
					ttl.LabelCronjobNamespace: "staging",
					ttl.LabelDeleteNamespace:  "false",
				},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "30 14 15 3 *",
			},
		})

		cmd := newRootCmd(defaultConfigFactory, testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"get", "myapp", "--release-namespace", "staging"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "staging")
	})
}

func TestUnsetCmd(t *testing.T) {
	origNs := os.Getenv("HELM_NAMESPACE")
	defer func() { _ = os.Setenv("HELM_NAMESPACE", origNs) }()
	_ = os.Setenv("HELM_NAMESPACE", "default")

	t.Run("unset existing TTL", func(t *testing.T) {
		client := fake.NewClientset(&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp-default-ttl",
				Namespace: "default",
				Labels: map[string]string{
					ttl.LabelManagedBy: ttl.LabelManagedByValue,
					ttl.LabelRelease:   "myapp",
				},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "30 14 15 6 *",
			},
		})

		cmd := newRootCmd(defaultConfigFactory, testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"unset", "myapp"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "TTL removed")
	})

	t.Run("unset TTL not found", func(t *testing.T) {
		client := fake.NewClientset()

		cmd := newRootCmd(defaultConfigFactory, testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"unset", "myapp"})

		err := cmd.Execute()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no TTL set")
	})

	t.Run("kube client error", func(t *testing.T) {
		cmd := newRootCmd(defaultConfigFactory, errorKubeFactory())
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"unset", "myapp"})

		err := cmd.Execute()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "kubernetes client")
	})

	t.Run("release-namespace flag overrides env", func(t *testing.T) {
		client := fake.NewClientset(&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp-staging-ttl",
				Namespace: "staging",
				Labels: map[string]string{
					ttl.LabelManagedBy: ttl.LabelManagedByValue,
					ttl.LabelRelease:   "myapp",
				},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "30 14 15 6 *",
			},
		})

		cmd := newRootCmd(defaultConfigFactory, testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"unset", "myapp", "--release-namespace", "staging"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "TTL removed")
		assert.Contains(t, buf.String(), "staging")
	})
}

func TestCleanupRBACCmd(t *testing.T) {
	origNs := os.Getenv("HELM_NAMESPACE")
	defer func() { _ = os.Setenv("HELM_NAMESPACE", origNs) }()
	_ = os.Setenv("HELM_NAMESPACE", "default")

	t.Run("no orphans found", func(t *testing.T) {
		client := fake.NewClientset()

		cmd := newRootCmd(defaultConfigFactory, testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"cleanup-rbac"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "No orphaned resources found")
	})

	t.Run("finds and deletes orphans", func(t *testing.T) {
		labels := map[string]string{
			ttl.LabelManagedBy:        ttl.LabelManagedByValue,
			ttl.LabelRelease:          "myapp",
			ttl.LabelReleaseNamespace: "default",
			ttl.LabelCronjobNamespace: "default",
		}

		client := fake.NewClientset(
			&corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default", Labels: labels},
			},
		)

		cmd := newRootCmd(defaultConfigFactory, testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"cleanup-rbac"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "Deleted")
	})

	t.Run("dry run", func(t *testing.T) {
		labels := map[string]string{
			ttl.LabelManagedBy:        ttl.LabelManagedByValue,
			ttl.LabelRelease:          "myapp",
			ttl.LabelReleaseNamespace: "default",
			ttl.LabelCronjobNamespace: "default",
		}

		client := fake.NewClientset(
			&corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default", Labels: labels},
			},
		)

		cmd := newRootCmd(defaultConfigFactory, testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"cleanup-rbac", "--dry-run"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "Would delete")
	})

	t.Run("kube client error", func(t *testing.T) {
		cmd := newRootCmd(defaultConfigFactory, errorKubeFactory())
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"cleanup-rbac"})

		err := cmd.Execute()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "kubernetes client")
	})

	t.Run("rejects extra args", func(t *testing.T) {
		cmd := newRootCmd(defaultConfigFactory, defaultKubeClientFactory)
		cmd.SetArgs([]string{"cleanup-rbac", "extra"})
		err := cmd.Execute()
		assert.Error(t, err)
	})

	t.Run("release-namespace flag overrides env", func(t *testing.T) {
		labels := map[string]string{
			ttl.LabelManagedBy:        ttl.LabelManagedByValue,
			ttl.LabelRelease:          "myapp",
			ttl.LabelReleaseNamespace: "staging",
			ttl.LabelCronjobNamespace: "staging",
		}

		client := fake.NewClientset(
			&corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{Name: "myapp-staging-ttl", Namespace: "staging", Labels: labels},
			},
		)

		cmd := newRootCmd(defaultConfigFactory, testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"cleanup-rbac", "--release-namespace", "staging"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "Deleted")
	})
}

func TestRunCmd(t *testing.T) {
	origNs := os.Getenv("HELM_NAMESPACE")
	defer func() { _ = os.Setenv("HELM_NAMESPACE", origNs) }()
	_ = os.Setenv("HELM_NAMESPACE", "default")

	t.Run("run TTL happy path", func(t *testing.T) {
		store := setupTestStore(t, "myapp", "default")
		client := fake.NewClientset(&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp-default-ttl",
				Namespace: "default",
				Labels: map[string]string{
					ttl.LabelManagedBy:        ttl.LabelManagedByValue,
					ttl.LabelRelease:          "myapp",
					ttl.LabelReleaseNamespace: "default",
					ttl.LabelCronjobNamespace: "default",
					ttl.LabelDeleteNamespace:  "false",
				},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "30 14 15 3 *",
			},
		})

		cmd := newRootCmd(testConfigFactory(store), testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"run", "myapp"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "TTL executed")
		assert.Contains(t, buf.String(), "myapp")

		// Verify CronJob was deleted
		ctx := context.Background()
		_, err = client.BatchV1().CronJobs("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		assert.Error(t, err)
	})

	t.Run("TTL not found", func(t *testing.T) {
		store := setupTestStore(t, "myapp", "default")
		client := fake.NewClientset()

		cmd := newRootCmd(testConfigFactory(store), testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"run", "myapp"})

		err := cmd.Execute()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no TTL set")
	})

	t.Run("config error", func(t *testing.T) {
		client := fake.NewClientset()

		cmd := newRootCmd(errorConfigFactory(), testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"run", "myapp"})

		err := cmd.Execute()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "configuration")
	})

	t.Run("kube client error", func(t *testing.T) {
		store := setupTestStore(t, "myapp", "default")

		cmd := newRootCmd(testConfigFactory(store), errorKubeFactory())
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"run", "myapp"})

		err := cmd.Execute()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "kubernetes client")
	})

	t.Run("too few args", func(t *testing.T) {
		cmd := newRootCmd(defaultConfigFactory, defaultKubeClientFactory)
		cmd.SetArgs([]string{"run"})
		err := cmd.Execute()
		assert.Error(t, err)
	})

	t.Run("cross-namespace flag", func(t *testing.T) {
		_ = os.Setenv("HELM_NAMESPACE", "staging")
		defer func() { _ = os.Setenv("HELM_NAMESPACE", "default") }()

		store := setupTestStore(t, "myapp", "staging")
		client := fake.NewClientset(&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp-staging-ttl",
				Namespace: "ops",
				Labels: map[string]string{
					ttl.LabelManagedBy:        ttl.LabelManagedByValue,
					ttl.LabelRelease:          "myapp",
					ttl.LabelReleaseNamespace: "staging",
					ttl.LabelCronjobNamespace: "ops",
					ttl.LabelDeleteNamespace:  "false",
				},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "0 12 1 1 *",
			},
		})

		cmd := newRootCmd(testConfigFactory(store), testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"run", "myapp", "--cronjob-namespace", "ops"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "TTL executed")
	})

	t.Run("release already gone", func(t *testing.T) {
		mem := driver.NewMemory()
		store := storage.Init(mem)
		client := fake.NewClientset(&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp-default-ttl",
				Namespace: "default",
				Labels: map[string]string{
					ttl.LabelManagedBy:        ttl.LabelManagedByValue,
					ttl.LabelRelease:          "myapp",
					ttl.LabelReleaseNamespace: "default",
					ttl.LabelCronjobNamespace: "default",
					ttl.LabelDeleteNamespace:  "false",
				},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "30 14 15 3 *",
			},
		})

		cmd := newRootCmd(testConfigFactory(store), testKubeFactoryWithClient(client))
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"run", "myapp"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, stderr.String(), "Warning")
		assert.Contains(t, stdout.String(), "TTL executed")
	})

	t.Run("release-namespace flag overrides env", func(t *testing.T) {
		store := setupTestStore(t, "myapp", "staging")
		client := fake.NewClientset(&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp-staging-ttl",
				Namespace: "staging",
				Labels: map[string]string{
					ttl.LabelManagedBy:        ttl.LabelManagedByValue,
					ttl.LabelRelease:          "myapp",
					ttl.LabelReleaseNamespace: "staging",
					ttl.LabelCronjobNamespace: "staging",
					ttl.LabelDeleteNamespace:  "false",
				},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "30 14 15 3 *",
			},
		})

		cmd := newRootCmd(testConfigFactory(store), testKubeFactoryWithClient(client))
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"run", "myapp", "--release-namespace", "staging"})

		err := cmd.Execute()
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "TTL executed")
		assert.Contains(t, buf.String(), "staging")
	})
}

func TestGetReleaseNamespace(t *testing.T) {
	origNs := os.Getenv("HELM_NAMESPACE")
	defer func() { _ = os.Setenv("HELM_NAMESPACE", origNs) }()

	t.Run("with HELM_NAMESPACE set", func(t *testing.T) {
		_ = os.Setenv("HELM_NAMESPACE", "custom-ns")
		assert.Equal(t, "custom-ns", getReleaseNamespace(""))
	})

	t.Run("with HELM_NAMESPACE empty", func(t *testing.T) {
		_ = os.Setenv("HELM_NAMESPACE", "")
		assert.Equal(t, "default", getReleaseNamespace(""))
	})

	t.Run("with HELM_NAMESPACE unset", func(t *testing.T) {
		_ = os.Unsetenv("HELM_NAMESPACE")
		assert.Equal(t, "default", getReleaseNamespace(""))
	})

	t.Run("override takes precedence over HELM_NAMESPACE", func(t *testing.T) {
		_ = os.Setenv("HELM_NAMESPACE", "from-env")
		assert.Equal(t, "from-flag", getReleaseNamespace("from-flag"))
	})

	t.Run("override takes precedence over default", func(t *testing.T) {
		_ = os.Unsetenv("HELM_NAMESPACE")
		assert.Equal(t, "explicit", getReleaseNamespace("explicit"))
	})
}
