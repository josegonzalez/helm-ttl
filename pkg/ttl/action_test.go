package ttl

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfiguration_EnvironmentVariables(t *testing.T) {
	origNamespace := os.Getenv("HELM_NAMESPACE")
	origDriver := os.Getenv("HELM_DRIVER")
	origKubeconfig := os.Getenv("KUBECONFIG")
	defer func() {
		_ = os.Setenv("HELM_NAMESPACE", origNamespace)
		_ = os.Setenv("HELM_DRIVER", origDriver)
		_ = os.Setenv("KUBECONFIG", origKubeconfig)
	}()

	t.Run("with default environment", func(t *testing.T) {
		_ = os.Unsetenv("HELM_NAMESPACE")
		_ = os.Unsetenv("HELM_DRIVER")

		cfg, err := NewConfiguration()
		require.NoError(t, err)
		assert.NotNil(t, cfg)
	})

	t.Run("with HELM_NAMESPACE set", func(t *testing.T) {
		_ = os.Setenv("HELM_NAMESPACE", "custom-namespace")

		cfg, err := NewConfiguration()
		require.NoError(t, err)
		assert.NotNil(t, cfg)
	})

	t.Run("with HELM_DRIVER set to memory", func(t *testing.T) {
		_ = os.Setenv("HELM_DRIVER", "memory")

		cfg, err := NewConfiguration()
		require.NoError(t, err)
		assert.NotNil(t, cfg)
	})

	t.Run("with empty HELM_NAMESPACE defaults to 'default'", func(t *testing.T) {
		_ = os.Setenv("HELM_NAMESPACE", "")

		cfg, err := NewConfiguration()
		require.NoError(t, err)
		assert.NotNil(t, cfg)
	})

	t.Run("with empty HELM_DRIVER defaults to 'secrets'", func(t *testing.T) {
		_ = os.Setenv("HELM_DRIVER", "")

		cfg, err := NewConfiguration()
		require.NoError(t, err)
		assert.NotNil(t, cfg)
	})

	t.Run("with invalid HELM_DRIVER returns error", func(t *testing.T) {
		_ = os.Setenv("HELM_DRIVER", "invalid-driver-that-does-not-exist")

		_, err := NewConfiguration()
		assert.Error(t, err)
	})
}
