package self_managed_connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/redact"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testArn = "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123"

type mockConnectClient struct {
	listFn   func() ([]string, error)
	configFn func(name string) (map[string]any, error)
	statusFn func(name string) (map[string]any, error)
}

func (m *mockConnectClient) ListConnectors() ([]string, error) { return m.listFn() }
func (m *mockConnectClient) GetConnectorConfig(n string) (map[string]any, error) {
	return m.configFn(n)
}
func (m *mockConnectClient) GetConnectorStatus(n string) (map[string]any, error) {
	return m.statusFn(n)
}

func stateWithCluster() *types.State {
	return &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{
					Name:     "us-east-1",
					Clusters: []types.DiscoveredCluster{{Arn: testArn, Name: "test-cluster"}},
				},
			},
		},
	}
}

func newScannerWithClient(t *testing.T, st *types.State, arn string, client ConnectAPIClient) (*SelfManagedConnectorsScanner, string) {
	t.Helper()
	stateFile := filepath.Join(t.TempDir(), "kcp-state.json")
	return &SelfManagedConnectorsScanner{
		StateFile:  stateFile,
		State:      st,
		SourceType: types.SourceTypeMSK,
		ClusterArn: arn,
		client:     client,
	}, stateFile
}

const testOSKID = "production-kafka"

func stateWithOSKCluster() *types.State {
	return &types.State{
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{{ID: testOSKID}},
		},
	}
}

func TestScanner_RedactsConfigBeforePersist(t *testing.T) {
	client := &mockConnectClient{
		listFn: func() ([]string, error) { return []string{"pg-sink"}, nil },
		configFn: func(string) (map[string]any, error) {
			return map[string]any{"database.password": "hunter2", "tasks.max": "3"}, nil
		},
		statusFn: func(string) (map[string]any, error) {
			return map[string]any{"connector": map[string]any{"state": "RUNNING"}}, nil
		},
	}
	st := stateWithCluster()
	scanner, stateFile := newScannerWithClient(t, st, testArn, client)

	require.NoError(t, scanner.Run())

	cluster, err := st.GetClusterByArn(testArn)
	require.NoError(t, err)
	require.NotNil(t, cluster.KafkaAdminClientInformation.SelfManagedConnectors)
	conns := cluster.KafkaAdminClientInformation.SelfManagedConnectors.Connectors
	require.Len(t, conns, 1)
	assert.Equal(t, redact.Placeholder, conns[0].Config["database.password"], "secret redacted before persist")
	assert.Equal(t, "3", conns[0].Config["tasks.max"], "benign config preserved")
	assert.Equal(t, "RUNNING", conns[0].State, "connector state extracted from status")

	data, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "hunter2", "raw secret must not appear in the persisted state file")
}

func TestScanner_NoConnectors(t *testing.T) {
	client := &mockConnectClient{
		listFn:   func() ([]string, error) { return []string{}, nil },
		configFn: func(string) (map[string]any, error) { return nil, nil },
		statusFn: func(string) (map[string]any, error) { return nil, nil },
	}
	st := stateWithCluster()
	scanner, _ := newScannerWithClient(t, st, testArn, client)
	require.NoError(t, scanner.Run(), "empty connector list is not an error")
}

func TestScanner_ClusterArnNotFound(t *testing.T) {
	client := &mockConnectClient{
		listFn:   func() ([]string, error) { return []string{"pg-sink"}, nil },
		configFn: func(string) (map[string]any, error) { return map[string]any{}, nil },
		statusFn: func(string) (map[string]any, error) { return map[string]any{}, nil },
	}
	st := stateWithCluster()
	scanner, _ := newScannerWithClient(t, st, "arn:aws:kafka:us-east-1:999:cluster/missing/x", client)
	require.Error(t, scanner.Run(), "cluster ARN not present in state is an error")
}

func TestScanner_DoesNotLogRawSecret(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	client := &mockConnectClient{
		listFn:   func() ([]string, error) { return []string{"pg-sink"}, nil },
		configFn: func(string) (map[string]any, error) { return map[string]any{"database.password": "hunter2"}, nil },
		statusFn: func(string) (map[string]any, error) { return map[string]any{}, nil },
	}
	st := stateWithCluster()
	scanner, _ := newScannerWithClient(t, st, testArn, client)
	require.NoError(t, scanner.Run())
	assert.NotContains(t, buf.String(), "hunter2", "raw secret must never be logged")
}

// --- getConnectorDetails: worker_id -> ConnectHost capture (restored from 6a99cb8f) ---

func TestScanner_GetConnectorDetails_CapturesWorkerID(t *testing.T) {
	client := &mockConnectClient{
		configFn: func(string) (map[string]any, error) {
			return map[string]any{"connector.class": "test.Connector"}, nil
		},
		statusFn: func(string) (map[string]any, error) {
			return map[string]any{"connector": map[string]any{"state": "RUNNING", "worker_id": "connect-worker-1:8083"}}, nil
		},
	}
	s := &SelfManagedConnectorsScanner{client: client}
	conn, _, err := s.getConnectorDetails("c1")
	require.NoError(t, err)
	assert.Equal(t, "RUNNING", conn.State)
	assert.Equal(t, "connect-worker-1:8083", conn.ConnectHost, "ConnectHost populated from connector.worker_id")
}

func TestScanner_GetConnectorDetails_MissingWorkerID(t *testing.T) {
	client := &mockConnectClient{
		configFn: func(string) (map[string]any, error) { return map[string]any{"connector.class": "test.Connector"}, nil },
		statusFn: func(string) (map[string]any, error) {
			return map[string]any{"connector": map[string]any{"state": "RUNNING"}}, nil
		},
	}
	s := &SelfManagedConnectorsScanner{client: client}
	conn, _, err := s.getConnectorDetails("c1")
	require.NoError(t, err)
	assert.Equal(t, "", conn.ConnectHost, "absent worker_id leaves ConnectHost empty")
}

func TestScanner_GetConnectorDetails_NonStringWorkerID(t *testing.T) {
	client := &mockConnectClient{
		configFn: func(string) (map[string]any, error) { return map[string]any{"connector.class": "test.Connector"}, nil },
		statusFn: func(string) (map[string]any, error) {
			return map[string]any{"connector": map[string]any{"state": "RUNNING", "worker_id": nil}}, nil
		},
	}
	s := &SelfManagedConnectorsScanner{client: client}
	conn, _, err := s.getConnectorDetails("c1")
	require.NoError(t, err)
	assert.Equal(t, "", conn.ConnectHost, "non-string worker_id is ignored")
}

// --- updateStateWithConnectors: MSK/OSK routing (restored from 6a99cb8f) ---

func TestScanner_UpdateStateWithConnectors_OSK_Success(t *testing.T) {
	st := stateWithOSKCluster()
	s := &SelfManagedConnectorsScanner{State: st, SourceType: types.SourceTypeOSK, ClusterID: testOSKID}
	require.NoError(t, s.updateStateWithConnectors([]types.SelfManagedConnector{{Name: "c1"}}))

	cl, err := st.GetOSKClusterByID(testOSKID)
	require.NoError(t, err)
	require.NotNil(t, cl.KafkaAdminClientInformation.SelfManagedConnectors)
	assert.Len(t, cl.KafkaAdminClientInformation.SelfManagedConnectors.Connectors, 1)
}

func TestScanner_UpdateStateWithConnectors_OSK_NotFound(t *testing.T) {
	s := &SelfManagedConnectorsScanner{State: stateWithOSKCluster(), SourceType: types.SourceTypeOSK, ClusterID: "no-such-cluster"}
	err := s.updateStateWithConnectors([]types.SelfManagedConnector{{Name: "c1"}})
	require.Error(t, err)
}

func TestScanner_UpdateStateWithConnectors_UnsupportedSourceType(t *testing.T) {
	s := &SelfManagedConnectorsScanner{State: stateWithCluster(), SourceType: types.SourceType("bogus"), ClusterArn: testArn}
	err := s.updateStateWithConnectors([]types.SelfManagedConnector{{Name: "c1"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported source type")
}

// --- updateStateWithConnectMetrics: MSK/OSK routing (restored from 6a99cb8f) ---

func TestScanner_UpdateStateWithConnectMetrics_MSK_Success(t *testing.T) {
	st := stateWithCluster()
	cl, err := st.GetClusterByArn(testArn)
	require.NoError(t, err)
	cl.KafkaAdminClientInformation.SetSelfManagedConnectors([]types.SelfManagedConnector{{Name: "c1"}})

	s := &SelfManagedConnectorsScanner{State: st, SourceType: types.SourceTypeMSK, ClusterArn: testArn}
	metrics := &types.ConnectClusterMetrics{}
	require.NoError(t, s.updateStateWithConnectMetrics(metrics))

	cl2, _ := st.GetClusterByArn(testArn)
	assert.Same(t, metrics, cl2.KafkaAdminClientInformation.SelfManagedConnectors.Metrics)
}

func TestScanner_UpdateStateWithConnectMetrics_MSK_NoConnectors(t *testing.T) {
	s := &SelfManagedConnectorsScanner{State: stateWithCluster(), SourceType: types.SourceTypeMSK, ClusterArn: testArn}
	err := s.updateStateWithConnectMetrics(&types.ConnectClusterMetrics{})
	require.Error(t, err, "metrics with no prior connectors in state is an error")
}

func TestScanner_UpdateStateWithConnectMetrics_MSK_ClusterNotFound(t *testing.T) {
	s := &SelfManagedConnectorsScanner{State: stateWithCluster(), SourceType: types.SourceTypeMSK, ClusterArn: "arn:aws:kafka:us-east-1:999:cluster/missing/x"}
	err := s.updateStateWithConnectMetrics(&types.ConnectClusterMetrics{})
	require.Error(t, err)
}

func TestScanner_UpdateStateWithConnectMetrics_OSK_Success(t *testing.T) {
	st := stateWithOSKCluster()
	cl, err := st.GetOSKClusterByID(testOSKID)
	require.NoError(t, err)
	cl.KafkaAdminClientInformation.SetSelfManagedConnectors([]types.SelfManagedConnector{{Name: "c1"}})

	s := &SelfManagedConnectorsScanner{State: st, SourceType: types.SourceTypeOSK, ClusterID: testOSKID}
	metrics := &types.ConnectClusterMetrics{}
	require.NoError(t, s.updateStateWithConnectMetrics(metrics))

	cl2, _ := st.GetOSKClusterByID(testOSKID)
	assert.Same(t, metrics, cl2.KafkaAdminClientInformation.SelfManagedConnectors.Metrics)
}

// --- collectConnectMetrics: guard rails (no network) ---

func TestScanner_CollectConnectMetrics_NilCreds(t *testing.T) {
	s := &SelfManagedConnectorsScanner{metricsSource: "jolokia", metricsClusterCreds: nil}
	_, err := s.collectConnectMetrics(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "credentials")
}

func TestScanner_CollectConnectMetrics_UnsupportedSource(t *testing.T) {
	s := &SelfManagedConnectorsScanner{metricsSource: "bogus", metricsClusterCreds: &types.OSKClusterAuth{}}
	_, err := s.collectConnectMetrics(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported metrics source")
}

// --- Run: OSK end-to-end + partial failure ---

func TestScanner_Run_OSK_PersistsConnectors(t *testing.T) {
	client := &mockConnectClient{
		listFn:   func() ([]string, error) { return []string{"c1"}, nil },
		configFn: func(string) (map[string]any, error) { return map[string]any{"tasks.max": "1"}, nil },
		statusFn: func(string) (map[string]any, error) {
			return map[string]any{"connector": map[string]any{"state": "RUNNING"}}, nil
		},
	}
	st := stateWithOSKCluster()
	stateFile := filepath.Join(t.TempDir(), "kcp-state.json")
	s := &SelfManagedConnectorsScanner{StateFile: stateFile, State: st, SourceType: types.SourceTypeOSK, ClusterID: testOSKID, client: client}
	require.NoError(t, s.Run())

	cl, err := st.GetOSKClusterByID(testOSKID)
	require.NoError(t, err)
	require.NotNil(t, cl.KafkaAdminClientInformation.SelfManagedConnectors)
	assert.Len(t, cl.KafkaAdminClientInformation.SelfManagedConnectors.Connectors, 1)
}

func TestScanner_Run_PartialFailure(t *testing.T) {
	client := &mockConnectClient{
		listFn: func() ([]string, error) { return []string{"good", "bad", "good2"}, nil },
		configFn: func(name string) (map[string]any, error) {
			if name == "bad" {
				return nil, errors.New("config fetch failed")
			}
			return map[string]any{"tasks.max": "1"}, nil
		},
		statusFn: func(string) (map[string]any, error) {
			return map[string]any{"connector": map[string]any{"state": "RUNNING"}}, nil
		},
	}
	st := stateWithCluster()
	s, _ := newScannerWithClient(t, st, testArn, client)
	require.NoError(t, s.Run(), "a single connector failure must not fail the whole scan")

	cl, _ := st.GetClusterByArn(testArn)
	require.NotNil(t, cl.KafkaAdminClientInformation.SelfManagedConnectors)
	assert.Len(t, cl.KafkaAdminClientInformation.SelfManagedConnectors.Connectors, 2, "only the healthy connectors are recorded")
}

// A metrics-collection failure must never abort the connector scan: connectors
// are already persisted and the metrics error is logged as a warning only.
func TestScanner_Run_MetricsFailureDoesNotAbortScan(t *testing.T) {
	client := &mockConnectClient{
		listFn:   func() ([]string, error) { return []string{"c1"}, nil },
		configFn: func(string) (map[string]any, error) { return map[string]any{"tasks.max": "1"}, nil },
		statusFn: func(string) (map[string]any, error) {
			return map[string]any{"connector": map[string]any{"state": "RUNNING"}}, nil
		},
	}
	st := stateWithCluster()
	stateFile := filepath.Join(t.TempDir(), "kcp-state.json")
	// metricsSource is set but no creds are provided, so collectConnectMetrics errors.
	s := &SelfManagedConnectorsScanner{
		StateFile: stateFile, State: st, SourceType: types.SourceTypeMSK, ClusterArn: testArn,
		client: client, metricsSource: "jolokia", metricsClusterCreds: nil,
	}
	require.NoError(t, s.Run(), "metrics collection failure must not abort the scan")

	cl, _ := st.GetClusterByArn(testArn)
	require.NotNil(t, cl.KafkaAdminClientInformation.SelfManagedConnectors)
	assert.Len(t, cl.KafkaAdminClientInformation.SelfManagedConnectors.Connectors, 1, "connectors persisted despite metrics failure")
}

func TestScanner_UpdateStateWithConnectMetrics_OSK_NoConnectors(t *testing.T) {
	s := &SelfManagedConnectorsScanner{State: stateWithOSKCluster(), SourceType: types.SourceTypeOSK, ClusterID: testOSKID}
	require.Error(t, s.updateStateWithConnectMetrics(&types.ConnectClusterMetrics{}))
}

func TestScanner_UpdateStateWithConnectMetrics_OSK_ClusterNotFound(t *testing.T) {
	s := &SelfManagedConnectorsScanner{State: stateWithOSKCluster(), SourceType: types.SourceTypeOSK, ClusterID: "no-such-cluster"}
	require.Error(t, s.updateStateWithConnectMetrics(&types.ConnectClusterMetrics{}))
}

func TestScanner_CollectConnectJolokiaMetrics_NoJolokiaConfig(t *testing.T) {
	s := &SelfManagedConnectorsScanner{metricsSource: "jolokia", metricsDuration: "5m", metricsInterval: "10s"}
	_, err := s.collectConnectJolokiaMetrics(context.Background(), types.OSKClusterAuth{ID: "c"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jolokia")
}

func TestScanner_CollectConnectPrometheusMetrics_NoPrometheusConfig(t *testing.T) {
	s := &SelfManagedConnectorsScanner{metricsSource: "prometheus", metricsRange: "7d"}
	_, err := s.collectConnectPrometheusMetrics(context.Background(), types.OSKClusterAuth{ID: "c"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prometheus")
}

func TestHTTPConnectClient_BasicAuthSendsBasicAuth(t *testing.T) {
	var gotUser, gotPass string
	var gotOK bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, gotOK = r.BasicAuth()
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := &HTTPConnectClient{
		baseURL:    srv.URL,
		httpClient: srv.Client(),
		authMethod: types.ConnectAuthMethodBasicAuth,
		basicAuth:  types.ConnectBasicAuth{Username: "u", Password: "p"},
	}
	_, err := c.ListConnectors()
	require.NoError(t, err)
	assert.True(t, gotOK, "basic auth header sent for Basic auth")
	assert.Equal(t, "u", gotUser)
	assert.Equal(t, "p", gotPass)
}

func TestHTTPConnectClient_UnauthenticatedSendsNoAuth(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hadAuth = r.Header.Get("Authorization") != ""
		_, _ = w.Write([]byte(`["a","b"]`))
	}))
	defer srv.Close()

	c := &HTTPConnectClient{baseURL: srv.URL, httpClient: srv.Client(), authMethod: types.ConnectAuthMethodUnauthenticated}
	names, err := c.ListConnectors()
	require.NoError(t, err)
	assert.False(t, hadAuth, "no auth header for unauthenticated")
	assert.Equal(t, []string{"a", "b"}, names)
}

func TestHTTPConnectClient_Non200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := &HTTPConnectClient{baseURL: srv.URL, httpClient: srv.Client(), authMethod: types.ConnectAuthMethodUnauthenticated}
	_, err := c.ListConnectors()
	require.Error(t, err, "non-200 REST response is an error")
}

func TestNewScanner_TLSCertLoadFailureReturnsError(t *testing.T) {
	// A bad TLS cert path must surface as an error from the constructor, not a
	// later nil-pointer panic when the scanner runs.
	opts := SelfManagedConnectorsScannerOpts{
		ConnectRestURL: "http://localhost:8083",
		AuthMethod:     types.ConnectAuthMethodTls,
		TlsAuth: types.ConnectTlsAuth{
			CACert:     filepath.Join(t.TempDir(), "missing-ca.pem"),
			ClientCert: filepath.Join(t.TempDir(), "missing-cert.pem"),
			ClientKey:  filepath.Join(t.TempDir(), "missing-key.pem"),
		},
	}
	_, err := NewSelfManagedConnectorsScanner(opts)
	require.Error(t, err, "bad TLS cert path must return an error, not panic later")
}

func TestCmd_MutuallyExclusiveAuthMethods(t *testing.T) {
	cmd := NewScanSelfManagedConnectorsCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--state-file", "s", "--connect-rest-url", "u", "--cluster-id", "a",
		"--use-tls", "--use-basic-auth",
	})
	require.Error(t, cmd.Execute(), "two auth methods must be rejected")
}

func TestCmd_RequiresAnAuthMethod(t *testing.T) {
	cmd := NewScanSelfManagedConnectorsCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--state-file", "s", "--connect-rest-url", "u", "--cluster-id", "a"})
	require.Error(t, cmd.Execute(), "an auth method is required")
}

// --- U2a: updateStateWithConnectMetrics (MSK-only attachment) ---

func TestUpdateStateWithConnectMetrics_NoConnectors_Errors(t *testing.T) {
	// The cluster has no SelfManagedConnectors object yet — attaching metrics
	// must fail clearly rather than panic on a nil dereference.
	scanner := &SelfManagedConnectorsScanner{State: stateWithCluster(), SourceType: types.SourceTypeMSK, ClusterArn: testArn}
	err := scanner.updateStateWithConnectMetrics(&types.ConnectClusterMetrics{})
	require.Error(t, err)
}

func TestUpdateStateWithConnectMetrics_AttachesMetrics(t *testing.T) {
	st := stateWithCluster()
	cluster, err := st.GetClusterByArn(testArn)
	require.NoError(t, err)
	cluster.KafkaAdminClientInformation.SetSelfManagedConnectors([]types.SelfManagedConnector{{Name: "pg-sink"}})

	scanner := &SelfManagedConnectorsScanner{State: st, SourceType: types.SourceTypeMSK, ClusterArn: testArn}
	m := &types.ConnectClusterMetrics{}
	require.NoError(t, scanner.updateStateWithConnectMetrics(m))

	got, err := st.GetClusterByArn(testArn)
	require.NoError(t, err)
	require.Same(t, m, got.KafkaAdminClientInformation.SelfManagedConnectors.Metrics, "metrics attached to the connectors object")
}

// --- U2b: collectConnectMetrics dispatch + collector guards ---

func TestCollectConnectMetrics_NilCreds_Errors(t *testing.T) {
	// metricsSource is set but no cluster credentials were resolved.
	scanner := &SelfManagedConnectorsScanner{metricsSource: "jolokia"}
	_, err := scanner.collectConnectMetrics(context.Background())
	require.Error(t, err)
}

func TestCollectConnectMetrics_MissingJolokiaSection_Errors(t *testing.T) {
	// jolokia requested but the resolved creds carry no jolokia config.
	scanner := &SelfManagedConnectorsScanner{
		metricsSource:       "jolokia",
		metricsDuration:     "5m",
		metricsInterval:     "10s",
		metricsClusterCreds: &types.OSKClusterAuth{ID: testArn},
	}
	_, err := scanner.collectConnectMetrics(context.Background())
	require.Error(t, err)
}

func TestCollectConnectMetrics_MissingPrometheusSection_Errors(t *testing.T) {
	scanner := &SelfManagedConnectorsScanner{
		metricsSource:       "prometheus",
		metricsRange:        "7d",
		metricsClusterCreds: &types.OSKClusterAuth{ID: testArn},
	}
	_, err := scanner.collectConnectMetrics(context.Background())
	require.Error(t, err)
}

// Abuse (R10): a never-responding endpoint must not hang the scan. The reused
// Jolokia client honors ctx (NewRequestWithContext) and also has a 10s client
// timeout, so a short ctx deadline returns promptly with an error rather than
// blocking indefinitely.
func TestCollectConnectMetrics_CtxHonored_NoHang(t *testing.T) {
	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hang
	}))
	defer srv.Close()
	defer close(hang)

	scanner := &SelfManagedConnectorsScanner{
		State:           stateWithCluster(),
		ClusterArn:      testArn,
		metricsSource:   "jolokia",
		metricsDuration: "5m",
		metricsInterval: "10s",
		metricsClusterCreds: &types.OSKClusterAuth{
			ID:      testArn,
			Jolokia: &types.JolokiaConfig{Endpoints: []string{srv.URL}},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := scanner.collectConnectMetrics(ctx)
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err, "ctx deadline must surface as an error")
		// The 100ms ctx should end collection well under 1s; the outer guard
		// (2s) only has to exceed this bound so a regression to a slow return
		// fails the assertion rather than the t.Fatal.
		require.Less(t, time.Since(start), 1*time.Second, "must not block on the 10s client timeout")
	case <-time.After(2 * time.Second):
		t.Fatal("collectConnectMetrics hung past the ctx deadline")
	}
}

// --- U2c: Run() metrics wiring (integration via mock endpoints) ---

func connectMockClient() *mockConnectClient {
	return &mockConnectClient{
		listFn:   func() ([]string, error) { return []string{"pg-sink"}, nil },
		configFn: func(string) (map[string]any, error) { return map[string]any{"tasks.max": "3"}, nil },
		statusFn: func(string) (map[string]any, error) {
			return map[string]any{"connector": map[string]any{"state": "RUNNING"}}, nil
		},
	}
}

// connectJolokiaHandler serves canned Jolokia responses for the Connect worker
// metric MBeans (ConnectMetricDefinitions), matching on the metric-name segment
// of the request path.
func connectJolokiaHandler(w http.ResponseWriter, r *http.Request) {
	p := r.URL.String()
	var resp map[string]any
	switch {
	case strings.Contains(p, "connect-worker-metrics"):
		resp = map[string]any{"status": 200, "value": map[string]any{"connector-count": 5.0, "task-count": 10.0}}
	case strings.Contains(p, "source-task-metrics"):
		resp = map[string]any{"status": 200, "value": map[string]any{
			"kafka.connect:type=source-task-metrics,connector=c,task=0": map[string]any{
				"source-record-write-rate": 100.0, "source-record-poll-rate": 120.0,
			},
		}}
	case strings.Contains(p, "connect-metrics"):
		resp = map[string]any{"status": 200, "value": map[string]any{
			"kafka.connect:client-id=c1,type=connect-metrics": map[string]any{
				"incoming-byte-rate": 1000.0, "outgoing-byte-rate": 500.0,
				"connection-count": 3.0, "request-rate": 10.0,
			},
		}}
	default:
		resp = map[string]any{"status": 404, "error": "not found"}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// connectPromHandler serves a canned matrix result for any query_range request.
func connectPromHandler(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "matrix",
			"result": []map[string]any{
				{"metric": map[string]string{}, "values": [][]any{
					{1710000000.0, "5"},
					{1710003600.0, "6"},
				}},
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func TestRun_JolokiaMetricsAttached(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(connectJolokiaHandler))
	defer srv.Close()

	st := stateWithCluster()
	scanner, _ := newScannerWithClient(t, st, testArn, connectMockClient())
	scanner.metricsSource = "jolokia"
	scanner.metricsDuration = "1s"
	scanner.metricsInterval = "100ms"
	scanner.metricsClusterCreds = &types.OSKClusterAuth{
		ID:      testArn,
		Jolokia: &types.JolokiaConfig{Endpoints: []string{srv.URL}},
	}

	require.NoError(t, scanner.Run())

	cluster, err := st.GetClusterByArn(testArn)
	require.NoError(t, err)
	conns := cluster.KafkaAdminClientInformation.SelfManagedConnectors
	require.NotNil(t, conns)
	require.Len(t, conns.Connectors, 1)
	require.NotNil(t, conns.Metrics, "Connect metrics collected and attached")
	require.Contains(t, conns.Metrics.Aggregates, "connector-count")
	// End-to-end: the boundary mapper records the producing backend (U3/R2).
	require.Equal(t, types.MetricBackendJolokia, conns.Metrics.Metadata.MetricsSource, "jolokia run records metrics_source")
}

func TestRun_PrometheusMetricsAttached(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(connectPromHandler))
	defer srv.Close()

	st := stateWithCluster()
	scanner, _ := newScannerWithClient(t, st, testArn, connectMockClient())
	scanner.metricsSource = "prometheus"
	scanner.metricsRange = "1d"
	scanner.metricsClusterCreds = &types.OSKClusterAuth{
		ID:         testArn,
		Prometheus: &types.PrometheusConfig{URL: srv.URL},
	}

	require.NoError(t, scanner.Run())

	cluster, err := st.GetClusterByArn(testArn)
	require.NoError(t, err)
	conns := cluster.KafkaAdminClientInformation.SelfManagedConnectors
	require.NotNil(t, conns)
	require.NotNil(t, conns.Metrics, "Connect metrics collected and attached")
	require.Contains(t, conns.Metrics.Aggregates, "connector-count")
	// End-to-end: the boundary mapper records the producing backend (U3/R2).
	require.Equal(t, types.MetricBackendPrometheus, conns.Metrics.Metadata.MetricsSource, "prometheus run records metrics_source")
}

// R1: with no --metrics the scan behaves exactly as before — connectors
// persisted, Metrics stays nil.
func TestRun_NoMetrics_MetricsNil(t *testing.T) {
	st := stateWithCluster()
	scanner, _ := newScannerWithClient(t, st, testArn, connectMockClient())

	require.NoError(t, scanner.Run())

	cluster, err := st.GetClusterByArn(testArn)
	require.NoError(t, err)
	require.NotNil(t, cluster.KafkaAdminClientInformation.SelfManagedConnectors)
	require.Nil(t, cluster.KafkaAdminClientInformation.SelfManagedConnectors.Metrics)
}

// R10: an unreachable metrics endpoint must not fail the scan; connectors persist.
func TestRun_JolokiaUnreachable_NonFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(connectJolokiaHandler))
	addr := srv.URL
	srv.Close() // endpoint is now unreachable

	st := stateWithCluster()
	scanner, stateFile := newScannerWithClient(t, st, testArn, connectMockClient())
	scanner.metricsSource = "jolokia"
	scanner.metricsDuration = "300ms"
	scanner.metricsInterval = "100ms"
	scanner.metricsClusterCreds = &types.OSKClusterAuth{
		ID:      testArn,
		Jolokia: &types.JolokiaConfig{Endpoints: []string{addr}},
	}

	require.NoError(t, scanner.Run(), "metrics failure must not fail the scan")

	data, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	require.Contains(t, string(data), "pg-sink", "connectors persisted despite metrics failure")
}

// R10: a malformed metrics response must not panic or fail the scan.
func TestRun_JolokiaMalformed_NonFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("this is not json"))
	}))
	defer srv.Close()

	st := stateWithCluster()
	scanner, stateFile := newScannerWithClient(t, st, testArn, connectMockClient())
	scanner.metricsSource = "jolokia"
	scanner.metricsDuration = "300ms"
	scanner.metricsInterval = "100ms"
	scanner.metricsClusterCreds = &types.OSKClusterAuth{
		ID:      testArn,
		Jolokia: &types.JolokiaConfig{Endpoints: []string{srv.URL}},
	}

	require.NoError(t, scanner.Run())

	data, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	require.Contains(t, string(data), "pg-sink", "connectors persisted despite malformed metrics")
}

// Abuse (R11): on a metrics failure, no credential value may appear in logs.
func TestRun_MetricsFailure_NoSecretInLogs(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	srv := httptest.NewServer(http.HandlerFunc(connectJolokiaHandler))
	addr := srv.URL
	srv.Close() // unreachable → collection fails, warnings logged

	const secret = "topsecret-jolokia-pw"
	st := stateWithCluster()
	scanner, _ := newScannerWithClient(t, st, testArn, connectMockClient())
	scanner.metricsSource = "jolokia"
	scanner.metricsDuration = "300ms"
	scanner.metricsInterval = "100ms"
	scanner.metricsClusterCreds = &types.OSKClusterAuth{
		ID: testArn,
		Jolokia: &types.JolokiaConfig{
			Endpoints: []string{addr},
			Auth:      &types.JolokiaAuthConfig{Username: "monitor", Password: secret},
		},
	}

	require.NoError(t, scanner.Run())
	require.NotContains(t, buf.String(), secret, "credential value must never appear in logs (R11)")
}

// Abuse (R12): TLS config must be honored — collection over an HTTPS endpoint
// succeeds only if WithJolokiaTLS is applied (no silent plain-HTTP downgrade).
func TestRun_JolokiaTLSHonored(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(connectJolokiaHandler))
	defer srv.Close()

	st := stateWithCluster()
	scanner, _ := newScannerWithClient(t, st, testArn, connectMockClient())
	scanner.metricsSource = "jolokia"
	scanner.metricsDuration = "1s"
	scanner.metricsInterval = "100ms"
	scanner.metricsClusterCreds = &types.OSKClusterAuth{
		ID: testArn,
		Jolokia: &types.JolokiaConfig{
			Endpoints: []string{srv.URL},
			TLS:       &types.JolokiaTLSConfig{InsecureSkipVerify: true},
		},
	}

	require.NoError(t, scanner.Run())

	cluster, err := st.GetClusterByArn(testArn)
	require.NoError(t, err)
	conns := cluster.KafkaAdminClientInformation.SelfManagedConnectors
	require.NotNil(t, conns.Metrics, "HTTPS collection succeeded ⇒ TLS option applied")
	require.Contains(t, conns.Metrics.Aggregates, "connector-count")
}

// R9 / AE3 (scanner path): a metrics run followed by a re-run WITHOUT --metrics
// must leave previously-collected metrics intact through a real persist→reload
// cycle. The scanner path preserves via SetSelfManagedConnectors (it loads full
// state and mutates in place), distinct from the mergeSelfManagedConnectors path
// exercised in internal/types.
func TestRun_ReRunWithoutMetrics_PreservesMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(connectJolokiaHandler))
	defer srv.Close()

	stateFile := filepath.Join(t.TempDir(), "kcp-state.json")
	require.NoError(t, stateWithCluster().PersistStateFile(stateFile))

	// Run 1: collect metrics and persist.
	st1, err := types.NewStateFromFile(stateFile)
	require.NoError(t, err)
	scanner1 := &SelfManagedConnectorsScanner{
		StateFile: stateFile, State: st1, SourceType: types.SourceTypeMSK, ClusterArn: testArn, client: connectMockClient(),
		metricsSource: "jolokia", metricsDuration: "500ms", metricsInterval: "100ms",
		metricsClusterCreds: &types.OSKClusterAuth{ID: testArn, Jolokia: &types.JolokiaConfig{Endpoints: []string{srv.URL}}},
	}
	require.NoError(t, scanner1.Run())

	st2, err := types.NewStateFromFile(stateFile)
	require.NoError(t, err)
	c2, err := st2.GetClusterByArn(testArn)
	require.NoError(t, err)
	require.NotNil(t, c2.KafkaAdminClientInformation.SelfManagedConnectors.Metrics, "run 1 collected metrics")

	// Run 2: re-scan WITHOUT --metrics on the reloaded state.
	scanner2 := &SelfManagedConnectorsScanner{
		StateFile: stateFile, State: st2, SourceType: types.SourceTypeMSK, ClusterArn: testArn, client: connectMockClient(),
	}
	require.NoError(t, scanner2.Run())

	st3, err := types.NewStateFromFile(stateFile)
	require.NoError(t, err)
	c3, err := st3.GetClusterByArn(testArn)
	require.NoError(t, err)
	require.NotNil(t, c3.KafkaAdminClientInformation.SelfManagedConnectors.Metrics,
		"metrics preserved across a re-run without --metrics (R9/AE3)")
}

// Documents current behavior: a cluster with zero connectors returns early
// (before the metrics block), so --metrics has nothing to attach to and is
// silently skipped. Guards against an accidental nil-deref or panic on that path.
func TestRun_ZeroConnectorsWithMetrics_SkipsMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(connectJolokiaHandler))
	defer srv.Close()

	client := &mockConnectClient{
		listFn:   func() ([]string, error) { return []string{}, nil },
		configFn: func(string) (map[string]any, error) { return nil, nil },
		statusFn: func(string) (map[string]any, error) { return nil, nil },
	}
	st := stateWithCluster()
	scanner, _ := newScannerWithClient(t, st, testArn, client)
	scanner.metricsSource = "jolokia"
	scanner.metricsDuration = "500ms"
	scanner.metricsInterval = "100ms"
	scanner.metricsClusterCreds = &types.OSKClusterAuth{ID: testArn, Jolokia: &types.JolokiaConfig{Endpoints: []string{srv.URL}}}

	require.NoError(t, scanner.Run(), "zero connectors is not an error")

	cluster, err := st.GetClusterByArn(testArn)
	require.NoError(t, err)
	require.Nil(t, cluster.KafkaAdminClientInformation.SelfManagedConnectors,
		"no connectors object created, so no metrics attached")
}

// Abuse (R12): symmetric TLS-honored check for the Prometheus backend.
func TestRun_PrometheusTLSHonored(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(connectPromHandler))
	defer srv.Close()

	st := stateWithCluster()
	scanner, _ := newScannerWithClient(t, st, testArn, connectMockClient())
	scanner.metricsSource = "prometheus"
	scanner.metricsRange = "1d"
	scanner.metricsClusterCreds = &types.OSKClusterAuth{
		ID: testArn,
		Prometheus: &types.PrometheusConfig{
			URL: srv.URL,
			TLS: &types.PrometheusTLSConfig{InsecureSkipVerify: true},
		},
	}

	require.NoError(t, scanner.Run())

	cluster, err := st.GetClusterByArn(testArn)
	require.NoError(t, err)
	conns := cluster.KafkaAdminClientInformation.SelfManagedConnectors
	require.NotNil(t, conns.Metrics, "HTTPS collection succeeded ⇒ TLS option applied")
	require.Contains(t, conns.Metrics.Aggregates, "connector-count")
}

// TestCmd_ClusterArnFlagRemoved locks the --cluster-arn -> --cluster-id rename:
// the old flag must no longer be accepted, so stale scripts fail loudly.
func TestCmd_ClusterArnFlagRemoved(t *testing.T) {
	cmd := NewScanSelfManagedConnectorsCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--state-file", "s", "--connect-rest-url", "u", "--cluster-arn", "a", "--use-unauthenticated"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown flag")
	assert.Contains(t, err.Error(), "cluster-arn")
}

func TestCmd_RejectsInvalidMetricsSource(t *testing.T) {
	cmd := NewScanSelfManagedConnectorsCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--state-file", "s", "--connect-rest-url", "u", "--cluster-id", "a",
		"--use-unauthenticated", "--metrics", "bogus",
	})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jolokia")
	assert.Contains(t, err.Error(), "prometheus")
}

func TestCmd_JolokiaRequiresDuration(t *testing.T) {
	cmd := NewScanSelfManagedConnectorsCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--state-file", "s", "--connect-rest-url", "u", "--cluster-id", "a",
		"--use-unauthenticated", "--metrics", "jolokia",
	})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metrics-duration")
}

func TestCmd_JolokiaDurationMustExceedInterval(t *testing.T) {
	cmd := NewScanSelfManagedConnectorsCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--state-file", "s", "--connect-rest-url", "u", "--cluster-id", "a",
		"--use-unauthenticated", "--credentials-file", "c.yaml",
		"--metrics", "jolokia", "--metrics-duration", "5s", "--metrics-interval", "10s",
	})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "greater than")
}

func TestCmd_PrometheusRejectsBadRange(t *testing.T) {
	cmd := NewScanSelfManagedConnectorsCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--state-file", "s", "--connect-rest-url", "u", "--cluster-id", "a",
		"--use-unauthenticated", "--credentials-file", "c.yaml",
		"--metrics", "prometheus", "--metrics-range", "bogus",
	})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metrics-range")
}

// --- parseScanSelfManagedConnectorsOpts: source-type detection & validation ---

func resetCmdGlobals() {
	stateFile = ""
	connectRestURL = ""
	clusterID = ""
	sourceType = ""
	useBasicAuth = false
	useTls = false
	useUnauthenticated = false
	username = ""
	password = ""
	tlsCaCert = ""
	tlsClientCert = ""
	tlsClientKey = ""
	metricsSource = ""
	metricsDuration = ""
	metricsInterval = "10s"
	metricsRange = ""
	credentialsFile = ""
}

func writeStateFile(t *testing.T, st *types.State) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kcp-state.json")
	require.NoError(t, st.PersistStateFile(path))
	return path
}

func TestParseOpts_AutoDetectsMSKFromArn(t *testing.T) {
	resetCmdGlobals()
	stateFile = writeStateFile(t, stateWithCluster())
	connectRestURL = "http://localhost:8083"
	clusterID = testArn
	useUnauthenticated = true

	opts, err := parseScanSelfManagedConnectorsOpts()
	require.NoError(t, err)
	assert.Equal(t, types.SourceTypeMSK, opts.SourceType)
	assert.Equal(t, testArn, opts.ClusterArn)
	assert.Empty(t, opts.ClusterID)
}

func TestParseOpts_AutoDetectsOSKFromNonArn(t *testing.T) {
	resetCmdGlobals()
	stateFile = writeStateFile(t, stateWithOSKCluster())
	connectRestURL = "http://localhost:8083"
	clusterID = testOSKID
	useUnauthenticated = true

	opts, err := parseScanSelfManagedConnectorsOpts()
	require.NoError(t, err)
	assert.Equal(t, types.SourceTypeOSK, opts.SourceType)
	assert.Equal(t, testOSKID, opts.ClusterID)
	assert.Empty(t, opts.ClusterArn)
}

func TestParseOpts_ExplicitSourceTypeOverridesAutoDetect(t *testing.T) {
	// An ARN-shaped cluster id, but explicit --source-type osk must win.
	resetCmdGlobals()
	st := &types.State{OSKSources: &types.OSKSourcesState{Clusters: []types.OSKDiscoveredCluster{{ID: testArn}}}}
	stateFile = writeStateFile(t, st)
	connectRestURL = "http://localhost:8083"
	clusterID = testArn
	sourceType = "osk"
	useUnauthenticated = true

	opts, err := parseScanSelfManagedConnectorsOpts()
	require.NoError(t, err)
	assert.Equal(t, types.SourceTypeOSK, opts.SourceType)
	assert.Equal(t, testArn, opts.ClusterID)
}

func TestParseOpts_InvalidSourceTypeRejected(t *testing.T) {
	resetCmdGlobals()
	stateFile = writeStateFile(t, stateWithOSKCluster())
	connectRestURL = "http://localhost:8083"
	clusterID = testOSKID
	sourceType = "bogus"
	useUnauthenticated = true

	_, err := parseScanSelfManagedConnectorsOpts()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source-type")
}

func TestParseOpts_ClusterNotInState(t *testing.T) {
	resetCmdGlobals()
	stateFile = writeStateFile(t, stateWithOSKCluster())
	connectRestURL = "http://localhost:8083"
	clusterID = "not-present"
	useUnauthenticated = true

	_, err := parseScanSelfManagedConnectorsOpts()
	require.Error(t, err)
}

// When metrics are requested but the target cluster has no matching entry in the
// credentials file, opts parsing must fail rather than silently skip metrics.
func TestParseOpts_MetricsClusterMissingFromCredsFile(t *testing.T) {
	resetCmdGlobals()
	stateFile = writeStateFile(t, stateWithOSKCluster())
	connectRestURL = "http://localhost:8083"
	clusterID = testOSKID
	useUnauthenticated = true
	metricsSource = "jolokia"
	metricsDuration = "5m"

	credsPath := filepath.Join(t.TempDir(), "creds.yaml")
	credsYAML := "clusters:\n" +
		"  - id: some-other-cluster\n" +
		"    bootstrap_servers:\n" +
		"      - localhost:9092\n" +
		"    auth_method:\n" +
		"      unauthenticated_plaintext:\n" +
		"        use: true\n" +
		"    jolokia:\n" +
		"      endpoints:\n" +
		"        - http://localhost:8781/jolokia\n"
	require.NoError(t, os.WriteFile(credsPath, []byte(credsYAML), 0o600))
	credentialsFile = credsPath

	_, err := parseScanSelfManagedConnectorsOpts()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "credentials file")
}
