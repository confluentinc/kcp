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

func TestCreateClusterLink_DefaultLinkModeDestinationNoConnectionMode(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	svc := NewConfluentCloudService(srv.Client())
	cfg := Config{RestEndpoint: srv.URL, ClusterID: "dest-123", LinkName: "l", APIKey: "a", APISecret: "b"}
	err := svc.CreateClusterLink(context.Background(), cfg, CreateClusterLinkRequest{
		SourceClusterID:        "src",
		SourceBootstrapServers: []string{"source:29092"},
		SecurityProtocol:       "PLAINTEXT",
	})
	require.NoError(t, err)

	configs := configMapFromBody(t, gotBody)
	require.Equal(t, "DESTINATION", configs["link.mode"], "empty LinkMode defaults to DESTINATION")
	require.NotContains(t, configs, "connection.mode", "no connection.mode for default destination-initiated link")
}

func TestCreateClusterLink_SourceInitiatedSourceSide(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	svc := NewConfluentCloudService(srv.Client())
	cfg := Config{RestEndpoint: srv.URL, ClusterID: "dest-123", LinkName: "l", APIKey: "a", APISecret: "b"}
	err := svc.CreateClusterLink(context.Background(), cfg, CreateClusterLinkRequest{
		// SourceClusterID intentionally empty: the SOURCE-side link object must
		// NOT carry source_cluster_id (only the DESTINATION side does).
		SourceBootstrapServers: []string{"source:29092"},
		SecurityProtocol:       "PLAINTEXT",
		LinkMode:               "SOURCE",
		ConnectionMode:         "OUTBOUND",
	})
	require.NoError(t, err)

	configs := configMapFromBody(t, gotBody)
	require.Equal(t, "SOURCE", configs["link.mode"])
	require.Equal(t, "OUTBOUND", configs["connection.mode"])
	require.NotContains(t, gotBody, "source_cluster_id", "source-side link must omit source_cluster_id")
}

func TestCreateClusterLink_SourceInitiatedDestinationSide(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	svc := NewConfluentCloudService(srv.Client())
	cfg := Config{RestEndpoint: srv.URL, ClusterID: "dest-123", LinkName: "l", APIKey: "a", APISecret: "b"}
	err := svc.CreateClusterLink(context.Background(), cfg, CreateClusterLinkRequest{
		SourceClusterID:        "src",
		SourceBootstrapServers: []string{"source:29092"},
		SecurityProtocol:       "PLAINTEXT",
		LinkMode:               "DESTINATION",
		ConnectionMode:         "INBOUND",
	})
	require.NoError(t, err)

	configs := configMapFromBody(t, gotBody)
	require.Equal(t, "DESTINATION", configs["link.mode"])
	require.Equal(t, "INBOUND", configs["connection.mode"])
	require.Equal(t, "src", gotBody["source_cluster_id"], "destination-side link must carry source_cluster_id")
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

func TestCreateClusterLink_ConfigOverrideReplacesByName(t *testing.T) {
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
		SourceBootstrapServers: []string{"kcp-reachable:9092"},
		SecurityProtocol:       "PLAINTEXT",
		// Explicit override of a derived default (bootstrap.servers).
		Configs: map[string]string{"bootstrap.servers": "link-reachable:29092"},
	})
	require.NoError(t, err)

	// The override wins, and the key must appear exactly once (no duplicate).
	configs := configMapFromBody(t, gotBody)
	require.Equal(t, "link-reachable:29092", configs["bootstrap.servers"])
	count := 0
	for _, e := range gotBody["configs"].([]any) {
		if e.(map[string]any)["name"].(string) == "bootstrap.servers" {
			count++
		}
	}
	require.Equal(t, 1, count, "bootstrap.servers must not be duplicated when overridden")
}

func TestCreateClusterLink_SaslJaasRequiredWithMechanism(t *testing.T) {
	svc := NewConfluentCloudService(http.DefaultClient)
	cfg := Config{RestEndpoint: "http://unused.invalid", ClusterID: "d", LinkName: "l"}
	// SaslMechanism set without SaslJaasConfig must fail fast, before any HTTP call.
	err := svc.CreateClusterLink(context.Background(), cfg, CreateClusterLinkRequest{
		SourceClusterID:  "s",
		SecurityProtocol: "SASL_SSL",
		SaslMechanism:    "PLAIN",
	})
	require.ErrorContains(t, err, "SaslJaasConfig is required")
}

func TestCreateClusterLink_TLSMaterialEmitsSSLConfigs(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	svc := NewConfluentCloudService(srv.Client())
	cfg := Config{RestEndpoint: srv.URL, ClusterID: "d", LinkName: "l", APIKey: "a", APISecret: "b"}
	err := svc.CreateClusterLink(context.Background(), cfg, CreateClusterLinkRequest{
		SourceClusterID:        "s",
		SourceBootstrapServers: []string{"source:29095"},
		SecurityProtocol:       "SSL",
		SourceTLS: &SourceTLSMaterial{
			CACertPEM:     "CA-PEM",
			ClientCertPEM: "CERT-PEM",
			ClientKeyPEM:  "KEY-PEM",
		},
	})
	require.NoError(t, err)
	configs := configMapFromBody(t, gotBody)
	require.Equal(t, "PEM", configs["ssl.truststore.type"])
	require.Equal(t, "CA-PEM", configs["ssl.truststore.certificates"])
	require.Equal(t, "PEM", configs["ssl.keystore.type"])
	require.Equal(t, "CERT-PEM", configs["ssl.keystore.certificate.chain"])
	require.Equal(t, "KEY-PEM", configs["ssl.keystore.key"])
}

func TestCreateClusterLink_TLSMaterialCAOnly(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	svc := NewConfluentCloudService(srv.Client())
	cfg := Config{RestEndpoint: srv.URL, ClusterID: "d", LinkName: "l", APIKey: "a", APISecret: "b"}
	err := svc.CreateClusterLink(context.Background(), cfg, CreateClusterLinkRequest{
		SourceClusterID:        "s",
		SourceBootstrapServers: []string{"source:29095"},
		SecurityProtocol:       "SSL",
		SourceTLS:              &SourceTLSMaterial{CACertPEM: "CA-PEM"},
	})
	require.NoError(t, err)
	configs := configMapFromBody(t, gotBody)
	require.Equal(t, "CA-PEM", configs["ssl.truststore.certificates"])
	require.NotContains(t, configs, "ssl.keystore.key", "no keystore for one-way SSL")
	require.NotContains(t, configs, "ssl.keystore.certificate.chain")
}

func TestCreateClusterLink_KeystorePairRequired(t *testing.T) {
	svc := NewConfluentCloudService(http.DefaultClient)
	cfg := Config{RestEndpoint: "http://unused.invalid", ClusterID: "d", LinkName: "l"}
	err := svc.CreateClusterLink(context.Background(), cfg, CreateClusterLinkRequest{
		SourceClusterID: "s", SecurityProtocol: "SSL",
		SourceTLS: &SourceTLSMaterial{CACertPEM: "CA", ClientCertPEM: "CERT"}, // no key
	})
	require.ErrorContains(t, err, "ClientCertPEM and ClientKeyPEM must both be set")
}

func TestCreateMirrorTopic_RequestShape(t *testing.T) {
	clusterID := "cid"
	linkName := "lk"

	var gotPath, gotMethod string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	svc := NewConfluentCloudService(srv.Client())
	cfg := Config{RestEndpoint: srv.URL, ClusterID: clusterID, LinkName: linkName, APIKey: "a", APISecret: "b"}

	err := svc.CreateMirrorTopic(context.Background(), cfg, "orders", "dc-orders")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/kafka/v3/clusters/cid/links/lk/mirrors", gotPath)
	require.Equal(t, "orders", gotBody["source_topic_name"])
	require.Equal(t, "dc-orders", gotBody["mirror_topic_name"])
}

func TestCreateMirrorTopic_NoContentIsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	svc := NewConfluentCloudService(srv.Client())
	cfg := Config{RestEndpoint: srv.URL, ClusterID: "cid", LinkName: "lk", APIKey: "a", APISecret: "b"}

	err := svc.CreateMirrorTopic(context.Background(), cfg, "orders", "dc-orders")
	require.NoError(t, err)
}

func TestCreateMirrorTopic_Unauthorized(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))

		svc := NewConfluentCloudService(srv.Client())
		cfg := Config{RestEndpoint: srv.URL, ClusterID: "cid", LinkName: "lk", APIKey: "a", APISecret: "b"}

		err := svc.CreateMirrorTopic(context.Background(), cfg, "orders", "dc-orders")
		require.Error(t, err)
		require.Contains(t, err.Error(), "authentication failed")
		srv.Close()
	}
}

func TestCreateMirrorTopic_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error_code":404,"message":"link not found"}`))
	}))
	defer srv.Close()

	svc := NewConfluentCloudService(srv.Client())
	cfg := Config{RestEndpoint: srv.URL, ClusterID: "cid", LinkName: "lk", APIKey: "a", APISecret: "b"}

	err := svc.CreateMirrorTopic(context.Background(), cfg, "orders", "dc-orders")
	require.Error(t, err)
	require.Contains(t, err.Error(), "404")
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
