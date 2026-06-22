//go:build integration

// Package migrateclusterlink is an end-to-end test of `kcp migrate apply` for a
// cluster link, against two Confluent Platform (cp-server) brokers brought up
// via docker-compose (see the Makefile target test-migrate-clusterlink). It
// exercises every source authentication method the link can use.
package migrateclusterlink

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const destREST = "http://localhost:28090"

// TestMigrateApply_ClusterLink_AuthMatrix drives the built kcp binary end-to-end
// once per source auth method: dry-run previews without creating, apply creates
// the link (which reaches ACTIVE), and a second apply is an idempotent no-op.
func TestMigrateApply_ClusterLink_AuthMatrix(t *testing.T) {
	waitForClusterID(t, destREST)
	waitForClusterID(t, "http://localhost:18090") // source REST ready => broker up
	destID := clusterID(destREST)
	require.NotEmpty(t, destID)

	for _, m := range []struct{ dir, link string }{
		{"plaintext", "src-to-dest"},
		{"scram256", "scram256"},
		{"scram512", "scram512"},
		{"plain", "plain"},
		{"tls", "tlsenc"},
		{"mtls", "mtls"},
	} {
		t.Run(m.dir, func(t *testing.T) {
			manifest := "testdata/" + m.dir + "/migration.yaml"

			// dry-run previews a create and changes nothing.
			out, err := runKCP(t, manifest, "--dry-run")
			require.NoError(t, err, out)
			require.Contains(t, out, "Planned")
			require.Equal(t, "", linkState(destID, m.link), "dry-run must not create the link")

			// apply creates the link.
			out, err = runKCP(t, manifest)
			require.NoError(t, err, out)
			require.Contains(t, out, "1 created")

			// the link reaches ACTIVE (cp-server's healthy cluster-link state).
			requireLinkState(t, destID, m.link, "ACTIVE")

			// re-apply is an idempotent no-op (read-first; never re-creates).
			out, err = runKCP(t, manifest)
			require.NoError(t, err, out)
			require.Contains(t, out, "1 already present")
		})
	}
}

// runKCP runs the built ../../kcp binary with `migrate apply -f <manifest>` from
// this directory (so the manifest's ./testdata/* and ./certs/* relative paths resolve).
func runKCP(t *testing.T, manifest string, extra ...string) (string, error) {
	t.Helper()
	args := append([]string{"migrate", "apply", "-f", manifest}, extra...)
	cmd := exec.Command("../../kcp", args...)
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// waitForClusterID polls a CP REST endpoint until /kafka/v3/clusters reports a
// non-empty cluster id, meaning the broker is fully up.
func waitForClusterID(t *testing.T, restURL string) {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if id := clusterID(restURL); id != "" {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("cluster id at %s never became non-empty", restURL)
}

// clusterID returns data[0].cluster_id from /kafka/v3/clusters, or "" on any error.
func clusterID(restURL string) string {
	resp, err := http.Get(restURL + "/kafka/v3/clusters")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			ClusterID string `json:"cluster_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || len(body.Data) == 0 {
		return ""
	}
	return body.Data[0].ClusterID
}

// linkState returns the named dest link's link_state, or "" if the link does not
// exist or the endpoint is momentarily unreachable. Returning "" on transport
// error (rather than failing) lets the poll in requireLinkState retry through a
// transient REST blip instead of hard-failing the test.
func linkState(destID, linkName string) string {
	resp, err := http.Get(destREST + "/kafka/v3/clusters/" + destID + "/links/" + linkName)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var body struct {
		LinkState string `json:"link_state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ""
	}
	return body.LinkState
}

// requireLinkState polls until the named link reaches want, failing on timeout.
func requireLinkState(t *testing.T, destID, linkName, want string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if linkState(destID, linkName) == want {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("link %q did not reach state %q (last: %q)", linkName, want, linkState(destID, linkName))
}
