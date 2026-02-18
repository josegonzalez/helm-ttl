package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/josegonzalez/helm-ttl/pkg/ttl"
	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/action"
	"k8s.io/client-go/kubernetes"
)

var version = "dev"

// Factory types for dependency injection in tests.
type configFactory func() (*action.Configuration, error)
type kubeClientFactory func() (kubernetes.Interface, error)

// Default factories use the real implementations.
var (
	defaultConfigFactory     configFactory     = ttl.NewConfiguration
	defaultKubeClientFactory kubeClientFactory = ttl.NewKubeClient
)

func main() {
	if err := newRootCmd(defaultConfigFactory, defaultKubeClientFactory).Execute(); err != nil {
		os.Exit(1)
	}
}

func getReleaseNamespace() string {
	ns := os.Getenv("HELM_NAMESPACE")
	if ns == "" {
		return "default"
	}

	return ns
}

func newRootCmd(cfgFactory configFactory, kubeFactory kubeClientFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "helm-ttl",
		Short:   "Manage TTL (time-to-live) for Helm releases",
		Version: version,
	}

	cmd.AddCommand(
		newSetCmd(cfgFactory, kubeFactory),
		newGetCmd(kubeFactory),
		newUnsetCmd(kubeFactory),
		newCleanupRBACCmd(kubeFactory),
	)

	return cmd
}

func newSetCmd(cfgFactory configFactory, kubeFactory kubeClientFactory) *cobra.Command {
	var (
		serviceAccount       string
		createServiceAccount bool
		helmImage            string
		kubectlImage         string
		cronjobNamespace     string
		deleteNamespace      bool
	)

	cmd := &cobra.Command{
		Use:   "set RELEASE DURATION",
		Short: "Set TTL for a Helm release",
		Long: `Set a time-to-live for a Helm release. When the TTL expires, the release
will be automatically uninstalled via a Kubernetes CronJob.

Duration supports:
  - Go durations: 30m, 2h, 24h, 168h
  - Days shorthand: 7d, 30d
  - Human-readable: 6 hours, 3 days, 2 weeks, 30 mins
  - Natural language: tomorrow, "next monday", "in 2 hours"`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			releaseName := args[0]
			duration := args[1]

			releaseNs := getReleaseNamespace()
			cjNs := cronjobNamespace
			if cjNs == "" {
				cjNs = releaseNs
			}

			cfg, err := cfgFactory()
			if err != nil {
				return fmt.Errorf("failed to create configuration: %w", err)
			}

			client, err := kubeFactory()
			if err != nil {
				return fmt.Errorf("failed to create kubernetes client: %w", err)
			}

			ctx := context.Background()
			if err := ttl.SetTTL(ctx, cfg, client, ttl.SetTTLOptions{
				ReleaseName:          releaseName,
				ReleaseNamespace:     releaseNs,
				CronjobNamespace:     cjNs,
				Duration:             duration,
				ServiceAccount:       serviceAccount,
				CreateServiceAccount: createServiceAccount,
				HelmImage:            helmImage,
				KubectlImage:         kubectlImage,
				DeleteNamespace:      deleteNamespace,
			}); err != nil {
				var notFound *ttl.ReleaseNotFoundError
				if errors.As(err, &notFound) {
					return fmt.Errorf("release %q not found in namespace %q", releaseName, releaseNs)
				}

				var saNotFound *ttl.ServiceAccountNotFoundError
				if errors.As(err, &saNotFound) {
					return fmt.Errorf("service account %q not found in namespace %q; use --create-service-account to create it", serviceAccount, cjNs)
				}

				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "TTL set for release %q in namespace %q\n", releaseName, releaseNs)
			return nil
		},
	}

	cmd.Flags().StringVar(&serviceAccount, "service-account", "default", "service account for CronJob")
	cmd.Flags().BoolVar(&createServiceAccount, "create-service-account", false, "create the service account and RBAC resources")
	cmd.Flags().StringVar(&helmImage, "helm-image", "", "Helm container image (default: "+ttl.DefaultHelmImage+")")
	cmd.Flags().StringVar(&kubectlImage, "kubectl-image", "", "kubectl container image (default: "+ttl.DefaultKubectlImage+")")
	cmd.Flags().StringVar(&cronjobNamespace, "cronjob-namespace", "", "namespace for the CronJob (default: release namespace)")
	cmd.Flags().BoolVar(&deleteNamespace, "delete-namespace", false, "also delete the release namespace after uninstalling")

	return cmd
}

func newGetCmd(kubeFactory kubeClientFactory) *cobra.Command {
	var (
		outputFormat     string
		cronjobNamespace string
	)

	cmd := &cobra.Command{
		Use:   "get RELEASE",
		Short: "Get current TTL for a Helm release",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			releaseName := args[0]
			releaseNs := getReleaseNamespace()
			cjNs := cronjobNamespace
			if cjNs == "" {
				cjNs = releaseNs
			}

			client, err := kubeFactory()
			if err != nil {
				return fmt.Errorf("failed to create kubernetes client: %w", err)
			}

			ctx := context.Background()
			info, err := ttl.GetTTL(ctx, client, releaseName, releaseNs, cjNs)
			if err != nil {
				var notFound *ttl.TTLNotFoundError
				if errors.As(err, &notFound) {
					return fmt.Errorf("no TTL set for release %q in namespace %q", releaseName, releaseNs)
				}

				return err
			}

			output, err := ttl.FormatOutput(*info, outputFormat)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprint(cmd.OutOrStdout(), output)
			return nil
		},
	}

	cmd.Flags().StringVarP(&outputFormat, "output", "o", "text", "output format: text, yaml, json")
	cmd.Flags().StringVar(&cronjobNamespace, "cronjob-namespace", "", "namespace where the CronJob lives (default: release namespace)")

	return cmd
}

func newUnsetCmd(kubeFactory kubeClientFactory) *cobra.Command {
	var cronjobNamespace string

	cmd := &cobra.Command{
		Use:   "unset RELEASE",
		Short: "Remove TTL from a Helm release",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			releaseName := args[0]
			releaseNs := getReleaseNamespace()
			cjNs := cronjobNamespace
			if cjNs == "" {
				cjNs = releaseNs
			}

			client, err := kubeFactory()
			if err != nil {
				return fmt.Errorf("failed to create kubernetes client: %w", err)
			}

			ctx := context.Background()
			if err := ttl.UnsetTTL(ctx, client, releaseName, releaseNs, cjNs); err != nil {
				var notFound *ttl.TTLNotFoundError
				if errors.As(err, &notFound) {
					return fmt.Errorf("no TTL set for release %q in namespace %q", releaseName, releaseNs)
				}

				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "TTL removed for release %q in namespace %q\n", releaseName, releaseNs)
			return nil
		},
	}

	cmd.Flags().StringVar(&cronjobNamespace, "cronjob-namespace", "", "namespace where the CronJob lives (default: release namespace)")

	return cmd
}

func newCleanupRBACCmd(kubeFactory kubeClientFactory) *cobra.Command {
	var (
		dryRun        bool
		allNamespaces bool
	)

	cmd := &cobra.Command{
		Use:   "cleanup-rbac",
		Short: "Delete orphaned SA/RBAC resources",
		Long: `Find and delete ServiceAccount and RBAC resources created by helm ttl set
whose CronJobs have already fired or been deleted.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := kubeFactory()
			if err != nil {
				return fmt.Errorf("failed to create kubernetes client: %w", err)
			}

			releaseNs := getReleaseNamespace()
			namespaces := []string{releaseNs}

			ctx := context.Background()
			orphaned, err := ttl.CleanupOrphaned(ctx, client, namespaces, allNamespaces, dryRun)
			if err != nil {
				return err
			}

			if len(orphaned) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No orphaned resources found")
				return nil
			}

			for _, o := range orphaned {
				if dryRun {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Would delete %s\n", o)
				} else {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s\n", o)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be deleted without deleting")
	cmd.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "search all namespaces for orphaned resources")

	return cmd
}
