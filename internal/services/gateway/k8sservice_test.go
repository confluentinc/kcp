package gateway

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// TestK8sService_InjectedClientsServeWithoutKubeconfig checks that a service
// built from injected clients serves an exported method with no kubeconfig at
// all.
func TestK8sService_InjectedClientsServeWithoutKubeconfig(t *testing.T) {
	gw := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": GatewayGroup + "/" + GatewayVersion,
		"kind":       GatewayKind,
		"metadata": map[string]any{
			"name":       "gw",
			"namespace":  "ns",
			"generation": int64(7),
		},
	}}
	dyn := newFakeDynamicClient(gw)
	typed := newFakeClientset()

	svc := newK8sServiceWithClients(dyn, typed)

	gen, err := svc.GetGatewayGeneration(context.Background(), "ns", "gw")
	require.NoError(t, err)
	assert.Equal(t, int64(7), gen)

	cs, err := svc.clientset()
	require.NoError(t, err)
	assert.Same(t, typed, cs)
}

// TestK8sService_BadKubeconfigErrorIsStable checks that a failed build is
// attempted once and its error, worded as before, is returned on every call.
func TestK8sService_BadKubeconfigErrorIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist")
	svc := NewK8sService(path)

	builds := 0
	svc.clients.restConfig = func(p string) (*rest.Config, error) {
		builds++
		return RESTConfig(p)
	}

	_, err1 := svc.GetGatewayGeneration(context.Background(), "ns", "gw")
	require.Error(t, err1)
	_, err2 := svc.GetGatewayGeneration(context.Background(), "ns", "gw")
	require.Error(t, err2)

	assert.Equal(t, err1.Error(), err2.Error())
	assert.Contains(t, err1.Error(), "failed to build config: ")
	assert.Equal(t, 1, builds)

	// The typed client shares the one config build.
	_, err3 := svc.clientset()
	require.Error(t, err3)
	assert.Contains(t, err3.Error(), "failed to build config: ")
	assert.Equal(t, 1, builds)
}

// TestK8sService_ConcurrentFirstUse checks the lazy build under concurrent
// first use; run it with -race.
func TestK8sService_ConcurrentFirstUse(t *testing.T) {
	svc := NewK8sService("")
	svc.clients.restConfig = func(string) (*rest.Config, error) {
		return &rest.Config{Host: "https://127.0.0.1:1"}, nil
	}

	const n = 8
	got := make([]kubernetes.Interface, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], errs[i] = svc.clientset()
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		require.NoError(t, errs[i])
		assert.Same(t, got[0], got[i])
	}
	require.NotNil(t, got[0])
}
