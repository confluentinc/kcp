package execute

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/offset"
)

// This file exercises the static-mode (AAO) branch the way
// cmd_migration_execute_tbm_test.go exercises the dynamic one: the real
// manifest → buildExecutorOpts → MigrationExecutor.Run path, with the four
// downstream services stubbed through aaoDependencies and the live
// migplan.Reconcile (which cannot run in-process) replaced by staticResult.

// staticRouteGatewayYAML and the two fragments are the shapes migplan.Reconcile
// returns for a static route: a route-level fence block, and a streamingDomain
// flip for the switchover.
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
	staticFenceYAML      = "fence:\n  scope: ALL\n  errorCode: BROKER_NOT_AVAILABLE\n"
	staticSwitchoverYAML = "streamingDomain:\n  name: confluent-cloud\n  bootstrapServerId: sasl-plain\n"
)

// staticResult is the stand-in for the *migplan.Result a live
// migplan.Reconcile would return for a static route over topics.
func staticResult(topics []string) *migplan.Result {
	return &migplan.Result{
		Route:          "migration-route",
		Topics:         topics,
		FenceYAML:      staticFenceYAML,
		SwitchoverYAML: staticSwitchoverYAML,
		GatewayYAML:    staticRouteGatewayYAML,
		Mode:           "static",
	}
}

// closableOffsets adds the Close the AAO executor defers on its offset
// providers to a stub that has nothing to close.
type closableOffsets struct{ offset.Provider }

func (closableOffsets) Close() error { return nil }

// stubAAODependencies builds aaoDependencies over the shared stubs, with gw
// and cl substituted when non-nil so a test can record what reaches them.
func stubAAODependencies(gw gateway.Service, cl clusterlink.Service) aaoDependencies {
	if gw == nil {
		gw = stubGatewayServiceImpl{}
	}
	if cl == nil {
		cl = stubClusterLinkServiceImpl{}
	}
	offsets := func(MigrationExecutorOpts) (offsetProviderCloser, error) {
		return closableOffsets{zeroLagOffsetProvider{}}, nil
	}
	return aaoDependencies{
		sourceOffset:       offsets,
		destinationOffset:  offsets,
		gatewayService:     func(string) gateway.Service { return gw },
		clusterLinkService: func(clusterlink.HTTPClient) clusterlink.Service { return cl },
	}
}

// runAAOBranch drives the static branch exactly as runMigrationExecute does
// after its live reconcile: a fresh config from the manifest, buildExecutorOpts,
// then MigrationExecutor.Run, with deps in place of the live services.
// editGateway, when non-nil, stands in for a CLI policy override (the same
// substitution the TBM tests make; see runTBMBranchWithConfig).
func runAAOBranch(t *testing.T, f fixture, topics []string, editGateway func(*manifest.GatewayMigration), runReportPath string, deps aaoDependencies) error {
	t.Helper()
	g := loadGateway(t, f.manifestPath)
	if editGateway != nil {
		editGateway(g)
	}
	kubeConfigPath, err := resolveKubeConfigPath(g)
	require.NoError(t, err)
	config := buildFreshMigrationConfig(g, g.Metadata.Name, kubeConfigPath)

	opts, err := buildExecutorOpts(g, &config, staticResult(topics))
	require.NoError(t, err)
	opts.RunReportPath = runReportPath

	return newMigrationExecutorWithDeps(opts, deps).Run()
}

func TestExecute_StaticMode_RunsToCompletionOnStubbedServices(t *testing.T) {
	f := newFixture(t, nil)
	require.NoError(t, runAAOBranch(t, f, []string{"t1.order"}, nil, "", stubAAODependencies(nil, nil)))
}

// PromoteBatchSize reaches MigrationActions.SetPromoteBatchSize: with a cap of
// 1, each PromoteMirrorTopics call submits exactly one topic; uncapped, both
// go in one call.
func TestExecute_StaticMode_PromoteBatchSizeReachesAAOActions(t *testing.T) {
	topics := []string{"t1.order", "t2.inventory"}

	t.Run("policy caps the batch", func(t *testing.T) {
		rec := &recordingClusterLinkService{}
		editGateway := func(g *manifest.GatewayMigration) { g.Spec.DefaultPolicies.PromoteBatchSize = 1 }
		require.NoError(t, runAAOBranch(t, newFixture(t, nil), topics, editGateway, "", stubAAODependencies(nil, rec)))
		assert.Equal(t, 1, rec.maxBatch(), "promoteBatchSize 1 must cap every promote batch at one topic")
	})

	t.Run("unlimited promotes all at once (control)", func(t *testing.T) {
		rec := &recordingClusterLinkService{}
		require.NoError(t, runAAOBranch(t, newFixture(t, nil), topics, nil, "", stubAAODependencies(nil, rec)))
		assert.Equal(t, len(topics), rec.maxBatch(),
			"with no cap both topics promote in one call — proving the policy, not chance, capped the run above")
	})
}

// GatewayConfigPort reaches config.GatewayConfigPort before the capability
// probe (DetectCapability) runs.
func TestExecute_StaticMode_GatewayConfigPortReachesAAO(t *testing.T) {
	rec := &recordingGatewayService{}
	editGateway := func(g *manifest.GatewayMigration) { g.Spec.DefaultPolicies.GatewayConfigPort = 9999 }
	require.NoError(t, runAAOBranch(t, newFixture(t, nil), []string{"t1.order"}, editGateway, "", stubAAODependencies(rec, nil)))
	assert.Equal(t, 9999, rec.port(), "gatewayConfigPort must reach the AAO capability probe")
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
	require.NoError(t, runAAOBranch(t, newFixture(t, nil), []string{"t1.order"}, editGateway, "", stubAAODependencies(rec, nil)))

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
	require.NoError(t, runAAOBranch(t, newFixture(t, nil), []string{"t1.order"}, nil, path, stubAAODependencies(nil, nil)))

	raw, err := os.ReadFile(path)
	require.NoError(t, err, "the run report must be written")
	var report migration.RunReport
	require.NoError(t, json.Unmarshal(raw, &report))

	assert.Equal(t, migration.StateSwitched, report.FinalState)
	assert.Equal(t, migration.RunOutcomeCompleted, report.Outcome)
	var events []string
	for _, s := range report.Stages {
		events = append(events, s.Event)
	}
	assert.Equal(t, []string{
		migration.EventInitialize, migration.EventWaitForLags, migration.EventFence,
		migration.EventPauseOffsetSync, migration.EventVerifyFence, migration.EventPromote, migration.EventSwitch,
	}, events)
}

// alterRecordingClusterLink records the value of every consumer.offset.sync.enable
// AlterConfigs sets, in call order.
type alterRecordingClusterLink struct {
	stubClusterLinkServiceImpl
	mu  sync.Mutex
	set []string
}

func (r *alterRecordingClusterLink) AlterConfigs(_ context.Context, _ clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range alts {
		if a.Name == "consumer.offset.sync.enable" {
			r.set = append(r.set, a.Value)
		}
	}
	return nil
}

// With pauseConsumerOffsetSync, the pause runs inside the FSM and the
// post-execute bookend in Run restores the declared baseline afterwards.
func TestExecute_StaticMode_RestoresOffsetSyncAfterSuccess(t *testing.T) {
	const anchor = "    name: msk-to-cc\n"
	f := newFixture(t, func(doc string) string {
		require.Contains(t, doc, anchor)
		return strings.Replace(doc, anchor, anchor+"    pauseConsumerOffsetSync: true\n    consumerOffsetSyncBaseline: enabled\n", 1)
	})
	rec := &alterRecordingClusterLink{}
	require.NoError(t, runAAOBranch(t, f, []string{"t1.order"}, nil, "", stubAAODependencies(nil, rec)))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Equal(t, []string{"false", "true"}, rec.set,
		"the pause stage disables offset sync, then Run's bookend restores the declared baseline")
}
