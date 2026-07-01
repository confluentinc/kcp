//go:build integration

package schemaregistry

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

// These tests replace the former run.sh. They assume setup.sh has stood up the two
// Schema Registry instances (8081 unauthenticated, 8082 basic auth) and registered
// the seed subjects; the Makefile target brings the env up before `go test` and
// tears it down after. Each test execs the repo-root kcp binary, then asserts the
// scanned state — not merely that kcp exited 0.

// seedSubjects are the four subjects setup.sh registers on each instance.
var seedSubjects = []string{"orders-value", "orders-key", "events-value", "test-topic-1-value"}

// runScan execs `../../kcp scan schema-registry <args>` and returns combined output.
func runScan(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("../../kcp", append([]string{"scan", "schema-registry"}, args...)...)
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// loadState reads and unmarshals a kcp state file into types.State.
func loadState(t *testing.T, path string) types.State {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "reading state file")
	var st types.State
	require.NoError(t, json.Unmarshal(data, &st), "unmarshalling state")
	return st
}

// assertRegistry finds the SR entry for url and asserts it carries the seed subjects.
func assertRegistry(t *testing.T, st types.State, url string) {
	t.Helper()
	require.NotNil(t, st.SchemaRegistries, "state has no schema_registries")
	var subjects []string
	found := false
	for _, sr := range st.SchemaRegistries.ConfluentSchemaRegistry {
		if sr.URL == url {
			found = true
			for _, s := range sr.Subjects {
				subjects = append(subjects, s.Name)
			}
		}
	}
	require.True(t, found, "no schema registry entry for %s", url)
	require.GreaterOrEqual(t, len(subjects), len(seedSubjects), "expected >= %d subjects, got %d", len(seedSubjects), len(subjects))
	for _, want := range seedSubjects {
		assert.Contains(t, subjects, want, "seed subject %q must be scanned", want)
	}
}

func TestSchemaRegistryScan(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "sr-unauth.json")
		require.NoError(t, os.WriteFile(state, []byte("{}"), 0600))

		out, err := runScan(t, "--sr-type", "confluent",
			"--url", "http://localhost:8081",
			"--use-unauthenticated",
			"--state-file", state)
		require.NoError(t, err, out)

		assertRegistry(t, loadState(t, state), "http://localhost:8081")
	})

	t.Run("basic-auth", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "sr-basic.json")
		require.NoError(t, os.WriteFile(state, []byte("{}"), 0600))

		out, err := runScan(t, "--sr-type", "confluent",
			"--url", "http://localhost:8082",
			"--use-basic-auth",
			"--username", "schemauser",
			"--password", "schemapass",
			"--state-file", state)
		require.NoError(t, err, out)

		assertRegistry(t, loadState(t, state), "http://localhost:8082")
	})
}
