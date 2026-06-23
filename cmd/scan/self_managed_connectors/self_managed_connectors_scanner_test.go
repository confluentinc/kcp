package self_managed_connectors

import (
	"bytes"
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
		ClusterArn: arn,
		client:     client,
	}, stateFile
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
		"--state-file", "s", "--connect-rest-url", "u", "--cluster-arn", "a",
		"--use-tls", "--use-sasl-scram",
	})
	require.Error(t, cmd.Execute(), "two auth methods must be rejected")
}

func TestCmd_RequiresAnAuthMethod(t *testing.T) {
	cmd := NewScanSelfManagedConnectorsCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--state-file", "s", "--connect-rest-url", "u", "--cluster-arn", "a"})
	require.Error(t, cmd.Execute(), "an auth method is required")
}
