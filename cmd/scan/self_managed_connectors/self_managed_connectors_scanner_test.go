package self_managed_connectors

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
	metrics := &types.ProcessedClusterMetrics{}
	require.NoError(t, s.updateStateWithConnectMetrics(metrics))

	cl2, _ := st.GetClusterByArn(testArn)
	assert.Same(t, metrics, cl2.KafkaAdminClientInformation.SelfManagedConnectors.Metrics)
}

func TestScanner_UpdateStateWithConnectMetrics_MSK_NoConnectors(t *testing.T) {
	s := &SelfManagedConnectorsScanner{State: stateWithCluster(), SourceType: types.SourceTypeMSK, ClusterArn: testArn}
	err := s.updateStateWithConnectMetrics(&types.ProcessedClusterMetrics{})
	require.Error(t, err, "metrics with no prior connectors in state is an error")
}

func TestScanner_UpdateStateWithConnectMetrics_MSK_ClusterNotFound(t *testing.T) {
	s := &SelfManagedConnectorsScanner{State: stateWithCluster(), SourceType: types.SourceTypeMSK, ClusterArn: "arn:aws:kafka:us-east-1:999:cluster/missing/x"}
	err := s.updateStateWithConnectMetrics(&types.ProcessedClusterMetrics{})
	require.Error(t, err)
}

func TestScanner_UpdateStateWithConnectMetrics_OSK_Success(t *testing.T) {
	st := stateWithOSKCluster()
	cl, err := st.GetOSKClusterByID(testOSKID)
	require.NoError(t, err)
	cl.KafkaAdminClientInformation.SetSelfManagedConnectors([]types.SelfManagedConnector{{Name: "c1"}})

	s := &SelfManagedConnectorsScanner{State: st, SourceType: types.SourceTypeOSK, ClusterID: testOSKID}
	metrics := &types.ProcessedClusterMetrics{}
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
	require.Error(t, s.updateStateWithConnectMetrics(&types.ProcessedClusterMetrics{}))
}

func TestScanner_UpdateStateWithConnectMetrics_OSK_ClusterNotFound(t *testing.T) {
	s := &SelfManagedConnectorsScanner{State: stateWithOSKCluster(), SourceType: types.SourceTypeOSK, ClusterID: "no-such-cluster"}
	require.Error(t, s.updateStateWithConnectMetrics(&types.ProcessedClusterMetrics{}))
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

func TestHTTPConnectClient_SaslScramSendsBasicAuth(t *testing.T) {
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
		authMethod: types.ConnectAuthMethodSaslScram,
		saslAuth:   types.ConnectSaslScramAuth{Username: "u", Password: "p"},
	}
	_, err := c.ListConnectors()
	require.NoError(t, err)
	assert.True(t, gotOK, "basic auth header sent for SASL/SCRAM")
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
		"--use-tls", "--use-sasl-scram",
	})
	require.Error(t, cmd.Execute(), "two auth methods must be rejected")
}

func TestCmd_RequiresAnAuthMethod(t *testing.T) {
	cmd := NewScanSelfManagedConnectorsCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--state-file", "s", "--connect-rest-url", "u", "--cluster-id", "a"})
	require.Error(t, cmd.Execute(), "an auth method is required")
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
	useSaslScram = false
	useTls = false
	useUnauthenticated = false
	saslScramUsername = ""
	saslScramPassword = ""
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
