package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"
)

const testKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://kube.example.invalid:6443
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user:
    token: test-token
`

// An explicit kubeconfig resolves exactly as client-go's BuildConfigFromFlags
// resolves it.
func TestRESTConfig_ExplicitPathMatchesClientcmd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(path, []byte(testKubeconfig), 0o600))

	got, err := RESTConfig(path)
	require.NoError(t, err)
	want, err := clientcmd.BuildConfigFromFlags("", path)
	require.NoError(t, err)

	assert.Equal(t, "https://kube.example.invalid:6443", got.Host)
	assert.Equal(t, want.Host, got.Host)
	assert.Equal(t, want.BearerToken, got.BearerToken)
}

// Outside a pod, an empty path falls back exactly as BuildConfigFromFlags does.
func TestRESTConfig_EmptyPathOutsideAPodFallsBackLikeClientcmd(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	got, gotErr := RESTConfig("")
	want, wantErr := clientcmd.BuildConfigFromFlags("", "")

	if wantErr != nil {
		require.Error(t, gotErr)
		assert.Equal(t, wantErr.Error(), gotErr.Error())
		return
	}
	require.NoError(t, gotErr)
	assert.Equal(t, want.Host, got.Host)
}
