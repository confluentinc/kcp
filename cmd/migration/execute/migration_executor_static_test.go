package execute

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
)

// This file exercises the static-mode branch the way
// migration_executor_dynamic_test.go exercises the dynamic one: the real
// manifest → runStaticBranch path, with the four
// downstream services stubbed through executorDependencies (stubDeps) and the live
// migplan.Reconcile (which cannot run in-process) replaced by staticResult.

// staticRouteGatewayYAML and the artifacts below are the shapes migplan.Reconcile
// returns for a static route: a route-level fence block, the whole switched
// route, and the whole route as a rollback leaves it.
const staticRouteGatewayYAML = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: gateway-initial
  namespace: confluent
spec:
  routes:
    - name: migration-route
      endpoint: kafka-gw.example.com:9092
      streamingDomain:
        name: source
        bootstrapServerId: sasl-scram
`

const (
	staticFenceYAML         = "fence:\n  scope: ALL\n  errorCode: BROKER_NOT_AVAILABLE\n"
	staticSwitchoverYAML    = "route:\n  name: migration-route\n  endpoint: kafka-gw.example.com:9092\n  streamingDomain:\n    name: confluent-cloud\n    bootstrapServerId: sasl-plain\n"
	staticRollbackFenceYAML = "route:\n  name: migration-route\n  endpoint: kafka-gw.example.com:9092\n  streamingDomain:\n    name: source\n    bootstrapServerId: sasl-scram\n"
)

// staticResult is the stand-in for the *migplan.Result a live
// migplan.Reconcile would return for a static route over topics.
func staticResult(topics []string) *migplan.Result {
	return &migplan.Result{
		Route:          "migration-route",
		PromoteTopics:  topics,
		MigrateTopics:  topics,
		FenceYAML:      staticFenceYAML,
		SwitchoverYAML: staticSwitchoverYAML,
		GatewayYAML:    staticRouteGatewayYAML,
		Mode:           "static",

		RollbackFenceYAML: staticRollbackFenceYAML,
		RollbackAllowed:   true,
	}
}

// runStaticBranchWithConfig drives runStaticBranch directly, as
// runDynamicBranchWithConfig does for the dynamic branch: a fresh config from
// the manifest and staticResult standing in for the live reconcile.
// editGateway, when non-nil, stands in for a CLI policy override.
func runStaticBranchWithConfig(t *testing.T, f fixture, topics []string, editGateway func(*manifest.GatewayMigration), runReportPath string, deps executorDependencies) error {
	t.Helper()
	_, err := runStaticBranchWithResult(t, f, staticResult(topics), editGateway, runReportPath, deps)
	return err
}

// runStaticBranchWithResult is runStaticBranchWithConfig with the stand-in
// reconcile result supplied by the caller, returning the branch's stdout.
func runStaticBranchWithResult(t *testing.T, f fixture, res *migplan.Result, editGateway func(*manifest.GatewayMigration), runReportPath string, deps executorDependencies) (string, error) {
	t.Helper()
	g := loadGateway(t, f.manifestPath)
	if editGateway != nil {
		editGateway(g)
	}
	config := buildFreshMigrationConfig(g, g.Metadata.Name)

	var out strings.Builder
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	err := runStaticBranch(cmd, g, &config, res, deps, runReportPath)
	return out.String(), err
}

func TestExecute_StaticMode_RunsToCompletionOnStubbedServices(t *testing.T) {
	f := newFixture(t, nil)
	require.NoError(t, runStaticBranchWithConfig(t, f, []string{"t1.order"}, nil, "", stubDeps(nil, nil)))
}

// PromoteBatchSize reaches MigrationActions.SetPromoteBatchSize: with a cap of
// 1, each PromoteMirrorTopics call submits exactly one topic; uncapped, both
// go in one call.
func TestExecute_StaticMode_PromoteBatchSizeReachesStaticActions(t *testing.T) {
	topics := []string{"t1.order", "t2.inventory"}

	t.Run("policy caps the batch", func(t *testing.T) {
		rec := &recordingClusterLinkService{}
		editGateway := func(g *manifest.GatewayMigration) { g.Spec.DefaultPolicies.PromoteBatchSize = 1 }
		require.NoError(t, runStaticBranchWithConfig(t, newFixture(t, nil), topics, editGateway, "", stubDeps(nil, rec)))
		assert.Equal(t, 1, rec.maxBatch(), "promoteBatchSize 1 must cap every promote batch at one topic")
	})

	t.Run("unlimited promotes all at once (control)", func(t *testing.T) {
		rec := &recordingClusterLinkService{}
		require.NoError(t, runStaticBranchWithConfig(t, newFixture(t, nil), topics, nil, "", stubDeps(nil, rec)))
		assert.Equal(t, len(topics), rec.maxBatch(),
			"with no cap both topics promote in one call — proving the policy, not chance, capped the run above")
	})
}

// GatewayConfigPort reaches config.GatewayConfigPort before the capability
// probe (DetectCapability) runs.
func TestExecute_StaticMode_GatewayConfigPortReachesStaticCapabilityProbe(t *testing.T) {
	rec := &recordingGatewayService{}
	editGateway := func(g *manifest.GatewayMigration) { g.Spec.DefaultPolicies.GatewayConfigPort = 9999 }
	require.NoError(t, runStaticBranchWithConfig(t, newFixture(t, nil), []string{"t1.order"}, editGateway, "", stubDeps(rec, nil)))
	assert.Equal(t, 9999, rec.port(), "gatewayConfigPort must reach the static capability probe")
}

// readinessRecordingGateway records the timeout passed to each
// WaitForGatewayReady call — where the rollout timeout surfaces.
type readinessRecordingGateway struct {
	recordingGatewayService
	mu       sync.Mutex
	timeouts []time.Duration
}

func (r *readinessRecordingGateway) WaitForGatewayReady(_ context.Context, _, _ string, _ int64, _, timeout time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
	r.mu.Lock()
	r.timeouts = append(r.timeouts, timeout)
	r.mu.Unlock()
	return nil
}

// RolloutTimeout reaches MigrationActions.SetRolloutTimeout: every gateway
// readiness wait (fence and switch) is bounded by it.
func TestExecute_StaticMode_RolloutTimeoutReachesGatewayWaits(t *testing.T) {
	rec := &readinessRecordingGateway{}
	editGateway := func(g *manifest.GatewayMigration) { g.Spec.DefaultPolicies.RolloutTimeout = 7 * time.Minute }
	require.NoError(t, runStaticBranchWithConfig(t, newFixture(t, nil), []string{"t1.order"}, editGateway, "", stubDeps(rec, nil)))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.timeouts, 2, "one readiness wait for the fence, one for the switch")
	for _, got := range rec.timeouts {
		assert.Equal(t, 7*time.Minute, got, "rolloutTimeout must bound every gateway readiness wait")
	}
}

// A run-report path wires the recorder into the orchestrator: the report on
// disk names every stage of the completed run.
func TestExecute_StaticMode_WritesRunReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-report.json")
	require.NoError(t, runStaticBranchWithConfig(t, newFixture(t, nil), []string{"t1.order"}, nil, path, stubDeps(nil, nil)))

	raw, err := os.ReadFile(path)
	require.NoError(t, err, "the run report must be written")
	var report migration.RunReport
	require.NoError(t, json.Unmarshal(raw, &report))

	assert.Equal(t, migration.StateOffsetSyncRestored, report.FinalState)
	assert.Equal(t, migration.RunOutcomeCompleted, report.Outcome)
	var events []string
	for _, s := range report.Stages {
		events = append(events, s.Event)
	}
	assert.Equal(t, []string{
		migration.EventInitialize, migration.EventWaitForLags, migration.EventFence,
		migration.EventPauseOffsetSync, migration.EventVerifyFence, migration.EventPromote, migration.EventSwitch,
		migration.EventRestoreOffsetSync,
	}, events)
}

// alterRecordingClusterLink records the value of every consumer.offset.sync.enable
// AlterConfigs sets, in call order, and fails a SET to failValue when set.
type alterRecordingClusterLink struct {
	stubClusterLinkServiceImpl
	mu        sync.Mutex
	set       []string
	failValue string
}

func (r *alterRecordingClusterLink) AlterConfigs(_ context.Context, _ clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range alts {
		if a.Name == "consumer.offset.sync.enable" {
			r.set = append(r.set, a.Value)
			if r.failValue != "" && a.Value == r.failValue {
				return fmt.Errorf("503 alter boom")
			}
		}
	}
	return nil
}

// pausingFixture is the default fixture with spec.clusterLink opting into the
// offset-sync pause against an enabled baseline.
func pausingFixture(t *testing.T) fixture {
	const anchor = "    name: msk-to-cc\n"
	return newFixture(t, func(doc string) string {
		require.Contains(t, doc, anchor)
		return strings.Replace(doc, anchor, anchor+"    pauseConsumerOffsetSync: true\n    consumerOffsetSyncBaseline: enabled\n", 1)
	})
}

// With pauseConsumerOffsetSync and a restore in the plan, the pause and the
// restore both run inside the FSM: offset sync is disabled after the fence and
// set back to the declared baseline after the switch.
func TestExecute_StaticMode_RestoresOffsetSyncAfterSuccess(t *testing.T) {
	rec := &alterRecordingClusterLink{}
	res := staticResult([]string{"t1.order"})
	res.RestoreOffsetSync = true
	_, err := runStaticBranchWithResult(t, pausingFixture(t), res, nil, "", stubDeps(nil, rec))
	require.NoError(t, err)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Equal(t, []string{"false", "true"}, rec.set,
		"the pause stage disables offset sync, then the restore stage sets the declared baseline")
}

// The restore is the plan's call: with the pause opted in but no restore in
// the plan, nothing after the FSM sets offset sync back.
func TestExecute_StaticMode_RestoresOnlyWhenThePlanOwesIt(t *testing.T) {
	rec := &alterRecordingClusterLink{}
	_, err := runStaticBranchWithResult(t, pausingFixture(t), staticResult([]string{"t1.order"}), nil, "", stubDeps(nil, rec))
	require.NoError(t, err)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Equal(t, []string{"false"}, rec.set, "only the pause; no restore the plan did not ask for")
}

// A failed restore fails the run: execute returns an error and does not report
// the migration completed, so the operator re-runs it and the re-run retries
// the restore.
func TestExecute_StaticMode_RestoreFailureFailsTheRun(t *testing.T) {
	rec := &alterRecordingClusterLink{failValue: "true"}
	res := staticResult([]string{"t1.order"})
	res.RestoreOffsetSync = true
	out, err := runStaticBranchWithResult(t, pausingFixture(t), res, nil, "", stubDeps(nil, rec))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "restoring consumer offset sync")
	assert.Contains(t, err.Error(), "503 alter boom")
	assert.NotContains(t, out, "Migration completed", "a failed restore must not report the migration completed")
}
