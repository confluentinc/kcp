package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestGatewayExamplesAreValid keeps each documented example in step with the
// parser and validator, and proves each declares the type its file name says.
// Credentials are referenced files, so the examples carry no secrets and
// validate structurally without any I/O.
func TestGatewayExamplesAreValid(t *testing.T) {
	for file, want := range map[string]MigrationType{
		"gateway-static-migration.yaml":  MigrationTypeStatic,
		"gateway-dynamic-migration.yaml": MigrationTypeDynamic,
		"gateway-route-conversion.yaml":  MigrationTypeDynamicToStatic,
	} {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile("../../docs/assets/gateway-examples/" + file)
			require.NoError(t, err)
			g, err := ParseGatewayMigration(data)
			require.NoError(t, err)
			require.Empty(t, g.Validate(), "the documented example must validate clean")
			require.Equal(t, want, g.Spec.MigrationType())
		})
	}
}

// TestGatewayExampleResolvesEveryLeg proves the example is not merely
// structurally valid: substituting real credential files for its illustrative
// /etc/kcp/*.yaml paths, all three connection legs resolve to the auth shapes
// the file's own comments document.
func TestGatewayExampleResolvesEveryLeg(t *testing.T) {
	data, err := os.ReadFile("../../docs/assets/gateway-examples/gateway-static-migration.yaml")
	require.NoError(t, err)
	doc := string(data)

	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0600))
		return p
	}
	doc = strings.Replace(doc, "credentials: /etc/kcp/source-creds.yaml",
		"credentials: "+write("source-creds.yaml", "sasl_scram:\n  username: admin\n  password: secret\n  mechanism: SHA512\n"), 1)
	doc = strings.Replace(doc, "clusterCredentials: /etc/kcp/dest-kafka-creds.yaml",
		"clusterCredentials: "+write("dest-kafka-creds.yaml", "sasl_plain:\n  username: CC_KEY\n  password: CC_SECRET\n  tls: true\n"), 1)
	doc = strings.Replace(doc, "linkCredentials: /etc/kcp/link-creds.yaml",
		"linkCredentials: "+write("link-creds.yaml", "api_key: CC_KEY\napi_secret: CC_SECRET\n"), 1)

	g, err := ParseGatewayMigration([]byte(doc))
	require.NoError(t, err)
	require.Empty(t, g.Validate())

	src, errs := g.SourceCredentials()
	require.Empty(t, errs)
	require.NotNil(t, src.SASLScram)

	dst, errs := g.DestinationKafkaCredentials()
	require.Empty(t, errs)
	require.NotNil(t, dst.SASLPlain)

	rest, err := g.RestCredentials()
	require.NoError(t, err)
	require.Equal(t, "CC_KEY", rest.APIKey)
}

// TestGatewayRouteConversionExampleIsValid: a conversion declares no topic
// selection and carries the conversion policies.
func TestGatewayRouteConversionExampleIsValid(t *testing.T) {
	data, err := os.ReadFile("../../docs/assets/gateway-examples/gateway-route-conversion.yaml")
	require.NoError(t, err)
	g, err := ParseGatewayMigration(data)
	require.NoError(t, err)
	require.NotNil(t, g.Spec.DynamicToStaticConversion)
	require.Nil(t, g.Spec.DeclaredTopicGroup(), "a conversion selects no topics")
	require.Equal(t, 30*time.Second, g.Spec.DynamicToStaticConversion.Policies.DetectUnroutedCommitsDuration)
}
