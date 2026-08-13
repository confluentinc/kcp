package execute

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const applyManifest = `apiVersion: kcp.confluent.io/v1alpha1
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
      fenced: FENCED_PATH
      switchover: SWITCHOVER_PATH
`

type fixture struct {
	manifestPath string
	stateFile    string
	fencedPath   string
	switchPath   string
	dir          string
}

// newFixture writes a manifest, its two CR files, and a state file holding a
// migration whose persisted config matches the manifest.
func newFixture(t *testing.T, mutate func(string) string) fixture {
	t.Helper()
	dir := t.TempDir()
	f := fixture{
		dir:          dir,
		manifestPath: filepath.Join(dir, "gateway-migration.yaml"),
		stateFile:    filepath.Join(dir, "migration-state.json"),
		fencedPath:   filepath.Join(dir, "fenced.yaml"),
		switchPath:   filepath.Join(dir, "switchover.yaml"),
	}
	require.NoError(t, os.WriteFile(f.fencedPath, []byte("kind: Gateway\nname: fenced\n"), 0600))
	require.NoError(t, os.WriteFile(f.switchPath, []byte("kind: Gateway\nname: switchover\n"), 0600))

	doc := strings.ReplaceAll(applyManifest, "FENCED_PATH", f.fencedPath)
	doc = strings.ReplaceAll(doc, "SWITCHOVER_PATH", f.switchPath)
	if mutate != nil {
		doc = mutate(doc)
	}
	require.NoError(t, os.WriteFile(f.manifestPath, []byte(doc), 0600))

	f.writeState(t, func(*migration.MigrationConfig) {})
	return f
}

// writeState persists a config built from the UNMUTATED manifest, then applies
// the caller's edit — so a test can make the file and the snapshot disagree.
func (f fixture) writeState(t *testing.T, edit func(*migration.MigrationConfig)) {
	t.Helper()
	cfg := migration.MigrationConfig{
		MigrationId:         "msk-prod-to-cc-batch-1",
		SourceBootstrap:     "b-1.msk.us-east-1.amazonaws.com:9096",
		ClusterBootstrap:    "pkc-xxxxx.us-east-1.aws.confluent.cloud:9092",
		K8sNamespace:        "confluent",
		InitialCrName:       "gateway-initial",
		KubeConfigPath:      "/some/kube/config",
		ClusterId:           "lkc-abc123",
		ClusterRestEndpoint: "https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443",
		ClusterLinkName:     "msk-to-cc",
		Topics:              []string{"t1.order", "t2.inventory"},
		FencedCrYAML:        []byte("kind: Gateway\nname: fenced\n"),
		SwitchoverCrYAML:    []byte("kind: Gateway\nname: switchover\n"),
		CurrentState:        migration.StateInitialized,
	}
	edit(&cfg)
	state := migration.NewMigrationState()
	state.UpsertMigration(cfg)
	require.NoError(t, state.WriteToFile(f.stateFile))
}

func runApply(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewMigrationApplyCmd()
	var out bytes.Buffer
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

func persistedConfig(t *testing.T, f fixture) *migration.MigrationConfig {
	t.Helper()
	state, err := migration.NewMigrationStateFromFile(f.stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)
	return cfg
}

// --- command surface ---

// TestApply_IsNamedApply — decision 7. Renaming now rather than when the PRFAQ
// ships it spares customers two runbook rewrites for one audience.
func TestApply_IsNamedApply(t *testing.T) {
	assert.Equal(t, "apply", NewMigrationApplyCmd().Name())
}

// TestExecute_IsAHiddenAlias — existing runbooks and scripts keep working.
func TestExecute_IsAHiddenAlias(t *testing.T) {
	cmd := NewMigrationExecuteCmd()
	assert.Equal(t, "execute", cmd.Name())
	assert.True(t, cmd.Hidden, "the old verb stays available but is not advertised")
}

func TestApply_FlagSurfaceIsFourFlags(t *testing.T) {
	cmd := NewMigrationApplyCmd()
	var names []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	assert.ElementsMatch(t,
		[]string{"file", "migration-state-file", "migration-id", "accept-spec-change"}, names)
}

func TestApply_RetiredFlagsAreGone(t *testing.T) {
	f := newFixture(t, nil)
	for _, flag := range []string{
		"--lag-threshold", "--cluster-api-key", "--cluster-api-secret", "--aws-region",
		"--sasl-scram-mechanism", "--promote-batch-size", "--rollout-timeout",
		"--detect-unrouted-producers-duration", "--consumer-offset-sync-drain-duration",
		"--use-sasl-iam", "--insecure-skip-tls-verify", "--cluster-rest-ca-cert",
	} {
		t.Run(flag, func(t *testing.T) {
			_, err := runApply(t, "-f", f.manifestPath, "--migration-state-file", f.stateFile, flag, "1")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unknown flag")
		})
	}
}

func TestApply_RequiresFile(t *testing.T) {
	_, err := runApply(t)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file")
}

// --- migration id resolution ---

// TestApply_ResolvesMigrationIdFromMetadataName — --migration-id survives as an
// override only; the manifest names the migration.
func TestApply_ResolvesMigrationIdFromMetadataName(t *testing.T) {
	f := newFixture(t, nil)
	g := loadGateway(t, f.manifestPath)
	assert.Equal(t, "msk-prod-to-cc-batch-1", resolveMigrationID(g, ""))
	assert.Equal(t, "migration-abc-uuid", resolveMigrationID(g, "migration-abc-uuid"),
		"an explicit --migration-id addresses a pre-existing uuid-keyed row")
}

func TestApply_ErrorsWhenMigrationNotInStateFile(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return strings.Replace(doc, "  name: msk-prod-to-cc-batch-1", "  name: no-such-migration", 1)
	})
	_, err := runApply(t, "-f", f.manifestPath, "--migration-state-file", f.stateFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-migration")
	assert.Contains(t, err.Error(), "kcp migration list")
}

// --- drift check (§13) ---

func TestDrift_NoDriftWhenManifestMatchesSnapshot(t *testing.T) {
	f := newFixture(t, nil)
	g := loadGateway(t, f.manifestPath)
	// The snapshot holds an expanded topic list and the manifest omits topics.
	assert.Empty(t, detectDrift(g, persistedConfig(t, f)))
}

func TestDrift_DetectsChangedTopology(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*migration.MigrationConfig)
		want string
	}{
		"source bootstrap":  {func(c *migration.MigrationConfig) { c.SourceBootstrap = "other:9092" }, "spec.source"},
		"cluster bootstrap": {func(c *migration.MigrationConfig) { c.ClusterBootstrap = "other:9092" }, "spec.target"},
		"cluster id":        {func(c *migration.MigrationConfig) { c.ClusterId = "lkc-other" }, "spec.target"},
		"rest endpoint":     {func(c *migration.MigrationConfig) { c.ClusterRestEndpoint = "https://other" }, "spec.target"},
		"link name":         {func(c *migration.MigrationConfig) { c.ClusterLinkName = "other-link" }, "spec.clusterLink"},
		"namespace":         {func(c *migration.MigrationConfig) { c.K8sNamespace = "other-ns" }, "spec.gateway"},
		"initial cr":        {func(c *migration.MigrationConfig) { c.InitialCrName = "other-cr" }, "spec.gateway"},
		"pause offset sync": {func(c *migration.MigrationConfig) { c.PauseConsumerOffsetSync = true }, "spec.clusterLink"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, nil)
			f.writeState(t, tc.edit)
			drift := detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f))
			require.NotEmpty(t, drift)
			assert.Contains(t, strings.Join(drift, " "), tc.want)
		})
	}
}

// TestDrift_OmittedTopicsMatchTheExpandedSnapshot is the §13 asymmetry: after
// the first apply an omitted spec.topics compares against a snapshot back-filled
// with every active mirror, so "omitted" must equal "whatever was expanded".
func TestDrift_OmittedTopicsMatchTheExpandedSnapshot(t *testing.T) {
	f := newFixture(t, nil)
	f.writeState(t, func(c *migration.MigrationConfig) {
		c.Topics = []string{"anything", "at", "all"}
	})
	assert.Empty(t, detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f)))
}

func TestDrift_DetectsChangedExplicitTopics(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return doc + "  topics: ['t1.order', 't3.new']\n"
	})
	f.writeState(t, func(c *migration.MigrationConfig) {
		c.Topics = []string{"t1.order", "t2.inventory"}
	})
	drift := detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f))
	require.NotEmpty(t, drift)
	joined := strings.Join(drift, " ")
	assert.Contains(t, joined, "spec.topics")
	assert.Contains(t, joined, "1 added")
	assert.Contains(t, joined, "1 removed")
}

// TestDrift_NeverNamesTopics — counts only. Dumping a topic list into a
// terminal error is the one thing this project's error copy must not do.
func TestDrift_NeverNamesTopics(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return doc + "  topics: ['secret-topic-name']\n"
	})
	f.writeState(t, func(c *migration.MigrationConfig) { c.Topics = []string{"other-topic"} })
	drift := detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f))
	joined := strings.Join(drift, " ")
	assert.NotContains(t, joined, "secret-topic-name")
	assert.NotContains(t, joined, "other-topic")
}

func TestDrift_DetectsChangedCRBytes(t *testing.T) {
	f := newFixture(t, nil)
	require.NoError(t, os.WriteFile(f.fencedPath, []byte("kind: Gateway\nname: EDITED\n"), 0600))
	drift := detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f))
	require.NotEmpty(t, drift)
	assert.Contains(t, strings.Join(drift, " "), "fenced CR")
}

// TestDrift_UnreadableCRIsNotFatal — apply is resume-safe and may run from a
// different cwd or pod after a crash, possibly with the gateway already fenced.
// A moved CR file must not strand a mid-flight cutover.
func TestDrift_UnreadableCRIsNotFatal(t *testing.T) {
	f := newFixture(t, nil)
	require.NoError(t, os.Remove(f.fencedPath))
	assert.Empty(t, detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f)),
		"an unreadable CR degrades to a warning, never a drift error")
}

// TestDrift_KubeconfigPathIsNotCompared — for the same reason: apply may
// legitimately run from a different machine or pod.
func TestDrift_KubeconfigPathIsNotCompared(t *testing.T) {
	f := newFixture(t, nil)
	f.writeState(t, func(c *migration.MigrationConfig) { c.KubeConfigPath = "/a/totally/different/path" })
	assert.Empty(t, detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f)))
}

// TestDrift_PolicyIsNeverCompared — policy is read fresh on every apply, which
// is what lets a caller vary it between init and apply.
func TestDrift_PolicyIsNeverCompared(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return doc + "  policy:\n    promoteBatchSize: 7\n    rolloutTimeout: 3m\n"
	})
	assert.Empty(t, detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f)))
}

// TestDrift_CredentialsAreNotComparable — credentials are never persisted, so
// defect 1's changed-between-runs half stays open by construction.
func TestDrift_CredentialsAreNotComparable(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return strings.Replace(doc, "        password: secret", "        password: rotated", 1)
	})
	assert.Empty(t, detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f)))
}

// --- drift response (§13's two rows) ---

func TestApply_DriftBeforeThePointOfNoReturnSaysReRunInit(t *testing.T) {
	for _, state := range []string{migration.StateUninitialized, migration.StateInitialized, migration.StateLagsOk} {
		t.Run(state, func(t *testing.T) {
			f := newFixture(t, nil)
			f.writeState(t, func(c *migration.MigrationConfig) {
				c.CurrentState = state
				c.ClusterLinkName = "changed-link"
			})
			_, err := runApply(t, "-f", f.manifestPath, "--migration-state-file", f.stateFile)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "Re-run init")
			assert.Contains(t, err.Error(), "spec.clusterLink")
		})
	}
}

func TestApply_DriftMidFlightSaysAcceptSpecChange(t *testing.T) {
	f := newFixture(t, nil)
	f.writeState(t, func(c *migration.MigrationConfig) {
		c.CurrentState = migration.StateFenced
		c.ClusterLinkName = "changed-link"
	})
	_, err := runApply(t, "-f", f.manifestPath, "--migration-state-file", f.stateFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--accept-spec-change")
	assert.Contains(t, err.Error(), "already fenced")
	assert.NotContains(t, err.Error(), "Re-run init",
		"re-running init is not safe once producers are fenced")
}

// TestApply_AcceptSpecChangeOverridesDrift — the override must actually get
// past the check (the run then fails later, on the network, which is expected
// in a unit test).
func TestApply_AcceptSpecChangeOverridesDrift(t *testing.T) {
	f := newFixture(t, nil)
	f.writeState(t, func(c *migration.MigrationConfig) {
		c.CurrentState = migration.StateFenced
		c.ClusterLinkName = "changed-link"
	})
	_, err := runApply(t, "-f", f.manifestPath, "--migration-state-file", f.stateFile, "--accept-spec-change")
	if err != nil {
		assert.NotContains(t, err.Error(), "config file has changed")
	}
}

// --- policy is read fresh ---

func TestApply_ReadsPolicyFromTheManifestOnEveryRun(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return doc + `  policy:
    lagThreshold: 42
    promoteBatchSize: 7
    rolloutTimeout: 3m
    detectUnroutedProducersDuration: 30s
    consumerOffsetSyncDrainDuration: 15s
`
	})
	g := loadGateway(t, f.manifestPath)
	cfg := persistedConfig(t, f)
	opts, err := buildExecutorOpts(g, cfg, *migration.NewMigrationState(), f.stateFile)
	require.NoError(t, err)

	assert.EqualValues(t, 42, opts.LagThreshold)
	assert.Equal(t, 7, opts.PromoteBatchSize)
	assert.EqualValues(t, 180, opts.RolloutTimeout.Seconds())
	assert.EqualValues(t, 30, opts.MigrationConfig.DetectUnroutedProducersDuration.Seconds())
	assert.EqualValues(t, 15, opts.MigrationConfig.ConsumerOffsetSyncDrainDuration.Seconds())
}

// --- source auth mapping ---

func TestApply_MapsSourceAuthOntoExecutorOpts(t *testing.T) {
	for name, tc := range map[string]struct {
		block  string
		assert func(*testing.T, MigrationExecutorOpts)
	}{
		"sasl_scram": {
			"      sasl_scram:\n        username: u\n        password: p\n        mechanism: SHA256",
			func(t *testing.T, o MigrationExecutorOpts) {
				assert.Equal(t, "u", o.SaslScramUsername)
				assert.Equal(t, "p", o.SaslScramPassword)
				assert.Equal(t, "SHA256", o.SaslScramMechanism)
			},
		},
		"iam": {
			"      iam:\n        region: eu-west-2",
			func(t *testing.T, o MigrationExecutorOpts) {
				assert.Equal(t, "eu-west-2", o.AWSRegion, "iam.region replaces --aws-region")
			},
		},
		"sasl_plain": {
			"      sasl_plain:\n        username: pu\n        password: pp\n        tls: true",
			func(t *testing.T, o MigrationExecutorOpts) {
				assert.Equal(t, "pu", o.SaslPlainUsername)
				assert.True(t, o.SaslPlainUseTLS, "tls: true must not be silently dropped to cleartext")
			},
		},
		"unauthenticated_plaintext": {
			"      unauthenticated_plaintext: {}",
			func(t *testing.T, o MigrationExecutorOpts) {
				assert.Empty(t, o.SaslScramUsername)
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, func(doc string) string {
				return strings.Replace(doc,
					"      sasl_scram:\n        username: admin\n        password: secret\n        mechanism: SHA512",
					tc.block, 1)
			})
			g := loadGateway(t, f.manifestPath)
			opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile)
			require.NoError(t, err)
			tc.assert(t, opts)
		})
	}
}

// TestApply_InsecureSkipReachesAllThreeLegs preserves today's single-flag
// fan-out: --insecure-skip-tls-verify reached the source, the destination Kafka
// leg and the destination REST leg from one place.
func TestApply_InsecureSkipReachesAllThreeLegs(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		doc = strings.Replace(doc, "    credentials:\n      sasl_scram:",
			"    credentials:\n      insecure_skip_tls_verify: true\n      sasl_scram:", 1)
		return strings.Replace(doc, "      credentials:\n        sasl_plain:",
			"      credentials:\n        insecure_skip_tls_verify: true\n        sasl_plain:", 1)
	})
	g := loadGateway(t, f.manifestPath)
	opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile)
	require.NoError(t, err)
	assert.True(t, opts.InsecureSkipTLSVerify)

	rest, err := g.RestCredentials()
	require.NoError(t, err)
	assert.True(t, rest.InsecureSkipVerify, "the derived REST leg inherits it")
}

// TestApply_DestinationKeyAndSecretFeedBothLegs — one pair, two legs, as today.
func TestApply_DestinationKeyAndSecretFeedBothLegs(t *testing.T) {
	f := newFixture(t, nil)
	g := loadGateway(t, f.manifestPath)
	opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile)
	require.NoError(t, err)
	assert.Equal(t, "CC_KEY", opts.ClusterApiKey)
	assert.Equal(t, "CC_SECRET", opts.ClusterApiSecret)
}

// --- ported preRunE errors (§6) ---

// TestApply_PortsExecutesBespokePreRunErrors: execute's three hand-written
// preRunE errors must have explicit homes in the manifest validator, or they
// are silently dropped when the flag lattice is deleted.
func TestApply_PortsExecutesBespokePreRunErrors(t *testing.T) {
	t.Run("invalid sasl_scram mechanism", func(t *testing.T) {
		f := newFixture(t, func(doc string) string {
			return strings.Replace(doc, "        mechanism: SHA512", "        mechanism: SHA1", 1)
		})
		_, err := runApply(t, "-f", f.manifestPath, "--migration-state-file", f.stateFile)
		require.Error(t, err)
		assert.Contains(t, strings.ToLower(err.Error()), "mechanism")
	})

	t.Run("sub-10s detect duration", func(t *testing.T) {
		f := newFixture(t, func(doc string) string {
			return doc + "  policy:\n    detectUnroutedProducersDuration: 5s\n"
		})
		_, err := runApply(t, "-f", f.manifestPath, "--migration-state-file", f.stateFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "detectUnroutedProducersDuration")
	})

	t.Run("negative drain duration", func(t *testing.T) {
		f := newFixture(t, func(doc string) string {
			return doc + "  policy:\n    consumerOffsetSyncDrainDuration: -5s\n"
		})
		_, err := runApply(t, "-f", f.manifestPath, "--migration-state-file", f.stateFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "consumerOffsetSyncDrainDuration")
	})
}

// --- credential persistence boundary ---

func TestApply_NeverPersistsCredentials(t *testing.T) {
	f := newFixture(t, nil)
	g := loadGateway(t, f.manifestPath)
	cfg := persistedConfig(t, f)
	_, err := buildExecutorOpts(g, cfg, *migration.NewMigrationState(), f.stateFile)
	require.NoError(t, err)

	state := migration.NewMigrationState()
	state.UpsertMigration(*cfg)
	out := filepath.Join(f.dir, "written.json")
	require.NoError(t, state.WriteToFile(out))

	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	for _, secret := range []string{"secret", "CC_SECRET", "CC_KEY"} {
		assert.NotContains(t, string(raw), secret)
	}
}
