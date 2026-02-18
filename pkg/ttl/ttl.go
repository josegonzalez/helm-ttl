package ttl

import (
	"context"
	"fmt"
	"io"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"helm.sh/helm/v3/pkg/action"
)

// ReleaseNotFoundError is returned when a Helm release does not exist.
type ReleaseNotFoundError struct {
	Name string
}

func (e *ReleaseNotFoundError) Error() string {
	return fmt.Sprintf("release %q not found", e.Name)
}

// TTLNotFoundError is returned when no TTL CronJob exists for a release.
type TTLNotFoundError struct {
	Name string
}

func (e *TTLNotFoundError) Error() string {
	return fmt.Sprintf("no TTL set for release %q", e.Name)
}

// ServiceAccountNotFoundError is returned when the specified service account does not exist.
type ServiceAccountNotFoundError struct {
	Name      string
	Namespace string
}

func (e *ServiceAccountNotFoundError) Error() string {
	return fmt.Sprintf("service account %q not found in namespace %q", e.Name, e.Namespace)
}

// SetTTLOptions contains the parameters for setting a TTL on a release.
type SetTTLOptions struct {
	ReleaseName          string
	ReleaseNamespace     string
	CronjobNamespace     string
	Duration             string
	ServiceAccount       string
	CreateServiceAccount bool
	HelmImage            string
	KubectlImage         string
	DeleteNamespace      bool
}

// SetTTL sets or updates the TTL for a Helm release.
func SetTTL(ctx context.Context, cfg *action.Configuration, client kubernetes.Interface, opts SetTTLOptions) error {
	// Validate release exists using storage directly
	_, err := cfg.Releases.Last(opts.ReleaseName)
	if err != nil {
		return &ReleaseNotFoundError{Name: opts.ReleaseName}
	}

	// Validate namespace separation if delete-namespace
	if opts.DeleteNamespace && opts.ReleaseNamespace == opts.CronjobNamespace {
		return fmt.Errorf("cannot use --delete-namespace when CronJob namespace (%s) equals release namespace (%s)", opts.CronjobNamespace, opts.ReleaseNamespace)
	}

	now := time.Now()
	targetTime, err := ParseTimeInput(opts.Duration, now)
	if err != nil {
		return fmt.Errorf("invalid duration: %w", err)
	}

	schedule := TimeToCronSchedule(targetTime)

	resourceName, err := ResourceName(opts.ReleaseName, opts.ReleaseNamespace)
	if err != nil {
		return err
	}

	// Determine service account name
	saName := opts.ServiceAccount
	if opts.CreateServiceAccount && saName == "default" {
		saName = resourceName
	}

	// Create SA + RBAC if requested
	if opts.CreateServiceAccount {
		if err := CreateServiceAccountAndRBAC(ctx, client, opts.ReleaseName, opts.ReleaseNamespace, opts.CronjobNamespace, saName, opts.DeleteNamespace); err != nil {
			return fmt.Errorf("failed to create service account and RBAC: %w", err)
		}
	} else {
		// Validate the service account exists
		_, err := client.CoreV1().ServiceAccounts(opts.CronjobNamespace).Get(ctx, saName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return &ServiceAccountNotFoundError{Name: saName, Namespace: opts.CronjobNamespace}
			}

			return fmt.Errorf("failed to check service account: %w", err)
		}
	}

	// Build CronJob
	cj, err := BuildCronJob(CronJobOptions{
		ReleaseName:      opts.ReleaseName,
		ReleaseNamespace: opts.ReleaseNamespace,
		CronjobNamespace: opts.CronjobNamespace,
		Schedule:         schedule,
		ServiceAccount:   saName,
		HelmImage:        opts.HelmImage,
		KubectlImage:     opts.KubectlImage,
		DeleteNamespace:  opts.DeleteNamespace,
	})
	if err != nil {
		return fmt.Errorf("failed to build CronJob: %w", err)
	}

	// Create or update CronJob
	existing, err := client.BatchV1().CronJobs(opts.CronjobNamespace).Get(ctx, resourceName, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to check existing CronJob: %w", err)
		}

		// Create new
		_, err = client.BatchV1().CronJobs(opts.CronjobNamespace).Create(ctx, cj, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create CronJob: %w", err)
		}
	} else {
		// Update existing
		existing.Spec = cj.Spec
		existing.Labels = cj.Labels
		_, err = client.BatchV1().CronJobs(opts.CronjobNamespace).Update(ctx, existing, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update CronJob: %w", err)
		}
	}

	return nil
}

// GetTTL retrieves the TTL information for a Helm release.
func GetTTL(ctx context.Context, client kubernetes.Interface, releaseName, releaseNamespace, cronjobNamespace string) (*TTLInfo, error) {
	resourceName, err := ResourceName(releaseName, releaseNamespace)
	if err != nil {
		return nil, err
	}

	cj, err := client.BatchV1().CronJobs(cronjobNamespace).Get(ctx, resourceName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, &TTLNotFoundError{Name: releaseName}
		}

		return nil, fmt.Errorf("failed to get CronJob: %w", err)
	}

	scheduledDate, err := ParseCronSchedule(cj.Spec.Schedule)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CronJob schedule: %w", err)
	}

	deleteNs := cj.Labels[LabelDeleteNamespace] == "true"

	return &TTLInfo{
		ReleaseName:      releaseName,
		ReleaseNamespace: releaseNamespace,
		CronjobNamespace: cronjobNamespace,
		ScheduledDate:    FormatScheduledDate(scheduledDate),
		CronSchedule:     cj.Spec.Schedule,
		DeleteNamespace:  deleteNs,
	}, nil
}

// UnsetTTL removes the TTL from a Helm release by deleting the CronJob
// and cleaning up associated RBAC resources.
func UnsetTTL(ctx context.Context, client kubernetes.Interface, releaseName, releaseNamespace, cronjobNamespace string) error {
	resourceName, err := ResourceName(releaseName, releaseNamespace)
	if err != nil {
		return err
	}

	// Delete CronJob
	err = client.BatchV1().CronJobs(cronjobNamespace).Delete(ctx, resourceName, metav1.DeleteOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return &TTLNotFoundError{Name: releaseName}
		}

		return fmt.Errorf("failed to delete CronJob: %w", err)
	}

	// Clean up RBAC resources (best effort)
	_ = CleanupRBAC(ctx, client, releaseName, releaseNamespace, cronjobNamespace)

	return nil
}

// ContainerResult holds the exit information for a single container.
type ContainerResult struct {
	Name     string
	ExitCode int32
}

// RunTTLResult contains the result of running a TTL action.
type RunTTLResult struct {
	ReleaseName      string
	ReleaseNamespace string
	DeletedNamespace bool
	JobFailed        bool
	ContainerResults []ContainerResult
}

// RunTTL immediately executes the TTL action for a release by creating a
// Kubernetes Job from the CronJob's template, streaming container logs,
// and checking exit codes.
func RunTTL(ctx context.Context, client kubernetes.Interface, w io.Writer, logFetcher LogFetcher, releaseName, releaseNamespace, cronjobNamespace string) (*RunTTLResult, error) {
	resourceName, err := ResourceName(releaseName, releaseNamespace)
	if err != nil {
		return nil, err
	}

	// Look up the CronJob to verify TTL exists and get configuration
	cj, err := client.BatchV1().CronJobs(cronjobNamespace).Get(ctx, resourceName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, &TTLNotFoundError{Name: releaseName}
		}

		return nil, fmt.Errorf("failed to get CronJob: %w", err)
	}

	deleteNamespace := cj.Labels[LabelDeleteNamespace] == "true"

	result := &RunTTLResult{
		ReleaseName:      releaseName,
		ReleaseNamespace: releaseNamespace,
	}

	// Build and create the Job
	jobName := resourceName + "-run"
	job := BuildJobFromCronJob(cj, jobName)

	_, err = client.BatchV1().Jobs(cronjobNamespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create Job: %w", err)
	}

	// Watch pod and stream logs
	var runErr error
	func() {
		pod, err := waitForPod(ctx, client, cronjobNamespace, jobName)
		if err != nil {
			runErr = err
			return
		}

		// Process init containers, then main containers from the actual pod
		// so we capture any injected sidecars
		allContainers := make([]string, 0, len(pod.Spec.InitContainers)+len(pod.Spec.Containers))
		for _, c := range pod.Spec.InitContainers {
			allContainers = append(allContainers, c.Name)
		}
		for _, c := range pod.Spec.Containers {
			allContainers = append(allContainers, c.Name)
		}

		for _, containerName := range allContainers {
			exitCode, err := waitForContainerTermination(ctx, client, cronjobNamespace, pod.Name, containerName)
			if err != nil {
				runErr = err
				return
			}

			_ = streamContainerLogs(ctx, logFetcher, w, cronjobNamespace, pod.Name, containerName)

			result.ContainerResults = append(result.ContainerResults, ContainerResult{
				Name:     containerName,
				ExitCode: exitCode,
			})

			if exitCode != 0 {
				result.JobFailed = true
			}
		}
	}()

	// Cleanup always runs, even on failure
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Delete the Job (best-effort)
	propagation := metav1.DeletePropagationBackground
	_ = client.BatchV1().Jobs(cronjobNamespace).Delete(cleanupCtx, jobName, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})

	// Clean up RBAC resources (best effort)
	_ = CleanupRBAC(cleanupCtx, client, releaseName, releaseNamespace, cronjobNamespace)

	// Handle namespace deletion
	if deleteNamespace {
		err := client.CoreV1().Namespaces().Delete(cleanupCtx, releaseNamespace, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to delete namespace: %w", err)
		}

		result.DeletedNamespace = true
	}

	if runErr != nil {
		return result, runErr
	}

	if result.JobFailed {
		return result, fmt.Errorf("job failed: one or more containers exited with non-zero status")
	}

	return result, nil
}

