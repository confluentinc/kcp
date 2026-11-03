package gateway

import (
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// RESTConfig builds the Kubernetes client config for kubeConfigPath. An empty
// path means kcp is running in a pod and uses its in-cluster service account —
// the same result clientcmd.BuildConfigFromFlags("", "") gives, without the
// "Neither --kubeconfig nor --master was specified" warning client-go logs on
// every call. Outside a pod, an empty path falls through to
// BuildConfigFromFlags unchanged, including its warnings.
func RESTConfig(kubeConfigPath string) (*rest.Config, error) {
	if kubeConfigPath == "" {
		if config, err := rest.InClusterConfig(); err == nil {
			return config, nil
		}
	}
	return clientcmd.BuildConfigFromFlags("", kubeConfigPath)
}
