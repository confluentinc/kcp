package manifest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExampleManifestIsValid(t *testing.T) {
	data, err := os.ReadFile("../../docs/assets/migration-assets/migration.example.yaml")
	require.NoError(t, err)
	m, err := Parse(data)
	require.NoError(t, err)
	require.Empty(t, m.Validate(), "docs/assets/migration-assets/migration.example.yaml must validate clean")
}

// TestDocExamplesAreValid parses and structurally validates every example
// manifest shipped under docs/assets/migration-assets/examples/, so the
// documented examples can't drift out of sync with the manifest schema.
func TestDocExamplesAreValid(t *testing.T) {
	matches, err := filepath.Glob("../../docs/assets/migration-assets/examples/*/migration.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, matches, "expected example manifests under docs/assets/migration-assets/examples/")
	for _, path := range matches {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			m, err := Parse(data)
			require.NoError(t, err)
			require.Empty(t, m.Validate(), "%s must validate clean", path)
		})
	}
}

func TestValidCPFixtureIsValid(t *testing.T) {
	m, err := Parse(readFixture(t, "valid_cp.yaml"))
	require.NoError(t, err)
	require.Empty(t, m.Validate())
}

func TestValidCCFixtureIsValid(t *testing.T) {
	m, err := Parse(readFixture(t, "valid_cc.yaml"))
	require.NoError(t, err)
	require.Empty(t, m.Validate(), "valid_cc.yaml must validate clean (it must not put a managed key in clusterLink.configs)")
}
