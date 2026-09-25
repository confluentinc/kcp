package execute

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/testsupport"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// executeManifest is the canonical GatewayMigration. Every credentials slot is a
// PATH; newFixture substitutes the SOURCE_CREDS / DEST_KAFKA_CREDS / LINK_CREDS
// sentinels with real files it writes into the temp dir, so the resolvers read
// them exactly as init/execute do at runtime.
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
  route:
    name: migration-route
    topicGroup:
      - topicPatterns:
          - '.*'
    targetStreamingDomain: confluent-cloud
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

// newFixture writes a manifest (with the default credentials files). There is
// no migration state file any more and no fenced or switchover CR
// file — every one of those is derived fresh, from the manifest and live
// reconcile, on every execute.
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

// freshConfig builds the MigrationConfig runMigrationExecute itself would
// build for f's manifest — pure manifest projections, exactly as
// buildFreshMigrationConfig produces on every run now that there is no
// migration state file to read an existing entry from.
func freshConfig(t *testing.T, f fixture) (*manifest.GatewayMigration, *migration.MigrationConfig) {
	t.Helper()
	g := loadGateway(t, f.manifestPath)
	kubeConfigPath, err := resolveKubeConfigPath(g)
	require.NoError(t, err)
	cfg := buildFreshMigrationConfig(g, "msk-prod-to-cc-batch-1", kubeConfigPath)
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

// TestExecute_VisibleFlagSurface — there is no migration state file any more
// (--migration-state-file was dropped); what stays on the command line is the
// manifest path and the per-policy overrides that vary a spec.defaultPolicies
// value for a single run. --run-report is registered but hidden (a diagnostics
// path whose only consumer is the performance rig), so it is asserted
// separately rather than padding the advertised surface.
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
	}, visible)

	runReport := cmd.Flags().Lookup("run-report")
	require.NotNil(t, runReport, "run-report must stay registered for the performance rig")
	assert.True(t, runReport.Hidden, "run-report is a diagnostics flag and must stay hidden")
}

// TestExecute_RetiredFlagsAreGone — the topology/auth flags moved into the
// manifest, and --migration-state-file itself is retired. The
// per-policy override flags (--lag-threshold, --rollout-timeout, etc.) are NOT
// here: they are the live surface, asserted by TestExecute_VisibleFlagSurface.
func TestExecute_RetiredFlagsAreGone(t *testing.T) {
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

// TestExecute_AlwaysCallsReconcile proves every run reaches migplan.Reconcile:
// there is no migration state file and no persisted position to skip it for.
// The fixture's kubeconfig path does not exist, so Reconcile's live gateway
// pull fails immediately and deterministically, surfacing through
// runMigrationExecute's own wrap.
func TestExecute_AlwaysCallsReconcile(t *testing.T) {
	f := newFixture(t, nil)

	_, err := runExecute(t, "--migration-yaml", f.manifestPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to produce the reconcile plan",
		"every execute run must call migplan.Reconcile — there is no persisted position to resume from")
}

// --- policy is read fresh ---

func TestExecute_ReadsPolicyFromTheManifestOnEveryRun(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return doc + `  defaultPolicies:
    lagThreshold: 42
    promoteBatchSize: 7
    rolloutTimeout: 3m
    detectUnroutedProducersDuration: 30s
    consumerOffsetSyncDrainDuration: 15s
`
	})
	g, cfg := freshConfig(t, f)
	opts, err := buildExecutorOpts(g, cfg, nil)
	require.NoError(t, err)

	assert.EqualValues(t, 42, opts.LagThreshold)
	assert.Equal(t, 7, opts.PromoteBatchSize)
	assert.EqualValues(t, 180, opts.RolloutTimeout.Seconds())
	assert.EqualValues(t, 30, opts.MigrationConfig.DetectUnroutedProducersDuration.Seconds())
	assert.EqualValues(t, 15, opts.MigrationConfig.ConsumerOffsetSyncDrainDuration.Seconds())
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

	p := manifest.DefaultPolicies{
		PromoteBatchSize:                100,
		RolloutTimeout:                  10 * time.Minute,
		DetectUnroutedProducersDuration: 30 * time.Second,
	}
	applyPolicyOverrides(cmd, &p)

	assert.Equal(t, 60*time.Second, p.DetectUnroutedProducersDuration, "an explicit flag replaces the default")
	assert.Equal(t, 0, p.PromoteBatchSize, "an explicit 0 override replaces a non-zero default")
	assert.Equal(t, 10*time.Minute, p.RolloutTimeout, "an unset flag leaves the manifest default untouched")
}

// TestExecute_PolicyOverrideReachesExecutorOpts — an override applied to the
// manifest's defaults flows all the way through buildExecutorOpts, the same path
// runMigrationExecute takes.
func TestExecute_PolicyOverrideReachesExecutorOpts(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return doc + "  defaultPolicies:\n    lagThreshold: 5\n    detectUnroutedProducersDuration: 30s\n"
	})
	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.Flags().Parse([]string{
		"--migration-yaml", f.manifestPath,
		"--lag-threshold", "99",
		"--detect-unrouted-producers-duration", "60s",
	}))

	g, cfg := freshConfig(t, f)
	applyPolicyOverrides(cmd, &g.Spec.DefaultPolicies)
	require.Empty(t, g.Spec.DefaultPolicies.Validate())

	opts, err := buildExecutorOpts(g, cfg, nil)
	require.NoError(t, err)
	assert.EqualValues(t, 99, opts.LagThreshold)
	assert.EqualValues(t, 60, opts.MigrationConfig.DetectUnroutedProducersDuration.Seconds())
}

// TestExecute_RecordsLastRunPolicies — buildExecutorOpts stamps the effective
// policy (manifest defaults with this run's overrides applied) onto the config
// as an observational LastRunPolicies record for the run report. It is never
// read back by kcp, so this proves it is at least written, and that it
// captures the OVERRIDE rather than the manifest default.
func TestExecute_RecordsLastRunPolicies(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return doc + "  defaultPolicies:\n    lagThreshold: 5\n    promoteBatchSize: 3\n    rolloutTimeout: 2m\n    detectUnroutedProducersDuration: 30s\n    hotReloadTimeout: 45s\n    gatewayConfigPort: 9090\n"
	})
	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.Flags().Parse([]string{
		"--migration-yaml", f.manifestPath,
		"--lag-threshold", "99",
	}))

	g, cfg := freshConfig(t, f)
	applyPolicyOverrides(cmd, &g.Spec.DefaultPolicies)
	require.Empty(t, g.Spec.DefaultPolicies.Validate())

	opts, err := buildExecutorOpts(g, cfg, nil)
	require.NoError(t, err)

	rec := opts.MigrationConfig.LastRunPolicies
	require.NotNil(t, rec, "the effective policy must be recorded on the config")
	assert.Equal(t, 99, rec.LagThreshold, "the override, not the manifest default, is recorded")
	assert.Equal(t, 3, rec.PromoteBatchSize)
	assert.Equal(t, 2*time.Minute, rec.RolloutTimeout)
	assert.Equal(t, 30*time.Second, rec.DetectUnroutedProducersDuration)
	assert.Equal(t, time.Duration(0), rec.ConsumerOffsetSyncDrainDuration, "an unset knob is recorded as its zero")
	assert.Equal(t, 45*time.Second, rec.HotReloadTimeout)
	assert.Equal(t, 9090, rec.GatewayConfigPort)
}

// TestExecute_PolicyLogArgsCoverEveryDefaultPolicy — the audit log line that
// records "executing migration with effective policy" is hand-mirrored from
// DefaultPolicies and has already drifted (hotReloadTimeout and gatewayConfigPort
// were silently dropped). effectivePolicyLogArgs is the single place the log's
// copy lives; this pins every field to it so a future field cannot slip out of
// the audit trail unnoticed.
func TestExecute_PolicyLogArgsCoverEveryDefaultPolicy(t *testing.T) {
	p := manifest.DefaultPolicies{
		LagThreshold:                    11,
		PromoteBatchSize:                22,
		RolloutTimeout:                  33 * time.Second,
		DetectUnroutedProducersDuration: 44 * time.Second,
		ConsumerOffsetSyncDrainDuration: 55 * time.Second,
		HotReloadTimeout:                66 * time.Second,
		GatewayConfigPort:               9099,
	}
	kv := kvMap(t, effectivePolicyLogArgs("mig-1", p))

	// The two the audit line silently dropped — the whole point of this test.
	assert.Equal(t, 66*time.Second, kv["hot_reload_timeout"])
	assert.Equal(t, 9099, kv["gateway_config_port"])

	// And the rest, so no field drops out unnoticed later.
	assert.Equal(t, "mig-1", kv["migration_id"])
	assert.Equal(t, 11, kv["lag_threshold"])
	assert.Equal(t, 22, kv["promote_batch_size"])
	assert.Equal(t, 33*time.Second, kv["rollout_timeout"])
	assert.Equal(t, 44*time.Second, kv["detect_unrouted_producers_duration"])
	assert.Equal(t, 55*time.Second, kv["consumer_offset_sync_drain_duration"])

	// Every DefaultPolicies field must appear as a policy key (plus migration_id):
	// the count guards against a new field being added to the struct but not to
	// the log.
	assert.Len(t, kv, reflect.TypeOf(p).NumField()+1)
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

// TestExecute_InitDoesNotCarryLastRunPolicies — the record is absent until the
// first execute: a freshly-built config (buildFreshMigrationConfig's pure
// manifest projection) must not carry an empty block, which is why the field
// is a pointer with omitempty.
func TestExecute_InitDoesNotCarryLastRunPolicies(t *testing.T) {
	f := newFixture(t, nil)
	_, cfg := freshConfig(t, f)
	assert.Nil(t, cfg.LastRunPolicies,
		"a migration config that has not yet gone through buildExecutorOpts must have no LastRunPolicies record")
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

// --- source auth mapping ---

func TestExecute_MapsSourceAuthOntoExecutorOpts(t *testing.T) {
	for name, tc := range map[string]struct {
		block  string
		assert func(*testing.T, StaticMigrationExecutorOpts)
	}{
		"sasl_scram": {
			"sasl_scram:\n  username: u\n  password: p\n  mechanism: SHA256\n",
			func(t *testing.T, o StaticMigrationExecutorOpts) {
				sc := o.SourceConn.AuthMethod.SASLScram
				require.NotNil(t, sc)
				assert.Equal(t, "u", sc.Username)
				assert.Equal(t, "p", sc.Password)
				assert.Equal(t, "SHA256", sc.Mechanism)
			},
		},
		"iam": {
			"iam:\n  region: eu-west-2\n",
			func(t *testing.T, o StaticMigrationExecutorOpts) {
				require.NotNil(t, o.SourceConn.AuthMethod.IAM)
				assert.Equal(t, "eu-west-2", o.SourceConn.AuthMethod.IAM.Region)
			},
		},
		"sasl_plain": {
			"sasl_plain:\n  username: pu\n  password: pp\n  tls: true\n",
			func(t *testing.T, o StaticMigrationExecutorOpts) {
				sp := o.SourceConn.AuthMethod.SASLPlain
				require.NotNil(t, sp)
				assert.Equal(t, "pu", sp.Username)
				assert.True(t, sp.UseTLS, "tls: true must not be silently dropped to cleartext")
			},
		},
		"unauthenticated_plaintext": {
			"unauthenticated_plaintext: {}\n",
			func(t *testing.T, o StaticMigrationExecutorOpts) {
				assert.NotNil(t, o.SourceConn.AuthMethod.UnauthenticatedPlaintext)
				assert.Nil(t, o.SourceConn.AuthMethod.SASLScram)
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixtureCreds(t, credOverrides{source: tc.block}, nil)
			g, cfg := freshConfig(t, f)
			opts, err := buildExecutorOpts(g, cfg, nil)
			require.NoError(t, err)
			tc.assert(t, opts)
		})
	}
}

// TestExecute_InsecureSkipIsPerLegFile — each credentials file opts into
// skipping TLS verification independently, so all three legs can be relaxed by
// setting it on each file. There is no longer a single fan-out flag.
func TestExecute_InsecureSkipIsPerLegFile(t *testing.T) {
	f := newFixtureCreds(t, credOverrides{
		source:    "insecure_skip_tls_verify: true\n" + defaultSourceCred,
		destKafka: "insecure_skip_tls_verify: true\n" + defaultDestKafkaCred,
		link:      "api_key: CC_KEY\napi_secret: CC_SECRET\ninsecure_skip_verify: true\n",
	}, nil)
	g, cfg := freshConfig(t, f)
	opts, err := buildExecutorOpts(g, cfg, nil)
	require.NoError(t, err)
	assert.True(t, opts.SourceConn.InsecureSkipTLSVerify)
	assert.True(t, opts.DestKafkaInsecureSkipTLSVerify)
	assert.True(t, opts.RestCreds.InsecureSkipVerify)

	rest, err := g.RestCredentials()
	require.NoError(t, err)
	assert.True(t, rest.InsecureSkipVerify, "the REST leg reads its own linkCredentials file")
}

// TestExecute_DestinationKafkaUsesItsClusterCredentials — the destination Kafka
// leg is authenticated from spec.target.kafka.clusterCredentials.
func TestExecute_DestinationKafkaUsesItsClusterCredentials(t *testing.T) {
	f := newFixture(t, nil)
	g, cfg := freshConfig(t, f)
	opts, err := buildExecutorOpts(g, cfg, nil)
	require.NoError(t, err)
	assert.Equal(t, types.AuthTypeSASLPlain, opts.DestAuthType)
	require.NotNil(t, opts.DestAuthMethod.SASLPlain)
	assert.Equal(t, "CC_KEY", opts.DestAuthMethod.SASLPlain.Username)
	assert.Equal(t, "CC_SECRET", opts.DestAuthMethod.SASLPlain.Password)
}

// TestExecute_DestSASLPlainDefaultsToTLS is the ⚠️ backward-compat fix (A.3):
// the old destination client always dialled SASL_SSL over the public trust
// store. AdminOptionForAuthMethod maps sasl_plain with no ca_cert/tls to
// cleartext SASL_PLAINTEXT, so buildExecutorOpts must default UseTLS=true when
// neither is set — the single most important regression to prove, since every
// existing manifest never sets tls: nor ca_cert: on the destination.
func TestExecute_DestSASLPlainDefaultsToTLS(t *testing.T) {
	f := newFixtureCreds(t, credOverrides{
		destKafka: "sasl_plain:\n  username: CC_KEY\n  password: CC_SECRET\n",
	}, nil)
	g, cfg := freshConfig(t, f)
	opts, err := buildExecutorOpts(g, cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, opts.DestAuthMethod.SASLPlain)
	assert.True(t, opts.DestAuthMethod.SASLPlain.UseTLS, "no ca_cert/tls set must still default to SASL_SSL, not a silent downgrade to SASL_PLAINTEXT")
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
	g, cfg := freshConfig(t, f)
	opts, err := buildExecutorOpts(g, cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, opts.DestAuthMethod.SASLPlain)
	assert.Equal(t, ca, opts.DestAuthMethod.SASLPlain.CACert)
	assert.False(t, opts.DestAuthMethod.SASLPlain.UseTLS, "ca_cert already selects SASL_SSL; the compat default must not also flip UseTLS")
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
			return doc + "  defaultPolicies:\n    detectUnroutedProducersDuration: 5s\n"
		})
		_, err := runExecute(t, "--migration-yaml", f.manifestPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "detectUnroutedProducersDuration")
	})

	t.Run("negative drain duration", func(t *testing.T) {
		f := newFixture(t, func(doc string) string {
			return doc + "  defaultPolicies:\n    consumerOffsetSyncDrainDuration: -5s\n"
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
	g, cfg := freshConfig(t, f)
	opts, err := buildExecutorOpts(g, cfg, nil)
	require.NoError(t, err)

	assert.True(t, opts.SourceConn.InsecureSkipTLSVerify, "the source asked for it")
	assert.False(t, opts.DestKafkaInsecureSkipTLSVerify, "the destination Kafka leg did not")
	assert.False(t, opts.RestCreds.InsecureSkipVerify, "nor the destination REST leg")
}

// TestExecute_DestinationInsecureSkipDoesNotReachTheSource — the same in reverse.
// Each leg is its own file, so the destination Kafka leg relaxing verification
// reaches neither the source nor the (separate) REST leg.
func TestExecute_DestinationInsecureSkipDoesNotReachTheSource(t *testing.T) {
	f := newFixtureCreds(t, credOverrides{
		destKafka: "insecure_skip_tls_verify: true\n" + defaultDestKafkaCred,
	}, nil)
	g, cfg := freshConfig(t, f)
	opts, err := buildExecutorOpts(g, cfg, nil)
	require.NoError(t, err)

	assert.False(t, opts.SourceConn.InsecureSkipTLSVerify)
	assert.True(t, opts.DestKafkaInsecureSkipTLSVerify)
	assert.False(t, opts.RestCreds.InsecureSkipVerify, "the REST leg is its own file and did not ask for it")
}

// TestExecute_LinkCredentialsGovernTheRestLeg — the REST leg is driven entirely
// by spec.clusterLink.linkCredentials, so a link file that did not ask to skip
// verification keeps verifying no matter what the other legs set.
func TestExecute_LinkCredentialsGovernTheRestLeg(t *testing.T) {
	f := newFixtureCreds(t, credOverrides{
		source: "insecure_skip_tls_verify: true\n" + defaultSourceCred,
		link:   "api_key: K\napi_secret: S\n",
	}, nil)
	g, cfg := freshConfig(t, f)
	opts, err := buildExecutorOpts(g, cfg, nil)
	require.NoError(t, err)

	assert.True(t, opts.SourceConn.InsecureSkipTLSVerify)
	assert.False(t, opts.RestCreds.InsecureSkipVerify,
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
	g, cfg := freshConfig(t, f)
	opts, err := buildExecutorOpts(g, cfg, nil)
	require.NoError(t, err)

	require.NotNil(t, opts.DestAuthMethod.SASLPlain, "the Kafka leg uses spec.target.kafka.clusterCredentials")
	assert.Equal(t, "CC_KEY", opts.DestAuthMethod.SASLPlain.Username)
	assert.Equal(t, "CC_SECRET", opts.DestAuthMethod.SASLPlain.Password)
	assert.Equal(t, "REST_ONLY_KEY", opts.RestCreds.APIKey, "the REST leg uses linkCredentials")
	assert.Equal(t, "REST_ONLY_SECRET", opts.RestCreds.APISecret)
}

// TestExecute_RestCredentialsComeFromLinkCredentials — the REST leg is resolved
// from spec.clusterLink.linkCredentials, never derived from the Kafka leg.
func TestExecute_RestCredentialsComeFromLinkCredentials(t *testing.T) {
	f := newFixture(t, nil)
	g, cfg := freshConfig(t, f)
	opts, err := buildExecutorOpts(g, cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, opts.DestAuthMethod.SASLPlain)
	assert.Equal(t, "CC_KEY", opts.DestAuthMethod.SASLPlain.Username)
	assert.Equal(t, "CC_KEY", opts.RestCreds.APIKey)
}

// --- helper functions ---

func TestBuildFreshMigrationConfig_PopulatesManifestFields(t *testing.T) {
	f := newFixture(t, nil)
	g := loadGateway(t, f.manifestPath)
	kubeConfigPath, err := resolveKubeConfigPath(g)
	require.NoError(t, err)

	cfg := buildFreshMigrationConfig(g, "msk-prod-to-cc-batch-1", kubeConfigPath)

	assert.Equal(t, "msk-prod-to-cc-batch-1", cfg.MigrationId)
	assert.Equal(t, "b-1.msk.us-east-1.amazonaws.com:9096", cfg.SourceBootstrap)
	assert.Equal(t, "pkc-xxxxx.us-east-1.aws.confluent.cloud:9092", cfg.ClusterBootstrap)
	assert.Equal(t, "lkc-abc123", cfg.ClusterId)
	assert.Equal(t, "https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443", cfg.ClusterRestEndpoint)
	assert.Equal(t, "msk-to-cc", cfg.ClusterLinkName)
	assert.Equal(t, "confluent", cfg.K8sNamespace)
	assert.Equal(t, "gateway-initial", cfg.InitialCrName)
	assert.Equal(t, "migration-route", cfg.Route)
	assert.Equal(t, "confluent-cloud", cfg.TargetDomain)
	assert.Equal(t, []string{".*"}, cfg.TopicPatterns, "declared topicPatterns is read straight off the manifest")
	assert.False(t, cfg.PauseConsumerOffsetSync)
	assert.Empty(t, cfg.ConsumerOffsetSyncBaseline, "fixture manifest declares no baseline")
	assert.Empty(t, cfg.Topics, "topics require a live migplan.Reconcile — not set here")
	assert.Empty(t, cfg.FenceYAML)
	assert.Empty(t, cfg.Mode)
}

func TestResolveKubeConfigPath_DefaultsToHomeDir(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "") // not in a pod → developer default
	f := newFixture(t, nil)
	g := loadGateway(t, f.manifestPath)

	kubeConfigPath, err := resolveKubeConfigPath(g)
	require.NoError(t, err)

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".kube", "config"), kubeConfigPath)
}

func TestResolveKubeConfigPath_InClusterWhenInPod(t *testing.T) {
	// Running inside a pod (KUBERNETES_SERVICE_HOST set) with an unset manifest
	// kubeconfig must resolve to in-cluster config (empty path → client-go
	// in-cluster), matching the dynamic branch — not the developer's ~/.kube/config, which does
	// not exist in a pod.
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.96.0.1")
	f := newFixture(t, nil)
	g := loadGateway(t, f.manifestPath)

	kubeConfigPath, err := resolveKubeConfigPath(g)
	require.NoError(t, err)
	assert.Equal(t, "", kubeConfigPath, "an unset kubeconfig in a pod must resolve to in-cluster (empty path)")
}

// --- --dry-run ---

// TestExecute_DryRun_ValidatesPolicyOverrides is a regression test: --dry-run
// used to return before command-line policy overrides were applied and
// validated, so an invalid override (e.g. a negative lag threshold, rejected
// on a real run) silently passed under --dry-run instead. The override must
// now be rejected before reconcile is ever attempted.
func TestExecute_DryRun_ValidatesPolicyOverrides(t *testing.T) {
	f := newFixture(t, nil)

	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--dry-run", "--lag-threshold=-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lagThreshold: must not be negative")
	assert.NotContains(t, err.Error(), "failed to produce the reconcile plan",
		"an invalid override must be rejected before reconcile is attempted")
}

// TestExecute_DryRun_FailsAtReconcile proves --dry-run still reaches
// migplan.Reconcile (and only that) — it runs no FSM transition and, with no
// migration state file to touch in the first place, has nothing else to skip.
func TestExecute_DryRun_FailsAtReconcile(t *testing.T) {
	f := newFixture(t, nil)

	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--dry-run")
	require.Error(t, err, "reconcile fails deterministically against the fixture's unreachable kubeconfig")
	assert.Contains(t, err.Error(), "failed to produce the reconcile plan")
}
