package tbm

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestOrchestrator(t *testing.T) (*TBMOrchestrator, *migration.MigrationConfig) {
	t.Helper()

	config := &migration.MigrationConfig{
		MigrationId:   "test-tbm-1",
		K8sNamespace:  "confluent",
		InitialCrName: "gateway-initial",
	}
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) { return "", nil },
	}
	// Configured (not the bare zero-value mock) because realisticReconcileResult
	// (used by several tests below) sets Topics: []string{"t1.order"}, which a
	// full uninitialized->switched walk carries all the way into Promote —
	// an unconfigured mock would fail there with "not configured".
	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{MirrorTopicName: name})
			}
			return resp, nil
		},
		listMirrorTopicsFn: func(context.Context, clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{{MirrorTopicName: "t1.order", MirrorStatus: clusterlink.MirrorStatusStopped}}, nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, cl)
	actions.promotePollInterval = time.Millisecond
	orchestrator := NewTBMOrchestrator(config, actions)
	return orchestrator, config
}

func TestTBMOrchestrator_Execute_WalksEveryStepFromUninitialized(t *testing.T) {
	orchestrator, _ := newTestOrchestrator(t)

	require.NoError(t, orchestrator.Execute(context.Background(), realisticReconcileResult(), 10, 0, clusterlink.BasicAuth{}))

	assert.Equal(t, StateSwitched, orchestrator.fsm.Current())
}

func TestTBMOrchestrator_Execute_CtxCancellationStopsAtLastCompletedStep(t *testing.T) {
	orchestrator, _ := newTestOrchestrator(t)
	// Force the fence step to block on ctx: a real gateway wait that never
	// resolves on its own, cancellable only by ctx, is what proves the walk
	// stops mid-step rather than after the whole Execute call completes.
	orchestrator.actions.gatewayService.(*mockGatewayService).waitForGatewayAcceptedFn = func(ctx context.Context, _, _ string, _, _ time.Duration) error {
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := orchestrator.Execute(ctx, realisticReconcileResult(), 10, 0, clusterlink.BasicAuth{})

	require.Error(t, err)
	assert.NotEqual(t, StateSwitched, orchestrator.fsm.Current())
}

func TestTBMOrchestrator_Execute_InitializeCapturesReconcileArtifacts(t *testing.T) {
	orchestrator, config := newTestOrchestrator(t)

	res := realisticReconcileResult()

	require.NoError(t, orchestrator.Execute(context.Background(), res, 10, 0, clusterlink.BasicAuth{}))

	assert.Equal(t, res.Topics, config.Topics)
	assert.Equal(t, res.FenceYAML, config.FenceYAML)
	assert.Equal(t, res.SwitchoverYAML, config.SwitchoverYAML)
	assert.Equal(t, res.GatewayYAML, config.GatewayYAML)
	assert.Equal(t, res.Route, config.Route)

}

func TestTBMOrchestrator_Execute_RefusedReconcilePlanFailsAndConfigNotAdvanced(t *testing.T) {
	orchestrator, config := newTestOrchestrator(t)

	res := &migplan.Result{Refused: true, Reasons: []string{"topic t1.order has replication lag", "gateway rejected the fence spec"}}

	err := orchestrator.Execute(context.Background(), res, 10, 0, clusterlink.BasicAuth{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "topic t1.order has replication lag")
	assert.Contains(t, err.Error(), "gateway rejected the fence spec")
	assert.Equal(t, StateUninitialized, orchestrator.fsm.Current())
	assert.Empty(t, config.Topics)
}

func TestTBMOrchestrator_Execute_UnroutedProducersDetected_UnfencesAndRollsBackToInitialized(t *testing.T) {
	var applyCount int
	var lastRP gateway.RoutePatch
	var call int32
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, rp gateway.RoutePatch, configID string) (string, error) {
			applyCount++
			lastRP = rp
			return "", nil
		},
	}
	cl := &mockClusterLinkService{}
	sourceOffset := &mockOffsetProvider{
		// a.sourceOffset is shared between wait_for_lags's own offset sweep and
		// verify_fence's two snapshots (both read the same live source
		// cluster), so the first call here is wait_for_lags's sweep, not
		// verify_fence's baseline — the first two calls hold steady at 1000
		// (wait_for_lags's sweep, then verify_fence's baseline snapshot) and
		// only the third (verify_fence's post-window snapshot) shows the rise.
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt32(&call, 1)
			if n <= 2 {
				return map[int32]int64{0: 1000}, nil
			}
			return map[int32]int64{0: 1500}, nil // rogue producer during the window
		},
	}
	config := &migration.MigrationConfig{
		MigrationId:   "test-tbm-rollback",
		K8sNamespace:  "confluent",
		InitialCrName: "gateway-initial",
	}
	actions := NewTBMActions(sourceOffset, zeroLagOffsetProvider(), gw, cl)
	orchestrator := NewTBMOrchestrator(config, actions)
	var out strings.Builder
	orchestrator.reporter = &reporter{out: &out, err: io.Discard}

	err := orchestrator.Execute(context.Background(), realisticReconcileResult(), 10, 5*time.Millisecond, clusterlink.BasicAuth{})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)
	assert.Contains(t, out.String(), "Unrouted producers detected — removing fence to restore traffic",
		"the rollback banner names detection as the reason, as a capitalised sentence")
	assert.Equal(t, StateInitialized, orchestrator.fsm.Current(), "a detected rollback must leave the batch at initialized, so a resume re-checks lag for real before re-fencing")
	assert.Equal(t, 2, applyCount, "fence applies once, the abort_fence rollback's unfence applies once more")

	// testGatewayYAML's migration-route already carries a rules.routing block
	// before any fence — only FenceYAML's "fencing" key is grafted on top of
	// it (see realisticReconcileResult's FenceYAML). So the unfenced route's
	// rules must still have routing (never removed) and must NOT have
	// fencing (the thing the rollback undoes) — not "no rules at all".
	assert.Equal(t, "", lastRP.Field, "the unfence rollback must whole-route replace, not set a single field")
	route, ok := lastRP.Value.(map[string]interface{})
	require.True(t, ok)
	rules, ok := route["rules"].(map[string]interface{})
	require.True(t, ok, "the unfenced route must still carry its original rules.routing block")
	_, hasFencing := rules["fencing"]
	assert.False(t, hasFencing, "the unfenced route must not carry the fencing block the rollback is undoing")
	_, hasRouting := rules["routing"]
	assert.True(t, hasRouting, "the unfenced route must still have the routing block testGatewayYAML always had")

}

func TestTBMOrchestrator_Execute_StableOffsets_NoRollback(t *testing.T) {
	orchestrator, _ := newTestOrchestrator(t)

	err := orchestrator.Execute(context.Background(), realisticReconcileResult(), 10, 5*time.Millisecond, clusterlink.BasicAuth{})

	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orchestrator.fsm.Current())
}

func TestTBMOrchestrator_Execute_VerifyFetchError_AbortsFenceAndRollsBack(t *testing.T) {
	// Any halting error in the fence-up, pre-promote window — here a transient
	// offset-fetch failure during verify (NOT a rogue-producer detection) — must
	// abort the fence and roll back to initialized. Nothing is promoted yet and
	// KCP is alive, so the safe move is to unfence and let the operator re-run.
	var applyCount int
	var lastRP gateway.RoutePatch
	var call int32
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, rp gateway.RoutePatch, _ string) (string, error) {
			applyCount++
			lastRP = rp
			return "", nil
		},
	}
	cl := &mockClusterLinkService{}
	// call 1 = wait_for_lags sweep, 2 = verify baseline, 3 = verify post-window.
	// Fail the post-window snapshot: a fetch error, not a rogue detection.
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			if atomic.AddInt32(&call, 1) >= 3 {
				return nil, fmt.Errorf("connection reset by peer")
			}
			return map[int32]int64{0: 1000}, nil
		},
	}
	config := &migration.MigrationConfig{MigrationId: "test-tbm-verify-fetch-error", K8sNamespace: "confluent", InitialCrName: "gateway-initial"}
	actions := NewTBMActions(sourceOffset, zeroLagOffsetProvider(), gw, cl)
	orchestrator := NewTBMOrchestrator(config, actions)
	var out strings.Builder
	orchestrator.reporter = &reporter{out: &out, err: io.Discard}

	err := orchestrator.Execute(context.Background(), realisticReconcileResult(), 10, 5*time.Millisecond, clusterlink.BasicAuth{})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrUnroutedProducers, "a fetch failure is not a rogue-producer detection")
	assert.Contains(t, err.Error(), "connection reset by peer")
	assert.Contains(t, out.String(), "Verifying fence failed — removing fence to restore traffic",
		"the rollback banner names the failed step, as a capitalised sentence")
	assert.Equal(t, StateInitialized, orchestrator.fsm.Current(),
		"a halting error while fenced and pre-promote must abort_fence back to initialized")
	assert.Equal(t, 2, applyCount, "fence applies once, the abort_fence unfence applies once more")
	assert.Equal(t, "", lastRP.Field, "the unfence must be a whole-route replace")
}

func TestTBMOrchestrator_Execute_VerifyError_CtxCancelled_NoAbort(t *testing.T) {
	// A context cancellation (Ctrl-C / kill) can't do the unfence IO, so it must
	// NOT attempt abort_fence — it leaves the fenced partial world for the
	// idempotent resume. Guards the ctx.Err()==nil half of the decision.
	var applyCount int
	var call int32
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, rp gateway.RoutePatch, _ string) (string, error) {
			applyCount++
			return "", nil
		},
	}
	cl := &mockClusterLinkService{}
	ctx, cancel := context.WithCancel(context.Background())
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			if atomic.AddInt32(&call, 1) >= 3 {
				cancel() // simulate Ctrl-C mid-verify
				return nil, context.Canceled
			}
			return map[int32]int64{0: 1000}, nil
		},
	}
	config := &migration.MigrationConfig{MigrationId: "test-tbm-ctx-cancel", K8sNamespace: "confluent", InitialCrName: "gateway-initial"}
	actions := NewTBMActions(sourceOffset, zeroLagOffsetProvider(), gw, cl)
	orchestrator := NewTBMOrchestrator(config, actions)

	err := orchestrator.Execute(ctx, realisticReconcileResult(), 10, 5*time.Millisecond, clusterlink.BasicAuth{})
	require.Error(t, err)
	assert.Equal(t, StateFenced, orchestrator.fsm.Current(),
		"a cancelled context must leave the fenced world for resume — no abort_fence")
	assert.Equal(t, 1, applyCount, "only the fence applies; no unfence on a dead context")
}

// ----- TBM kill-point matrix -----
//
// Each kill-point test below constructs, via the fakes plus a constructed
// *migplan.Result, the exact live state a kill at one point would leave,
// drives one from-zero Execute, and asserts convergence to
// switched plus a second Execute (fed the Result a real migplan.Reconcile
// would return once nothing is left, killPointDoneResult) is a pure no-op —
// zero additional gateway patches, zero additional PromoteMirrorTopics
// calls. Mirrors internal/services/migration/orchestrator_test.go's AAO
// kill-point section exactly in shape; TBM has no offset-sync stage, so it
// has no counterpart to TestAAO_OffsetSyncPaused.
//
// No live-observation is added anywhere: every row is expressed as fake
// behaviour plus a constructed Result, and the FSM only ever consumes
// config.Topics/FenceYAML/SwitchoverYAML — set once by Initialize from that
// Result — never anything read live from the cluster to decide what to skip
// (see workflow.go's Initialize and the plan-driven no-op guards on
// Fence/Promote/Switch).

// killPointFenceYAML and killPointSwitchoverYAML are TBM kill-point matrix
// fixtures covering two topics (t1.order, t2.payment) — mutually consistent
// with testGatewayYAML (same route, same rules shape as
// realisticReconcileResult in gateway_test.go), used by every kill-point
// test below. The mocked gateway never parses their topic lists — only that
// "rules" is non-empty and carries the expected top-level key ("fencing" /
// "conditions") matters to any assertion.
const killPointFenceYAML = `rules:
  routing:
    coordination:
      group: source
    default: source
  fencing:
    - topics: ["t1.order", "t2.payment"]
      blocked: true
`

const killPointSwitchoverYAML = `rules:
  routing:
    coordination:
      group: source
    default: source
    conditions:
      - topics: ["t1.order", "t2.payment"]
        streamingDomain: target
`

// killPointFullResult builds the migplan.Result for a fully migratable plan
// covering topics — the Result every "everything still to do" row below
// drives its first Execute call with.
func killPointFullResult(topics []string) *migplan.Result {
	return &migplan.Result{
		Route:          "migration-route",
		Topics:         topics,
		FenceYAML:      killPointFenceYAML,
		SwitchoverYAML: killPointSwitchoverYAML,
		GatewayYAML:    testGatewayYAML,
		Mode:           "dynamic",
	}
}

// killPointDoneResult builds the migplan.Result a real migplan.Reconcile
// returns once nothing at all remains for this batch: no per-topic promote
// work (Topics) and no gateway-level work either (FenceYAML/SwitchoverYAML
// empty too) — mirrors reconcileDynamic's own "nothing inflight" outcome.
// This is the constructed Result every row's second ("must now be a no-op")
// Execute call below is driven with.
func killPointDoneResult() *migplan.Result {
	return &migplan.Result{
		Route:       "migration-route",
		Topics:      []string{},
		GatewayYAML: testGatewayYAML,
		Mode:        "dynamic",
		// FenceYAML/SwitchoverYAML deliberately left "" — nothing in flight.
	}
}

// newTBMKillPointOrchestrator is the shared builder behind every
// kill-point test — TBM's counterpart to
// internal/services/migration/orchestrator_test.go's
// newAAOKillPointOrchestrator. It lets a test seed which of allMirrorTopics'
// mirrors are ALREADY STOPPED before Execute ever runs — modelling exactly
// the live state a kill at a mid/late-promote point would leave — and
// records every gateway-route patch (in call order, as the full RoutePatch
// so a test can inspect its rules fragment) and every PromoteMirrorTopics
// call (topic list, in call order), so each test can assert on them directly,
// including that a second Execute adds none.
//
// readyProgress, when non-empty, replaces the default (immediately
// succeeding, no-progress-reported) WaitForGatewayReady fake with one that
// reports each entry via onProgress, in order, before returning nil — the
// harness's fake pod-waiter for the convergence-wait tests, exactly mirroring
// newAAOKillPointOrchestrator's identical mechanism (see its doc comment for
// why this is the simplest fake that both lets the wait return and still
// proves the step observed an unconverged state before succeeding). Both
// Fence's and Switch's confirm step share this one mock (the default
// capability here is VerifyRollout with no configId, so VerifyTransition
// always falls back to WaitForGatewayReady), so a non-empty readyProgress is
// replayed on every call reaching it, fence's and switch's alike.
func newTBMKillPointOrchestrator(
	t *testing.T,
	allMirrorTopics []string,
	initiallyStopped []string,
	readyProgress []gateway.GatewayReadinessProgress,
) (orch *TBMOrchestrator, config *migration.MigrationConfig, patchCalls *[]gateway.RoutePatch, promoteCalls *[][]string, readyEventsOut *[]gateway.GatewayReadinessProgress) {
	t.Helper()

	config = &migration.MigrationConfig{
		MigrationId:   "test-tbm-killpoint",
		K8sNamespace:  "confluent",
		InitialCrName: "gateway-initial",
	}

	promotedTopics := make(map[string]bool, len(initiallyStopped))
	for _, topic := range initiallyStopped {
		promotedTopics[topic] = true
	}

	var patches []gateway.RoutePatch
	var promotes [][]string
	var readyEvents []gateway.GatewayReadinessProgress

	gw := &mockGatewayService{
		patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, configID string) (string, error) {
			patches = append(patches, rp)
			return configID, nil
		},
		waitForGatewayReadyFn: func(ctx context.Context, namespace, name string, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
			for _, p := range readyProgress {
				readyEvents = append(readyEvents, p)
				onProgress(p)
			}
			return nil
		},
	}

	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			out := make([]clusterlink.MirrorTopic, len(allMirrorTopics))
			for i, name := range allMirrorTopics {
				status := clusterlink.MirrorStatusActive
				if promotedTopics[name] {
					status = clusterlink.MirrorStatusStopped
				}
				out[i] = clusterlink.MirrorTopic{MirrorTopicName: name, MirrorStatus: status}
			}
			return out, nil
		},
		promoteMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			promotes = append(promotes, append([]string(nil), topicNames...))
			for _, name := range topicNames {
				promotedTopics[name] = true
			}
			data := make([]struct {
				MirrorTopicName string `json:"mirror_topic_name"`
				ErrorMessage    string `json:"error_message,omitempty"`
				ErrorCode       int    `json:"error_code,omitempty"`
			}, len(topicNames))
			for i, name := range topicNames {
				data[i].MirrorTopicName = name
			}
			return &clusterlink.PromoteMirrorTopicsResponse{Data: data}, nil
		},
	}

	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, cl)
	actions.promotePollInterval = time.Millisecond

	orch = NewTBMOrchestrator(config, actions)

	return orch, config, &patches, &promotes, &readyEvents
}

// TestTBM_FreshFullRun: a pristine batch, never
// fenced, mirrors ACTIVE, every topic migratable. A from-zero walk must
// fence, promote both topics, and switch — and a second run, once reconcile
// reports nothing left, must be a pure no-op.
func TestTBM_FreshFullRun(t *testing.T) {
	topics := []string{"t1.order", "t2.payment"}
	orch, config, patchCalls, promoteCalls, _ := newTBMKillPointOrchestrator(t, topics, nil, nil)

	err := orch.Execute(context.Background(), killPointFullResult(topics), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	assert.Len(t, *patchCalls, 2, "one fence apply and one switch apply")
	require.Len(t, *promoteCalls, 1, "both zero-lag topics promoted in one batch")
	assert.ElementsMatch(t, topics, (*promoteCalls)[0])

	// A second run: a fresh reconcile now reports nothing left at all. The
	// from-zero walk still visits every step, but every step's plan-driven
	// no-op guard must fire — zero new mutations.
	patchesBefore := len(*patchCalls)
	promotesBefore := len(*promoteCalls)

	rerun := NewTBMOrchestrator(config, orch.actions) // a re-run is a fresh process over the same world
	err = rerun.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, rerun.fsm.Current())

	assert.Len(t, *patchCalls, patchesBefore, "a completed batch's re-run must apply no gateway patches")
	assert.Len(t, *promoteCalls, promotesBefore, "a completed batch's re-run must issue no promote calls")
}

// The kill-point seam interrupts a TBM run right after the named checkpoint
// state via a real context cancellation, mirroring the AAO seam. A run that
// would reach StateSwitched must instead stop at StateFenced with a
// cancellation error, having fenced but neither promoted nor switched.
func TestTBMOrchestrator_Execute_KillPointEnvCancelsAfterState(t *testing.T) {
	t.Setenv(killpoint.EnvVar, StateFenced)
	topics := []string{"t1.order", "t2.payment"}
	orch, _, patchCalls, promoteCalls, _ := newTBMKillPointOrchestrator(t, topics, nil, nil)

	err := orch.Execute(context.Background(), killPointFullResult(topics), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})

	require.Error(t, err, "execute must exit non-zero when the kill-point env var fires")
	assert.ErrorIs(t, err, context.Canceled, "the interruption must surface as a context cancellation")
	assert.Equal(t, StateFenced, orch.fsm.Current(), "the run must stop right after the fenced checkpoint")
	assert.Len(t, *patchCalls, 1, "only the fence apply happened — the switch must not run")
	assert.Empty(t, *promoteCalls, "no promote occurred — interrupted before the promote stage")
}

// TestTBM_AlreadyFencedNoReapply: the world a kill
// right after fencing leaves, mirrors still ACTIVE (nothing was promoted by
// the prior, killed run). The FSM walks the whole workflow — there is no live read that could tell
// this run "the route is already fenced" apart from a fresh one, so Fence's
// apply is unconditionally re-issued every run. What this pins is that
// re-issuing that apply is safe: exactly one fence patch, never doubled
// (dynamic PrependFence idempotency), and promotion/switch proceed
// normally — landing at the same converged, idempotent-on-a-second-run place
// as a fresh batch.
func TestTBM_AlreadyFencedNoReapply(t *testing.T) {
	topics := []string{"t1.order", "t2.payment"}
	orch, config, patchCalls, promoteCalls, _ := newTBMKillPointOrchestrator(t, topics, nil, nil)

	err := orch.Execute(context.Background(), killPointFullResult(topics), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	assert.Len(t, *patchCalls, 2,
		"one fence re-apply plus one switch apply — never doubled")
	require.Len(t, *promoteCalls, 1)
	assert.ElementsMatch(t, topics, (*promoteCalls)[0])

	patchesBefore := len(*patchCalls)
	promotesBefore := len(*promoteCalls)
	rerun := NewTBMOrchestrator(config, orch.actions) // a re-run is a fresh process over the same world
	err = rerun.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Len(t, *patchCalls, patchesBefore)
	assert.Len(t, *promoteCalls, promotesBefore)
}

// TestTBM_FenceWaitsForConvergence: fenced, but the
// serving pods have not yet converged on the fenced config. There is no
// separate "poll until ready" loop in this test harness to hook — that loop
// lives entirely inside the production K8sService the gateway mock replaces
// — so "waits for convergence, then proceeds" is modelled as the mocked
// WaitForGatewayReady call itself reporting one not-yet-converged progress
// tick before its own converged return (see newTBMKillPointOrchestrator's
// doc comment on readyProgress). This is the simplest fake that both lets
// the wait return and still proves the step observed an unconverged state —
// not reported done while unconverged — before succeeding.
func TestTBM_FenceWaitsForConvergence(t *testing.T) {
	notReady := gateway.GatewayReadinessProgress{RolloutDetected: true, InitialPodCount: 2, PodsReady: 0}
	converged := gateway.GatewayReadinessProgress{RolloutDetected: true, InitialPodCount: 2, PodsReady: 2}
	topics := []string{"t1.order", "t2.payment"}

	orch, config, patchCalls, promoteCalls, readyEvents := newTBMKillPointOrchestrator(
		t, topics, nil, []gateway.GatewayReadinessProgress{notReady, converged})

	err := orch.Execute(context.Background(), killPointFullResult(topics), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err, "the fence step must succeed once convergence is reported, not error out on the interim tick")
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	require.NotEmpty(t, *readyEvents, "the fence/switch convergence wait must have been exercised")
	first := (*readyEvents)[0]
	assert.NotEqual(t, first.InitialPodCount, first.PodsReady,
		"the first reported tick must be the not-yet-converged one — the step must not appear done while unconverged")
	last := (*readyEvents)[len(*readyEvents)-1]
	assert.Equal(t, last.InitialPodCount, last.PodsReady, "the wait must end at convergence")

	assert.Len(t, *patchCalls, 2)
	require.Len(t, *promoteCalls, 1)

	patchesBefore := len(*patchCalls)
	promotesBefore := len(*promoteCalls)
	rerun := NewTBMOrchestrator(config, orch.actions) // a re-run is a fresh process over the same world
	err = rerun.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Len(t, *patchCalls, patchesBefore)
	assert.Len(t, *promoteCalls, promotesBefore)
}

// TestTBM_MidPromoteMix: a kill mid-promotion,
// where t1.order has already reached STOPPED and t2.payment is still ACTIVE.
// A real migplan.Reconcile excludes t1.order from the promote set entirely —
// the filtering is reconcile's job, done once, live — so the constructed
// Result here lists only t2.payment. This must drive Promote to
// promote t2.payment alone, never re-issuing PromoteMirrorTopics for the
// already-STOPPED t1.order.
func TestTBM_MidPromoteMix(t *testing.T) {
	allTopics := []string{"t1.order", "t2.payment"}
	orch, config, patchCalls, promoteCalls, _ := newTBMKillPointOrchestrator(
		t, allTopics, []string{"t1.order"}, nil)

	midResult := &migplan.Result{
		Route:          "migration-route",
		Topics:         []string{"t2.payment"},
		FenceYAML:      killPointFenceYAML,
		SwitchoverYAML: killPointSwitchoverYAML,
		GatewayYAML:    testGatewayYAML,
		Mode:           "dynamic",
	}

	err := orch.Execute(context.Background(), midResult, 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	require.Len(t, *promoteCalls, 1, "exactly one promote batch")
	assert.Equal(t, []string{"t2.payment"}, (*promoteCalls)[0],
		"only the not-yet-stopped topic is promoted — t1.order (already STOPPED) is never re-promoted")
	assert.Len(t, *patchCalls, 2, "fence + switch, gated by the still-nonempty (single-topic) plan")

	patchesBefore := len(*patchCalls)
	promotesBefore := len(*promoteCalls)
	rerun := NewTBMOrchestrator(config, orch.actions) // a re-run is a fresh process over the same world
	err = rerun.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Len(t, *patchCalls, patchesBefore)
	assert.Len(t, *promoteCalls, promotesBefore)
}

// TestTBM_Execute_ResumeAtAwaitStopped drives a full Execute for a resume where
// t1 is mid-promotion (PENDING_STOPPED) and t2 is fresh (ACTIVE). The reconcile
// Result carries BOTH in Topics but marks t1 AwaitStopped. The run must promote
// ONLY t2 — t1 is waited for, never re-promoted (a re-promote of an
// already-promoting mirror is fatal at CC) — then, once t1 reaches STOPPED,
// switch and converge. This is the end-to-end AwaitStopped resume path
// (Result.AwaitStopped -> config.AwaitStopped -> awaitingStop) that neither the
// piecewise Promote unit test nor S2 (already-STOPPED / SwitchOnly) exercises.
func TestTBM_Execute_ResumeAtAwaitStopped(t *testing.T) {
	allTopics := []string{"t1.order", "t2.payment"}
	var mu sync.Mutex
	var promotes [][]string
	promoted := map[string]bool{}
	var t1ListCalls int64

	gw := &mockGatewayService{
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) { return "", nil },
	}
	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(context.Context, clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			// t1 is PENDING_STOPPED for its first observation (mid-promotion on
			// resume), then settles to STOPPED. t2 is ACTIVE until promoted.
			t1Status := clusterlink.MirrorStatusPendingStopped
			if atomic.AddInt64(&t1ListCalls, 1) >= 2 {
				t1Status = clusterlink.MirrorStatusStopped
			}
			t2Status := clusterlink.MirrorStatusActive
			mu.Lock()
			if promoted["t2.payment"] {
				t2Status = clusterlink.MirrorStatusStopped
			}
			mu.Unlock()
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "t1.order", MirrorStatus: t1Status},
				{MirrorTopicName: "t2.payment", MirrorStatus: t2Status},
			}, nil
		},
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, names []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			mu.Lock()
			promotes = append(promotes, append([]string(nil), names...))
			for _, n := range names {
				promoted[n] = true
			}
			mu.Unlock()
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, n := range names {
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{MirrorTopicName: n})
			}
			return resp, nil
		},
	}
	config := &migration.MigrationConfig{MigrationId: "test-tbm-await", K8sNamespace: "confluent", InitialCrName: "gateway-initial"}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, cl)
	actions.promotePollInterval = time.Millisecond
	orch := NewTBMOrchestrator(config, actions)

	res := &migplan.Result{
		Route:          "migration-route",
		Topics:         allTopics,            // both are in-flight (must reach STOPPED before switch)
		AwaitStopped:   []string{"t1.order"}, // t1 is already promoting (PENDING_STOPPED)
		FenceYAML:      killPointFenceYAML,
		SwitchoverYAML: killPointSwitchoverYAML,
		GatewayYAML:    testGatewayYAML,
		Mode:           "dynamic",
	}

	err := orch.Execute(context.Background(), res, 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current(), "the resume must drive to switched")

	mu.Lock()
	defer mu.Unlock()
	var allPromoted []string
	for _, batch := range promotes {
		allPromoted = append(allPromoted, batch...)
	}
	assert.NotContains(t, allPromoted, "t1.order",
		"an AwaitStopped (PENDING_STOPPED) topic must be waited for, never re-promoted")
	assert.Contains(t, allPromoted, "t2.payment",
		"the genuinely migratable topic must still be promoted")
}

// TestTBM_PromotedNotSwitched: every mirror already STOPPED (nothing left to
// promote) but the switch not yet applied. A real migplan.Reconcile for this
// state returns Topics=[] but non-empty FenceYAML/SwitchoverYAML (reconcile's
// promote set excludes already-stopped topics, but its inflight set — which
// gates whether artifacts are built at all — still includes them, since the
// switch is still owed). Fence and Switch key their no-op on those artifacts,
// not on Topics, so Promote makes no call at all but Switch still applies and
// the run still converges.
func TestTBM_PromotedNotSwitched(t *testing.T) {
	allTopics := []string{"t1.order", "t2.payment"}
	orch, config, patchCalls, promoteCalls, _ := newTBMKillPointOrchestrator(
		t, allTopics, allTopics, nil)

	allPromotedResult := &migplan.Result{
		Route:          "migration-route",
		Topics:         []string{},
		FenceYAML:      killPointFenceYAML,
		SwitchoverYAML: killPointSwitchoverYAML,
		GatewayYAML:    testGatewayYAML,
		Mode:           "dynamic",
	}

	err := orch.Execute(context.Background(), allPromotedResult, 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	assert.Empty(t, *promoteCalls, "nothing left to promote — Promote must make no call at all")
	assert.Len(t, *patchCalls, 2,
		"fence + switch must both still apply — a non-empty artifact is owed regardless of the empty promote set")

	patchesBefore := len(*patchCalls)
	promotesBefore := len(*promoteCalls)
	rerun := NewTBMOrchestrator(config, orch.actions) // a re-run is a fresh process over the same world
	err = rerun.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Len(t, *patchCalls, patchesBefore)
	assert.Len(t, *promoteCalls, promotesBefore)
}

// TestTBM_SwitchWaitsForConvergence: the switch CR landed but the serving
// pods have not yet converged on it. Like TestTBM_PromotedNotSwitched, this
// needs Topics=[] (nothing left to promote) with a non-empty SwitchoverYAML
// for the switch to actually run. The convergence wait is faked the same way
// as TestTBM_FenceWaitsForConvergence:
// the mocked WaitForGatewayReady call itself reports a not-yet-converged
// progress tick before its own converged return.
func TestTBM_SwitchWaitsForConvergence(t *testing.T) {
	notReady := gateway.GatewayReadinessProgress{RolloutDetected: true, InitialPodCount: 2, PodsReady: 0}
	converged := gateway.GatewayReadinessProgress{RolloutDetected: true, InitialPodCount: 2, PodsReady: 2}
	allTopics := []string{"t1.order", "t2.payment"}

	orch, config, patchCalls, promoteCalls, readyEvents := newTBMKillPointOrchestrator(
		t, allTopics, allTopics, []gateway.GatewayReadinessProgress{notReady, converged})

	allPromotedResult := &migplan.Result{
		Route:          "migration-route",
		Topics:         []string{},
		FenceYAML:      killPointFenceYAML,
		SwitchoverYAML: killPointSwitchoverYAML,
		GatewayYAML:    testGatewayYAML,
		Mode:           "dynamic",
	}

	err := orch.Execute(context.Background(), allPromotedResult, 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err, "the switch step must succeed once convergence is reported, not error out on the interim tick")
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	require.NotEmpty(t, *readyEvents, "the fence/switch convergence wait must have been exercised")
	first := (*readyEvents)[0]
	assert.NotEqual(t, first.InitialPodCount, first.PodsReady,
		"the first reported tick must be the not-yet-converged one — the step must not appear done while unconverged")
	last := (*readyEvents)[len(*readyEvents)-1]
	assert.Equal(t, last.InitialPodCount, last.PodsReady, "the wait must end at convergence")

	assert.Empty(t, *promoteCalls)
	assert.Len(t, *patchCalls, 2)

	patchesBefore := len(*patchCalls)
	promotesBefore := len(*promoteCalls)
	rerun := NewTBMOrchestrator(config, orch.actions) // a re-run is a fresh process over the same world
	err = rerun.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Len(t, *patchCalls, patchesBefore)
	assert.Len(t, *promoteCalls, promotesBefore)
}

// TestTBM_DoneIsNoop — live state: fully switched already, batch fence gone,
// every mirror STOPPED. A real migplan.Reconcile classifies the batch
// Unchanged and returns a Result with no artifacts at all
// (killPointDoneResult): Topics, FenceYAML and
// SwitchoverYAML all empty. Every one of Fence/Promote/Switch's plan-driven
// no-op guards must fire — zero patches, zero promotes — and the FSM still
// walks through every step to switched rather than needing any special-cased
// short-circuit to get there.
func TestTBM_DoneIsNoop(t *testing.T) {
	allTopics := []string{"t1.order", "t2.payment"}
	orch, config, patchCalls, promoteCalls, _ := newTBMKillPointOrchestrator(
		t, allTopics, allTopics, nil)

	err := orch.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	assert.Empty(t, *patchCalls, "zero gateway patches — fence and switch must both no-op")
	assert.Empty(t, *promoteCalls, "zero promote calls")

	// A second Execute is byte-for-byte identical: still zero mutations.
	rerun := NewTBMOrchestrator(config, orch.actions) // a re-run is a fresh process over the same world
	err = rerun.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, rerun.fsm.Current())
	assert.Empty(t, *patchCalls)
	assert.Empty(t, *promoteCalls)
}

// TestTBM_MultiBatchComposite: a multi-batch composite where t0.legacy is a
// wholly separate, already-fully-migrated batch — Unchanged by reconcile (it
// never appears in Topics/FenceYAML/SwitchoverYAML at all, unlike
// TestTBM_MidPromoteMix's t1.order, which is still part of the same inflight
// plan), its mirror STOPPED from the very start of this run —
// while t1.order/t2.payment are a second, still-migratable batch in the same
// Result. This must converge the active batch to switched while never
// touching t0.legacy (no PromoteMirrorTopics call ever names it) and never
// doubling the fence/switch patch count for having two batches present.
func TestTBM_MultiBatchComposite(t *testing.T) {
	allTopics := []string{"t0.legacy", "t1.order", "t2.payment"}
	orch, config, patchCalls, promoteCalls, _ := newTBMKillPointOrchestrator(
		t, allTopics, []string{"t0.legacy"}, nil)

	activeTopics := []string{"t1.order", "t2.payment"}
	res := killPointFullResult(activeTopics)

	err := orch.Execute(context.Background(), res, 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	assert.Len(t, *patchCalls, 2, "one fence, one switch — never doubled for having two batches present")
	require.Len(t, *promoteCalls, 1, "the active batch promotes together, in one call")
	assert.ElementsMatch(t, activeTopics, (*promoteCalls)[0])
	for _, call := range *promoteCalls {
		assert.NotContains(t, call, "t0.legacy", "the completed batch's topic must never be re-promoted")
	}

	patchesBefore := len(*patchCalls)
	promotesBefore := len(*promoteCalls)
	rerun := NewTBMOrchestrator(config, orch.actions) // a re-run is a fresh process over the same world
	err = rerun.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Len(t, *patchCalls, patchesBefore)
	assert.Len(t, *promoteCalls, promotesBefore)
}
