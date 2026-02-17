package ttl

import (
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// LabelManagedBy is the standard Kubernetes label for resource management.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	// LabelManagedByValue is the value used for resources created by this plugin.
	LabelManagedByValue = "helm-ttl"
	// LabelRelease is the label used to identify the Helm release.
	LabelRelease = "helm-ttl/release"
	// LabelReleaseNamespace is the label for the release namespace.
	LabelReleaseNamespace = "helm-ttl/release-namespace"
	// LabelCronjobNamespace is the label for the CronJob namespace.
	LabelCronjobNamespace = "helm-ttl/cronjob-namespace"
	// LabelDeleteNamespace indicates whether the namespace should be deleted.
	LabelDeleteNamespace = "helm-ttl/delete-namespace"

	// maxResourceNameLen is the max length for CronJob names.
	// CronJob creates Jobs with a suffix, and Jobs create Pods with a suffix.
	// CronJob name + "-" + 10-char timestamp = Job name (max 63 chars)
	// We limit CronJob names to 52 chars to be safe.
	maxResourceNameLen = 52

	// DefaultHelmImage is the default Helm container image.
	DefaultHelmImage = "alpine/helm:latest"
	// DefaultKubectlImage is the default kubectl container image.
	DefaultKubectlImage = "alpine/k8s:latest"
)

// ResourceName returns the standard resource name for a release TTL.
// Format: <release>-<releaseNamespace>-ttl
func ResourceName(releaseName, releaseNamespace string) (string, error) {
	name := fmt.Sprintf("%s-%s-ttl", releaseName, releaseNamespace)
	if len(name) > maxResourceNameLen {
		return "", fmt.Errorf("resource name %q exceeds maximum length of %d characters (got %d); use shorter release or namespace names", name, maxResourceNameLen, len(name))
	}

	return name, nil
}

// CronJobOptions contains the parameters for building a CronJob.
type CronJobOptions struct {
	ReleaseName      string
	ReleaseNamespace string
	CronjobNamespace string
	Schedule         string
	ServiceAccount   string
	HelmImage        string
	KubectlImage     string
	DeleteNamespace  bool
}

// BuildCronJob constructs a Kubernetes CronJob that will uninstall a Helm release
// and optionally delete the namespace, then clean up itself.
func BuildCronJob(opts CronJobOptions) (*batchv1.CronJob, error) {
	if opts.DeleteNamespace && opts.ReleaseNamespace == opts.CronjobNamespace {
		return nil, fmt.Errorf("cannot use --delete-namespace when CronJob namespace (%s) equals release namespace (%s); the CronJob would delete its own namespace", opts.CronjobNamespace, opts.ReleaseNamespace)
	}

	name, err := ResourceName(opts.ReleaseName, opts.ReleaseNamespace)
	if err != nil {
		return nil, err
	}

	if opts.HelmImage == "" {
		opts.HelmImage = DefaultHelmImage
	}

	if opts.KubectlImage == "" {
		opts.KubectlImage = DefaultKubectlImage
	}

	deleteNsStr := "false"
	if opts.DeleteNamespace {
		deleteNsStr = "true"
	}

	labels := map[string]string{
		LabelManagedBy:        LabelManagedByValue,
		LabelRelease:          opts.ReleaseName,
		LabelReleaseNamespace: opts.ReleaseNamespace,
		LabelCronjobNamespace: opts.CronjobNamespace,
		LabelDeleteNamespace:  deleteNsStr,
	}

	// Init container 1: helm uninstall
	helmUninstall := corev1.Container{
		Name:    "helm-uninstall",
		Image:   opts.HelmImage,
		Command: []string{"helm", "uninstall", opts.ReleaseName, "--namespace", opts.ReleaseNamespace},
	}

	initContainers := []corev1.Container{helmUninstall}

	// Init container 2 (conditional): kubectl delete namespace
	if opts.DeleteNamespace {
		deleteNs := corev1.Container{
			Name:    "delete-namespace",
			Image:   opts.KubectlImage,
			Command: []string{"kubectl", "delete", "namespace", opts.ReleaseNamespace},
		}
		initContainers = append(initContainers, deleteNs)
	}

	// Main container: self-cleanup (delete the CronJob itself)
	selfCleanup := corev1.Container{
		Name:    "self-cleanup",
		Image:   opts.KubectlImage,
		Command: []string{"kubectl", "delete", "cronjob", name, "--namespace", opts.CronjobNamespace},
	}

	var failedLimit int32
	var successLimit int32 = 1
	var backoffLimit int32

	cronjob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: opts.CronjobNamespace,
			Labels:    labels,
		},
		Spec: batchv1.CronJobSpec{
			Schedule:                   opts.Schedule,
			ConcurrencyPolicy:          batchv1.ForbidConcurrent,
			FailedJobsHistoryLimit:     &failedLimit,
			SuccessfulJobsHistoryLimit: &successLimit,
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: batchv1.JobSpec{
					BackoffLimit: &backoffLimit,
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: labels,
						},
						Spec: corev1.PodSpec{
							ServiceAccountName: opts.ServiceAccount,
							RestartPolicy:      corev1.RestartPolicyNever,
							InitContainers:     initContainers,
							Containers:         []corev1.Container{selfCleanup},
						},
					},
				},
			},
		},
	}

	return cronjob, nil
}
