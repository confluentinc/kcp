package targets

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfluentPlatform_ClusterID_Caches(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"data":[{"cluster_id":"dest-cluster-42"}]}`))
	}))
	defer srv.Close()
	tgt := NewConfluentPlatformTarget(srv.URL, &Credentials{Basic: &BasicAuth{Username: "a", Password: "b"}}, srv.Client())
	id1, err := tgt.ClusterID(context.Background())
	require.NoError(t, err)
	id2, err := tgt.ClusterID(context.Background())
	require.NoError(t, err)
	require.Equal(t, id1, id2)
	require.Equal(t, 1, calls, "second ClusterID call must use the cache, not re-request")
}

func TestConfluentPlatform_ClusterID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/kafka/v3/clusters", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":[{"cluster_id":"dest-cluster-42"}]}`))
	}))
	defer srv.Close()

	tgt := NewConfluentPlatformTarget(srv.URL, &Credentials{Basic: &BasicAuth{Username: "admin", Password: "x"}}, srv.Client())
	id, err := tgt.ClusterID(context.Background())
	require.NoError(t, err)
	require.Equal(t, "dest-cluster-42", id)
}

func TestConfluentPlatform_GetClusterLink_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tgt := NewConfluentPlatformTarget(srv.URL, &Credentials{Basic: &BasicAuth{Username: "a", Password: "b"}}, srv.Client())
	// ClusterID must be primed first; inject via the helper used by the real flow.
	tgt.clusterID = "dest-1"
	link, err := tgt.GetClusterLink(context.Background(), "missing")
	require.NoError(t, err)
	require.Nil(t, link, "absent link returns (nil, nil) so the reconciler treats it as 'to create'")
}
