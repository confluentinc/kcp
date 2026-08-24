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

// TestGatewayExampleManifestIsValid keeps the documented gateway example in
// step with the parser and validator. The example is secret-bearing by design
// (inline credentials), so it uses ${ENV_VAR} references throughout — which
// means the test must supply them.
func TestGatewayExampleManifestIsValid(t *testing.T) {
	for _, v := range []string{"MSK_USERNAME", "MSK_PASSWORD", "CC_API_KEY", "CC_API_SECRET"} {
		t.Setenv(v, "example-value")
	}
	data, err := os.ReadFile("../../docs/assets/gateway-examples/gateway-migration.yaml")
	require.NoError(t, err)
	g, err := ParseGatewayMigration(data)
	require.NoError(t, err)
	require.Empty(t, g.Validate(), "the documented gateway example must validate clean")
}

// TestGatewayExampleResolvesEveryLeg proves the example is not merely
// structurally valid: all three connection legs resolve from it.
func TestGatewayExampleResolvesEveryLeg(t *testing.T) {
	for _, v := range []string{"MSK_USERNAME", "MSK_PASSWORD", "CC_API_KEY", "CC_API_SECRET"} {
		t.Setenv(v, "example-value")
	}
	data, err := os.ReadFile("../../docs/assets/gateway-examples/gateway-migration.yaml")
	require.NoError(t, err)
	g, err := ParseGatewayMigration(data)
	require.NoError(t, err)

	src, errs := g.SourceCredentials()
	require.Empty(t, errs)
	require.NotNil(t, src.SASLScram)

	dst, errs := g.DestinationKafkaCredentials()
	require.Empty(t, errs)
	require.NotNil(t, dst.SASLPlain)

	rest, err := g.RestCredentials()
	require.NoError(t, err)
	require.Equal(t, "example-value", rest.APIKey)
}
