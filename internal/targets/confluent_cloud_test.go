package targets

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// A CC target must use the manifest-supplied cluster id and never call the
// (CC-404) GET /kafka/v3/clusters list endpoint.
func TestNewConfluentCloudTarget_SeedsClusterID_NoDiscovery(t *testing.T) {
	listHit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/kafka/v3/clusters" {
			listHit = true
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	creds := &Credentials{APIKey: "k", APISecret: "s"}
	tgt, err := NewConfluentCloudTarget(srv.URL, "lkc-abc123", creds, http.DefaultClient)
	require.NoError(t, err)

	id, err := tgt.ClusterID(context.Background())
	require.NoError(t, err)
	require.Equal(t, "lkc-abc123", id)
	require.False(t, listHit, "CC target must not call GET /kafka/v3/clusters")
}

// A CC target cannot discover its own id, so an empty clusterID must be rejected
// at construction rather than falling through to the CC-404 list endpoint.
func TestNewConfluentCloudTarget_EmptyClusterID_Errors(t *testing.T) {
	tgt, err := NewConfluentCloudTarget("https://example.confluent.cloud", "", &Credentials{APIKey: "k", APISecret: "s"}, http.DefaultClient)
	require.Error(t, err)
	require.Nil(t, tgt)
	require.Contains(t, err.Error(), "clusterId is required")
}
