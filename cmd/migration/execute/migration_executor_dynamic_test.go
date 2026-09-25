package execute

import (
	"bytes"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file exercises the dynamic-mode branch of `execute` the way
// migration_executor_static_test.go exercises the static one.
//
// The command layer calls migplan.Reconcile with no injectable seam, and a real
// Reconcile dials live Kafka and pulls the live Gateway CR (see
// internal/services/migplan/run.go), so it cannot succeed — nor resolve a
// Mode — inside this test process. These tests therefore call runDynamicBranch
// directly (runDynamicBranchWithConfig below), standing in for that live call
// with a hand-built *migplan.Result and MigrationConfig, and stub the
// downstream services through executorDependencies (stubDeps).

// dynamicRouteGatewayYAML is a minimal dynamic-route Gateway CR fixture,
// mirroring internal/services/migration/tbm's own test fixture of the same
// shape. A dynamic route carries a `rules:` block (routing/coordination), the
// shape migplan resolves to Mode:"dynamic"; a static route would instead carry
// a fence/streamingDomain block.
const dynamicRouteGatewayYAML = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: gateway-initial
  namespace: confluent
spec:
  streamingDomains:
    - name: source
      kafkaCluster:
        name: source-cluster
    - name: target
      kafkaCluster:
        name: target-cluster
  routes:
    - name: migration-route
      endpoint: kafka-gw.example.com:9092
      streamingDomains:
        - name: source
          bootstrapServerId: sasl-scram
        - name: target
          bootstrapServerId: sasl-plain
      rules:
        routing:
          coordination:
            group: source
          default: source
`

// dynamicFenceYAML / dynamicSwitchoverYAML are the mutually-consistent rules:
// fragments a live migplan.Reconcile would produce, so the fence/switch
// transitions can graft them onto dynamicRouteGatewayYAML's migration-route
// and succeed against the stub gateway.
const (
	dynamicFenceYAML      = "rules:\n  routing:\n    coordination:\n      group: source\n    default: source\n  fencing:\n    - topics: [\"t1.order\"]\n"
	dynamicSwitchoverYAML = "rules:\n  routing:\n    coordination:\n      group: source\n    default: source\n    conditions:\n      - topics: [\"t1.order\"]\n        streamingDomain: target\n"
)

// dynamicConfig builds the MigrationConfig a dynamic-mode run drives — the
// same pure manifest projection buildFreshMigrationConfig produces for f's
// manifest, plus Mode and the fence/switchover/gateway artifacts a live
// migplan.Reconcile would have produced for this fixture. There is no
// migration state file to read any of this from any more, so a test
// that needs to drive runDynamicBranch directly builds the config and its
// matching *migplan.Result (dynamicResult) by hand. edit runs last, so a test
// can vary a single field.
func dynamicConfig(t *testing.T, f fixture, edit func(*migration.MigrationConfig)) *migration.MigrationConfig {
	t.Helper()
	g := loadGateway(t, f.manifestPath)
	kubeConfigPath, err := resolveKubeConfigPath(g)
	require.NoError(t, err)
	cfg := buildFreshMigrationConfig(g, "msk-prod-to-cc-batch-1", kubeConfigPath)
	cfg.Mode = "dynamic"
	cfg.Topics = []string{"t1.order"}
	cfg.GatewayYAML = dynamicRouteGatewayYAML
	cfg.FenceYAML = dynamicFenceYAML
	cfg.SwitchoverYAML = dynamicSwitchoverYAML
	if edit != nil {
		edit(&cfg)
	}
	return &cfg
}

// dynamicResult builds the *migplan.Result matching config exactly — the
// stand-in for what a live migplan.Reconcile would have returned for it.
func dynamicResult(config *migration.MigrationConfig) *migplan.Result {
	return &migplan.Result{
		Route:          "migration-route",
		Topics:         config.Topics,
		FenceYAML:      config.FenceYAML,
		SwitchoverYAML: config.SwitchoverYAML,
		GatewayYAML:    config.GatewayYAML,
		Mode:           config.Mode,
	}
}

// runDynamicBranchWithConfig drives runDynamicBranch directly against config and its
// matching dynamicResult, standing in for the live migplan.Reconcile call
// runMigrationExecute now makes on every invocation ahead of the mode dispatch
// — a call that cannot succeed in this process (see this file's header
// comment). This still exercises the SAME tbm.TBMOrchestrator.Execute call
// runDynamicBranch makes in production, just reached via a reconcile-free front
// door. editGateway, when non-nil, mutates the loaded manifest before
// dispatch — the substitute for a CLI flag override (applyPolicyOverrides
// needs real cobra flag parsing, which this seam skips; setting the manifest
// field directly is equivalent for what these tests observe).
func runDynamicBranchWithConfig(
	t *testing.T, f fixture, config *migration.MigrationConfig,
	editGateway func(*manifest.GatewayMigration),
	deps executorDependencies,
) (string, error) {
	t.Helper()
	g := loadGateway(t, f.manifestPath)
	if editGateway != nil {
		editGateway(g)
	}

	res := dynamicResult(config)

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)

	err := runDynamicBranch(cmd, g, config, res, deps)
	return out.String(), err
}

// --- a dynamic run completes on stubbed services ---

// TestExecute_DynamicMode_RunsToCompletionOnStubbedServices proves a
// dynamic-mode run on stubbed services walks tbm.TBMOrchestrator's FSM all the
// way to switched and prints the "Migration completed" banner.
func TestExecute_DynamicMode_RunsToCompletionOnStubbedServices(t *testing.T) {
	f := newFixture(t, nil)
	config := dynamicConfig(t, f, nil)

	out, err := runDynamicBranchWithConfig(t, f, config, nil, stubDeps(nil, nil))
	require.NoError(t, err)
	assert.Contains(t, out, "Migration completed")
}

// --- --promote-batch-size reaches tbm.TBMActions.SetPromoteBatchSize ---

// TestExecute_DynamicMode_PromoteBatchSizeReachesDynamicActions confirms that
// --promote-batch-size reaches tbm.TBMActions.SetPromoteBatchSize for a
// dynamic-mode run. Two topics are staged
// at zero lag; the recorded per-call batch sizes discriminate a capped run
// (batch size 1 → each PromoteMirrorTopics call submits exactly one topic) from
// an uncapped one (both promoted in a single call).
func TestExecute_DynamicMode_PromoteBatchSizeReachesDynamicActions(t *testing.T) {
	topics := []string{"t1.order", "t2.inventory"}

	t.Run("flag caps the batch", func(t *testing.T) {
		f := newFixture(t, nil)
		config := dynamicConfig(t, f, func(c *migration.MigrationConfig) { c.Topics = topics })
		rec := &recordingClusterLinkService{}
		// The CLI's --promote-batch-size relies on real cobra flag parsing
		// (applyPolicyOverrides), which runDynamicBranchWithConfig's reconcile-free
		// seam skips (see its doc comment) — setting the manifest's
		// spec.defaultPolicies field directly is equivalent for what this test
		// observes: runDynamicBranch always reads the effective policy from there.
		editGateway := func(g *manifest.GatewayMigration) { g.Spec.DefaultPolicies.PromoteBatchSize = 1 }
		_, err := runDynamicBranchWithConfig(t, f, config, editGateway, stubDeps(nil, rec))
		require.NoError(t, err)
		assert.Equal(t, 1, rec.maxBatch(),
			"--promote-batch-size 1 must cap every promote batch at one topic")
	})

	t.Run("unlimited promotes all at once (control)", func(t *testing.T) {
		f := newFixture(t, nil)
		config := dynamicConfig(t, f, func(c *migration.MigrationConfig) { c.Topics = topics })
		rec := &recordingClusterLinkService{}
		_, err := runDynamicBranchWithConfig(t, f, config, nil, stubDeps(nil, rec))
		require.NoError(t, err)
		assert.Equal(t, len(topics), rec.maxBatch(),
			"with no batch cap both topics promote in a single call — proving the flag, not chance, produced the capped run above")
	})
}

// --- Test 3: a dynamic run records LastRunPolicies ---

// TestExecute_DynamicMode_RecordsLastRunPolicies confirms that a
// dynamic-mode run populates config.LastRunPolicies with its five supported
// fields (read from the effective policy — the manifest's spec.defaultPolicies,
// no flags here — the same source tbm.TBMActions reads, so this also pins
// policy plumbing parity with the static branch), while
// ConsumerOffsetSyncDrainDuration stays zero because the dynamic FSM has no
// offset-sync-pause stage to record a value for.
func TestExecute_DynamicMode_RecordsLastRunPolicies(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return doc + "  defaultPolicies:\n" +
			"    lagThreshold: 42\n" +
			"    promoteBatchSize: 7\n" +
			"    rolloutTimeout: 3m\n" +
			"    detectUnroutedProducersDuration: 30s\n" +
			"    hotReloadTimeout: 45s\n" +
			"    gatewayConfigPort: 9090\n"
	})
	config := dynamicConfig(t, f, nil)

	_, err := runDynamicBranchWithConfig(t, f, config, nil, stubDeps(nil, nil))
	require.NoError(t, err)

	rec := config.LastRunPolicies
	require.NotNil(t, rec, "a dynamic-mode run must record the effective policy")
	assert.Equal(t, 42, rec.LagThreshold)
	assert.Equal(t, 7, rec.PromoteBatchSize, "the manifest default reaches the record with no flag set — the same policy plumbing as the static branch")
	assert.Equal(t, 3*time.Minute, rec.RolloutTimeout)
	assert.Equal(t, 30*time.Second, rec.DetectUnroutedProducersDuration)
	assert.Equal(t, 45*time.Second, rec.HotReloadTimeout)
	assert.Equal(t, 9090, rec.GatewayConfigPort)
	assert.Equal(t, time.Duration(0), rec.ConsumerOffsetSyncDrainDuration,
		"the dynamic FSM has no offset-sync-pause stage, so this field stays zero")
}

// --- --gateway-config-port reaches the dynamic capability probe ---

// TestExecute_DynamicMode_GatewayConfigPortReachesDynamicCapabilityProbe
// confirms end-to-end through the command that --gateway-config-port reaches
// config.GatewayConfigPort before the dynamic capability probe
// (DetectCapability) runs.
func TestExecute_DynamicMode_GatewayConfigPortReachesDynamicCapabilityProbe(t *testing.T) {
	f := newFixture(t, nil)
	config := dynamicConfig(t, f, nil)

	rec := &recordingGatewayService{}
	// The CLI's --gateway-config-port relies on real cobra flag parsing
	// (applyPolicyOverrides), which runDynamicBranchWithConfig's reconcile-free
	// seam skips — setting the manifest field directly is equivalent for
	// what this test observes: runDynamicBranch reads GatewayConfigPort straight
	// from g.Spec.DefaultPolicies.
	editGateway := func(g *manifest.GatewayMigration) { g.Spec.DefaultPolicies.GatewayConfigPort = 9999 }
	_, err := runDynamicBranchWithConfig(t, f, config, editGateway, stubDeps(rec, nil))
	require.NoError(t, err)
	assert.Equal(t, 9999, rec.port(),
		"--gateway-config-port must reach config.GatewayConfigPort before the dynamic capability probe")
	assert.Equal(t, 9999, config.GatewayConfigPort)
}

// --- refusal decision (pauseConsumerOffsetSync on a dynamic route) ---

// TestPauseOffsetSyncRefusedForDynamic pins the exact refusal decision
// runMigrationExecute makes: a pauseConsumerOffsetSync manifest is refused for
// a dynamic route and permitted for a static one (which honors the field).
// That branch is unreachable through the command in-process (it needs a live
// migplan.Reconcile), so the decision is factored into
// pauseOffsetSyncRefusedForDynamic and asserted directly here.
func TestPauseOffsetSyncRefusedForDynamic(t *testing.T) {
	set := &manifest.GatewayMigration{}
	set.Spec.ClusterLink.PauseConsumerOffsetSync = true
	unset := &manifest.GatewayMigration{}

	assert.True(t, pauseOffsetSyncRefusedForDynamic("dynamic", set),
		"a dynamic route with pauseConsumerOffsetSync set must be refused")
	assert.False(t, pauseOffsetSyncRefusedForDynamic("dynamic", unset),
		"nothing to refuse when the field is unset")
	assert.False(t, pauseOffsetSyncRefusedForDynamic("static", set),
		"a static route honors pauseConsumerOffsetSync — no refusal")
}
