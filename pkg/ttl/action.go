package ttl

import (
	"os"

	"helm.sh/helm/v3/pkg/action"
)

// NewConfiguration creates a new Helm action configuration.
// When namespace is non-empty it is used directly; otherwise the
// value falls back to the HELM_NAMESPACE env var or "default".
func NewConfiguration(namespace string) (*action.Configuration, error) {
	cfg := new(action.Configuration)

	if namespace == "" {
		namespace = os.Getenv("HELM_NAMESPACE")
	}
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
