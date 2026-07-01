//go:build integration

package connectscan

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This suite replaces the former osk-scan/run-connect.sh. It assumes setup.sh has
// stood up the Connect env (1 plaintext broker + three single-node Connect workers:
// unauthenticated, HTTP-Basic, and mTLS) and created the test-heartbeat connector
// on each; the Makefile target brings the env up before `go test` and tears it down
// after. Each test execs the repo-root kcp binary and asserts the scanned state.
//
// kcp is exec'd with cwd = repo root so --credentials-file / cert paths resolve the
// same way they do for the other integration suites.

const (
	credDir   = "integration-tests/connect-scan/credentials"
	certDir   = "integration-tests/connect-scan/certs"
	clusterID = "connect-kafka"
)

func runKCP(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("./kcp", args...)
	cmd.Dir = "../.." // repo root
	b, err := cmd.CombinedOutput()
	return string(b), err
}

func loadCluster(t *testing.T, statePath string) types.OSKDiscoveredCluster {
	t.Helper()
	data, err := os.ReadFile(statePath)
	require.NoError(t, err, "reading state file")
	var st types.State
	require.NoError(t, json.Unmarshal(data, &st), "unmarshalling state")
	require.NotNil(t, st.OSKSources, "state has no osk_sources")
	require.NotEmpty(t, st.OSKSources.Clusters, "state has no osk_sources.clusters")
	return st.OSKSources.Clusters[0]
}

func assertConnectorsDiscovered(t *testing.T, statePath string) {
	t.Helper()
	c := loadCluster(t, statePath)
	require.NotNil(t, c.KafkaAdminClientInformation.SelfManagedConnectors, "no self_managed_connectors in state")
	conns := c.KafkaAdminClientInformation.SelfManagedConnectors.Connectors
	require.NotEmpty(t, conns, "expected at least one self-managed connector (test-heartbeat)")
	withHost := 0
	for _, cn := range conns {
		if cn.ConnectHost != "" {
			withHost++
		}
	}
	assert.Positive(t, withHost, "connectors present but none have connect_host populated")
}

// TestConnectScanAuthMethods scans a Connect worker over EVERY REST auth method the
// `scan self-managed-connectors` command supports — unauthenticated, HTTP Basic
// (--use-sasl-scram), and mTLS (--use-tls) — asserting the connector is discovered
// in each case (so the auth/TLS wiring is exercised, not just that kcp exited 0).
func TestConnectScanAuthMethods(t *testing.T) {
	methods := []struct {
		name    string
		restURL string
		auth    []string // kcp auth flags for this method
	}{
		{
			name:    "unauthenticated",
			restURL: "http://localhost:18083",
			auth:    []string{"--use-unauthenticated"},
		},
		{
			name:    "basic-auth",
			restURL: "http://localhost:18085",
			auth: []string{
				"--use-sasl-scram",
				"--sasl-scram-username", "connectuser",
				"--sasl-scram-password", "connectpass",
			},
		},
		{
			name:    "mtls",
			restURL: "https://localhost:18086",
			auth: []string{
				"--use-tls",
				"--tls-ca-cert", certDir + "/ca-cert.pem",
				"--tls-client-cert", certDir + "/client-cert.pem",
				"--tls-client-key", certDir + "/client-key.pem",
			},
		},
		{
			// Basic auth over HTTPS, verifying the server against the private CA:
			// exercises --tls-ca-cert on a non-mTLS method.
			name:    "basic-auth-https-ca",
			restURL: "https://localhost:18087",
			auth: []string{
				"--use-sasl-scram",
				"--sasl-scram-username", "connectuser",
				"--sasl-scram-password", "connectpass",
				"--tls-ca-cert", certDir + "/ca-cert.pem",
			},
		},
		{
			// Same HTTPS endpoint, but skip verification instead of supplying a CA:
			// exercises --insecure-skip-tls-verify on a non-mTLS method.
			name:    "basic-auth-https-skip-verify",
			restURL: "https://localhost:18087",
			auth: []string{
				"--use-sasl-scram",
				"--sasl-scram-username", "connectuser",
				"--sasl-scram-password", "connectpass",
				"--insecure-skip-tls-verify",
			},
		},
	}

	for _, m := range methods {
		t.Run(m.name, func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "state.json")
			// Seed the base cluster (self-managed-connectors attaches to it).
			out, err := runKCP(t, "scan", "clusters", "--source-type", "apache-kafka",
				"--credentials-file", credDir+"/kafka-plaintext.yaml",
				"--state-file", state)
			require.NoError(t, err, out)

			args := append([]string{"scan", "self-managed-connectors",
				"--state-file", state,
				"--connect-rest-url", m.restURL,
				"--cluster-id", clusterID}, m.auth...)
			out, err = runKCP(t, args...)
			require.NoError(t, err, out)

			assertConnectorsDiscovered(t, state)
		})
	}
}

// TestConnectScanMetrics collects Connect worker metrics via Jolokia (on the
// unauthenticated worker) and asserts data points were gathered.
func TestConnectScanMetrics(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	out, err := runKCP(t, "scan", "clusters", "--source-type", "apache-kafka",
		"--credentials-file", credDir+"/kafka-plaintext.yaml",
		"--state-file", state)
	require.NoError(t, err, out)

	out, err = runKCP(t, "scan", "self-managed-connectors",
		"--state-file", state,
		"--connect-rest-url", "http://localhost:18083",
		"--cluster-id", clusterID,
		"--use-unauthenticated",
		"--metrics", "jolokia",
		"--metrics-duration", "30s",
		"--metrics-interval", "10s",
		"--credentials-file", credDir+"/connect-jolokia.yaml")
	require.NoError(t, err, out)

	c := loadCluster(t, state)
	require.NotNil(t, c.KafkaAdminClientInformation.SelfManagedConnectors, "no self_managed_connectors in state")
	m := c.KafkaAdminClientInformation.SelfManagedConnectors.Metrics
	require.NotNil(t, m, "no Connect metrics collected")
	assert.NotEmpty(t, m.Metrics, "expected at least one Connect metric data point")
}
