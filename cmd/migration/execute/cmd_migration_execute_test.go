package execute

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/targets"
	"github.com/confluentinc/kcp/internal/testsupport"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// executeManifest is the canonical GatewayMigration. Every credentials slot is a
// PATH; newFixture substitutes the SOURCE_CREDS / DEST_KAFKA_CREDS / LINK_CREDS
// sentinels with real files it writes into the temp dir, so the resolvers read
// them exactly as execute does at runtime.
const executeManifest = `apiVersion: kcp.confluent.io/v1alpha1
kind: GatewayMigration
metadata:
  name: msk-prod-to-cc-batch-1
spec:
  source:
    type: msk
    bootstrapServers:
      - b-1.msk.us-east-1.amazonaws.com:9096
    credentials: SOURCE_CREDS
  target:
    type: confluent-cloud
    clusterId: lkc-abc123
    kafka:
      bootstrapServers:
        - pkc-xxxxx.us-east-1.aws.confluent.cloud:9092
      restEndpoint: https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443
      clusterCredentials: DEST_KAFKA_CREDS
  clusterLink:
    name: msk-to-cc
    bootstrapServers:
      - pkc-xxxxx.us-east-1.aws.confluent.cloud:9092
    linkCredentials: LINK_CREDS
  gateway:
    namespace: confluent
    cr-name: gateway-initial
  staticMigration:
    route:
      name: migration-route
      targetStreamingDomain: confluent-cloud
    topicGroup:
      - topicPatterns:
          - '.*'
`

// Default credentials-file bodies for the canonical fixture.
const (
	defaultSourceCred    = "sasl_scram:\n  username: admin\n  password: secret\n  mechanism: SHA512\n"
	defaultDestKafkaCred = "sasl_plain:\n  username: CC_KEY\n  password: CC_SECRET\n  tls: true\n"
	defaultLinkCred      = "api_key: CC_KEY\napi_secret: CC_SECRET\n"
)

// credOverrides customises the three credentials files a fixture writes. An
// empty field uses the default body.
type credOverrides struct {
	source    string
	destKafka string
	link      string
}

type fixture struct {
	manifestPath string
	dir          string
}

// newFixture writes a manifest (with the default credentials files).
func newFixture(t *testing.T, mutate func(string) string) fixture {
	return newFixtureCreds(t, credOverrides{}, mutate)
}

// newFixtureCreds is newFixture with control over the credentials-file bodies,
// for tests that exercise a specific auth block or per-leg TLS setting.
func newFixtureCreds(t *testing.T, creds credOverrides, mutate func(string) string) fixture {
	t.Helper()
	dir := t.TempDir()
	f := fixture{
		dir:          dir,
		manifestPath: filepath.Join(dir, "gateway-migration.yaml"),
	}

	doc := executeManifest
	doc = strings.Replace(doc, "SOURCE_CREDS", testsupport.WriteCredFile(t, dir, "source-creds.yaml", creds.source, defaultSourceCred), 1)
	doc = strings.Replace(doc, "DEST_KAFKA_CREDS", testsupport.WriteCredFile(t, dir, "dest-kafka-creds.yaml", creds.destKafka, defaultDestKafkaCred), 1)
	doc = strings.Replace(doc, "LINK_CREDS", testsupport.WriteCredFile(t, dir, "link-creds.yaml", creds.link, defaultLinkCred), 1)
	if mutate != nil {
		doc = mutate(doc)
	}
	require.NoError(t, os.WriteFile(f.manifestPath, []byte(doc), 0600))

	return f
}

// dynamicFixture is the canonical manifest declared as a dynamicMigration.
func dynamicFixture(t *testing.T) fixture {
	t.Helper()
	return newFixture(t, func(doc string) string {
		require.Contains(t, doc, "  staticMigration:\n")
		return strings.Replace(doc, "  staticMigration:\n", "  dynamicMigration:\n", 1)
	})
}

// fixtureForMode returns the fixture whose declared type matches a plan's Mode.
func fixtureForMode(t *testing.T, mode string) fixture {
	t.Helper()
	switch mode {
	case "dynamic":
		return dynamicFixture(t)
	case "convert":
		return convertFixture(t)
	}
	return newFixture(t, nil)
}

func runExecute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewMigrationExecuteCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func loadGateway(t *testing.T, path string) *manifest.GatewayMigration {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	g, err := manifest.ParseGatewayMigration(data)
	require.NoError(t, err)
	return g
}

// freshConfig builds the MigrationConfig runMigrationExecute builds for f's
// manifest, via buildFreshMigrationConfig.
func freshConfig(t *testing.T, f fixture) (*manifest.GatewayMigration, *migration.MigrationConfig) {
	t.Helper()
	g := loadGateway(t, f.manifestPath)
	cfg := buildFreshMigrationConfig(g, "msk-prod-to-cc-batch-1")
	return g, &cfg
}

// --- command surface ---

// TestExecute_IsNamedExecute — the manifest work deliberately kept the existing
// verb, so runbooks, scripts and the published docs stay correct.
func TestExecute_IsNamedExecute(t *testing.T) {
	cmd := NewMigrationExecuteCmd()
	assert.Equal(t, "execute", cmd.Name())
	assert.False(t, cmd.Hidden, "execute is the advertised verb, not an alias")
	assert.Empty(t, cmd.Deprecated, "execute is not deprecated")
}

// TestExecute_VisibleFlagSurface — the visible flags are the manifest path,
// --dry-run and the per-policy overrides that vary a spec.<section>.policies value
// for a single run.
func TestExecute_VisibleFlagSurface(t *testing.T) {
	cmd := NewMigrationExecuteCmd()
	var visible []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if !f.Hidden {
			visible = append(visible, f.Name)
		}
	})
	assert.ElementsMatch(t, []string{
		"migration-yaml",
		"lag-threshold", "promote-batch-size", "rollout-timeout",
		"detect-unrouted-producers-duration", "consumer-offset-sync-drain-duration",
		"hot-reload-timeout", "gateway-config-port", "dry-run",
		"detect-unrouted-commits-duration", "offset-sync-concurrency",
	}, visible)
}

// TestExecute_HasNoTopologyOrAuthFlags — topology and auth come from the
// manifest, so execute has no flags for them, and no --migration-state-file.
// The per-policy override flags (--lag-threshold, --rollout-timeout, etc.)
// are NOT here: they are the live surface, asserted by
// TestExecute_VisibleFlagSurface.
func TestExecute_HasNoTopologyOrAuthFlags(t *testing.T) {
	f := newFixture(t, nil)
	for _, flag := range []string{
		"--cluster-api-key", "--cluster-api-secret", "--aws-region",
		"--sasl-scram-mechanism", "--use-sasl-iam",
		"--insecure-skip-tls-verify", "--cluster-rest-ca-cert",
		"--migration-state-file",
	} {
		t.Run(flag, func(t *testing.T) {
			_, err := runExecute(t, "--migration-yaml", f.manifestPath, flag, "1")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unknown flag")
		})
	}
}

func TestExecute_RequiresMigrationYaml(t *testing.T) {
	_, err := runExecute(t)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migration-yaml")
}

// --- reconcile runs on every invocation ---

// TestExecute_AlwaysCallsReconcile proves every run reaches migplan.Reconcile.
// The fixture's kubeconfig path does not exist, so Reconcile's live gateway
// pull fails immediately and deterministically, surfacing through
// runMigrationExecute's own wrap.
func TestExecute_AlwaysCallsReconcile(t *testing.T) {
	f := newFixture(t, nil)

	_, err := runExecute(t, "--migration-yaml", f.manifestPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to produce the reconcile plan",
		"every execute run must call migplan.Reconcile")
}

// --- policy is read fresh ---

func TestExecute_ReadsPolicyFromTheManifestOnEveryRun(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return doc + `    policies:
      lagThreshold: 42
      promoteBatchSize: 7
      rolloutTimeout: 3m
      detectUnroutedProducersDuration: 30s
      consumerOffsetSyncDrainDuration: 15s
`
	})
	g, cfg := freshConfig(t, f)
	policy := g.Spec.StaticMigration.Policies
	applyCommonPolicy(cfg, policy.CommonPolicies)
	cfg.DetectUnroutedProducersDuration = policy.DetectUnroutedProducersDuration
	cfg.ConsumerOffsetSyncDrainDuration = policy.ConsumerOffsetSyncDrainDuration

	// Both branches read these three straight from the declared section's policies.
	assert.Equal(t, 42, policy.LagThreshold)
	assert.Equal(t, 7, policy.PromoteBatchSize)
	assert.EqualValues(t, 180, policy.RolloutTimeout.Seconds())
	assert.EqualValues(t, 30, cfg.DetectUnroutedProducersDuration.Seconds())
	assert.EqualValues(t, 15, cfg.ConsumerOffsetSyncDrainDuration.Seconds())
}

// --- per-policy override flags ---

// TestExecute_PolicyOverrideFlagsReplaceManifestDefaults — only a flag the
// operator set explicitly overrides, and an explicit 0 (meaningful for every
// one of these) counts as set. An unset flag leaves the manifest default alone.
func TestExecute_PolicyOverrideFlagsReplaceManifestDefaults(t *testing.T) {
	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.Flags().Parse([]string{
		"--migration-yaml", "x",
		"--detect-unrouted-producers-duration", "60s",
		"--promote-batch-size", "0",
	}))

	g := gatewayOf(t, manifest.MigrationTypeStatic)
	g.Spec.StaticMigration.Policies = manifest.StaticPolicies{
		PromoteBatchSize:                100,
		RolloutTimeout:                  10 * time.Minute,
		DetectUnroutedProducersDuration: 30 * time.Second,
	}
	require.NoError(t, applyPolicyOverrides(cmd, g))
	p := g.Spec.StaticMigration.Policies

	assert.Equal(t, 60*time.Second, p.DetectUnroutedProducersDuration, "an explicit flag replaces the default")
	assert.Equal(t, 0, p.PromoteBatchSize, "an explicit 0 override replaces a non-zero default")
	assert.Equal(t, 10*time.Minute, p.RolloutTimeout, "an unset flag leaves the manifest default untouched")
}

// TestExecute_PolicyOverrideReachesTheConfig — an override applied to the
// manifest's defaults flows through applyPolicyOverrides onto the config, the
// same path runMigrationExecute takes for both branches.
func TestExecute_PolicyOverrideReachesTheConfig(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return doc + "    policies:\n      lagThreshold: 5\n      detectUnroutedProducersDuration: 30s\n"
	})
	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.Flags().Parse([]string{
		"--migration-yaml", f.manifestPath,
		"--lag-threshold", "99",
		"--detect-unrouted-producers-duration", "60s",
	}))

	g, cfg := freshConfig(t, f)
	require.NoError(t, applyPolicyOverrides(cmd, g))
	require.Empty(t, g.Spec.ValidatePolicies())

	policy := g.Spec.StaticMigration.Policies
	applyCommonPolicy(cfg, policy.CommonPolicies)
	cfg.DetectUnroutedProducersDuration = policy.DetectUnroutedProducersDuration
	assert.Equal(t, 99, policy.LagThreshold,
		"the override replaces the manifest default in the policy both branches read")
	assert.EqualValues(t, 60, cfg.DetectUnroutedProducersDuration.Seconds())
}

// An explicit 0 counts as set; for the commits window it skips the check.
func TestExecute_ConversionPolicyOverrides(t *testing.T) {
	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.Flags().Parse([]string{
		"--migration-yaml", "x",
		"--detect-unrouted-commits-duration", "45s",
		"--offset-sync-concurrency", "0",
	}))

	g := gatewayOf(t, manifest.MigrationTypeDynamicToStatic)
	g.Spec.DynamicToStaticConversion.Policies = manifest.DynamicToStaticPolicies{DetectUnroutedCommitsDuration: 20 * time.Second, OffsetSyncConcurrency: 4}
	require.NoError(t, applyPolicyOverrides(cmd, g))
	p := g.Spec.DynamicToStaticConversion.Policies

	assert.Equal(t, 45*time.Second, p.DetectUnroutedCommitsDuration, "an explicit flag replaces the default")
	assert.Equal(t, 0, p.OffsetSyncConcurrency, "an explicit 0 replaces a non-zero default")

	unset := NewMigrationExecuteCmd()
	require.NoError(t, unset.Flags().Parse([]string{"--migration-yaml", "x"}))
	g2 := gatewayOf(t, manifest.MigrationTypeDynamicToStatic)
	g2.Spec.DynamicToStaticConversion.Policies = manifest.DynamicToStaticPolicies{DetectUnroutedCommitsDuration: 20 * time.Second, OffsetSyncConcurrency: 4}
	require.NoError(t, applyPolicyOverrides(unset, g2))
	q := g2.Spec.DynamicToStaticConversion.Policies
	assert.Equal(t, 20*time.Second, q.DetectUnroutedCommitsDuration, "an unset flag leaves the manifest value alone")
	assert.Equal(t, 4, q.OffsetSyncConcurrency)

	skip := NewMigrationExecuteCmd()
	require.NoError(t, skip.Flags().Parse([]string{"--migration-yaml", "x", "--detect-unrouted-commits-duration", "0s"}))
	g3 := gatewayOf(t, manifest.MigrationTypeDynamicToStatic)
	g3.Spec.DynamicToStaticConversion.Policies = manifest.DynamicToStaticPolicies{DetectUnroutedCommitsDuration: 20 * time.Second}
	require.NoError(t, applyPolicyOverrides(skip, g3))
	assert.Zero(t, g3.Spec.DynamicToStaticConversion.Policies.DetectUnroutedCommitsDuration, "an explicit 0s skips the check")
}

// An unset commits window logs 0s (skipped); an unset concurrency logs its default 8.
func TestExecute_PolicyLogArgsLogEffectiveConversionDefaults(t *testing.T) {
	kv := kvMap(t, effectivePolicyLogArgs("mig-1", gatewayOf(t, manifest.MigrationTypeDynamicToStatic).Spec))
	assert.Equal(t, time.Duration(0), kv["detect_unrouted_commits_duration"])
	assert.Equal(t, 8, kv["offset_sync_concurrency"])
}

// kvMap turns slog-style key/value args into a map, requiring string keys.
func kvMap(t *testing.T, args []any) map[string]any {
	t.Helper()
	require.Zero(t, len(args)%2, "log args must be key/value pairs")
	m := make(map[string]any, len(args)/2)
	for i := 0; i < len(args); i += 2 {
		key, ok := args[i].(string)
		require.True(t, ok, "log arg %d must be a string key", i)
		m[key] = args[i+1]
	}
	return m
}

// TestExecute_InvalidPolicyOverrideIsRejected — an override can carry a value the
// manifest never did, so the effective policy is re-validated. A sub-10s detect
// duration is rejected before any network work.
func TestExecute_InvalidPolicyOverrideIsRejected(t *testing.T) {
	f := newFixture(t, nil)
	_, err := runExecute(t, "--migration-yaml", f.manifestPath,
		"--detect-unrouted-producers-duration", "5s")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "detectUnroutedProducersDuration")
}

// resolvedLegs resolves the three connection legs from the manifest exactly as
// both branches do: source and destination Kafka through sourceConn /
// destinationConn, and the cluster-link REST leg from linkCredentials.
func resolvedLegs(t *testing.T, g *manifest.GatewayMigration) (src, dst types.KafkaSourceConn, rest *targets.Credentials) {
	t.Helper()
	src, err := sourceConn(g)
	require.NoError(t, err)
	dst, err = destinationConn(g)
	require.NoError(t, err)
	rest, err = g.RestCredentials()
	require.NoError(t, err)
	return src, dst, rest
}

// --- source auth mapping ---

func TestExecute_MapsSourceAuthOntoSourceConn(t *testing.T) {
	for name, tc := range map[string]struct {
		block  string
		assert func(*testing.T, types.KafkaSourceConn)
	}{
		"sasl_scram": {
			"sasl_scram:\n  username: u\n  password: p\n  mechanism: SHA256\n",
			func(t *testing.T, o types.KafkaSourceConn) {
				sc := o.AuthMethod.SASLScram
				require.NotNil(t, sc)
				assert.Equal(t, "u", sc.Username)
				assert.Equal(t, "p", sc.Password)
				assert.Equal(t, "SHA256", sc.Mechanism)
			},
		},
		"iam": {
			"iam:\n  region: eu-west-2\n",
			func(t *testing.T, o types.KafkaSourceConn) {
				require.NotNil(t, o.AuthMethod.IAM)
				assert.Equal(t, "eu-west-2", o.AuthMethod.IAM.Region)
			},
		},
		"sasl_plain": {
			"sasl_plain:\n  username: pu\n  password: pp\n  tls: true\n",
			func(t *testing.T, o types.KafkaSourceConn) {
				sp := o.AuthMethod.SASLPlain
				require.NotNil(t, sp)
				assert.Equal(t, "pu", sp.Username)
				assert.True(t, sp.UseTLS, "tls: true must not be silently dropped to cleartext")
			},
		},
		"unauthenticated_plaintext": {
			"unauthenticated_plaintext: {}\n",
			func(t *testing.T, o types.KafkaSourceConn) {
				assert.NotNil(t, o.AuthMethod.UnauthenticatedPlaintext)
				assert.Nil(t, o.AuthMethod.SASLScram)
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixtureCreds(t, credOverrides{source: tc.block}, nil)
			conn, err := sourceConn(loadGateway(t, f.manifestPath))
			require.NoError(t, err)
			tc.assert(t, conn)
		})
	}
}

// TestExecute_InsecureSkipIsPerLegFile — each credentials file opts into
// skipping TLS verification independently, so all three legs can be relaxed by
// setting it on each file.
func TestExecute_InsecureSkipIsPerLegFile(t *testing.T) {
	f := newFixtureCreds(t, credOverrides{
		source:    "insecure_skip_tls_verify: true\n" + defaultSourceCred,
		destKafka: "insecure_skip_tls_verify: true\n" + defaultDestKafkaCred,
		link:      "api_key: CC_KEY\napi_secret: CC_SECRET\ninsecure_skip_verify: true\n",
	}, nil)
	src, dst, rest := resolvedLegs(t, loadGateway(t, f.manifestPath))
	assert.True(t, src.InsecureSkipTLSVerify)
	assert.True(t, dst.InsecureSkipTLSVerify)
	assert.True(t, rest.InsecureSkipVerify, "the REST leg reads its own linkCredentials file")
}

// TestExecute_DestinationKafkaUsesItsClusterCredentials — the destination Kafka
// leg is authenticated from spec.target.kafka.clusterCredentials.
func TestExecute_DestinationKafkaUsesItsClusterCredentials(t *testing.T) {
	f := newFixture(t, nil)
	_, dst, _ := resolvedLegs(t, loadGateway(t, f.manifestPath))
	dstType, err := dst.GetSelectedAuthType()
	require.NoError(t, err)
	assert.Equal(t, types.AuthTypeSASLPlain, dstType)
	require.NotNil(t, dst.AuthMethod.SASLPlain)
	assert.Equal(t, "CC_KEY", dst.AuthMethod.SASLPlain.Username)
	assert.Equal(t, "CC_SECRET", dst.AuthMethod.SASLPlain.Password)
}

// TestExecute_DestSASLPlainDefaultsToTLS is the ⚠️ backward-compat fix (A.3):
// the old destination client always dialled SASL_SSL over the public trust
// store. AdminOptionForAuthMethod maps sasl_plain with no ca_cert/tls to
// cleartext SASL_PLAINTEXT, so destinationConn must default UseTLS=true when
// neither is set — the single most important regression to prove, since every
// existing manifest never sets tls: nor ca_cert: on the destination.
func TestExecute_DestSASLPlainDefaultsToTLS(t *testing.T) {
	f := newFixtureCreds(t, credOverrides{
		destKafka: "sasl_plain:\n  username: CC_KEY\n  password: CC_SECRET\n",
	}, nil)
	_, dst, _ := resolvedLegs(t, loadGateway(t, f.manifestPath))
	require.NotNil(t, dst.AuthMethod.SASLPlain)
	assert.True(t, dst.AuthMethod.SASLPlain.UseTLS, "no ca_cert/tls set must still default to SASL_SSL, not a silent downgrade to SASL_PLAINTEXT")
}

// TestExecute_DestSASLPlainCACertIsNotOverridden — the compat default must not
// clobber an explicit ca_cert (already selects SASL_SSL) nor flip UseTLS when
// one is already set.
func TestExecute_DestSASLPlainCACertIsNotOverridden(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "dest-ca.pem")
	require.NoError(t, os.WriteFile(ca, []byte("pem"), 0600))

	f := newFixtureCreds(t, credOverrides{
		destKafka: "sasl_plain:\n  username: CC_KEY\n  password: CC_SECRET\n  ca_cert: " + ca + "\n",
	}, nil)
	_, dst, _ := resolvedLegs(t, loadGateway(t, f.manifestPath))
	require.NotNil(t, dst.AuthMethod.SASLPlain)
	assert.Equal(t, ca, dst.AuthMethod.SASLPlain.CACert)
	assert.False(t, dst.AuthMethod.SASLPlain.UseTLS, "ca_cert already selects SASL_SSL; the compat default must not also flip UseTLS")
}

// --- ported preRunE errors (§6) ---

// TestExecute_PortsBespokePreRunErrors: execute's three hand-written
// preRunE errors must have explicit homes in the manifest validator, or they
// are silently dropped when the flag lattice is deleted.
func TestExecute_PortsBespokePreRunErrors(t *testing.T) {
	t.Run("invalid sasl_scram mechanism", func(t *testing.T) {
		f := newFixtureCreds(t, credOverrides{
			source: "sasl_scram:\n  username: admin\n  password: secret\n  mechanism: SHA1\n",
		}, nil)
		_, err := runExecute(t, "--migration-yaml", f.manifestPath)
		require.Error(t, err)
		assert.Contains(t, strings.ToLower(err.Error()), "mechanism")
	})

	t.Run("sub-10s detect duration", func(t *testing.T) {
		f := newFixture(t, func(doc string) string {
			return doc + "    policies:\n      detectUnroutedProducersDuration: 5s\n"
		})
		_, err := runExecute(t, "--migration-yaml", f.manifestPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "detectUnroutedProducersDuration")
	})

	t.Run("negative drain duration", func(t *testing.T) {
		f := newFixture(t, func(doc string) string {
			return doc + "    policies:\n      consumerOffsetSyncDrainDuration: -5s\n"
		})
		_, err := runExecute(t, "--migration-yaml", f.manifestPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "consumerOffsetSyncDrainDuration")
	})
}

// --- security review F2/F4: TLS trust must be per-leg ---

// TestExecute_SourceInsecureSkipDoesNotReachTheDestination. The manifest spells
// insecure_skip_tls_verify per credentials block. Collapsing the blocks into
// one boolean means an operator relaxing TLS for a self-signed on-prem SOURCE
// also stops verifying the destination connections — which transmit the
// destination API key as SASL/PLAIN and as HTTP Basic. Anyone able to MITM the
// path to the destination then harvests them.
func TestExecute_SourceInsecureSkipDoesNotReachTheDestination(t *testing.T) {
	f := newFixtureCreds(t, credOverrides{
		source: "insecure_skip_tls_verify: true\n" + defaultSourceCred,
	}, nil)
	src, dst, rest := resolvedLegs(t, loadGateway(t, f.manifestPath))

	assert.True(t, src.InsecureSkipTLSVerify, "the source asked for it")
	assert.False(t, dst.InsecureSkipTLSVerify, "the destination Kafka leg did not")
	assert.False(t, rest.InsecureSkipVerify, "nor the destination REST leg")
}

// TestExecute_DestinationInsecureSkipDoesNotReachTheSource — the same in reverse.
// Each leg is its own file, so the destination Kafka leg relaxing verification
// reaches neither the source nor the (separate) REST leg.
func TestExecute_DestinationInsecureSkipDoesNotReachTheSource(t *testing.T) {
	f := newFixtureCreds(t, credOverrides{
		destKafka: "insecure_skip_tls_verify: true\n" + defaultDestKafkaCred,
	}, nil)
	src, dst, rest := resolvedLegs(t, loadGateway(t, f.manifestPath))

	assert.False(t, src.InsecureSkipTLSVerify)
	assert.True(t, dst.InsecureSkipTLSVerify)
	assert.False(t, rest.InsecureSkipVerify, "the REST leg is its own file and did not ask for it")
}

// TestExecute_LinkCredentialsGovernTheRestLeg — the REST leg is driven entirely
// by spec.clusterLink.linkCredentials, so a link file that did not ask to skip
// verification keeps verifying no matter what the other legs set.
func TestExecute_LinkCredentialsGovernTheRestLeg(t *testing.T) {
	f := newFixtureCreds(t, credOverrides{
		source: "insecure_skip_tls_verify: true\n" + defaultSourceCred,
		link:   "api_key: K\napi_secret: S\n",
	}, nil)
	src, _, rest := resolvedLegs(t, loadGateway(t, f.manifestPath))

	assert.True(t, src.InsecureSkipTLSVerify)
	assert.False(t, rest.InsecureSkipVerify,
		"a link credentials file that did not ask for it must keep verifying")
}

// --- security review F5: the Kafka leg authenticates with the KAFKA block ---

// TestExecute_DestinationKafkaUsesTheKafkaCredentialNotTheLinkOne. The
// destination bootstrap is dialled with SASL/PLAIN from clusterCredentials, while
// the REST leg uses the separate linkCredentials — feeding the broker from the
// REST credential would send a deliberately broader REST key to the broker
// instead of the narrower Kafka-scoped one.
func TestExecute_DestinationKafkaUsesTheKafkaCredentialNotTheLinkOne(t *testing.T) {
	f := newFixtureCreds(t, credOverrides{
		link: "api_key: REST_ONLY_KEY\napi_secret: REST_ONLY_SECRET\n",
	}, nil)
	_, dst, rest := resolvedLegs(t, loadGateway(t, f.manifestPath))

	require.NotNil(t, dst.AuthMethod.SASLPlain, "the Kafka leg uses spec.target.kafka.clusterCredentials")
	assert.Equal(t, "CC_KEY", dst.AuthMethod.SASLPlain.Username)
	assert.Equal(t, "CC_SECRET", dst.AuthMethod.SASLPlain.Password)
	assert.Equal(t, "REST_ONLY_KEY", rest.APIKey, "the REST leg uses linkCredentials")
	assert.Equal(t, "REST_ONLY_SECRET", rest.APISecret)
}

// TestExecute_RestCredentialsComeFromLinkCredentials — the REST leg is resolved
// from spec.clusterLink.linkCredentials, never derived from the Kafka leg.
func TestExecute_RestCredentialsComeFromLinkCredentials(t *testing.T) {
	f := newFixture(t, nil)
	_, dst, rest := resolvedLegs(t, loadGateway(t, f.manifestPath))
	require.NotNil(t, dst.AuthMethod.SASLPlain)
	assert.Equal(t, "CC_KEY", dst.AuthMethod.SASLPlain.Username)
	assert.Equal(t, "CC_KEY", rest.APIKey)
}

// --- helper functions ---

func TestBuildFreshMigrationConfig_PopulatesManifestFields(t *testing.T) {
	f := newFixture(t, nil)
	g := loadGateway(t, f.manifestPath)

	cfg := buildFreshMigrationConfig(g, "msk-prod-to-cc-batch-1")

	assert.Equal(t, "msk-prod-to-cc-batch-1", cfg.MigrationId)
	assert.Equal(t, "lkc-abc123", cfg.ClusterId)
	assert.Equal(t, "https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443", cfg.ClusterRestEndpoint)
	assert.Equal(t, "msk-to-cc", cfg.ClusterLinkName)
	assert.Equal(t, "confluent", cfg.K8sNamespace)
	assert.Equal(t, "gateway-initial", cfg.InitialCrName)
	assert.Equal(t, "migration-route", cfg.Route)
	assert.False(t, cfg.PauseConsumerOffsetSync)
	assert.Empty(t, cfg.ConsumerOffsetSyncBaseline, "fixture manifest declares no baseline")
	assert.Empty(t, cfg.Topics, "topics require a live migplan.Reconcile — not set here")
	assert.Empty(t, cfg.FenceYAML)
}

// --- --dry-run ---

// TestExecute_DryRun_ValidatesPolicyOverrides: --dry-run applies and validates
// command-line policy overrides as a real run does, so an invalid override
// (e.g. a negative lag threshold) is rejected before reconcile is attempted.
func TestExecute_DryRun_ValidatesPolicyOverrides(t *testing.T) {
	f := newFixture(t, nil)

	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--dry-run", "--lag-threshold=-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lagThreshold: must not be negative")
	assert.NotContains(t, err.Error(), "failed to produce the reconcile plan",
		"an invalid override must be rejected before reconcile is attempted")
}

// TestExecute_DryRun_RejectsInapplicablePolicyFlag — a policy flag the declared
// type does not have is an error under --dry-run too, before any I/O.
func TestExecute_DryRun_RejectsInapplicablePolicyFlag(t *testing.T) {
	f := newFixture(t, nil) // declares staticMigration

	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--dry-run", "--offset-sync-concurrency=4")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--offset-sync-concurrency")
	assert.Contains(t, err.Error(), "staticMigration")
	assert.NotContains(t, err.Error(), "failed to produce the reconcile plan",
		"an inapplicable flag must be rejected before reconcile is attempted")
}

// TestExecute_DryRun_IgnoresInapplicableEnvPolicy — an exported policy env var
// the declared type does not carry is silently ignored (as before types were
// declared); only an explicit --flag is an error.
func TestExecute_DryRun_IgnoresInapplicableEnvPolicy(t *testing.T) {
	f := newFixture(t, nil) // declares staticMigration
	t.Setenv("OFFSET_SYNC_CONCURRENCY", "4")

	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--dry-run")
	require.Error(t, err, "reconcile still fails against the fixture's unreachable kubeconfig")
	assert.Contains(t, err.Error(), "failed to produce the reconcile plan",
		"an env-derived inapplicable value must not be rejected as a flag")

	_, err = runExecute(t, "--migration-yaml", f.manifestPath, "--dry-run", "--offset-sync-concurrency=4")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--offset-sync-concurrency", "the explicit flag is still rejected")
}

// TestExecute_DryRun_FailsAtReconcile proves --dry-run reaches migplan.Reconcile
// and nothing else — it runs no FSM transition.
func TestExecute_DryRun_FailsAtReconcile(t *testing.T) {
	f := newFixture(t, nil)

	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--dry-run")
	require.Error(t, err, "reconcile fails deterministically against the fixture's unreachable kubeconfig")
	assert.Contains(t, err.Error(), "failed to produce the reconcile plan")
}

// --- executePlan: acting on the reconcile result ---

// serviceBuilds counts how many times each downstream service was built — the
// first thing either state machine's branch does, so zero builds means no
// state machine ran.
type serviceBuilds struct {
	offsets, gateway, clusterLink, convert int
}

func (b *serviceBuilds) total() int { return b.offsets + b.gateway + b.clusterLink + b.convert }

// countingDeps wraps base so every service build is counted in b.
func countingDeps(base executorDependencies, b *serviceBuilds) executorDependencies {
	counted := executorDependencies{
		offsets: func(g *manifest.GatewayMigration) (offset.Provider, offset.Provider, func() error, error) {
			b.offsets++
			return base.offsets(g)
		},
		gateway: func(g *manifest.GatewayMigration) (gateway.Service, error) {
			b.gateway++
			return base.gateway(g)
		},
		clusterLink: func(g *manifest.GatewayMigration) (clusterlink.Service, error) {
			b.clusterLink++
			return base.clusterLink(g)
		},
	}
	if base.convert != nil {
		counted.convert = func(g *manifest.GatewayMigration) (convertServices, error) {
			b.convert++
			return base.convert(g)
		}
	}
	return counted
}

// runExecutePlan drives executePlan for f's manifest with res standing in for
// this run's live reconcile, returning the command's stdout.
func runExecutePlan(t *testing.T, f fixture, res *migplan.Result, deps executorDependencies) (string, error) {
	t.Helper()
	g, config := freshConfig(t, f)
	var out strings.Builder
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	err := executePlan(cmd, g, config, res, deps)
	return out.String(), err
}

// A nothing-to-do result runs no state machine in either mode: no downstream
// service is even built, and the run reports completion with nothing to do.
func TestExecutePlan_NothingToDo_RunsNoStateMachine(t *testing.T) {
	for _, mode := range []string{"static", "dynamic"} {
		t.Run(mode, func(t *testing.T) {
			var builds serviceBuilds
			res := &migplan.Result{Route: "migration-route", PromoteTopics: []string{}, Mode: mode, NothingToDo: true}

			out, err := runExecutePlan(t, fixtureForMode(t, mode), res, countingDeps(stubDeps(nil, nil), &builds))

			require.NoError(t, err)
			assert.Zero(t, builds.total(), "no service may be built when there is nothing to do")
			assert.Contains(t, out, "Migration completed: msk-prod-to-cc-batch-1")
			assert.Contains(t, out, "nothing to do")
		})
	}
}

// Promoted but not switched: nothing is left to promote, but the switch is
// still owed, so the state machine runs.
func TestExecutePlan_PromotedNotSwitched_RunsTheStateMachine(t *testing.T) {
	cases := map[string]*migplan.Result{
		"static": staticResult([]string{}),
		"dynamic": {Route: "migration-route", PromoteTopics: []string{}, FenceYAML: dynamicFenceYAML,
			SwitchoverYAML: dynamicSwitchoverYAML, GatewayYAML: dynamicRouteGatewayYAML, Mode: "dynamic"},
	}
	for mode, res := range cases {
		t.Run(mode, func(t *testing.T) {
			var builds serviceBuilds

			out, err := runExecutePlan(t, fixtureForMode(t, mode), res, countingDeps(stubDeps(nil, nil), &builds))

			require.NoError(t, err)
			assert.NotZero(t, builds.total(), "the state machine must run to apply the owed switch")
			assert.Contains(t, out, "Migration completed: msk-prod-to-cc-batch-1")
			assert.NotContains(t, out, "nothing to do")
		})
	}
}

// Every topic migrated but an offset-sync restore still owed: the state
// machine runs, and its only write is the restore.
func TestExecutePlan_RestoreOnly_RunsTheStateMachine(t *testing.T) {
	var builds serviceBuilds
	rec := &alterRecordingClusterLink{}
	res := &migplan.Result{Route: "migration-route", PromoteTopics: []string{}, GatewayYAML: staticRouteGatewayYAML,
		Mode: "static", RestoreOffsetSync: true}

	out, err := runExecutePlan(t, pausingFixture(t), res, countingDeps(stubDeps(nil, rec), &builds))

	require.NoError(t, err)
	assert.NotZero(t, builds.total(), "the state machine must run to apply the owed restore")
	assert.NotContains(t, out, "nothing to do")
	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Equal(t, []string{"true"}, rec.set, "the only write is the restore to the enabled baseline")
}

// A refused result is an error, and nothing runs.
func TestExecutePlan_Refused_RunsNothing(t *testing.T) {
	var builds serviceBuilds
	res := &migplan.Result{Mode: "static", Refused: true, Reasons: []string{"t1: not found on the source"}}

	_, err := runExecutePlan(t, newFixture(t, nil), res, countingDeps(stubDeps(nil, nil), &builds))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "t1: not found on the source")
	assert.Zero(t, builds.total())
}

// A conversion plan runs the d2s state machine through runConvertBranch: it builds the gateway and the
// conversion's own services, never the offset providers or the cluster-link client the other branches use.
func TestExecutePlan_ConversionRunsTheConvertBranch(t *testing.T) {
	var builds serviceBuilds
	w := newConvertWorld("orders-app")

	out, err := runExecutePlan(t, convertFixture(t), convertPlanResult(), countingDeps(convertDeps(&patchRecordingGateway{}, w), &builds))

	require.NoError(t, err)
	assert.Contains(t, out, "Route conversion completed: msk-prod-to-cc-batch-1")
	assert.Equal(t, 1, builds.gateway)
	assert.Equal(t, 1, builds.convert)
	assert.Zero(t, builds.offsets, "a conversion builds no offset providers")
	assert.Zero(t, builds.clusterLink, "a conversion builds no cluster-link client")
}

// A completed conversion re-runs as nothing to do, reported in conversion terms.
func TestExecutePlan_ConversionNothingToDo(t *testing.T) {
	var builds serviceBuilds
	res := &migplan.Result{Route: "migration-route", Mode: "convert", NothingToDo: true}

	out, err := runExecutePlan(t, convertFixture(t), res, countingDeps(stubDeps(nil, nil), &builds))

	require.NoError(t, err)
	assert.Zero(t, builds.total())
	assert.Contains(t, out, "✅ Route conversion completed: msk-prod-to-cc-batch-1 — nothing to do: the route is already static on its target domain")
	assert.NotContains(t, out, "Migration completed")
}

// --- declared migration type: typed policies and flag applicability ---

func newExecuteCmd(t *testing.T) *cobra.Command {
	t.Helper()
	return NewMigrationExecuteCmd()
}

func gatewayOf(t *testing.T, mt manifest.MigrationType) *manifest.GatewayMigration {
	t.Helper()
	g := &manifest.GatewayMigration{}
	route := manifest.RouteRef{Name: "migration-route", TargetStreamingDomain: "cc"}
	tg := []manifest.TopicGroupEntry{{Topics: &[]string{"t1"}}}
	switch mt {
	case manifest.MigrationTypeStatic:
		g.Spec.StaticMigration = &manifest.StaticMigration{Route: route, TopicGroup: tg}
	case manifest.MigrationTypeDynamic:
		g.Spec.DynamicMigration = &manifest.DynamicMigration{Route: route, TopicGroup: tg}
	case manifest.MigrationTypeDynamicToStatic:
		g.Spec.DynamicToStaticConversion = &manifest.DynamicToStaticConversion{Route: route}
	}
	return g
}

// Review Focus 3.
func TestApplyPolicyOverrides_RejectsInapplicableFlag(t *testing.T) {
	cases := []struct {
		flag string
		val  string
		mt   manifest.MigrationType
	}{
		{"promote-batch-size", "5", manifest.MigrationTypeDynamicToStatic},
		{"lag-threshold", "1", manifest.MigrationTypeDynamicToStatic},
		{"detect-unrouted-producers-duration", "30s", manifest.MigrationTypeDynamicToStatic},
		{"consumer-offset-sync-drain-duration", "5s", manifest.MigrationTypeDynamic},
		{"detect-unrouted-commits-duration", "30s", manifest.MigrationTypeStatic},
		{"offset-sync-concurrency", "4", manifest.MigrationTypeDynamic},
	}
	for _, c := range cases {
		t.Run(c.flag+"/"+string(c.mt), func(t *testing.T) {
			cmd := newExecuteCmd(t)
			require.NoError(t, cmd.Flags().Set(c.flag, c.val))
			err := applyPolicyOverrides(cmd, gatewayOf(t, c.mt))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "--"+c.flag)
			assert.Contains(t, err.Error(), c.mt.SectionKey())
		})
	}
}

// With several inapplicable flags set, the error names the alphabetically first,
// so the message is deterministic run to run.
func TestCheckPolicyFlagsApply_NamesTheAlphabeticallyFirstOffender(t *testing.T) {
	for i := 0; i < 20; i++ {
		cmd := newExecuteCmd(t)
		require.NoError(t, cmd.Flags().Set("promote-batch-size", "5"))
		require.NoError(t, cmd.Flags().Set("lag-threshold", "1"))
		err := checkPolicyFlagsApply(cmd, manifest.MigrationTypeDynamicToStatic)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--lag-threshold")
		assert.NotContains(t, err.Error(), "--promote-batch-size")
	}
}

func TestApplyPolicyOverrides_AppliesToTheDeclaredSection(t *testing.T) {
	cmd := newExecuteCmd(t)
	require.NoError(t, cmd.Flags().Set("promote-batch-size", "7"))
	require.NoError(t, cmd.Flags().Set("hot-reload-timeout", "45s"))
	g := gatewayOf(t, manifest.MigrationTypeStatic)
	require.NoError(t, applyPolicyOverrides(cmd, g))
	assert.Equal(t, 7, g.Spec.StaticMigration.Policies.PromoteBatchSize)
	assert.Equal(t, 45*time.Second, g.Spec.StaticMigration.Policies.HotReloadTimeout)

	cmd = newExecuteCmd(t)
	require.NoError(t, cmd.Flags().Set("detect-unrouted-commits-duration", "20s"))
	g = gatewayOf(t, manifest.MigrationTypeDynamicToStatic)
	require.NoError(t, applyPolicyOverrides(cmd, g))
	assert.Equal(t, 20*time.Second, g.Spec.DynamicToStaticConversion.Policies.DetectUnroutedCommitsDuration)
}

// An explicit 0 is meaningful (promote all / no deadline / skip) and must win
// over the manifest default; an unset flag must leave the default alone.
func TestApplyPolicyOverrides_ZeroStillOverrides(t *testing.T) {
	cmd := newExecuteCmd(t)
	require.NoError(t, cmd.Flags().Set("promote-batch-size", "0"))
	g := gatewayOf(t, manifest.MigrationTypeDynamic)
	g.Spec.DynamicMigration.Policies = manifest.DynamicPolicies{PromoteBatchSize: 100, LagThreshold: 4}
	require.NoError(t, applyPolicyOverrides(cmd, g))
	assert.Equal(t, 0, g.Spec.DynamicMigration.Policies.PromoteBatchSize)
	assert.Equal(t, 4, g.Spec.DynamicMigration.Policies.LagThreshold, "an unset flag leaves the manifest default in force")
}

func TestExecutePlan_UnknownTypeErrors(t *testing.T) {
	cmd := newExecuteCmd(t)
	err := executePlan(cmd, &manifest.GatewayMigration{}, &migration.MigrationConfig{}, &migplan.Result{Mode: "static"}, executorDependencies{})
	require.ErrorContains(t, err, "exactly one of staticMigration, dynamicMigration, dynamicToStaticConversion")
}

func TestExecutePlan_ResultModeMustMatchDeclaredType(t *testing.T) {
	cmd := newExecuteCmd(t)
	g := gatewayOf(t, manifest.MigrationTypeDynamic)
	err := executePlan(cmd, g, &migration.MigrationConfig{}, &migplan.Result{Mode: "static"}, executorDependencies{})
	require.ErrorContains(t, err, "does not match the declared")
}

func TestEffectivePolicyLogArgs_NamesTheType(t *testing.T) {
	g := gatewayOf(t, manifest.MigrationTypeDynamic)
	kv := kvMap(t, effectivePolicyLogArgs("mig-1", g.Spec))
	assert.Equal(t, "mig-1", kv["migration_id"])
	assert.Equal(t, "DYNAMIC", kv["migration_type"])
	assert.Contains(t, kv, "promote_batch_size")
	assert.NotContains(t, kv, "offset_sync_concurrency", "a dynamic migration's audit line carries only its own policies")
}
