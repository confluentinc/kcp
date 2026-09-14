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
  topicGroup:
    - topicPatterns:
        - '.*'
      route: migration-route
      targetStreamingDomain: confluent-cloud
`

// Default credentials-file bodies for the canonical fixture. Values match the
// state written by writeState so the baseline resolves and drift-compares clean.
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
	stateFile    string
	dir          string
}

// newFixture writes a manifest (with the default credentials files) and a state
// file holding a migration whose persisted config matches the manifest. There is
// no fenced or switchover CR file — both are derived from the live initial CR at
// cutover.
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
		stateFile:    filepath.Join(dir, "migration-state.json"),
	}

	doc := executeManifest
	doc = strings.Replace(doc, "SOURCE_CREDS", testsupport.WriteCredFile(t, dir, "source-creds.yaml", creds.source, defaultSourceCred), 1)
	doc = strings.Replace(doc, "DEST_KAFKA_CREDS", testsupport.WriteCredFile(t, dir, "dest-kafka-creds.yaml", creds.destKafka, defaultDestKafkaCred), 1)
	doc = strings.Replace(doc, "LINK_CREDS", testsupport.WriteCredFile(t, dir, "link-creds.yaml", creds.link, defaultLinkCred), 1)
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
		Route:               "migration-route",
		TargetDomain:        "confluent-cloud",
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
// the state file (now optional, defaults to migration-state.json), the id override,
// and the per-policy overrides that vary a spec.defaultPolicies value for a single run.
// --run-report is registered but hidden (a diagnostics path whose only consumer is
// the performance rig), so it is asserted separately rather than padding the advertised surface.
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
		"hot-reload-timeout", "gateway-config-port", "dry-run",
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

// TestDrift_DetectsChangedTargetDomain — editing a route's target streaming
// domain after init is drift. The bootstrap server id is NOT part of the diff:
// it is derived from the live CR, not authored in the manifest, so there is
// nothing manifest-side to compare it against.
func TestDrift_DetectsChangedTargetDomain(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return strings.Replace(doc, "targetStreamingDomain: confluent-cloud", "targetStreamingDomain: confluent-cloud-2", 1)
	})
	drift := detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f))
	require.NotEmpty(t, drift)
	assert.Contains(t, strings.Join(drift, " "), "target domain")
}

// TestDrift_DetectsChangedRoute — a route that no longer matches the
// snapshot is drift.
func TestDrift_DetectsChangedRoute(t *testing.T) {
	f := newFixture(t, nil)
	f.writeState(t, func(c *migration.MigrationConfig) {
		c.Route = "a-different-route"
	})
	drift := detectDrift(loadGateway(t, f.manifestPath), persistedConfig(t, f))
	require.NotEmpty(t, drift)
	assert.Contains(t, strings.Join(drift, " "), "route")
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
	f := newFixtureCreds(t, credOverrides{
		source: "sasl_scram:\n  username: admin\n  password: rotated\n  mechanism: SHA512\n",
	}, nil)
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
		"Route":                   true,
		"TargetDomain":            true,
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
		// execute-time bookkeeping for whether kcp itself has already flipped
		// offset sync, not something the operator's YAML declares
		"PauseConsumerOffsetSyncFlipped": true,
		// runtime data populated by init from the live cluster link, not part
		// of the operator's declared spec
		"ClusterLinkConfigs": true,
		// derived artifacts migplan produced at init, not part of the
		// operator's declared spec — only the fence/switchover fragments'
		// SOURCE fields (Route, TargetDomain) are drift-checked; the rendered
		// artifacts and cleaned CR snapshot are not.
		"GatewayYAML":    true,
		"FenceYAML":      true,
		"SwitchoverYAML": true,
		// resolved live against the cluster's route-binding shape at init,
		// re-resolved authoritatively wherever migplan is re-run — reflects
		// the cluster's shape, not something the operator's manifest declares
		"Mode": true,
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

// --- drift response (unconditional refusal at every state) ---

func TestExecute_DriftRefusesUnconditionallyAtEveryState(t *testing.T) {
	for _, state := range []string{
		migration.StateUninitialized, migration.StateInitialized, migration.StateLagsOk,
		migration.StateFenced, migration.StateOffsetSyncPaused, migration.StateFenceVerified,
		migration.StatePromoted, migration.StateSwitched,
	} {
		t.Run(state, func(t *testing.T) {
			f := newFixture(t, nil)
			f.writeState(t, func(c *migration.MigrationConfig) {
				c.CurrentState = state
				c.ClusterLinkName = "changed-link"
			})
			g := loadGateway(t, f.manifestPath)
			cfg := persistedConfig(t, f)
			require.NotEmpty(t, detectDrift(g, cfg), "the fixture must actually have drift")

			err := checkSpecDrift(g, cfg)
			require.Error(t, err, "drift must refuse regardless of CurrentState — no override")
			assert.Contains(t, err.Error(), "changed since")
			assert.Contains(t, err.Error(), "new metadata.name")
		})
	}
}

func TestExecute_DriftRefusalNeverNamesTopics(t *testing.T) {
	f := newFixture(t, explicitTopics("secret-topic-name"))
	f.writeState(t, func(c *migration.MigrationConfig) { c.Topics = []string{"other-topic"} })
	g := loadGateway(t, f.manifestPath)

	err := checkSpecDrift(g, persistedConfig(t, f))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-topic-name")
	assert.NotContains(t, err.Error(), "other-topic")
}

// --- auto-create on first execute ---

// TestExecute_NoExistingStateFile_AutoCreatesEntryFromManifest proves a
// missing entry registers instead of erroring. The run then fails deep
// inside migplan.Reconcile's live gateway pull (no reachable cluster in this
// test process, same technique TestExecute_ResumeFromUninitialized_CallsReconcile
// already uses) — what this test cares about is that the entry landed on
// disk before that failure, not the failure itself.
func TestExecute_NoExistingStateFile_AutoCreatesEntryFromManifest(t *testing.T) {
	f := newFixture(t, nil)
	require.NoError(t, os.Remove(f.stateFile))

	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to produce the reconcile plan")

	state, err := migration.NewMigrationStateFromFile(f.stateFile)
	require.NoError(t, err, "the state file must exist even though the run then failed")
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)
	assert.Equal(t, migration.StateUninitialized, cfg.CurrentState)
	assert.Equal(t, "lkc-abc123", cfg.ClusterId)
	assert.Equal(t, "msk-to-cc", cfg.ClusterLinkName)
}

// TestExecute_MigrationStateFileFlagIsOptional_DefaultsToMigrationStateJSON
// mirrors execute-tbm's TestExecuteTBM_TbmStateFileOptional_DefaultsToMetadataNameAndResumes.
func TestExecute_MigrationStateFileFlagIsOptional_DefaultsToMigrationStateJSON(t *testing.T) {
	f := newFixture(t, nil)
	require.NoError(t, os.Remove(f.stateFile))
	dir := filepath.Dir(f.manifestPath)
	t.Chdir(dir)

	_, err := runExecute(t, "--migration-yaml", f.manifestPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to produce the reconcile plan")

	_, statErr := os.Stat(filepath.Join(dir, "migration-state.json"))
	assert.NoError(t, statErr, "omitting --migration-state-file must default to migration-state.json in the CWD")
}

// TestExecute_ExistingEntryIsUnaffectedByAutoCreate is the backward-
// compatibility guarantee: a pre-existing entry (as newFixture's own
// f.writeState already sets up at StateInitialized) resolves via
// GetMigrationById, never buildFreshMigrationConfig — proven here by an
// existing entry whose fields could not possibly have come from the
// manifest (a KubeConfigPath the manifest has no way to produce).
func TestExecute_ExistingEntryIsUnaffectedByAutoCreate(t *testing.T) {
	f := newFixture(t, nil)

	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
	require.Error(t, err) // fails later, past config resolution (no live cluster)

	cfg := persistedConfig(t, f)
	assert.Equal(t, "/some/kube/config", cfg.KubeConfigPath, "the pre-existing entry's own KubeConfigPath must survive untouched")
	assert.Equal(t, migration.StateInitialized, cfg.CurrentState, "an already-registered migration must not be reset to uninitialized")
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
	opts, err := buildExecutorOpts(g, cfg, *migration.NewMigrationState(), f.stateFile, nil)
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

	opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile, nil)
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

	opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile, nil)
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
			"sasl_scram:\n  username: u\n  password: p\n  mechanism: SHA256\n",
			func(t *testing.T, o MigrationExecutorOpts) {
				assert.Equal(t, "u", o.SaslScramUsername)
				assert.Equal(t, "p", o.SaslScramPassword)
				assert.Equal(t, "SHA256", o.SaslScramMechanism)
			},
		},
		"iam": {
			"iam:\n  region: eu-west-2\n",
			func(t *testing.T, o MigrationExecutorOpts) {
				assert.Equal(t, "eu-west-2", o.AWSRegion, "iam.region replaces --aws-region")
			},
		},
		"sasl_plain": {
			"sasl_plain:\n  username: pu\n  password: pp\n  tls: true\n",
			func(t *testing.T, o MigrationExecutorOpts) {
				assert.Equal(t, "pu", o.SaslPlainUsername)
				assert.True(t, o.SaslPlainUseTLS, "tls: true must not be silently dropped to cleartext")
			},
		},
		"unauthenticated_plaintext": {
			"unauthenticated_plaintext: {}\n",
			func(t *testing.T, o MigrationExecutorOpts) {
				assert.Empty(t, o.SaslScramUsername)
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixtureCreds(t, credOverrides{source: tc.block}, nil)
			g := loadGateway(t, f.manifestPath)
			opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile, nil)
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
	g := loadGateway(t, f.manifestPath)
	opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile, nil)
	require.NoError(t, err)
	assert.True(t, opts.SourceInsecureSkipTLSVerify)
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
	g := loadGateway(t, f.manifestPath)
	opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile, nil)
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
	g := loadGateway(t, f.manifestPath)
	opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile, nil)
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
	g := loadGateway(t, f.manifestPath)
	opts, err := buildExecutorOpts(g, persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile, nil)
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
	_, err := buildExecutorOpts(g, cfg, *migration.NewMigrationState(), f.stateFile, nil)
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
	f := newFixtureCreds(t, credOverrides{
		source: "insecure_skip_tls_verify: true\n" + defaultSourceCred,
	}, nil)
	opts, err := buildExecutorOpts(loadGateway(t, f.manifestPath), persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile, nil)
	require.NoError(t, err)

	assert.True(t, opts.SourceInsecureSkipTLSVerify, "the source asked for it")
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
	opts, err := buildExecutorOpts(loadGateway(t, f.manifestPath), persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile, nil)
	require.NoError(t, err)

	assert.False(t, opts.SourceInsecureSkipTLSVerify)
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
	opts, err := buildExecutorOpts(loadGateway(t, f.manifestPath), persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile, nil)
	require.NoError(t, err)

	assert.True(t, opts.SourceInsecureSkipTLSVerify)
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
	opts, err := buildExecutorOpts(loadGateway(t, f.manifestPath), persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile, nil)
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
	opts, err := buildExecutorOpts(loadGateway(t, f.manifestPath), persistedConfig(t, f), *migration.NewMigrationState(), f.stateFile, nil)
	require.NoError(t, err)
	require.NotNil(t, opts.DestAuthMethod.SASLPlain)
	assert.Equal(t, "CC_KEY", opts.DestAuthMethod.SASLPlain.Username)
	assert.Equal(t, "CC_KEY", opts.RestCreds.APIKey)
}

// --- StateUninitialized resume triggers a live migplan.Reconcile ---
//
// Neither test can reach MigrationExecutor.Run() successfully — there is no
// live Kubernetes cluster or Kafka broker in this test process, matching
// every other test in this file that drives the full runExecute surface (see
// e.g. TestExecute_ErrorsWhenMigrationNotInStateFile,
// TestExecute_DriftBeforeThePointOfNoReturnSaysReRunInit). What distinguishes
// the two cases is WHERE the run fails: migplan.Reconcile is called directly
// in runMigrationExecute (no injectable gateway source at that call site — see
// cmd_migration_execute.go), so a migration still at StateUninitialized fails
// fast inside Reconcile's own live gateway pull, surfacing runMigrationExecute's
// "failed to produce the reconcile plan" wrap. A migration already past
// StateUninitialized skips that call entirely and fails later, deeper in
// MigrationExecutor.Run() (e.g. connecting to the source Kafka cluster) — an
// error that does not carry the reconcile-plan wrap at all.

// TestExecute_ResumeFromUninitialized_CallsReconcile proves a migration still
// at StateUninitialized (a deferred --skip-validate init completing here)
// reaches migplan.Reconcile: the fixture's kubeconfig path does not exist, so
// Reconcile's live gateway pull fails immediately and deterministically,
// surfacing through runMigrationExecute's own wrap.
func TestExecute_ResumeFromUninitialized_CallsReconcile(t *testing.T) {
	f := newFixture(t, nil)
	f.writeState(t, func(c *migration.MigrationConfig) {
		c.CurrentState = migration.StateUninitialized
	})

	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to produce the reconcile plan",
		"resuming from StateUninitialized must call migplan.Reconcile")
}

// TestExecute_ResumeFromInitialized_NeverCallsReconcile confirms a migration
// already past StateUninitialized never triggers a live migplan.Reconcile call
// — the state-gated cost this task is specifically designed to avoid. The run
// still fails (no live cluster to execute against), but not via Reconcile's
// wrap: proof the call was skipped rather than merely tolerant of failure.
func TestExecute_ResumeFromInitialized_NeverCallsReconcile(t *testing.T) {
	f := newFixture(t, nil)
	f.writeState(t, func(c *migration.MigrationConfig) {
		c.CurrentState = migration.StateInitialized
	})

	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "reconcile plan",
		"a migration already past StateUninitialized must not call migplan.Reconcile")
}

// --- helper functions for auto-create and unconditional drift ---

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
	assert.Equal(t, migration.StateUninitialized, cfg.CurrentState)
	assert.Equal(t, "migration-route", cfg.Route)
	assert.Equal(t, "confluent-cloud", cfg.TargetDomain)
	assert.False(t, cfg.PauseConsumerOffsetSync)
	assert.Empty(t, cfg.Topics, "topics require a live migplan.Reconcile — not set here")
	assert.Empty(t, cfg.FenceYAML)
	assert.Empty(t, cfg.Mode)
}

func TestResolveKubeConfigPath_DefaultsToHomeDir(t *testing.T) {
	f := newFixture(t, nil)
	g := loadGateway(t, f.manifestPath)

	kubeConfigPath, err := resolveKubeConfigPath(g)
	require.NoError(t, err)

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".kube", "config"), kubeConfigPath)
}

// --- --dry-run ---

func TestExecute_DryRun_TouchesNoStateFile(t *testing.T) {
	f := newFixture(t, nil)
	require.NoError(t, os.Remove(f.stateFile))

	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--dry-run")
	require.Error(t, err, "reconcile fails deterministically against the fixture's unreachable kubeconfig")
	assert.Contains(t, err.Error(), "failed to produce the reconcile plan")

	_, statErr := os.Stat(f.stateFile)
	assert.True(t, os.IsNotExist(statErr), "dry-run must not create the migration state file")
}

func TestExecute_DryRun_DoesNotRequireMigrationStateFileFlag(t *testing.T) {
	f := newFixture(t, nil)
	require.NoError(t, os.Remove(f.stateFile))

	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--dry-run")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "migration-state-file", "dry-run must not require --migration-state-file")
}

func TestExecute_DryRun_ExistingEntryIsNotDriftChecked(t *testing.T) {
	// Drift-checking happens only on the non-dry-run path; dry-run never even
	// loads the state file, so a drifted existing entry must not surface as a
	// drift refusal under --dry-run.
	f := newFixture(t, nil)
	f.writeState(t, func(c *migration.MigrationConfig) { c.ClusterLinkName = "changed-link" })

	_, err := runExecute(t, "--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile, "--dry-run")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "changed since", "dry-run must not run the drift check")
}
