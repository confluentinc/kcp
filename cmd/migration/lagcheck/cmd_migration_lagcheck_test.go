package lagcheck

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const lagCheckManifest = `apiVersion: kcp.confluent.io/v1alpha1
kind: GatewayMigration
metadata:
  name: msk-prod-to-cc-batch-1
spec:
  source:
    type: msk
    bootstrapServers:
      - b-1.msk.us-east-1.amazonaws.com:9096
    credentials:
      sasl_scram:
        username: admin
        password: secret
        mechanism: SHA512
  target:
    type: confluent-cloud
    clusterId: lkc-abc123
    kafka:
      bootstrapServers:
        - pkc-xxxxx.us-east-1.aws.confluent.cloud:9092
      restEndpoint: https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443
      credentials:
        sasl_plain:
          username: CC_KEY
          password: CC_SECRET
          tls: true
  clusterLink:
    name: msk-to-cc
  gateway:
    namespace: confluent
    crs:
      initial: gateway-initial
      switchover: /etc/kcp/switchover.yaml
    fence:
      routes:
        - migration-route
`

func writeLagManifest(t *testing.T, mutate func(string) string) string {
	t.Helper()
	doc := lagCheckManifest
	if mutate != nil {
		doc = mutate(doc)
	}
	p := filepath.Join(t.TempDir(), "gateway-migration.yaml")
	require.NoError(t, os.WriteFile(p, []byte(doc), 0600))
	return p
}

func loadLagGateway(t *testing.T, path string) *manifest.GatewayMigration {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	g, err := manifest.ParseGatewayMigration(data)
	require.NoError(t, err)
	return g
}

// TestLagCheck_FlagSurfaceIsTwoFlags — decision 15: --migration-yaml and
// --poll-interval, nothing else. lag-check reads no state file, so a
// --migration-id would resolve nothing.
func TestLagCheck_FlagSurfaceIsTwoFlags(t *testing.T) {
	cmd := NewMigrationLagCheckCmd()
	var names []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	assert.ElementsMatch(t, []string{"migration-yaml", "poll-interval"}, names)
}

func TestLagCheck_RetiredFlagsAreGone(t *testing.T) {
	for _, flag := range []string{
		"--rest-endpoint", "--cluster-id", "--cluster-link-name",
		"--cluster-api-key", "--cluster-api-secret",
	} {
		t.Run(flag, func(t *testing.T) {
			cmd := NewMigrationLagCheckCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"--migration-yaml", writeLagManifest(t, nil), flag, "x"})
			err := cmd.Execute()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unknown flag")
		})
	}
}

func TestLagCheck_RequiresMigrationYaml(t *testing.T) {
	cmd := NewMigrationLagCheckCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(nil)
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migration-yaml")
}

// TestLagCheck_BuildsConfigFromManifest — the five values it needs map 1:1
// onto clusterlink.Config, and all five are in the manifest.
func TestLagCheck_BuildsConfigFromManifest(t *testing.T) {
	g := loadLagGateway(t, writeLagManifest(t, nil))
	cfg, _, err := buildLagCheckConfig(g)
	require.NoError(t, err)

	assert.Equal(t, "https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443", cfg.RestEndpoint)
	assert.Equal(t, "lkc-abc123", cfg.ClusterID)
	assert.Equal(t, "msk-to-cc", cfg.LinkName)
	assert.Equal(t, "CC_KEY", cfg.APIKey)
	assert.Equal(t, "CC_SECRET", cfg.APISecret)
}

// TestLagCheck_AlwaysWatchesEveryMirrorTopic — spec.topics has NO effect here;
// clusterlink.Config.Topics is empty so the TUI shows every mirror.
func TestLagCheck_AlwaysWatchesEveryMirrorTopic(t *testing.T) {
	g := loadLagGateway(t, writeLagManifest(t, func(doc string) string {
		return doc + "  topics: ['t1.order']\n"
	}))
	cfg, _, err := buildLagCheckConfig(g)
	require.NoError(t, err)
	assert.Empty(t, cfg.Topics, "spec.topics must not narrow the lag view")
}

// TestLagCheck_UsesDerivedRestCredentials — omitting restCredentials derives
// them, exactly as init and execute do.
func TestLagCheck_UsesDerivedRestCredentials(t *testing.T) {
	g := loadLagGateway(t, writeLagManifest(t, func(doc string) string {
		return strings.Replace(doc, "  clusterLink:", `      restCredentials:
        api_key: EXPLICIT_KEY
        api_secret: EXPLICIT_SECRET
  clusterLink:`, 1)
	}))
	cfg, _, err := buildLagCheckConfig(g)
	require.NoError(t, err)
	assert.Equal(t, "EXPLICIT_KEY", cfg.APIKey, "an explicit block wins over derivation")
}

// TestLagCheck_HonoursRestTLSTrust — a private-CA or self-signed destination
// REST endpoint must be reachable, or the manifest field would be a silent
// no-op.
func TestLagCheck_HonoursRestTLSTrust(t *testing.T) {
	g := loadLagGateway(t, writeLagManifest(t, func(doc string) string {
		return strings.Replace(doc, "      credentials:\n        sasl_plain:",
			"      credentials:\n        insecure_skip_tls_verify: true\n        sasl_plain:", 1)
	}))
	_, client, err := buildLagCheckConfig(g)
	require.NoError(t, err)
	require.NotNil(t, client, "an HTTP client carrying the REST leg's TLS trust must be built")
}

// TestLagCheck_WorksBeforeInitHasEverRun — its standalone use is preserved: the
// manifest can exist before any migration is registered, and lag-check reads no
// state file.
func TestLagCheck_WorksBeforeInitHasEverRun(t *testing.T) {
	g := loadLagGateway(t, writeLagManifest(t, nil))
	_, _, err := buildLagCheckConfig(g)
	require.NoError(t, err)
}

func TestLagCheck_RejectsMigrationKindManifest(t *testing.T) {
	p := filepath.Join(t.TempDir(), "migration.yaml")
	require.NoError(t, os.WriteFile(p, []byte(
		"apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: x\n"), 0600))

	cmd := NewMigrationLagCheckCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--migration-yaml", p})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GatewayMigration")
}

func TestLagCheck_ClampsPollInterval(t *testing.T) {
	assert.Equal(t, 1, clampPollInterval(0))
	assert.Equal(t, 1, clampPollInterval(-5))
	assert.Equal(t, 60, clampPollInterval(999))
	assert.Equal(t, 7, clampPollInterval(7))
}
