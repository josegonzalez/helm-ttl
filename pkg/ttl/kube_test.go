package ttl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRESTClientGetter(t *testing.T) {
	getter := NewRESTClientGetter("test-namespace", KubeOptions{})
	assert.NotNil(t, getter)
	assert.Equal(t, "test-namespace", getter.namespace)
}

func TestRESTClientGetter_ToRawKubeConfigLoader(t *testing.T) {
	t.Run("with default settings", func(t *testing.T) {
		_ = os.Unsetenv("KUBECONFIG")
		_ = os.Unsetenv("HELM_KUBECONTEXT")

		getter := NewRESTClientGetter("default", KubeOptions{})
		loader := getter.ToRawKubeConfigLoader()
		assert.NotNil(t, loader)
	})

	t.Run("with KUBECONFIG set", func(t *testing.T) {
		_ = os.Setenv("KUBECONFIG", "/tmp/test-kubeconfig")
		defer func() { _ = os.Unsetenv("KUBECONFIG") }()

		getter := NewRESTClientGetter("default", KubeOptions{})
		loader := getter.ToRawKubeConfigLoader()
		assert.NotNil(t, loader)
	})

	t.Run("with HELM_KUBECONTEXT set", func(t *testing.T) {
		_ = os.Setenv("HELM_KUBECONTEXT", "test-context")
		defer func() { _ = os.Unsetenv("HELM_KUBECONTEXT") }()

		getter := NewRESTClientGetter("default", KubeOptions{})
		loader := getter.ToRawKubeConfigLoader()
		assert.NotNil(t, loader)
	})

	t.Run("with both KUBECONFIG and HELM_KUBECONTEXT set", func(t *testing.T) {
		_ = os.Setenv("KUBECONFIG", "/tmp/test-kubeconfig")
		_ = os.Setenv("HELM_KUBECONTEXT", "test-context")
		defer func() {
			_ = os.Unsetenv("KUBECONFIG")
			_ = os.Unsetenv("HELM_KUBECONTEXT")
		}()

		getter := NewRESTClientGetter("custom-ns", KubeOptions{})
		loader := getter.ToRawKubeConfigLoader()
		assert.NotNil(t, loader)
	})

	t.Run("opts override env vars", func(t *testing.T) {
		_ = os.Setenv("KUBECONFIG", "/tmp/env-kubeconfig")
		_ = os.Setenv("HELM_KUBECONTEXT", "env-context")
		defer func() {
			_ = os.Unsetenv("KUBECONFIG")
			_ = os.Unsetenv("HELM_KUBECONTEXT")
		}()

		getter := NewRESTClientGetter("default", KubeOptions{
			KubeContext: "flag-context",
			Kubeconfig:  "/tmp/flag-kubeconfig",
		})
		assert.Equal(t, "flag-context", getter.kubeContext)
		assert.Equal(t, "/tmp/flag-kubeconfig", getter.kubeconfig)

		loader := getter.ToRawKubeConfigLoader()
		assert.NotNil(t, loader)
	})
}

func TestRESTClientGetter_ToRESTConfig_Error(t *testing.T) {
	_ = os.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
	defer func() { _ = os.Unsetenv("KUBECONFIG") }()

	getter := NewRESTClientGetter("default", KubeOptions{})
	_, err := getter.ToRESTConfig()
	assert.Error(t, err)
}

func TestRESTClientGetter_ToDiscoveryClient_Error(t *testing.T) {
	_ = os.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
	defer func() { _ = os.Unsetenv("KUBECONFIG") }()

	getter := NewRESTClientGetter("default", KubeOptions{})
	_, err := getter.ToDiscoveryClient()
	assert.Error(t, err)
}

func TestRESTClientGetter_ToRESTMapper_Error(t *testing.T) {
	_ = os.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
	defer func() { _ = os.Unsetenv("KUBECONFIG") }()

	getter := NewRESTClientGetter("default", KubeOptions{})
	_, err := getter.ToRESTMapper()
	assert.Error(t, err)
}

// createTestKubeconfig creates a minimal valid kubeconfig for testing
func createTestKubeconfig(t *testing.T) string {
	t.Helper()

	kubeconfig := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
    insecure-skip-tls-verify: true
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: test-token
`
	tmpDir := t.TempDir()
	kubeconfigPath := filepath.Join(tmpDir, "kubeconfig")
	err := os.WriteFile(kubeconfigPath, []byte(kubeconfig), 0600)
	require.NoError(t, err)
	return kubeconfigPath
}

func TestRESTClientGetter_ToRESTConfig_Success(t *testing.T) {
	kubeconfigPath := createTestKubeconfig(t)
	_ = os.Setenv("KUBECONFIG", kubeconfigPath)
	defer func() { _ = os.Unsetenv("KUBECONFIG") }()

	getter := NewRESTClientGetter("default", KubeOptions{})
	config, err := getter.ToRESTConfig()
	require.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "https://127.0.0.1:6443", config.Host)
}

func TestRESTClientGetter_ToRESTConfig_OptsKubeconfig(t *testing.T) {
	_ = os.Unsetenv("KUBECONFIG")

	kubeconfigPath := createTestKubeconfig(t)
	getter := NewRESTClientGetter("default", KubeOptions{Kubeconfig: kubeconfigPath})
	config, err := getter.ToRESTConfig()
	require.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "https://127.0.0.1:6443", config.Host)
}

func TestRESTClientGetter_ToDiscoveryClient_Success(t *testing.T) {
	kubeconfigPath := createTestKubeconfig(t)
	_ = os.Setenv("KUBECONFIG", kubeconfigPath)
	defer func() { _ = os.Unsetenv("KUBECONFIG") }()

	getter := NewRESTClientGetter("default", KubeOptions{})
	client, err := getter.ToDiscoveryClient()
	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestRESTClientGetter_ToRESTMapper_Success(t *testing.T) {
	kubeconfigPath := createTestKubeconfig(t)
	_ = os.Setenv("KUBECONFIG", kubeconfigPath)
	defer func() { _ = os.Unsetenv("KUBECONFIG") }()

	getter := NewRESTClientGetter("default", KubeOptions{})
	mapper, err := getter.ToRESTMapper()
	require.NoError(t, err)
	assert.NotNil(t, mapper)
}

func TestNewKubeClient_Error(t *testing.T) {
	_ = os.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
	defer func() { _ = os.Unsetenv("KUBECONFIG") }()

	_, err := NewKubeClient(KubeOptions{})
	assert.Error(t, err)
}

func TestNewKubeClient_Success(t *testing.T) {
	kubeconfigPath := createTestKubeconfig(t)
	_ = os.Setenv("KUBECONFIG", kubeconfigPath)
	defer func() { _ = os.Unsetenv("KUBECONFIG") }()

	client, err := NewKubeClient(KubeOptions{})
	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestNewKubeClient_OptsKubeconfig(t *testing.T) {
	_ = os.Unsetenv("KUBECONFIG")

	kubeconfigPath := createTestKubeconfig(t)
	client, err := NewKubeClient(KubeOptions{Kubeconfig: kubeconfigPath})
	require.NoError(t, err)
	assert.NotNil(t, client)
}
