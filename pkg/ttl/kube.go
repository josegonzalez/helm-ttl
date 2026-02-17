package ttl

import (
	"os"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

// RESTClientGetter implements genericclioptions.RESTClientGetter interface
type RESTClientGetter struct {
	namespace string
}

// NewRESTClientGetter creates a new RESTClientGetter
func NewRESTClientGetter(namespace string) *RESTClientGetter {
	return &RESTClientGetter{namespace: namespace}
}

// ToRESTConfig returns a REST config
func (r *RESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	return r.ToRawKubeConfigLoader().ClientConfig()
}

// ToDiscoveryClient returns a discovery client
func (r *RESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	config, err := r.ToRESTConfig()
	if err != nil {
		return nil, err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, err
	}

	return memory.NewMemCacheClient(discoveryClient), nil
}

// ToRESTMapper returns a REST mapper
func (r *RESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, err := r.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)
	return mapper, nil
}

// ToRawKubeConfigLoader returns a clientcmd loader
func (r *RESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()

	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}

	configOverrides := &clientcmd.ConfigOverrides{}
	if context := os.Getenv("HELM_KUBECONTEXT"); context != "" {
		configOverrides.CurrentContext = context
	}
	configOverrides.Context.Namespace = r.namespace

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
}

// NewKubeClient creates a new Kubernetes clientset from the current kubeconfig.
func NewKubeClient() (kubernetes.Interface, error) {
	getter := NewRESTClientGetter("default")
	config, err := getter.ToRESTConfig()
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(config)
}
