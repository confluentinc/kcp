package executetbm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration/tbm"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubReconcile is a no-op success engine for the FSM-scaffold tests: the
// manifest points at placeholder MSK/CC endpoints that are unreachable, so the
// real engine (which connects to the live gateway CR + source/target/link)
// cannot run here. The engine itself is tested in internal/services/migplan.
func stubReconcile(context.Context, *manifest.GatewayMigration, ...migplan.Option) (*migplan.Result, error) {
	return &migplan.Result{}, nil
}

// stubReconcileWithArtifacts returns a populated Result so tests can verify
// its fields land on the persisted TBMConfig via the initialize transition.
func stubReconcileWithArtifacts(context.Context, *manifest.GatewayMigration, ...migplan.Option) (*migplan.Result, error) {
	return &migplan.Result{
		Topics:         []string{"t1.order"},
		FenceYAML:      "rules:\n  fenced: true\n",
		SwitchoverYAML: "rules:\n  switched: true\n",
		GatewayYAML:    "apiVersion: v1\nkind: Gateway\n",
	}, nil
}

// stubReconcileRefused simulates an infeasible plan: not an I/O error, but a
// refusal the initialize transition must turn into a failed run.
func stubReconcileRefused(context.Context, *manifest.GatewayMigration, ...migplan.Option) (*migplan.Result, error) {
	return &migplan.Result{Refused: true, Reasons: []string{"topic t1.order has replication lag"}}, nil
}

// zeroLagOffsetProvider implements offset.Provider, reporting the same fixed
// offset for every topic requested — used for both source and destination in
// stubOffsetProviders, so every topic sees zero lag without dialing anything.
type zeroLagOffsetProvider struct{}

func (zeroLagOffsetProvider) GetMany(_ context.Context, topics []string) (map[string]map[int32]int64, error) {
	out := make(map[string]map[int32]int64, len(topics))
	for _, topic := range topics {
		out[topic] = map[int32]int64{0: 1000}
	}
	return out, nil
}

// stubOffsetProviders returns zero-lag providers without dialing anything,
// for tests whose manifests point at unreachable placeholder endpoints.
func stubOffsetProviders(*manifest.GatewayMigration) (offset.Provider, offset.Provider, func() error, error) {
	return zeroLagOffsetProvider{}, zeroLagOffsetProvider{}, func() error { return nil }, nil
}

// runExecuteTBMWithReconcile runs the command with the given reconcile func
// injected (and offset providers stubbed to zero lag), for tests that need to
// control what the "live" plan looks like without reaching Kubernetes,
// Kafka, or a real cluster.
func runExecuteTBMWithReconcile(t *testing.T, reconcile reconcileFunc, args ...string) (string, error) {
	t.Helper()
	cmd := newExecuteTBMCmd(reconcile, stubOffsetProviders)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// runExecuteTBMStubbed runs the command with the engine stubbed to a no-op
// success, for the end-to-end FSM tests that would otherwise dial unreachable
// endpoints.
func runExecuteTBMStubbed(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runExecuteTBMWithReconcile(t, stubReconcile, args...)
}

// gatewayManifestTemplate is a complete, valid GatewayMigration document with
// a templated metadata.name — the only field these tests vary.
const gatewayManifestTemplate = `apiVersion: kcp.confluent.io/v1alpha1
kind: GatewayMigration
metadata:
  name: %s
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
    clusterId: %s
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
    - topics:
        - t1.order
      route: migration-route
      targetStreamingDomain: confluent-cloud
`

func writeManifest(t *testing.T, dir, name, clusterId string) string {
	t.Helper()
	p := filepath.Join(dir, "gateway-migration.yaml")
	doc := fmt.Sprintf(gatewayManifestTemplate, name, clusterId)
	require.NoError(t, os.WriteFile(p, []byte(doc), 0600))
	return p
}

func runExecuteTBM(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewMigrationExecuteTBMCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// withFastTBMTransitions shrinks the tbm package's simulated transition delay
// for the duration of a test, restoring it on cleanup — otherwise a full
// six-step run takes many real seconds.
func withFastTBMTransitions(t *testing.T) {
	t.Helper()
	original := tbm.TransitionSimulatedDelay
	tbm.TransitionSimulatedDelay = time.Millisecond
	t.Cleanup(func() { tbm.TransitionSimulatedDelay = original })
}

// --- flag surface ---

func TestExecuteTBM_FlagSurfaceIncludesLagThresholdOverride(t *testing.T) {
	cmd := NewMigrationExecuteTBMCmd()
	var names []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	assert.ElementsMatch(t, []string{"migration-yaml", "tbm-state-file", "lag-threshold"}, names)
}

func TestExecuteTBM_RequiresMigrationYaml(t *testing.T) {
	dir := t.TempDir()
	_, err := runExecuteTBM(t, "--tbm-state-file", filepath.Join(dir, "tbm-state.json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migration-yaml")
}

func TestExecuteTBM_RequiresTbmStateFile(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, "tbm-batch-0", "lkc-abc123")
	_, err := runExecuteTBM(t, "--migration-yaml", manifestPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tbm-state-file")
}

// --- resolveTBMConfig: identity & drift, unit-level (no Execute involved) ---

func TestResolveTBMConfig_NoExistingEntry_CreatesFreshUninitialized(t *testing.T) {
	state := tbm.NewTBMState()

	cfg, err := resolveTBMConfig(state, "new-migration", "hash-1", "confluent", "gateway-initial")

	require.NoError(t, err)
	assert.Equal(t, "new-migration", cfg.MigrationId)
	assert.Equal(t, tbm.StateUninitialized, cfg.CurrentState)
	assert.Equal(t, "hash-1", cfg.ManifestHash)
	assert.Equal(t, "confluent", cfg.K8sNamespace)
	assert.Equal(t, "gateway-initial", cfg.InitialCrName)
}

func TestResolveTBMConfig_HashMatches_ResumesExisting(t *testing.T) {
	state := tbm.NewTBMState()
	state.UpsertMigration(tbm.TBMConfig{MigrationId: "mig-1", CurrentState: tbm.StateFenced, ManifestHash: "hash-1"})

	cfg, err := resolveTBMConfig(state, "mig-1", "hash-1", "confluent", "gateway-initial")

	require.NoError(t, err)
	assert.Equal(t, tbm.StateFenced, cfg.CurrentState)
}

func TestResolveTBMConfig_HashDiffers_RefusesUnconditionally(t *testing.T) {
	for _, currentState := range []string{tbm.StateUninitialized, tbm.StateFenced, tbm.StateSwitched} {
		t.Run(currentState, func(t *testing.T) {
			state := tbm.NewTBMState()
			state.UpsertMigration(tbm.TBMConfig{MigrationId: "mig-1", CurrentState: currentState, ManifestHash: "hash-1"})

			_, err := resolveTBMConfig(state, "mig-1", "hash-2", "confluent", "gateway-initial")

			require.Error(t, err, "drift must refuse regardless of CurrentState")
			assert.Contains(t, err.Error(), "changed since it was last run")
		})
	}
}

// --- end-to-end through the command ---

func TestExecuteTBM_SameManifest_ResumesAndThenShortCircuits(t *testing.T) {
	withFastTBMTransitions(t)
	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, "tbm-batch-2", "lkc-abc123")
	stateFile := filepath.Join(dir, "tbm-state.json")

	_, err := runExecuteTBMStubbed(t, "--migration-yaml", manifestPath, "--tbm-state-file", stateFile)
	require.NoError(t, err)

	state, err := tbm.NewTBMStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("tbm-batch-2")
	require.NoError(t, err)
	assert.Equal(t, tbm.StateSwitched, cfg.CurrentState)

	// Second run against the identical file: hash matches, already done ->
	// short-circuits via HasPendingWork without re-running the workflow.
	out, err := runExecuteTBMStubbed(t, "--migration-yaml", manifestPath, "--tbm-state-file", stateFile)
	require.NoError(t, err)
	assert.Contains(t, out, "already complete")
}

func TestExecuteTBM_TbmStateFileUnstatable_FailsInsteadOfTreatingAsFresh(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}
	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, "tbm-batch-unstatable", "lkc-abc123")

	// Strip execute (search) permission on the parent dir so os.Stat on the
	// state file path fails with permission-denied, not "does not exist".
	restrictedDir := filepath.Join(dir, "restricted")
	require.NoError(t, os.Mkdir(restrictedDir, 0700))
	stateFile := filepath.Join(restrictedDir, "tbm-state.json")
	require.NoError(t, os.Chmod(restrictedDir, 0000))
	t.Cleanup(func() { _ = os.Chmod(restrictedDir, 0700) })

	_, err := runExecuteTBM(t, "--migration-yaml", manifestPath, "--tbm-state-file", stateFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check tbm state file")
}

func TestExecuteTBM_ChangedManifest_RefusesEvenAfterDone(t *testing.T) {
	withFastTBMTransitions(t)
	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, "tbm-batch-3", "lkc-abc123")
	stateFile := filepath.Join(dir, "tbm-state.json")

	_, err := runExecuteTBMStubbed(t, "--migration-yaml", manifestPath, "--tbm-state-file", stateFile)
	require.NoError(t, err)

	// Edit the manifest (still schema-valid, still the same metadata.name).
	mutated := strings.ReplaceAll(fmt.Sprintf(gatewayManifestTemplate, "tbm-batch-3", "lkc-abc123"), "lkc-abc123", "lkc-changed")
	require.NoError(t, os.WriteFile(manifestPath, []byte(mutated), 0600))

	_, err = runExecuteTBMStubbed(t, "--migration-yaml", manifestPath, "--tbm-state-file", stateFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "changed since it was last run")

	state, err := tbm.NewTBMStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("tbm-batch-3")
	require.NoError(t, err)
	assert.Equal(t, tbm.StateSwitched, cfg.CurrentState, "the stale entry must be untouched by the refused run")
}

func TestExecuteTBM_ReconcilePlanArtifacts_PersistToStateFile(t *testing.T) {
	withFastTBMTransitions(t)
	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, "tbm-batch-artifacts", "lkc-abc123")
	stateFile := filepath.Join(dir, "tbm-state.json")

	_, err := runExecuteTBMWithReconcile(t, stubReconcileWithArtifacts, "--migration-yaml", manifestPath, "--tbm-state-file", stateFile)
	require.NoError(t, err)

	state, err := tbm.NewTBMStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("tbm-batch-artifacts")
	require.NoError(t, err)
	assert.Equal(t, tbm.StateSwitched, cfg.CurrentState)
	assert.Equal(t, []string{"t1.order"}, cfg.Topics)
	assert.Equal(t, "rules:\n  fenced: true\n", cfg.FenceYAML)
	assert.Equal(t, "rules:\n  switched: true\n", cfg.SwitchoverYAML)
	assert.Equal(t, "apiVersion: v1\nkind: Gateway\n", cfg.GatewayYAML)
}

func TestExecuteTBM_RefusedReconcilePlan_FailsRunAndLeavesStateUninitialized(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, "tbm-batch-refused", "lkc-abc123")
	stateFile := filepath.Join(dir, "tbm-state.json")

	_, err := runExecuteTBMWithReconcile(t, stubReconcileRefused, "--migration-yaml", manifestPath, "--tbm-state-file", stateFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "topic t1.order has replication lag")

	state, err := tbm.NewTBMStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("tbm-batch-refused")
	require.NoError(t, err)
	assert.Equal(t, tbm.StateUninitialized, cfg.CurrentState)
	assert.Empty(t, cfg.Topics)
}

// --- --lag-threshold override ---

func TestExecuteTBM_LagThresholdOverride_AppliesToEffectivePolicy(t *testing.T) {
	withFastTBMTransitions(t)
	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, "tbm-batch-lag-override", "lkc-abc123")
	stateFile := filepath.Join(dir, "tbm-state.json")

	var sawLagThreshold int
	captureReconcile := func(_ context.Context, g *manifest.GatewayMigration, _ ...migplan.Option) (*migplan.Result, error) {
		sawLagThreshold = g.Spec.DefaultPolicies.LagThreshold
		return &migplan.Result{}, nil
	}

	cmd := newExecuteTBMCmd(captureReconcile, stubOffsetProviders)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--migration-yaml", manifestPath, "--tbm-state-file", stateFile, "--lag-threshold", "500"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, 500, sawLagThreshold)
}

func TestExecuteTBM_LagThresholdOverride_NegativeValueRejected(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, "tbm-batch-lag-negative", "lkc-abc123")
	stateFile := filepath.Join(dir, "tbm-state.json")

	_, err := runExecuteTBMWithReconcile(t, stubReconcile, "--migration-yaml", manifestPath, "--tbm-state-file", stateFile, "--lag-threshold", "-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be negative")
}
