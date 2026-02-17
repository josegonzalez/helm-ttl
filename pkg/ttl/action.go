package ttl

import (
	"os"

	"helm.sh/helm/v3/pkg/action"
)

// NewConfiguration creates a new Helm action configuration.
// It reads configuration from environment variables set by Helm.
func NewConfiguration() (*action.Configuration, error) {
	cfg := new(action.Configuration)

	namespace := os.Getenv("HELM_NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}

	driver := os.Getenv("HELM_DRIVER")
	if driver == "" {
		driver = "secrets"
	}

	if err := cfg.Init(
		NewRESTClientGetter(namespace),
		namespace,
		driver,
		func(format string, v ...interface{}) {},
	); err != nil {
		return nil, err
	}

	return cfg, nil
}
