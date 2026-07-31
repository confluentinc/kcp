//go:build integration

package oskscan

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

// These tests replace the former run.sh. They assume setup.sh has stood up the OSK
// scan broker(s) and seeded topics/ACLs; the Makefile target brings the env up
// before `go test` and tears it down after. Each test execs the repo-root kcp
// binary and asserts the scanned state — not merely that kcp exited 0.
//
// kcp is exec'd with cwd = repo root (like the old run.sh) because the credential
// files reference cert paths relative to the repo root
// (e.g. integration-tests/osk-scan/certs/ca-cert.pem).

// seededTopics are created on every OSK broker by setup.sh.
var seededTopics = []string{"test-topic-1", "test-topic-2", "orders", "events"}

const credDir = "integration-tests/osk-scan/credentials"

// runScan execs `./kcp scan clusters ...` from the repo root and returns output.
func runScan(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("./kcp", append([]string{"scan", "clusters"}, args...)...)
	cmd.Dir = "../.." // repo root, so relative cert paths in creds resolve
	b, err := cmd.CombinedOutput()
	return string(b), err
}

func loadOSKCluster(t *testing.T, statePath string) types.OSKDiscoveredCluster {
	t.Helper()
	data, err := os.ReadFile(statePath)
	require.NoError(t, err, "reading state file")
	var st types.State
	require.NoError(t, json.Unmarshal(data, &st), "unmarshalling state")
	require.NotNil(t, st.OSKSources, "state has no osk_sources")
	require.NotEmpty(t, st.OSKSources.Clusters, "state has no osk_sources.clusters")
	return st.OSKSources.Clusters[0]
}

func assertSeededTopics(t *testing.T, c types.OSKDiscoveredCluster) {
	t.Helper()
	require.NotNil(t, c.KafkaAdminClientInformation.Topics, "no topics scanned")
	names := map[string]bool{}
	for _, d := range c.KafkaAdminClientInformation.Topics.Details {
		names[d.Name] = true
	}
	for _, want := range seededTopics {
		assert.True(t, names[want], "seeded topic %q must be scanned (got %v)", want, names)
	}
}

// TestOSKScanKafkaAuth scans the broker over every supported Kafka auth method and
// asserts the seeded topics are read back — proving the auth/TLS/CA plumbing works
// for each method, not just that kcp exited 0.
func TestOSKScanKafkaAuth(t *testing.T) {
	methods := []struct {
		name      string
		checkACLs bool // only the main osk-kafka broker carries the seeded ACLs
	}{
		{"plaintext", true},
		{"sasl", false},
		{"sasl-sha512", false},
		{"sasl-sha512-only", false},
		{"tls", false},
		{"sasl-ssl", false},
		{"sasl-ssl-cacert", false},
		{"sasl-plain", false},
		{"sasl-plain-ssl", false},
		{"unauth-tls", false},
	}
	for _, m := range methods {
		t.Run(m.name, func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "state.json")
			out, err := runScan(t, "--source-type", "apache-kafka",
				"--credentials-file", credDir+"/kafka-"+m.name+".yaml",
				"--state-file", state)
			require.NoError(t, err, out)

			c := loadOSKCluster(t, state)
			assertSeededTopics(t, c)
			if m.checkACLs {
				assert.NotEmpty(t, c.KafkaAdminClientInformation.Acls, "expected seeded ACLs on the main broker")
			}
		})
	}
}

// TestOSKScanMetrics scans with each metrics backend (Jolokia + Prometheus, across
// no-auth/auth/TLS) and asserts data points and aggregates were collected.
func TestOSKScanMetrics(t *testing.T) {
	metrics := []struct {
		name    string
		cred    string
		backend string // "jolokia" | "prometheus"
	}{
		{"jmx-noauth", "jmx-noauth", "jolokia"},
		{"prometheus-noauth", "prometheus-noauth", "prometheus"},
		{"jmx-auth", "jmx-auth", "jolokia"},
		{"jmx-tls", "jmx-tls", "jolokia"},
		{"prometheus-auth", "prometheus-auth", "prometheus"},
		{"prometheus-tls", "prometheus-tls", "prometheus"},
	}
	for _, m := range metrics {
		t.Run(m.name, func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "state.json")
			args := []string{"--source-type", "apache-kafka",
				"--credentials-file", credDir + "/" + m.cred + ".yaml",
				"--state-file", state,
				"--metrics", m.backend}
			switch m.backend {
			case "jolokia":
				args = append(args, "--metrics-duration", "10s", "--metrics-interval", "1s")
			case "prometheus":
				args = append(args, "--metrics-range", "30d")
			}
			out, err := runScan(t, args...)
			require.NoError(t, err, out)

			c := loadOSKCluster(t, state)
			require.NotNil(t, c.ClusterMetrics, "no metrics collected")
			assert.NotEmpty(t, c.ClusterMetrics.Metrics, "expected metric data points")
			assert.NotEmpty(t, c.ClusterMetrics.Aggregates, "expected metric aggregates")
		})
	}
}
