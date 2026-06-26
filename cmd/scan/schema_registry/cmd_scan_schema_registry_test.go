package schema_registry

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeMinimalStateFile writes an empty-but-valid kcp state file and returns its path.
func writeMinimalStateFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kcp-state.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	return path
}

// setConfluentFlags sets the package-level flag vars the confluent scan reads and
// restores them after the test to avoid cross-test bleed.
func setConfluentFlags(t *testing.T, serverURL, statePath string) {
	t.Helper()
	origURL, origUnauth, origBasic, origUser, origPass, origState := url, useUnauthenticated, useBasicAuth, username, password, stateFile
	t.Cleanup(func() {
		url, useUnauthenticated, useBasicAuth, username, password, stateFile = origURL, origUnauth, origBasic, origUser, origPass, origState
	})

	url = serverURL
	useUnauthenticated = true
	useBasicAuth = false
	username = ""
	password = ""
	stateFile = statePath
}

func TestRunScanConfluentSchemaRegistry_PreflightBlocksHtmlEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>not a registry</body></html>"))
	}))
	defer server.Close()

	setConfluentFlags(t, server.URL, writeMinimalStateFile(t))

	err := runScanConfluentSchemaRegistry()

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "invalid character",
		"the pre-flight must replace the opaque library decode error")
	assert.Contains(t, err.Error(), "does not look like a Schema Registry",
		"the actionable pre-flight message should reach the user")
}
