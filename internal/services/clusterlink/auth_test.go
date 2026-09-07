package clusterlink

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBasicAuth_SetsHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://x/", nil)
	BasicAuth{Username: "admin", Password: "secret"}.Apply(req)
	require.Equal(t, "Basic YWRtaW46c2VjcmV0", req.Header.Get("Authorization"))
}

func TestBearerAuth_SetsHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://x/", nil)
	BearerAuth{Token: "tok123"}.Apply(req)
	require.Equal(t, "Bearer tok123", req.Header.Get("Authorization"))
}

func TestNoHeaderAuth_SetsNothing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://x/", nil)
	NoHeaderAuth{}.Apply(req)
	require.Empty(t, req.Header.Get("Authorization"))
}
