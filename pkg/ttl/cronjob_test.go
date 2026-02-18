package ttl

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestParseImageFromDockerfile(t *testing.T) {
	t.Run("normal FROM line", func(t *testing.T) {
		got := parseImageFromDockerfile("FROM alpine/helm:3.14\n")
		assert.Equal(t, "alpine/helm:3.14", got)
	})

	t.Run("FROM with extra whitespace", func(t *testing.T) {
		got := parseImageFromDockerfile("  FROM   alpine/k8s:1.29  \n")
		assert.Equal(t, "alpine/k8s:1.29", got)
	})

	t.Run("empty input", func(t *testing.T) {
		got := parseImageFromDockerfile("")
		assert.Equal(t, "", got)
	})

	t.Run("lowercase from", func(t *testing.T) {
		got := parseImageFromDockerfile("from nginx:latest\n")
		assert.Equal(t, "nginx:latest", got)
	})

	t.Run("no FROM line", func(t *testing.T) {
		got := parseImageFromDockerfile("RUN echo hello\n")
		assert.Equal(t, "", got)
	})
}

func TestEmbeddedDefaults(t *testing.T) {
	t.Run("DefaultHelmImage is set", func(t *testing.T) {
		assert.NotEmpty(t, DefaultHelmImage)
		assert.Contains(t, DefaultHelmImage, "alpine/helm")
	})

	t.Run("DefaultKubectlImage is set", func(t *testing.T) {
		assert.NotEmpty(t, DefaultKubectlImage)
		assert.Contains(t, DefaultKubectlImage, "alpine/k8s")
	})
}

func TestResourceName(t *testing.T) {
	t.Run("basic name", func(t *testing.T) {
		name, err := ResourceName("myapp", "staging")
		require.NoError(t, err)
		assert.Equal(t, "myapp-staging-ttl", name)
	})

	t.Run("name at limit", func(t *testing.T) {
		// Create a name that's exactly at the limit
		release := strings.Repeat("a", 22)
		ns := strings.Repeat("b", 22)
		name, err := ResourceName(release, ns)
		require.NoError(t, err)
		assert.Len(t, name, 49) // 22 + 1 + 22 + 4 = 49
		assert.True(t, len(name) <= maxResourceNameLen)
	})

	t.Run("name exceeds limit", func(t *testing.T) {
		release := strings.Repeat("a", 30)
		ns := strings.Repeat("b", 30)
		_, err := ResourceName(release, ns)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds maximum length")
	})
}

func TestBuildCronJob(t *testing.T) {
	t.Run("basic CronJob - same namespace", func(t *testing.T) {
		opts := CronJobOptions{
			ReleaseName:      "myapp",
			ReleaseNamespace: "default",
			CronjobNamespace: "default",
			Schedule:         "30 14 15 6 *",
			ServiceAccount:   "default",
			HelmImage:        "alpine/helm:3.14",
			KubectlImage:     "alpine/k8s:1.29",
		}

		cj, err := BuildCronJob(opts)
		require.NoError(t, err)

		assert.Equal(t, "myapp-default-ttl", cj.Name)
		assert.Equal(t, "default", cj.Namespace)
		assert.Equal(t, "30 14 15 6 *", cj.Spec.Schedule)

		// Check labels
		assert.Equal(t, LabelManagedByValue, cj.Labels[LabelManagedBy])
		assert.Equal(t, "myapp", cj.Labels[LabelRelease])
		assert.Equal(t, "default", cj.Labels[LabelReleaseNamespace])
		assert.Equal(t, "default", cj.Labels[LabelCronjobNamespace])
		assert.Equal(t, "false", cj.Labels[LabelDeleteNamespace])

		// Check init containers
		spec := cj.Spec.JobTemplate.Spec.Template.Spec
		assert.Len(t, spec.InitContainers, 1)
		assert.Equal(t, "helm-uninstall", spec.InitContainers[0].Name)
		assert.Equal(t, []string{"helm", "uninstall", "myapp", "--namespace", "default"}, spec.InitContainers[0].Command)

		// Check main container
		assert.Len(t, spec.Containers, 1)
		assert.Equal(t, "self-cleanup", spec.Containers[0].Name)
		assert.Equal(t, []string{"kubectl", "delete", "cronjob", "myapp-default-ttl", "--namespace", "default"}, spec.Containers[0].Command)

		// Check service account
		assert.Equal(t, "default", spec.ServiceAccountName)
		assert.Equal(t, corev1.RestartPolicyNever, spec.RestartPolicy)
	})

	t.Run("cross-namespace CronJob", func(t *testing.T) {
		opts := CronJobOptions{
			ReleaseName:      "myapp",
			ReleaseNamespace: "staging",
			CronjobNamespace: "ops",
			Schedule:         "0 12 1 1 *",
			ServiceAccount:   "ttl-sa",
		}

		cj, err := BuildCronJob(opts)
		require.NoError(t, err)

		assert.Equal(t, "myapp-staging-ttl", cj.Name)
		assert.Equal(t, "ops", cj.Namespace)
		assert.Equal(t, "staging", cj.Labels[LabelReleaseNamespace])
		assert.Equal(t, "ops", cj.Labels[LabelCronjobNamespace])
	})

	t.Run("with delete-namespace", func(t *testing.T) {
		opts := CronJobOptions{
			ReleaseName:      "myapp",
			ReleaseNamespace: "staging",
			CronjobNamespace: "ops",
			Schedule:         "0 12 1 1 *",
			ServiceAccount:   "ttl-sa",
			DeleteNamespace:  true,
		}

		cj, err := BuildCronJob(opts)
		require.NoError(t, err)

		assert.Equal(t, "true", cj.Labels[LabelDeleteNamespace])

		spec := cj.Spec.JobTemplate.Spec.Template.Spec
		assert.Len(t, spec.InitContainers, 2)
		assert.Equal(t, "helm-uninstall", spec.InitContainers[0].Name)
		assert.Equal(t, "delete-namespace", spec.InitContainers[1].Name)
		assert.Equal(t, []string{"kubectl", "delete", "namespace", "staging"}, spec.InitContainers[1].Command)
	})

	t.Run("delete-namespace rejected when same namespace", func(t *testing.T) {
		opts := CronJobOptions{
			ReleaseName:      "myapp",
			ReleaseNamespace: "default",
			CronjobNamespace: "default",
			Schedule:         "0 12 1 1 *",
			ServiceAccount:   "default",
			DeleteNamespace:  true,
		}

		_, err := BuildCronJob(opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot use --delete-namespace")
	})

	t.Run("default images used when empty", func(t *testing.T) {
		opts := CronJobOptions{
			ReleaseName:      "myapp",
			ReleaseNamespace: "default",
			CronjobNamespace: "default",
			Schedule:         "0 12 1 1 *",
			ServiceAccount:   "default",
		}

		cj, err := BuildCronJob(opts)
		require.NoError(t, err)

		spec := cj.Spec.JobTemplate.Spec.Template.Spec
		assert.Equal(t, DefaultHelmImage, spec.InitContainers[0].Image)
		assert.Equal(t, DefaultKubectlImage, spec.Containers[0].Image)
	})

	t.Run("name too long", func(t *testing.T) {
		opts := CronJobOptions{
			ReleaseName:      strings.Repeat("a", 30),
			ReleaseNamespace: strings.Repeat("b", 30),
			CronjobNamespace: "default",
			Schedule:         "0 12 1 1 *",
			ServiceAccount:   "default",
		}

		_, err := BuildCronJob(opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds maximum length")
	})

	t.Run("labels propagated to pod template", func(t *testing.T) {
		opts := CronJobOptions{
			ReleaseName:      "myapp",
			ReleaseNamespace: "staging",
			CronjobNamespace: "ops",
			Schedule:         "0 12 1 1 *",
			ServiceAccount:   "ttl-sa",
		}

		cj, err := BuildCronJob(opts)
		require.NoError(t, err)

		podLabels := cj.Spec.JobTemplate.Spec.Template.Labels
		assert.Equal(t, LabelManagedByValue, podLabels[LabelManagedBy])
		assert.Equal(t, "myapp", podLabels[LabelRelease])
	})

	t.Run("history limits and backoff", func(t *testing.T) {
		opts := CronJobOptions{
			ReleaseName:      "myapp",
			ReleaseNamespace: "default",
			CronjobNamespace: "default",
			Schedule:         "0 12 1 1 *",
			ServiceAccount:   "default",
		}

		cj, err := BuildCronJob(opts)
		require.NoError(t, err)

		assert.Equal(t, int32(0), *cj.Spec.FailedJobsHistoryLimit)
		assert.Equal(t, int32(1), *cj.Spec.SuccessfulJobsHistoryLimit)
		assert.Equal(t, int32(0), *cj.Spec.JobTemplate.Spec.BackoffLimit)
	})
}
