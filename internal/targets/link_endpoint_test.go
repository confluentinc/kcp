package targets

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/stretchr/testify/require"
)

// TestLinkEndpoint_TwoEndpoints_NoCrosstalk proves the SAME constructor builds a
// working client for both a destination-style and a source-style REST endpoint:
// one client type, two endpoints, each returning its own cluster id with no
// cross-talk. This is what Task 6 relies on for source-initiated links.
func TestLinkEndpoint_TwoEndpoints_NoCrosstalk(t *testing.T) {
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/kafka/v3/clusters", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":[{"cluster_id":"destination-cluster"}]}`))
	}))
	defer dst.Close()

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/kafka/v3/clusters", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":[{"cluster_id":"source-cluster"}]}`))
	}))
	defer src.Close()

	dstEP := NewLinkEndpoint(dst.URL, &Credentials{Basic: &BasicAuth{Username: "d", Password: "d"}}, dst.Client())
	srcEP := NewLinkEndpoint(src.URL, &Credentials{Basic: &BasicAuth{Username: "s", Password: "s"}}, src.Client())

	dstID, err := dstEP.ClusterID(context.Background())
	require.NoError(t, err)
	srcID, err := srcEP.ClusterID(context.Background())
	require.NoError(t, err)

	require.Equal(t, "destination-cluster", dstID)
	require.Equal(t, "source-cluster", srcID, "source endpoint client must not pick up the destination id")
	require.NotEqual(t, dstID, srcID)
}

// TestLinkEndpoint_GetClusterLinkConfigs confirms that GetClusterLinkConfigs
// delegates to svc.ListConfigs and maps the JSON data array into a plain
// key/value map.
func TestLinkEndpoint_GetClusterLinkConfigs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"name":"cluster.link.prefix","value":"mig."},{"name":"consumer.offset.sync.enable","value":"true"}]}`))
	}))
	defer srv.Close()

	// &Credentials{} falls through to the default branch in authenticator(),
	// returning BasicAuth with empty username/password — fine for a no-auth server.
	e := NewLinkEndpoint(srv.URL, &Credentials{}, http.DefaultClient)
	e.clusterID = "c-1" // bypass ClusterID discovery; field is accessible within package
	got, err := e.GetClusterLinkConfigs(context.Background(), "l")
	require.NoError(t, err)
	require.Equal(t, "mig.", got["cluster.link.prefix"])
	require.Equal(t, "true", got["consumer.offset.sync.enable"])
}

// TestLinkEndpoint_MTLS_SourceEndpoint proves the same constructor builds a
// client that carries a client certificate for an mTLS source endpoint: the
// built client is accepted by a server that requires and verifies a client
// cert, where a default (certless) client is rejected at the handshake.
func TestLinkEndpoint_MTLS_SourceEndpoint(t *testing.T) {
	dir := t.TempDir()
	caPEM, certPEM, keyPEM := genCertMaterial(t)
	caFile := filepath.Join(dir, "ca.pem")
	crtFile := filepath.Join(dir, "client.pem")
	keyFile := filepath.Join(dir, "client.key")
	require.NoError(t, os.WriteFile(caFile, caPEM, 0600))
	require.NoError(t, os.WriteFile(crtFile, certPEM, 0600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0600))

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(caPEM))
	serverCert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/kafka/v3/clusters", r.URL.Path)
		// A client cert must have been presented and verified.
		require.NotEmpty(t, r.TLS.PeerCertificates, "server must see the client certificate")
		_, _ = w.Write([]byte(`{"data":[{"cluster_id":"mtls-source-cluster"}]}`))
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	srv.StartTLS()
	defer srv.Close()

	creds := &Credentials{MTLS: &MTLSCreds{CACert: caFile, ClientCert: crtFile, ClientKey: keyFile}}
	httpClient, err := creds.HTTPClient()
	require.NoError(t, err)

	ep := NewLinkEndpoint(srv.URL, creds, httpClient)
	id, err := ep.ClusterID(context.Background())
	require.NoError(t, err, "mtls source endpoint client must present its client cert and be accepted")
	require.Equal(t, "mtls-source-cluster", id)
}

// TestLinkEndpoint_ListTopics verifies ListTopics discovers the cluster id then
// calls GET /kafka/v3/clusters/{id}/topics and returns the topic names.
func TestLinkEndpoint_ListTopics(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/kafka/v3/clusters":
			_, _ = w.Write([]byte(`{"data":[{"cluster_id":"c-1"}]}`))
		case "/kafka/v3/clusters/c-1/topics":
			_, _ = w.Write([]byte(`{"data":[{"topic_name":"orders"},{"topic_name":"events"}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	e := NewLinkEndpoint(srv.URL, &Credentials{}, srv.Client())
	got, err := e.ListTopics(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"orders", "events"}, got)
	require.Equal(t, []string{"/kafka/v3/clusters", "/kafka/v3/clusters/c-1/topics"}, paths)
}

// TestLinkEndpoint_CreateTopic verifies CreateTopic discovers the cluster id
// then POSTs to /kafka/v3/clusters/{id}/topics.
func TestLinkEndpoint_CreateTopic(t *testing.T) {
	var createPath, createMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/kafka/v3/clusters" {
			_, _ = w.Write([]byte(`{"data":[{"cluster_id":"c-1"}]}`))
			return
		}
		createPath = r.URL.Path
		createMethod = r.Method
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	e := NewLinkEndpoint(srv.URL, &Credentials{}, srv.Client())
	err := e.CreateTopic(context.Background(), clusterlink.CreateTopicRequest{Name: "orders", Partitions: 6})
	require.NoError(t, err)
	require.Equal(t, "/kafka/v3/clusters/c-1/topics", createPath)
	require.Equal(t, http.MethodPost, createMethod)
}

// TestLinkEndpoint_ListMirrorTopics verifies ListMirrorTopics discovers the
// cluster id then calls GET .../links/{name}/mirrors and returns ALL mirrors
// (no topic filtering, since e.config(name) leaves Config.Topics empty).
func TestLinkEndpoint_ListMirrorTopics(t *testing.T) {
	var mirrorsPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/kafka/v3/clusters" {
			_, _ = w.Write([]byte(`{"data":[{"cluster_id":"c-1"}]}`))
			return
		}
		mirrorsPath = r.URL.Path
		_, _ = w.Write([]byte(`{"data":[{"mirror_topic_name":"a","mirror_status":"ACTIVE"},{"mirror_topic_name":"b","mirror_status":"PAUSED"}]}`))
	}))
	defer srv.Close()

	e := NewLinkEndpoint(srv.URL, &Credentials{}, srv.Client())
	got, err := e.ListMirrorTopics(context.Background(), "my-link")
	require.NoError(t, err)
	require.Equal(t, "/kafka/v3/clusters/c-1/links/my-link/mirrors", mirrorsPath)
	require.Len(t, got, 2, "must return all mirrors unfiltered")
	require.Equal(t, "a", got[0].MirrorTopicName)
	require.Equal(t, "b", got[1].MirrorTopicName)
}

// TestLinkEndpoint_CreateMirrorTopic verifies CreateMirrorTopic discovers the
// cluster id then POSTs to .../links/{name}/mirrors.
func TestLinkEndpoint_CreateMirrorTopic(t *testing.T) {
	var mirrorsPath, method string
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/kafka/v3/clusters" {
			_, _ = w.Write([]byte(`{"data":[{"cluster_id":"c-1"}]}`))
			return
		}
		mirrorsPath = r.URL.Path
		method = r.Method
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	e := NewLinkEndpoint(srv.URL, &Credentials{}, srv.Client())
	err := e.CreateMirrorTopic(context.Background(), "my-link", "orders", "orders")
	require.NoError(t, err)
	require.Equal(t, "/kafka/v3/clusters/c-1/links/my-link/mirrors", mirrorsPath)
	require.Equal(t, http.MethodPost, method)
	require.Equal(t, "orders", body["source_topic_name"])
}
