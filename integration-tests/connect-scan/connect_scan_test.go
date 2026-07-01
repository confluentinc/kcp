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
// stood up the minimal Connect env (1 plaintext broker + Connect worker with
// Jolokia) and created the test-heartbeat connector; the Makefile target brings
// the env up before `go test` and tears it down after. Each test execs the
// repo-root kcp binary and asserts the scanned state.
//
// kcp is exec'd with cwd = repo root so --credentials-file paths resolve the same
// way they do for the other integration suites.

const (
	credDir    = "integration-tests/connect-scan/credentials"
	clusterID  = "connect-kafka"
	connectURL = "http://localhost:18083"
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

func TestConnectScan(t *testing.T) {
	// Shared state file: the base cluster scan seeds it, then the connector scans
	// augment it (same order as a real run).
	state := filepath.Join(t.TempDir(), "state.json")

	// Seed the state with the base cluster (self-managed-connectors attaches to it).
	out, err := runKCP(t, "scan", "clusters", "--source-type", "apache-kafka",
		"--credentials-file", credDir+"/kafka-plaintext.yaml",
		"--state-file", state)
	require.NoError(t, err, out)

	t.Run("discover connectors", func(t *testing.T) {
		out, err := runKCP(t, "scan", "self-managed-connectors",
			"--state-file", state,
			"--connect-rest-url", connectURL,
			"--cluster-id", clusterID,
			"--use-unauthenticated")
		require.NoError(t, err, out)

		c := loadCluster(t, state)
		require.NotNil(t, c.KafkaAdminClientInformation.SelfManagedConnectors, "no self_managed_connectors in state")
		conns := c.KafkaAdminClientInformation.SelfManagedConnectors.Connectors
		require.NotEmpty(t, conns, "expected at least one self-managed connector (test-heartbeat)")

		// connect_host must be populated — the UI's per-Connect-host grouping depends on it.
		withHost := 0
		for _, cn := range conns {
			if cn.ConnectHost != "" {
				withHost++
			}
		}
		assert.Positive(t, withHost, "connectors present but none have connect_host populated")
	})

	t.Run("jolokia metrics", func(t *testing.T) {
		out, err := runKCP(t, "scan", "self-managed-connectors",
			"--state-file", state,
			"--connect-rest-url", connectURL,
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
	})
}
