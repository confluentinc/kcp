package clusterlink

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateClusterLink_PlaintextRequestShape(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	svc := NewConfluentCloudService(srv.Client())
	cfg := Config{RestEndpoint: srv.URL, ClusterID: "dest-123", LinkName: "src-to-dest", APIKey: "admin", APISecret: "admin-secret"}

	err := svc.CreateClusterLink(context.Background(), cfg, CreateClusterLinkRequest{
		SourceClusterID:        "src-999",
		SourceBootstrapServers: []string{"source:29092"},
		SecurityProtocol:       "PLAINTEXT",
	})
	require.NoError(t, err)

	require.Equal(t, "/kafka/v3/clusters/dest-123/links/", gotPath)
	require.Equal(t, "link_name=src-to-dest", gotQuery)
	require.Contains(t, gotAuth, "Basic ")
	require.Equal(t, "src-999", gotBody["source_cluster_id"])

	configs := configMapFromBody(t, gotBody)
	require.Equal(t, "source:29092", configs["bootstrap.servers"])
	require.Equal(t, "DESTINATION", configs["link.mode"])
	require.Equal(t, "PLAINTEXT", configs["security.protocol"])
	require.NotContains(t, configs, "sasl.mechanism", "no SASL for plaintext")
}

func TestCreateClusterLink_AlreadyExistsIsTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error_code":40902,"message":"Cluster link 'x' already exists."}`))
	}))
	defer srv.Close()

	svc := NewConfluentCloudService(srv.Client())
	cfg := Config{RestEndpoint: srv.URL, ClusterID: "dest-123", LinkName: "x"}
	err := svc.CreateClusterLink(context.Background(), cfg, CreateClusterLinkRequest{SourceClusterID: "s", SecurityProtocol: "PLAINTEXT"})
	require.ErrorIs(t, err, ErrLinkExists)
}

func TestCreateClusterLink_ConfigOverridesAppear(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	svc := NewConfluentCloudService(srv.Client())
	cfg := Config{RestEndpoint: srv.URL, ClusterID: "dest-1", LinkName: "l", APIKey: "a", APISecret: "b"}
	err := svc.CreateClusterLink(context.Background(), cfg, CreateClusterLinkRequest{
		SourceClusterID:        "src",
		SourceBootstrapServers: []string{"source:29092"},
		SecurityProtocol:       "PLAINTEXT",
		Configs:                map[string]string{"consumer.offset.sync.enable": "true"},
	})
	require.NoError(t, err)
	configs := configMapFromBody(t, gotBody)
	require.Equal(t, "true", configs["consumer.offset.sync.enable"])
}

// configMapFromBody flattens the request body's "configs":[{name,value}] array.
func configMapFromBody(t *testing.T, body map[string]any) map[string]string {
	t.Helper()
	out := map[string]string{}
	raw, ok := body["configs"].([]any)
	require.True(t, ok, "body must have configs array")
	for _, e := range raw {
		m := e.(map[string]any)
		out[m["name"].(string)] = m["value"].(string)
	}
	return out
}
