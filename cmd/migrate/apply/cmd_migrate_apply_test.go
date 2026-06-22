package apply

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	migrate "github.com/confluentinc/kcp/internal/migrate"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

type staticSource string

func (s staticSource) ClusterID(context.Context) (string, error) { return string(s), nil }

// startStubTarget serves the minimal CP REST surface: list clusters + get/create link.
func startStubTarget(t *testing.T, linkExists bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/kafka/v3/clusters", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"cluster_id":"dest-1"}]}`))
	})
	mux.HandleFunc("/kafka/v3/clusters/dest-1/links/src-to-dest", func(w http.ResponseWriter, _ *http.Request) {
		if linkExists {
			_, _ = w.Write([]byte(`{"link_name":"src-to-dest","source_cluster_id":"src-1","link_state":"AVAILABLE"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/kafka/v3/clusters/dest-1/links/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	return httptest.NewServer(mux)
}

// run executes the apply command with a source whose ClusterID is faked via the
// newSourceReader package-level seam (see cmd implementation).
func run(t *testing.T, srvURL string, dryRun bool) (stdout, stderr string, err error) {
	t.Helper()
	dir := t.TempDir()
	targetCreds := filepath.Join(dir, "target.yaml")
	require.NoError(t, os.WriteFile(targetCreds, []byte("basic:\n  username: admin\n  password: admin-secret\n"), 0600))
	sourceCreds := filepath.Join(dir, "source.yaml")
	require.NoError(t, os.WriteFile(sourceCreds, []byte(
		"clusters:\n  - id: src\n    bootstrap_servers: [\"source:29092\"]\n    auth_method:\n      unauthenticated_plaintext:\n        use: true\n"), 0600))
	manifest := filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(manifest, []byte(
		"apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: t\nspec:\n  source:\n    type: apache-kafka\n    credentials: "+sourceCreds+
			"\n  target:\n    type: confluent-platform\n    credentials: "+targetCreds+
			"\n    kafka:\n      restEndpoint: "+srvURL+"\n      bootstrapServers: [\"dest:29092\"]\n  clusterLink:\n    name: src-to-dest\n"), 0600))

	old := newSourceReader
	newSourceReader = func(types.OSKClusterAuth) migrate.Source { return staticSource("src-1") }
	t.Cleanup(func() { newSourceReader = old })
	cmd := NewMigrateApplyCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	args := []string{"-f", manifest}
	if dryRun {
		args = append(args, "--dry-run")
	}
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestApply_DryRun_PrintsPlanNoCreate(t *testing.T) {
	srv := startStubTarget(t, false)
	defer srv.Close()
	out, _, err := run(t, srv.URL, true)
	require.NoError(t, err)
	require.Contains(t, out, "cluster link \"src-to-dest\"")
	require.Contains(t, out, "Planned")
}

func TestApply_CreatesLink(t *testing.T) {
	srv := startStubTarget(t, false)
	defer srv.Close()
	out, _, err := run(t, srv.URL, false)
	require.NoError(t, err)
	require.Contains(t, out, "1 created")
}

func TestApply_AlreadyPresent(t *testing.T) {
	srv := startStubTarget(t, true)
	defer srv.Close()
	out, _, err := run(t, srv.URL, false)
	require.NoError(t, err)
	require.Contains(t, out, "1 already present")
}
