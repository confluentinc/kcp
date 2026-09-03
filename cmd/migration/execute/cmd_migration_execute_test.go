package execute

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const executeManifest = `apiVersion: kcp.confluent.io/v1alpha1
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
    cr-name: gateway-initial
  topicGroup:
    - topicPatterns:
        - '.*'
      route: migration-route
      targetStreamingDomain: confluent-cloud
`

type fixture struct {
	manifestPath string
	stateFile    string
	dir          string
}

// newFixture writes a manifest and a state file holding a migration whose
// persisted config matches the manifest. There is no fenced or switchover CR
// file — both are derived from the live initial CR at cutover.
func newFixture(t *testing.T, mutate func(string) string) fixture {
	t.Helper()
	dir := t.TempDir()
	f := fixture{
		dir:          dir,
		manifestPath: filepath.Join(dir, "gateway-migration.yaml"),
		stateFile:    filepath.Join(dir, "migration-state.json"),
	}

	doc := executeManifest
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
		FenceRoutes:         []string{"migration-route"},
		SwitchoverTargets:   []gateway.RouteSwitchoverTarget{{RouteName: "migration-route", StreamingDomainName: "confluent-cloud", BootstrapServerId: "SASL_PLAIN"}},
		CurrentState:        migration.StateInitialized,
	}
	edit(&cfg)
	state := migration.NewMigrationState()
	state.UpsertMigration(cfg)
	require.NoError(t, state.WriteToFile(f.stateFile))
}

// explicitTopics swaps the canonical manifest's match-all topicPatterns for a
// literal topics list, so a drift test can compare an explicit selection.
func explicitTopics(names ...string) func(string) string {
	block := "    - topics:\n"
	for _, n := range names {
		block += "        - " + n + "\n"
	}
	return func(doc string) string {
		return strings.Replace(doc, "    - topicPatterns:\n        - '.*'\n", block, 1)
	}
}

func runExecute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewMigrationExecuteCmd()
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

// TestExecute_IsNamedExecute — the manifest work deliberately kept the existing
// verb, so runbooks, scripts and the published docs stay correct.
func TestExecute_IsNamedExecute(t *testing.T) {
	cmd := NewMigrationExecuteCmd()
	assert.Equal(t, "execute", cmd.Name())
	assert.False(t, cmd.Hidden, "execute is the advertised verb, not an alias")
	assert.Empty(t, cmd.Deprecated, "execute is not deprecated")
}

// TestExecute_VisibleFlagSurface — the manifest work moved topology and auth
// into the config file; what stays on the command line is the manifest path,
// the state file, the id override, and the per-policy overrides that vary a
// spec.defaultPolicies value for a single run. --run-report is registered but
// hidden (a diagnostics path whose only consumer is the performance rig), so it
// is asserted separately rather than padding the advertised surface.
func TestExecute_VisibleFlagSurface(t *testing.T) {
	cmd := NewMigrationExecuteCmd()
	var visible []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if !f.Hidden {
			visible = append(visible, f.Name)
		}
	})
	assert.ElementsMatch(t, []string{
		"migration-yaml", "migration-state-file", "migration-id",
		"lag-threshold", "promote-batch-size", "rollout-timeout",
		"detect-unrouted-producers-duration", "consumer-offset-sync-drain-duration",
		"hot-reload-timeout", "gateway-config-port",
	}, visible)

	runReport := cmd.Flags().Lookup("run-report")
	require.NotNil(t, runReport, "run-report must stay registered for the performance rig")
	assert.True(t, runReport.Hidden, "run-report is a diagnostics flag and must stay hidden")
}

// TestExecute_RetiredFlagsAreGone — the topology/auth flags moved into the
// manifest. The per-policy override flags (--lag-threshold, --rollout-timeout,
// etc.) are NOT here: they are the live surface, asserted by
// TestExecute_VisibleFlagSurface.
func TestExecute_RetiredFlagsAreGone(t *testing.T) {
	f := newFixture(t, nil)
	for _, flag := range []string{
		"--cluster-api-key", "--cluster-api-secret", "--aws-region",
		"--sasl-scram-mechanism", "--use-sasl-iam",
		"--insecure-skip-tls-verify", "--cluster-rest-ca-cert",
	} {
		t.Run(flag, func(t *testing.T) {
			_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile, flag, "1")
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

// TestExecute_RequiresMigrationStateFile — the state file holds the topology
// snapshot execute treats as the source of truth, so it is a required input
// (no CWD default), matching every other state-file-consuming command.
func TestExecute_RequiresMigrationStateFile(t *testing.T) {
	f := newFixture(t, nil)
	_, err := runExecute(t, "--migration-yaml", f.manifestPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migration-state-file")
}

// --- migration id resolution ---

// TestExecute_ResolvesMigrationIdFromMetadataName — --migration-id survives as an
// override only; the manifest names the migration.
func TestExecute_ResolvesMigrationIdFromMetadataName(t *testing.T) {
	f := newFixture(t, nil)
	g := loadGateway(t, f.manifestPath)
	assert.Equal(t, "msk-prod-to-cc-batch-1", resolveMigrationID(g, ""))
	assert.Equal(t, "migration-abc-uuid", resolveMigrationID(g, "migration-abc-uuid"),
		"an explicit --migration-id addresses a pre-existing uuid-keyed row")
}

func TestExecute_ErrorsWhenMigrationNotInStateFile(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return strings.Replace(doc, "  name: msk-prod-to-cc-batch-1", "  name: no-such-migration", 1)
	})
	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
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

// TestDrift_MatchAllTopicsMatchTheExpandedSnapshot covers the drift asymmetry:
// after the first execute a match-all topicGroup selection compares against a
// snapshot back-filled with every active mirror, so match-all (no literal
// topics) must equal "whatever was expanded", not an empty list.
func TestDrift_MatchAllTopicsMatchTheExpandedSnapshot(t *testing.T) {
	f := newFixture(t, nil)
	f.writeState(t, func(c *migration.MigrationConfig) {
		c.Topics = []string{"anything", "at", "all"}
	})
	assert.Empty(t, detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f)))
}

func TestDrift_DetectsChangedExplicitTopics(t *testing.T) {
	f := newFixture(t, explicitTopics("t1.order", "t3.new"))
	f.writeState(t, func(c *migration.MigrationConfig) {
		c.Topics = []string{"t1.order", "t2.inventory"}
	})
	drift := detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f))
	require.NotEmpty(t, drift)
	joined := strings.Join(drift, " ")
	assert.Contains(t, joined, "spec.topicGroup")
	assert.Contains(t, joined, "1 added")
	assert.Contains(t, joined, "1 removed")
}

// TestDrift_NeverNamesTopics — counts only. Dumping a topic list into a
// terminal error is the one thing this project's error copy must not do.
func TestDrift_NeverNamesTopics(t *testing.T) {
	f := newFixture(t, explicitTopics("secret-topic-name"))
	f.writeState(t, func(c *migration.MigrationConfig) { c.Topics = []string{"other-topic"} })
	drift := detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f))
	joined := strings.Join(drift, " ")
	assert.NotContains(t, joined, "secret-topic-name")
	assert.NotContains(t, joined, "other-topic")
}

// TestDrift_DetectsChangedSwitchoverTarget — editing a route's target streaming
// domain after init is drift. The bootstrap server id is NOT part of the diff:
// it is derived from the live CR, not authored in the manifest, so there is
// nothing manifest-side to compare it against.
func TestDrift_DetectsChangedSwitchoverTarget(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return strings.Replace(doc, "targetStreamingDomain: confluent-cloud", "targetStreamingDomain: confluent-cloud-2", 1)
	})
	drift := detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f))
	require.NotEmpty(t, drift)
	assert.Contains(t, strings.Join(drift, " "), "switchover targets")
}

// TestDrift_DetectsChangedRoutes — a fence-route set that no longer matches the
// snapshot is drift, reported as a bare flag: counts/flag only, never route
// names. With one topicGroup entry the change is a snapshot that fenced an extra
// route the manifest no longer names.
func TestDrift_DetectsChangedRoutes(t *testing.T) {
	f := newFixture(t, nil)
	f.writeState(t, func(c *migration.MigrationConfig) {
		c.FenceRoutes = []string{"migration-route", "extra-route"}
	})
	drift := detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f))
	require.NotEmpty(t, drift)
	joined := strings.Join(drift, " ")
	assert.Contains(t, joined, "routes")
	assert.NotContains(t, joined, "extra-route", "drift output must never name routes")
}

// TestDrift_KubeconfigPathIsNotCompared — for the same reason: execute may
// legitimately run from a different machine or pod.
func TestDrift_KubeconfigPathIsNotCompared(t *testing.T) {
	f := newFixture(t, nil)
	f.writeState(t, func(c *migration.MigrationConfig) { c.KubeConfigPath = "/a/totally/different/path" })
	assert.Empty(t, detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f)))
}

// TestDrift_PolicyIsNeverCompared — policy is read fresh on every execute, which
// is what lets a caller vary it between init and execute.
func TestDrift_PolicyIsNeverCompared(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return doc + "  defaultPolicies:\n    promoteBatchSize: 7\n    rolloutTimeout: 3m\n"
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

// TestMigrationConfig_EveryFieldClassifiedForDrift is a classification guard,
// not a behavioral test: it proves every field on MigrationConfig has been
// deliberately triaged as either checked by detectDrift (a topology field the
// manifest can drift out from under) or exempt (identity/FSM bookkeeping,
// runtime data populated by init, policy re-read fresh every run, or
// host-specific). A field in neither set fails loudly, turning "someone added
// a field and forgot to teach detectDrift about it" from a silent gap into a
// build-breaking one.
//
// This proves triage, not implementation — it does not confirm a driftChecked
// field actually has a comparison in detectDrift. Pair any addition to
// driftChecked with a new case in TestDrift_DetectsChangedTopology (scalars)
// or a dedicated test (CR bytes, Topics); this test alone cannot catch a field
// that's classified as checked but never actually compared.
func TestMigrationConfig_EveryFieldClassifiedForDrift(t *testing.T) {
	// Must have a comparison in detectDrift.
	driftChecked := map[string]bool{
		"SourceBootstrap":         true,
		"ClusterBootstrap":        true,
		"ClusterId":               true,
		"ClusterRestEndpoint":     true,
		"ClusterLinkName":         true,
		"Topics":                  true,
		"PauseConsumerOffsetSync": true,
		"K8sNamespace":            true,
		"InitialCrName":           true,
		"FenceRoutes":             true,
		"SwitchoverTargets":       true,
	}
	// Deliberately not compared by detectDrift — each entry says why.
	driftExempt := map[string]bool{
		// identity / FSM bookkeeping, not part of the declared spec
		"MigrationId":  true,
		"CurrentState": true,
		// execute is resume-safe and may legitimately run from a different
		// machine or pod (TestDrift_KubeconfigPathIsNotCompared)
		"KubeConfigPath": true,
		// policy is re-read fresh from the manifest on every run; the
		// snapshot's copy is never authoritative (TestDrift_PolicyIsNeverCompared)
		"DetectUnroutedProducersDuration": true,
		"ConsumerOffsetSyncDrainDuration": true,
		// runtime data populated by init from the live cluster link, not part
		// of the operator's declared spec
		"ClusterLinkTopics":  true,
		"ClusterLinkConfigs": true,
		// execute-time bookkeeping for whether kcp itself has already flipped
		// offset sync, not something the operator's YAML declares
		"PauseConsumerOffsetSyncFlipped": true,
		// only the fenced/switchover CRs are drift-checked; the initial CR is
		// applied once at init and never revisited
		"InitialCrYAML": true,
		// observational record of the effective policy the last execute ran with —
		// written for humans/support, never read back by kcp, so it can no more
		// drift than policy itself (TestExecute_RecordsLastRunPolicies)
		"LastRunPolicies": true,
		// resolved live against the cluster's Gateway CRD/CR at init, and
		// re-resolved authoritatively at execute — reflects the cluster's
		// capability, not something the operator's manifest declares
		"GatewayVerificationMode": true,
		"GatewayHotReloadEnabled": true,
		"GatewayConfigPort":       true,
	}

	typ := reflect.TypeOf(migration.MigrationConfig{})
	require.Equal(t, typ.NumField(), len(driftChecked)+len(driftExempt),
		"MigrationConfig's field count doesn't match the classified total — a field was added without being classified")

	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		require.True(t, driftChecked[name] != driftExempt[name], // exactly one
			"MigrationConfig.%s is unclassified for drift detection — add it to driftChecked (with a detectDrift comparison and a behavioral test) or driftExempt (with a reason)", name)
	}
}

// --- drift response (§13's two rows) ---

func TestExecute_DriftBeforeThePointOfNoReturnSaysReRunInit(t *testing.T) {
	for _, state := range []string{migration.StateUninitialized, migration.StateInitialized, migration.StateLagsOk} {
		t.Run(state, func(t *testing.T) {
			f := newFixture(t, nil)
			f.writeState(t, func(c *migration.MigrationConfig) {
				c.CurrentState = state
				c.ClusterLinkName = "changed-link"
			})
			_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "Re-run init")
			assert.Contains(t, err.Error(), "spec.clusterLink")
		})
	}
}

// TestExecute_DriftMidFlightProceedsWithoutBlocking — past the point of no
// return, re-running init would strand the live cutover, so there is no longer a
// safe alternative to proceeding with the edited spec. checkSpecDrift warns and
// returns nil rather than blocking; the drift-consent flag it used to require is
// gone.
func TestExecute_DriftMidFlightProceedsWithoutBlocking(t *testing.T) {
	f := newFixture(t, nil)
	f.writeState(t, func(c *migration.MigrationConfig) {
		c.CurrentState = migration.StateFenced
		c.ClusterLinkName = "changed-link"
	})
	g := loadGateway(t, f.manifestPath)
	cfg := persistedConfig(t, f)
	require.NotEmpty(t, detectDrift(g, cfg), "the fixture must actually have drift")
	assert.NoError(t, checkSpecDrift(g, cfg),
		"drift past the point of no return must not block the cutover")
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

// --- per-policy override flags ---

// TestExecute_PolicyOverrideFlagsReplaceManifestDefaults — only a flag the
// operator set explicitly overrides, and an explicit 0 (meaningful for every
// one of these) counts as set. An unset flag leaves the manifest default alone.
func TestExecute_PolicyOverrideFlagsReplaceManifestDefaults(t *testing.T) {
	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.Flags().Parse([]string{
		"--migration-yaml", "x", "--migration-state-file", "y",
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
		"--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile,
		"--lag-threshold", "99",
		"--detect-unrouted-producers-duration", "60s",
	}))

	g := loadGateway(t, f.manifestPath)
	applyPolicyOverrides(cmd, &g.Spec.DefaultPolicies)
	require.Empty(t, g.Spec.DefaultPolicies.Validate())

	opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile)
	require.NoError(t, err)
	assert.EqualValues(t, 99, opts.LagThreshold)
	assert.EqualValues(t, 60, opts.MigrationConfig.DetectUnroutedProducersDuration.Seconds())
}

// TestExecute_RecordsLastRunPolicies — buildExecutorOpts stamps the effective
// policy (manifest defaults with this run's overrides applied) onto the config
// that saveState persists, as an observational LastRunPolicies record. It is
// never read back — hence drift-exempt — so this proves it is at least written,
// and that it captures the OVERRIDE rather than the manifest default.
func TestExecute_RecordsLastRunPolicies(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return doc + "  defaultPolicies:\n    lagThreshold: 5\n    promoteBatchSize: 3\n    rolloutTimeout: 2m\n    detectUnroutedProducersDuration: 30s\n    hotReloadTimeout: 45s\n    gatewayConfigPort: 9090\n"
	})
	cmd := NewMigrationExecuteCmd()
	require.NoError(t, cmd.Flags().Parse([]string{
		"--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile,
		"--lag-threshold", "99",
	}))

	g := loadGateway(t, f.manifestPath)
	applyPolicyOverrides(cmd, &g.Spec.DefaultPolicies)
	require.Empty(t, g.Spec.DefaultPolicies.Validate())

	opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile)
	require.NoError(t, err)

	rec := opts.MigrationConfig.LastRunPolicies
	require.NotNil(t, rec, "the effective policy must be recorded on the persisted config")
	assert.Equal(t, 99, rec.LagThreshold, "the override, not the manifest default, is recorded")
	assert.Equal(t, 3, rec.PromoteBatchSize)
	assert.Equal(t, 2*time.Minute, rec.RolloutTimeout)
	assert.Equal(t, 30*time.Second, rec.DetectUnroutedProducersDuration)
	assert.Equal(t, time.Duration(0), rec.ConsumerOffsetSyncDrainDuration, "an unset knob is recorded as its zero")
	assert.Equal(t, 45*time.Second, rec.HotReloadTimeout)
	assert.Equal(t, 9090, rec.GatewayConfigPort)
}

// TestExecute_RefusesStateFilePredatingSwitchover — a migration-state.json
// written before redundant-auth switchover existed has fence routes but no
// switchover targets. Left alone it fails deep in the switch step ("no
// switchover targets given") after traffic is already fenced; execute must
// refuse it up front, before any cluster contact, and point at the fix.
func TestExecute_RefusesStateFilePredatingSwitchover(t *testing.T) {
	f := newFixture(t, nil)
	f.writeState(t, func(c *migration.MigrationConfig) {
		c.SwitchoverTargets = nil // the shape only a pre-feature state file has
	})

	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redundant-auth switchover",
		"the message must name the feature the state file predates")
	assert.Contains(t, strings.ToLower(err.Error()), "init",
		"the message must point the operator at re-running init")
	assert.NotContains(t, err.Error(), "no switchover targets given",
		"it must fail here, not late at the switch step")
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
	kv := kvMap(t, effectivePolicyLogArgs("mig-1", "initialized", p))

	// The two the audit line silently dropped — the whole point of this test.
	assert.Equal(t, 66*time.Second, kv["hot_reload_timeout"])
	assert.Equal(t, 9099, kv["gateway_config_port"])

	// And the rest, so no field drops out unnoticed later.
	assert.Equal(t, "mig-1", kv["migration_id"])
	assert.Equal(t, "initialized", kv["state"])
	assert.Equal(t, 11, kv["lag_threshold"])
	assert.Equal(t, 22, kv["promote_batch_size"])
	assert.Equal(t, 33*time.Second, kv["rollout_timeout"])
	assert.Equal(t, 44*time.Second, kv["detect_unrouted_producers_duration"])
	assert.Equal(t, 55*time.Second, kv["consumer_offset_sync_drain_duration"])

	// Every DefaultPolicies field must appear as a policy key (plus migration_id
	// and state): the count guards against a new field being added to the struct
	// but not to the log.
	assert.Len(t, kv, reflect.TypeOf(p).NumField()+2)
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
// first execute: a freshly-initialised migration (the fixture's persisted config)
// must not carry an empty block, which is why the field is a pointer with
// omitempty.
func TestExecute_InitDoesNotCarryLastRunPolicies(t *testing.T) {
	f := newFixture(t, nil)
	assert.Nil(t, persistedConfig(t, f).LastRunPolicies,
		"a migration that has only been init'd must have no LastRunPolicies record")
}

// TestExecute_InvalidPolicyOverrideIsRejected — an override can carry a value the
// manifest never did, so the effective policy is re-validated. A sub-10s detect
// duration is rejected before any network work.
func TestExecute_InvalidPolicyOverrideIsRejected(t *testing.T) {
	f := newFixture(t, nil)
	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile,
		"--detect-unrouted-producers-duration", "5s")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "detectUnroutedProducersDuration")
}

// --- source auth mapping ---

func TestExecute_MapsSourceAuthOntoExecutorOpts(t *testing.T) {
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

// TestExecute_InsecureSkipReachesAllThreeLegs preserves today's single-flag
// fan-out: --insecure-skip-tls-verify reached the source, the destination Kafka
// leg and the destination REST leg from one place.
func TestExecute_InsecureSkipReachesAllThreeLegs(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		doc = strings.Replace(doc, "    credentials:\n      sasl_scram:",
			"    credentials:\n      insecure_skip_tls_verify: true\n      sasl_scram:", 1)
		return strings.Replace(doc, "      credentials:\n        sasl_plain:",
			"      credentials:\n        insecure_skip_tls_verify: true\n        sasl_plain:", 1)
	})
	g := loadGateway(t, f.manifestPath)
	opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile)
	require.NoError(t, err)
	assert.True(t, opts.SourceInsecureSkipTLSVerify)
	assert.True(t, opts.DestKafkaInsecureSkipTLSVerify)
	assert.True(t, opts.RestCreds.InsecureSkipVerify)

	rest, err := g.RestCredentials()
	require.NoError(t, err)
	assert.True(t, rest.InsecureSkipVerify, "the derived REST leg inherits it")
}

// TestExecute_DestinationKeyAndSecretFeedBothLegs — one pair, two legs, as today.
func TestExecute_DestinationKeyAndSecretFeedBothLegs(t *testing.T) {
	f := newFixture(t, nil)
	g := loadGateway(t, f.manifestPath)
	opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile)
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
	f := newFixture(t, func(doc string) string {
		return strings.Replace(doc,
			"        sasl_plain:\n          username: CC_KEY\n          password: CC_SECRET\n          tls: true",
			"        sasl_plain:\n          username: CC_KEY\n          password: CC_SECRET", 1)
	})
	g := loadGateway(t, f.manifestPath)
	opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile)
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

	f := newFixture(t, func(doc string) string {
		return strings.Replace(doc,
			"        sasl_plain:\n          username: CC_KEY\n          password: CC_SECRET\n          tls: true",
			"        sasl_plain:\n          username: CC_KEY\n          password: CC_SECRET\n          ca_cert: "+ca, 1)
	})
	g := loadGateway(t, f.manifestPath)
	opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile)
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
		f := newFixture(t, func(doc string) string {
			return strings.Replace(doc, "        mechanism: SHA512", "        mechanism: SHA1", 1)
		})
		_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
		require.Error(t, err)
		assert.Contains(t, strings.ToLower(err.Error()), "mechanism")
	})

	t.Run("sub-10s detect duration", func(t *testing.T) {
		f := newFixture(t, func(doc string) string {
			return doc + "  defaultPolicies:\n    detectUnroutedProducersDuration: 5s\n"
		})
		_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "detectUnroutedProducersDuration")
	})

	t.Run("negative drain duration", func(t *testing.T) {
		f := newFixture(t, func(doc string) string {
			return doc + "  defaultPolicies:\n    consumerOffsetSyncDrainDuration: -5s\n"
		})
		_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "consumerOffsetSyncDrainDuration")
	})
}

// --- credential persistence boundary ---

func TestExecute_NeverPersistsCredentials(t *testing.T) {
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

// --- security review F2/F4: TLS trust must be per-leg ---

// TestExecute_SourceInsecureSkipDoesNotReachTheDestination. The manifest spells
// insecure_skip_tls_verify per credentials block. Collapsing the blocks into
// one boolean means an operator relaxing TLS for a self-signed on-prem SOURCE
// also stops verifying the destination connections — which transmit the
// destination API key as SASL/PLAIN and as HTTP Basic. Anyone able to MITM the
// path to the destination then harvests them.
func TestExecute_SourceInsecureSkipDoesNotReachTheDestination(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return strings.Replace(doc, "    credentials:\n      sasl_scram:",
			"    credentials:\n      insecure_skip_tls_verify: true\n      sasl_scram:", 1)
	})
	opts, err := buildExecutorOpts(loadGateway(t, f.manifestPath), persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile)
	require.NoError(t, err)

	assert.True(t, opts.SourceInsecureSkipTLSVerify, "the source asked for it")
	assert.False(t, opts.DestKafkaInsecureSkipTLSVerify, "the destination Kafka leg did not")
	assert.False(t, opts.RestCreds.InsecureSkipVerify, "nor the destination REST leg")
}

// TestExecute_DestinationInsecureSkipDoesNotReachTheSource — the same in reverse.
func TestExecute_DestinationInsecureSkipDoesNotReachTheSource(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return strings.Replace(doc, "      credentials:\n        sasl_plain:",
			"      credentials:\n        insecure_skip_tls_verify: true\n        sasl_plain:", 1)
	})
	opts, err := buildExecutorOpts(loadGateway(t, f.manifestPath), persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile)
	require.NoError(t, err)

	assert.False(t, opts.SourceInsecureSkipTLSVerify)
	assert.True(t, opts.DestKafkaInsecureSkipTLSVerify)
	assert.True(t, opts.RestCreds.InsecureSkipVerify, "a DERIVED REST leg inherits from the Kafka block")
}

// TestExecute_ExplicitRestCredentialsGovernTheRestLeg — with restCredentials
// spelled out, its own insecure_skip_verify governs, and nothing else leaks in.
// Otherwise a declared private-CA ca_cert would be loaded and then rendered
// meaningless by an InsecureSkipVerify inherited from another leg.
func TestExecute_ExplicitRestCredentialsGovernTheRestLeg(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		doc = strings.Replace(doc, "    credentials:\n      sasl_scram:",
			"    credentials:\n      insecure_skip_tls_verify: true\n      sasl_scram:", 1)
		return strings.Replace(doc, "  clusterLink:", `      restCredentials:
        api_key: K
        api_secret: S
  clusterLink:`, 1)
	})
	opts, err := buildExecutorOpts(loadGateway(t, f.manifestPath), persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile)
	require.NoError(t, err)

	assert.True(t, opts.SourceInsecureSkipTLSVerify)
	assert.False(t, opts.RestCreds.InsecureSkipVerify,
		"an explicit REST block that did not ask for it must keep verifying")
}

// --- security review F5: the Kafka leg authenticates with the KAFKA block ---

// TestExecute_DestinationKafkaUsesTheKafkaCredentialNotTheRestOne. The
// destination bootstrap is dialled with SASL/PLAIN. Feeding it from
// restCredentials means a deliberately broader REST key reaches the broker
// instead of the narrower Kafka-scoped one — least privilege inverted.
func TestExecute_DestinationKafkaUsesTheKafkaCredentialNotTheRestOne(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return strings.Replace(doc, "  clusterLink:", `      restCredentials:
        api_key: REST_ONLY_KEY
        api_secret: REST_ONLY_SECRET
  clusterLink:`, 1)
	})
	opts, err := buildExecutorOpts(loadGateway(t, f.manifestPath), persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile)
	require.NoError(t, err)

	require.NotNil(t, opts.DestAuthMethod.SASLPlain, "the Kafka leg uses spec.target.kafka.credentials")
	assert.Equal(t, "CC_KEY", opts.DestAuthMethod.SASLPlain.Username)
	assert.Equal(t, "CC_SECRET", opts.DestAuthMethod.SASLPlain.Password)
	assert.Equal(t, "REST_ONLY_KEY", opts.RestCreds.APIKey, "the REST leg uses restCredentials")
	assert.Equal(t, "REST_ONLY_SECRET", opts.RestCreds.APISecret)
}

// TestExecute_DerivedRestCredentialsStillMatchTheKafkaLeg — the common case is
// unchanged: one pair feeds both legs.
func TestExecute_DerivedRestCredentialsStillMatchTheKafkaLeg(t *testing.T) {
	f := newFixture(t, nil)
	opts, err := buildExecutorOpts(loadGateway(t, f.manifestPath), persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile)
	require.NoError(t, err)
	require.NotNil(t, opts.DestAuthMethod.SASLPlain)
	assert.Equal(t, "CC_KEY", opts.DestAuthMethod.SASLPlain.Username)
	assert.Equal(t, "CC_KEY", opts.RestCreds.APIKey)
}
