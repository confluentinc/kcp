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
	require.NotEmpty(t, c.KafkaAdminClientInformation.ConnectClusters, "no connect_clusters in state")
	var conns []types.Connector
	for _, cc := range c.KafkaAdminClientInformation.ConnectClusters {
		conns = append(conns, cc.Connectors...)
	}
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
// (--use-basic-auth), and mTLS (--use-tls) — asserting the connector is discovered
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
				"--use-basic-auth",
				"--username", "connectuser",
				"--password", "connectpass",
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
				"--use-basic-auth",
				"--username", "connectuser",
				"--password", "connectpass",
				"--tls-ca-cert", certDir + "/ca-cert.pem",
			},
		},
		{
			// Same HTTPS endpoint, but skip verification instead of supplying a CA:
			// exercises --insecure-skip-tls-verify on a non-mTLS method.
			name:    "basic-auth-https-skip-verify",
			restURL: "https://localhost:18087",
			auth: []string{
				"--use-basic-auth",
				"--username", "connectuser",
				"--password", "connectpass",
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
	var cc *types.ConnectCluster
	for i := range c.KafkaAdminClientInformation.ConnectClusters {
		if c.KafkaAdminClientInformation.ConnectClusters[i].ConnectRestURL == "http://localhost:18083" {
			cc = &c.KafkaAdminClientInformation.ConnectClusters[i]
			break
		}
	}
	require.NotNil(t, cc, "no connect_cluster found for http://localhost:18083")
	m := cc.Metrics
	require.NotNil(t, m, "no Connect metrics collected")
	assert.NotEmpty(t, m.Metrics, "expected at least one Connect metric data point")
}

// writeMinimalOSKState writes a state file containing a single OSK cluster
// entry with the given ID, without running a live `scan clusters`. Some cases
// below only need to get past the state/cluster-existence check before
// hitting a credential-load failure that happens long before any network
// call, so they can be written directly here and run without Docker at all.
func writeMinimalOSKState(t *testing.T, path, id string) {
	t.Helper()
	st := types.NewStateFrom(nil)
	st.OSKSources.Clusters = append(st.OSKSources.Clusters, types.OSKDiscoveredCluster{
		ID:               id,
		BootstrapServers: []string{"localhost:9092"},
	})
	require.NoError(t, st.WriteToFile(path), "writing minimal state fixture")
}

// TestConnectScanMetricNameOverrideValidation proves the "prove errors" path:
// an unknown/typo'd prometheus.connect_metric_names key is rejected at
// credential-load time, so the scan fails fast with a helpful message instead
// of silently no-opping the override.
//
// This needs no Docker/live backend at all: credential validation
// (types.OSKCredentials.Validate) runs inside opts-parsing, before the
// scanner ever dials the Connect REST API or a metrics backend. The state
// file only needs to already contain a cluster with the right ID to satisfy
// the earlier cluster-existence check, so it's written directly via
// writeMinimalOSKState rather than produced by a live `scan clusters` run.
func TestConnectScanMetricNameOverrideValidation(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	writeMinimalOSKState(t, state, clusterID)

	out, err := runKCP(t, "scan", "self-managed-connectors",
		"--state-file", state,
		"--connect-rest-url", "http://localhost:1", // never dialed: fails before this
		"--cluster-id", clusterID,
		"--use-unauthenticated",
		"--metrics", "prometheus",
		"--metrics-range", "30d",
		"--credentials-file", credDir+"/prometheus-bad-override.yaml")

	require.Error(t, err, "scan must fail on an unknown connect_metric_names key; output: %s", out)
	assert.Contains(t, out, "unknown metric label",
		"error must name the problem so the user can self-correct")
	assert.Contains(t, out, "Task-Count", "error must name the offending key")
}

// TestConnectScanJMXMBeanOverrideMissing proves the Jolokia
// connect_mbean_overrides override is threaded end-to-end and degrades
// gracefully: task-count is overridden to an MBean the unauthenticated
// worker's Jolokia agent doesn't expose, so it is omitted from the results
// while every other Connect metric is still collected and the scan exits 0.
// (The unit tests added in Task 2 assert the accompanying loud Warn; here we
// prove the scan-level behaviour.)
func TestConnectScanJMXMBeanOverrideMissing(t *testing.T) {
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
		"--credentials-file", credDir+"/jmx-bad-override.yaml")
	require.NoError(t, err, out)

	c := loadCluster(t, state)
	var cc *types.ConnectCluster
	for i := range c.KafkaAdminClientInformation.ConnectClusters {
		if c.KafkaAdminClientInformation.ConnectClusters[i].ConnectRestURL == "http://localhost:18083" {
			cc = &c.KafkaAdminClientInformation.ConnectClusters[i]
			break
		}
	}
	require.NotNil(t, cc, "no connect_cluster found for http://localhost:18083")
	m := cc.Metrics
	require.NotNil(t, m, "no Connect metrics collected")

	_, ok := m.Aggregates["connector-count"]
	assert.True(t, ok, "connector-count must still be collected when only task-count's MBean is overridden")

	_, ok = m.Aggregates["task-count"]
	assert.False(t, ok, "task-count is overridden to a non-existent MBean and must be omitted, not defaulted")
}

// NOTE on scope: a third override case — proving the Prometheus
// connect_metric_names substitution end-to-end against a relabelled series,
// mirroring TestOSKScanPrometheusMetricNameOverride from PR #415 — is
// intentionally NOT covered in this suite. This harness's docker-compose
// stands up Kafka + Connect workers only (Jolokia-only metrics); there is no
// Prometheus backend or seed mechanism here to relabel a series against,
// unlike integration-tests/osk-scan (see its seed-prometheus-data.sh).
// Standing up a new Prometheus stack for this one case was judged out of
// scope for this change. The substitution logic itself is covered by the
// Task 3 unit tests in internal/services/prometheus
// (TestConnectQueryDefinitions_Override,
// TestConnectQueryDefinitions_OverridePreservesLabelFilterInjection).
