package tbm

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestOrchestrator(t *testing.T, initialState string) (*TBMOrchestrator, *migration.MigrationConfig, string) {
	t.Helper()

	config := &migration.MigrationConfig{
		MigrationId:   "test-tbm-1",
		CurrentState:  initialState,
		K8sNamespace:  "confluent",
		InitialCrName: "gateway-initial",
	}
	state := migration.NewMigrationState()
	stateFile := filepath.Join(t.TempDir(), "tbm-state.json")
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
	orchestrator := NewTBMOrchestrator(config, actions, state, stateFile)
	return orchestrator, config, stateFile
}

// newTestOrchestratorAtFSMState builds an orchestrator exactly like
// newTestOrchestrator, then positions its FSM directly at fsmState via
// fsm.SetState — a test-only bypass (no callbacks fire). NewTBMOrchestrator
// itself always starts the FSM at StateUninitialized now, regardless of
// config.CurrentState (start-from-zero — see orchestrator.go); this is for
// tests that check behavior AT a given FSM position (e.g. HasPendingWork's
// own predicate logic) without walking every earlier step to get there, and
// must NOT be used for a test that exercises Execute itself — Execute's own
// from-zero contract is what newTestOrchestrator (unmodified) plus a real
// walk pins.
func newTestOrchestratorAtFSMState(t *testing.T, fsmState string) (*TBMOrchestrator, *migration.MigrationConfig, string) {
	t.Helper()
	orchestrator, config, stateFile := newTestOrchestrator(t, fsmState)
	orchestrator.fsm.SetState(fsmState)
	return orchestrator, config, stateFile
}

// TestNewTBMOrchestrator_AlwaysStartsUninitialized pins the start-from-zero
// contract at its source: construction must ignore config.CurrentState
// entirely, even when it holds a fully-completed migration's persisted
// value. There is no resume position — reconcile (run every invocation, see
// cmd/migration/execute) and idempotent applies determine what happens on
// top of an FSM that always begins at StateUninitialized.
func TestNewTBMOrchestrator_AlwaysStartsUninitialized(t *testing.T) {
	cfg := &migration.MigrationConfig{CurrentState: StateSwitched, MigrationId: "m1"}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), &mockGatewayService{}, &mockClusterLinkService{})
	o := NewTBMOrchestrator(cfg, actions, migration.NewMigrationState(), filepath.Join(t.TempDir(), "s.json"))
	if got := o.fsm.Current(); got != StateUninitialized {
		t.Fatalf("TBM FSM start state = %q, want %q", got, StateUninitialized)
	}
	assert.True(t, o.HasPendingWork(),
		"immediately after construction the FSM can always take its first step (initialize)")
}

func TestTBMOrchestrator_Execute_WalksEveryStepFromUninitialized(t *testing.T) {
	orchestrator, config, stateFile := newTestOrchestrator(t, StateUninitialized)

	require.NoError(t, orchestrator.Execute(context.Background(), realisticReconcileResult(), 10, 0, clusterlink.BasicAuth{}))

	assert.Equal(t, StateSwitched, config.CurrentState)
	assert.False(t, orchestrator.HasPendingWork())

	loaded, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	persisted, err := loaded.GetMigrationById("test-tbm-1")
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, persisted.CurrentState)
}

// TestTBMOrchestrator_Execute_FromZero_IgnoresPersistedCurrentState replaces
// the old ResumesFromPartialState test, which pinned the REMOVED contract:
// construction read config.CurrentState to decide where the FSM started,
// bootstrap-demoted via the also-removed expire_fence edge. The FSM now
// always starts at StateUninitialized regardless of config.CurrentState (see
// TestNewTBMOrchestrator_AlwaysStartsUninitialized), so a stale persisted
// value here changes nothing: Execute walks the whole canonical workflow
// from Initialize every time.
//
// Loops every TBM state as the stale persisted CurrentState — there is no
// StateOffsetSyncPaused equivalent to skip, since TBM has no offset-sync
// stage, so this covers all of them — mirroring AAO's own parity test
// (TestOrchestrator_Execute_FromZero_IgnoresPersistedCurrentState in
// internal/services/migration/orchestrator_test.go). Like that test, this
// asserts not just the terminal state but a concrete side effect: the fence
// CR is actually (re-)applied, first, on every from-zero walk, regardless of
// what the stale persisted state claims — proving the run never trusts a
// persisted fenced posture, not merely that it happens to land at switched.
func TestTBMOrchestrator_Execute_FromZero_IgnoresPersistedCurrentState(t *testing.T) {
	for _, staleState := range []string{StateUninitialized, StateInitialized, StateLagsOk, StateFenced, StateFenceVerified, StatePromoted, StateSwitched} {
		t.Run("stale_"+staleState, func(t *testing.T) {
			orchestrator, config, stateFile := newTestOrchestrator(t, staleState)

			var appliedPatches []gateway.RoutePatch
			orchestrator.actions.gatewayService.(*mockGatewayService).patchGatewayRouteFn = func(_ context.Context, _, _ string, rp gateway.RoutePatch, configID string) (string, error) {
				appliedPatches = append(appliedPatches, rp)
				return configID, nil
			}

			require.NoError(t, orchestrator.Execute(context.Background(), realisticReconcileResult(), 10, 0, clusterlink.BasicAuth{}))

			assert.Equal(t, StateSwitched, config.CurrentState,
				"a from-zero walk must reach switched regardless of the stale persisted state")

			loaded, err := migration.NewMigrationStateFromFile(stateFile)
			require.NoError(t, err)
			persisted, err := loaded.GetMigrationById("test-tbm-1")
			require.NoError(t, err)
			assert.Equal(t, StateSwitched, persisted.CurrentState)

			require.NotEmpty(t, appliedPatches, "a from-zero walk must (re-)apply the fence CR")
			rules, ok := appliedPatches[0].Value.(map[string]interface{})
			require.True(t, ok, "the first gateway patch's value must be the rules fragment map")
			_, hasFencing := rules["fencing"]
			assert.True(t, hasFencing,
				"the first gateway patch of a from-zero walk is always the fence — the run never trusts a persisted fenced posture")
		})
	}
}

// TestTBMOrchestrator_HasPendingWork pins the HasPendingWork predicate's own
// logic — true for every state short of switched, false once switched —
// independent of how the FSM reached that state. It is exercised at each FSM
// position directly via newTestOrchestratorAtFSMState (a test-only bypass),
// since NewTBMOrchestrator's construction no longer positions the FSM from
// config.CurrentState at all (start-from-zero). Unlike AAO
// (cmd/migration/execute/migration_executor.go dropped its HasPendingWork
// short-circuit in 2c), TBM's own executor (tbm_executor.go) no longer uses
// it either as of this change — it is exercised here for its own sake.
func TestTBMOrchestrator_HasPendingWork(t *testing.T) {
	tests := []struct {
		name  string
		state string
		want  bool
	}{
		{"uninitialized has work", StateUninitialized, true},
		{"switched has no work", StateSwitched, false},
		{"unknown state reports pending", "some-future-state", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orchestrator, _, _ := newTestOrchestratorAtFSMState(t, tt.state)
			assert.Equal(t, tt.want, orchestrator.HasPendingWork())
		})
	}
}

func TestTBMOrchestrator_Execute_RefusesUnknownState(t *testing.T) {
	orchestrator, _, _ := newTestOrchestrator(t, "some-future-state")

	err := orchestrator.Execute(context.Background(), &migplan.Result{}, 10, 0, clusterlink.BasicAuth{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unrecognized")
}

func TestTBMOrchestrator_Execute_CtxCancellationStopsAtLastCompletedStep(t *testing.T) {
	orchestrator, config, _ := newTestOrchestrator(t, StateUninitialized)
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
	assert.NotEqual(t, StateSwitched, config.CurrentState)
}

func TestTBMOrchestrator_Execute_InitializeCapturesReconcileArtifacts(t *testing.T) {
	orchestrator, config, stateFile := newTestOrchestrator(t, StateUninitialized)

	res := realisticReconcileResult()

	require.NoError(t, orchestrator.Execute(context.Background(), res, 10, 0, clusterlink.BasicAuth{}))

	assert.Equal(t, res.Topics, config.Topics)
	assert.Equal(t, res.FenceYAML, config.FenceYAML)
	assert.Equal(t, res.SwitchoverYAML, config.SwitchoverYAML)
	assert.Equal(t, res.GatewayYAML, config.GatewayYAML)
	assert.Equal(t, res.Route, config.Route)

	loaded, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	persisted, err := loaded.GetMigrationById("test-tbm-1")
	require.NoError(t, err)
	assert.Equal(t, res.Topics, persisted.Topics)
	assert.Equal(t, res.FenceYAML, persisted.FenceYAML)
	assert.Equal(t, res.SwitchoverYAML, persisted.SwitchoverYAML)
	assert.Equal(t, res.GatewayYAML, persisted.GatewayYAML)
	assert.Equal(t, res.Route, persisted.Route)
}

func TestTBMOrchestrator_Execute_RefusedReconcilePlanFailsAndConfigNotAdvanced(t *testing.T) {
	orchestrator, config, _ := newTestOrchestrator(t, StateUninitialized)

	res := &migplan.Result{Refused: true, Reasons: []string{"topic t1.order has replication lag", "gateway rejected the fence spec"}}

	err := orchestrator.Execute(context.Background(), res, 10, 0, clusterlink.BasicAuth{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "topic t1.order has replication lag")
	assert.Contains(t, err.Error(), "gateway rejected the fence spec")
	assert.Equal(t, StateUninitialized, config.CurrentState)
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
		CurrentState:  StateUninitialized,
		K8sNamespace:  "confluent",
		InitialCrName: "gateway-initial",
	}
	state := migration.NewMigrationState()
	stateFile := filepath.Join(t.TempDir(), "tbm-state.json")
	actions := NewTBMActions(sourceOffset, zeroLagOffsetProvider(), gw, cl)
	orchestrator := NewTBMOrchestrator(config, actions, state, stateFile)

	err := orchestrator.Execute(context.Background(), realisticReconcileResult(), 10, 5*time.Millisecond, clusterlink.BasicAuth{})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)
	assert.Equal(t, StateInitialized, config.CurrentState, "a detected rollback must leave the batch at initialized, so a resume re-checks lag for real before re-fencing")
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

	loaded, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	persisted, err := loaded.GetMigrationById("test-tbm-rollback")
	require.NoError(t, err)
	assert.Equal(t, StateInitialized, persisted.CurrentState, "the rolled-back state must be persisted")
}

func TestTBMOrchestrator_Execute_StableOffsets_NoRollback(t *testing.T) {
	orchestrator, config, _ := newTestOrchestrator(t, StateUninitialized)

	err := orchestrator.Execute(context.Background(), realisticReconcileResult(), 10, 5*time.Millisecond, clusterlink.BasicAuth{})

	require.NoError(t, err)
	assert.Equal(t, StateSwitched, config.CurrentState)
}

// TestTBMOrchestrator_Bootstrap_ExpiresFenceVerificationOnResume and
// TestTBMOrchestrator_Bootstrap_ExpiresFencePostureOnResume previously pinned
// construction-time bootstrap demotions (expire_verification, expire_fence)
// that derived a safe resume point from config.CurrentState. That mechanism
// no longer exists — construction always starts the FSM at StateUninitialized
// (see TestNewTBMOrchestrator_AlwaysStartsUninitialized) — so these are
// deleted; their intent (a resume never trusts a stale fence/verification
// posture) is now covered, more strongly, by every from-zero Execute test in
// this file (e.g. TestTBMOrchestrator_Execute_FromZero_
// IgnoresPersistedCurrentState), which never special-cases a stale posture
// at all because the run never trusted it to begin with.

// ----- TBM kill-point matrix (Layer 1) -----
//
// The tests below cover every in-scope TBM row of the kill-point test matrix
// (Plan 2d, kill-point-test-matrix §2): construct, via the fakes plus a
// constructed *migplan.Result, the exact live state a kill at that row's
// point would leave, drive one from-zero Execute, and assert convergence to
// switched plus a second Execute (fed the Result a real migplan.Reconcile
// would return once nothing is left, killPointDoneResult) is a pure no-op —
// zero additional gateway patches, zero additional PromoteMirrorTopics
// calls. Mirrors internal/services/migration/orchestrator_test.go's AAO
// kill-point matrix section exactly in shape; TBM has no offset-sync, so
// there is no A-S1p equivalent to skip — only Layer-2 kill-injection is
// deferred (TestTBM_Layer2_KillInjection).
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
// realisticReconcileResult in gateway_test.go), used by every TestTBM_<row>
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
// TestTBM_<row> test — TBM's counterpart to
// internal/services/migration/orchestrator_test.go's
// newAAOKillPointOrchestrator. It lets a row seed which of allMirrorTopics'
// mirrors are ALREADY STOPPED before Execute ever runs — modelling exactly
// the live state a kill at a mid/late-promote point would leave — and
// records every gateway-route patch (in call order, as the full RoutePatch
// so a row can inspect its rules fragment) and every PromoteMirrorTopics
// call (topic list, in call order), so each row can assert on them directly,
// including that a second Execute adds none.
//
// initialCurrentState is written onto config.CurrentState purely as
// documentation of the persisted position a kill at this row's point would
// leave — construction ignores it (start-from-zero: see
// TestNewTBMOrchestrator_AlwaysStartsUninitialized and
// TestTBMOrchestrator_Execute_FromZero_IgnoresPersistedCurrentState above),
// so it has no effect on how Execute below behaves; it is here only so each
// row's harness call reads as "the state a kill would leave," matching the
// matrix.
//
// readyProgress, when non-empty, replaces the default (immediately
// succeeding, no-progress-reported) WaitForGatewayReady fake with one that
// reports each entry via onProgress, in order, before returning nil — the
// harness's fake pod-waiter for the *u rows (T-S1u/T-S4u), exactly mirroring
// newAAOKillPointOrchestrator's identical mechanism (see its doc comment for
// why this is the simplest fake that both lets the wait return and still
// proves the step observed an unconverged state before succeeding). Both
// Fence's and Switch's confirm step share this one mock (the default
// capability here is VerifyRollout with no configId, so VerifyTransition
// always falls back to WaitForGatewayReady), so a non-empty readyProgress is
// replayed on every call reaching it, fence's and switch's alike.
func newTBMKillPointOrchestrator(
	t *testing.T,
	initialCurrentState string,
	allMirrorTopics []string,
	initiallyStopped []string,
	readyProgress []gateway.GatewayReadinessProgress,
) (orch *TBMOrchestrator, config *migration.MigrationConfig, stateFilePath string, patchCalls *[]gateway.RoutePatch, promoteCalls *[][]string, readyEventsOut *[]gateway.GatewayReadinessProgress) {
	t.Helper()

	config = &migration.MigrationConfig{
		MigrationId:   "test-tbm-killpoint",
		CurrentState:  initialCurrentState,
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

	stateFilePath = filepath.Join(t.TempDir(), "tbm-state.json")
	migrationState := migration.NewMigrationState()

	orch = NewTBMOrchestrator(config, actions, migrationState, stateFilePath)

	return orch, config, stateFilePath, &patches, &promotes, &readyEvents
}

// loadPersistedTBMMigration loads and returns the persisted migration record
// for migrationId from stateFilePath, failing the test on any error.
func loadPersistedTBMMigration(t *testing.T, stateFilePath, migrationId string) *migration.MigrationConfig {
	t.Helper()
	loaded, err := migration.NewMigrationStateFromFile(stateFilePath)
	require.NoError(t, err)
	persisted, err := loaded.GetMigrationById(migrationId)
	require.NoError(t, err)
	return persisted
}

// TestTBM_S0_FreshFullRun covers matrix row T-S0: a pristine batch, never
// fenced, mirrors ACTIVE, every topic migratable. A from-zero walk must
// fence, promote both topics, and switch — and a second run, once reconcile
// reports nothing left, must be a pure no-op.
func TestTBM_S0_FreshFullRun(t *testing.T) {
	topics := []string{"t1.order", "t2.payment"}
	orch, config, stateFilePath, patchCalls, promoteCalls, _ := newTBMKillPointOrchestrator(t, StateUninitialized, topics, nil, nil)

	err := orch.Execute(context.Background(), killPointFullResult(topics), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, config.CurrentState)
	persisted := loadPersistedTBMMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateSwitched, persisted.CurrentState)

	assert.Len(t, *patchCalls, 2, "one fence apply and one switch apply")
	require.Len(t, *promoteCalls, 1, "both zero-lag topics promoted in one batch")
	assert.ElementsMatch(t, topics, (*promoteCalls)[0])

	// A second run: a fresh reconcile now reports nothing left at all. The
	// from-zero walk still visits every step, but every step's plan-driven
	// no-op guard must fire — zero new mutations.
	patchesBefore := len(*patchCalls)
	promotesBefore := len(*promoteCalls)

	err = orch.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, config.CurrentState)

	assert.Len(t, *patchCalls, patchesBefore, "a completed batch's re-run must apply no gateway patches")
	assert.Len(t, *promoteCalls, promotesBefore, "a completed batch's re-run must issue no promote calls")
}

// TestTBM_S1_AlreadyFencedNoReapply covers matrix row T-S1: the persisted
// CurrentState a kill right after fencing would leave, mirrors still ACTIVE
// (nothing was promoted by the prior, killed run). Construction ignores the
// stale state (see newTBMKillPointOrchestrator's doc comment), so the FSM
// still walks the whole workflow — there is no live read that could tell
// this run "the route is already fenced" apart from a fresh one, so Fence's
// apply is unconditionally re-issued every run. What this pins is that
// re-issuing that apply is safe: exactly one fence patch, never doubled
// (Task 2a's dynamic PrependFence idempotency), and promotion/switch proceed
// normally — landing at the same converged, idempotent-on-a-second-run place
// as a fresh batch.
func TestTBM_S1_AlreadyFencedNoReapply(t *testing.T) {
	topics := []string{"t1.order", "t2.payment"}
	orch, config, stateFilePath, patchCalls, promoteCalls, _ := newTBMKillPointOrchestrator(t, StateFenced, topics, nil, nil)

	err := orch.Execute(context.Background(), killPointFullResult(topics), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, config.CurrentState)
	persisted := loadPersistedTBMMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateSwitched, persisted.CurrentState)

	assert.Len(t, *patchCalls, 2,
		"one fence re-apply plus one switch apply — never doubled by resuming into a state that already says fenced")
	require.Len(t, *promoteCalls, 1)
	assert.ElementsMatch(t, topics, (*promoteCalls)[0])

	patchesBefore := len(*patchCalls)
	promotesBefore := len(*promoteCalls)
	err = orch.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Len(t, *patchCalls, patchesBefore)
	assert.Len(t, *promoteCalls, promotesBefore)
}

// TestTBM_S1u_WaitsForConvergence covers matrix row T-S1u: fenced, but the
// serving pods have not yet converged on the fenced config. There is no
// separate "poll until ready" loop in this test harness to hook — that loop
// lives entirely inside the production K8sService the gateway mock replaces
// — so "waits for convergence, then proceeds" is modelled as the mocked
// WaitForGatewayReady call itself reporting one not-yet-converged progress
// tick before its own converged return (see newTBMKillPointOrchestrator's
// doc comment on readyProgress). This is the simplest fake that both lets
// the wait return and still proves the step observed an unconverged state —
// not reported done while unconverged — before succeeding.
func TestTBM_S1u_WaitsForConvergence(t *testing.T) {
	notReady := gateway.GatewayReadinessProgress{RolloutDetected: true, InitialPodCount: 2, PodsReady: 0}
	converged := gateway.GatewayReadinessProgress{RolloutDetected: true, InitialPodCount: 2, PodsReady: 2}
	topics := []string{"t1.order", "t2.payment"}

	orch, config, stateFilePath, patchCalls, promoteCalls, readyEvents := newTBMKillPointOrchestrator(
		t, StateFenced, topics, nil, []gateway.GatewayReadinessProgress{notReady, converged})

	err := orch.Execute(context.Background(), killPointFullResult(topics), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err, "the fence step must succeed once convergence is reported, not error out on the interim tick")
	assert.Equal(t, StateSwitched, config.CurrentState)
	persisted := loadPersistedTBMMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateSwitched, persisted.CurrentState)

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
	err = orch.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Len(t, *patchCalls, patchesBefore)
	assert.Len(t, *promoteCalls, promotesBefore)
}

// TestTBM_S2_MidPromoteMix covers matrix row T-S2: a kill mid-promotion,
// where t1.order has already reached STOPPED and t2.payment is still ACTIVE.
// A real migplan.Reconcile excludes t1.order from the promote set entirely —
// the filtering is reconcile's job, done once, live — so the constructed
// Result here lists only t2.payment, exactly as the matrix row specifies
// ("result Topics = the not-yet-STOPPED only"). This must drive Promote to
// promote t2.payment alone, never re-issuing PromoteMirrorTopics for the
// already-STOPPED t1.order.
func TestTBM_S2_MidPromoteMix(t *testing.T) {
	allTopics := []string{"t1.order", "t2.payment"}
	orch, config, stateFilePath, patchCalls, promoteCalls, _ := newTBMKillPointOrchestrator(
		t, StatePromoted, allTopics, []string{"t1.order"}, nil)

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
	assert.Equal(t, StateSwitched, config.CurrentState)
	persisted := loadPersistedTBMMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateSwitched, persisted.CurrentState)

	require.Len(t, *promoteCalls, 1, "exactly one promote batch")
	assert.Equal(t, []string{"t2.payment"}, (*promoteCalls)[0],
		"only the not-yet-stopped topic is promoted — t1.order (already STOPPED) is never re-promoted")
	assert.Len(t, *patchCalls, 2, "fence + switch, gated by the still-nonempty (single-topic) plan")

	patchesBefore := len(*patchCalls)
	promotesBefore := len(*promoteCalls)
	err = orch.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Len(t, *patchCalls, patchesBefore)
	assert.Len(t, *promoteCalls, promotesBefore)
}

// TestTBM_S3_PromotedNotSwitched covers matrix row T-S3 — the bug-fix
// regression row: every mirror already STOPPED (nothing left to promote) but
// the switch not yet applied. A real migplan.Reconcile for this state
// returns Topics=[] but non-empty FenceYAML/SwitchoverYAML (reconcile's
// promote set excludes already-stopped topics, but its inflight set — which
// gates whether artifacts are built at all — still includes them, since the
// switch is still owed). Before Task 1's fix, Fence and Switch both no-op'd
// on the same len(config.Topics)==0 check Promote correctly uses, so they
// would have BOTH incorrectly no-op'd too, never applying the still-owed
// switch — a real correctness bug (a kill right after the last topic's
// promote completes would report the batch falsely complete without ever
// switching the gateway). This test pins the fix: Promote makes no call at
// all, but Switch still applies and the run still converges.
func TestTBM_S3_PromotedNotSwitched(t *testing.T) {
	allTopics := []string{"t1.order", "t2.payment"}
	orch, config, stateFilePath, patchCalls, promoteCalls, _ := newTBMKillPointOrchestrator(
		t, StatePromoted, allTopics, allTopics, nil)

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
	assert.Equal(t, StateSwitched, config.CurrentState)
	persisted := loadPersistedTBMMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateSwitched, persisted.CurrentState)

	assert.Empty(t, *promoteCalls, "nothing left to promote — Promote must make no call at all")
	assert.Len(t, *patchCalls, 2,
		"fence + switch must both still apply — a non-empty artifact is owed regardless of the empty promote set")

	patchesBefore := len(*patchCalls)
	promotesBefore := len(*promoteCalls)
	err = orch.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Len(t, *patchCalls, patchesBefore)
	assert.Len(t, *promoteCalls, promotesBefore)
}

// TestTBM_S4u_SwitchWaitsForConvergence covers matrix row T-S4u: the switch
// CR landed but the serving pods have not yet converged on it. Like T-S3,
// this needs Topics=[] (nothing left to promote) with a non-empty
// SwitchoverYAML for the switch to actually run — feasible only after the
// per-artifact no-op fix pinned by TestTBM_S3_PromotedNotSwitched. The
// convergence wait is faked the same way as TestTBM_S1u_WaitsForConvergence:
// the mocked WaitForGatewayReady call itself reports a not-yet-converged
// progress tick before its own converged return.
func TestTBM_S4u_SwitchWaitsForConvergence(t *testing.T) {
	notReady := gateway.GatewayReadinessProgress{RolloutDetected: true, InitialPodCount: 2, PodsReady: 0}
	converged := gateway.GatewayReadinessProgress{RolloutDetected: true, InitialPodCount: 2, PodsReady: 2}
	allTopics := []string{"t1.order", "t2.payment"}

	orch, config, stateFilePath, patchCalls, promoteCalls, readyEvents := newTBMKillPointOrchestrator(
		t, StateSwitched, allTopics, allTopics, []gateway.GatewayReadinessProgress{notReady, converged})

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
	assert.Equal(t, StateSwitched, config.CurrentState)
	persisted := loadPersistedTBMMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateSwitched, persisted.CurrentState)

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
	err = orch.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Len(t, *patchCalls, patchesBefore)
	assert.Len(t, *promoteCalls, promotesBefore)
}

// TestTBM_S4_DoneIsNoop covers matrix row T-S4 — the important one: it pins
// the deleted HasPendingWork short-circuit's replacement. Live state: fully
// switched already, batch fence gone, every mirror STOPPED. A real
// migplan.Reconcile classifies the batch Unchanged and returns a Result with
// no artifacts at all (killPointDoneResult): Topics, FenceYAML and
// SwitchoverYAML all empty. Every one of Fence/Promote/Switch's plan-driven
// no-op guards must fire — zero patches, zero promotes — and the FSM still
// walks through every step to switched rather than needing any special-cased
// short-circuit to get there.
func TestTBM_S4_DoneIsNoop(t *testing.T) {
	allTopics := []string{"t1.order", "t2.payment"}
	orch, config, stateFilePath, patchCalls, promoteCalls, _ := newTBMKillPointOrchestrator(
		t, StateSwitched, allTopics, allTopics, nil)

	err := orch.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, config.CurrentState)
	persisted := loadPersistedTBMMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateSwitched, persisted.CurrentState)

	assert.Empty(t, *patchCalls, "zero gateway patches — fence and switch must both no-op")
	assert.Empty(t, *promoteCalls, "zero promote calls")

	// A second Execute is byte-for-byte identical: still zero mutations.
	err = orch.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, config.CurrentState)
	assert.Empty(t, *patchCalls)
	assert.Empty(t, *promoteCalls)
}

// TestTBM_M_MultiBatchComposite covers matrix row T-M: a multi-batch
// composite where t0.legacy is a wholly separate, already-fully-migrated
// batch — Unchanged by reconcile (it never appears in Topics/FenceYAML/
// SwitchoverYAML at all, unlike T-S2's t1.order, which is still part of the
// same inflight plan), its mirror STOPPED from the very start of this run —
// while t1.order/t2.payment are a second, still-migratable batch in the same
// Result. This must converge the active batch to switched while never
// touching t0.legacy (no PromoteMirrorTopics call ever names it) and never
// doubling the fence/switch patch count for having two batches present.
func TestTBM_M_MultiBatchComposite(t *testing.T) {
	allTopics := []string{"t0.legacy", "t1.order", "t2.payment"}
	orch, config, stateFilePath, patchCalls, promoteCalls, _ := newTBMKillPointOrchestrator(
		t, StateUninitialized, allTopics, []string{"t0.legacy"}, nil)

	activeTopics := []string{"t1.order", "t2.payment"}
	res := killPointFullResult(activeTopics)

	err := orch.Execute(context.Background(), res, 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, config.CurrentState)
	persisted := loadPersistedTBMMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateSwitched, persisted.CurrentState)

	assert.Len(t, *patchCalls, 2, "one fence, one switch — never doubled for having two batches present")
	require.Len(t, *promoteCalls, 1, "the active batch promotes together, in one call")
	assert.ElementsMatch(t, activeTopics, (*promoteCalls)[0])
	for _, call := range *promoteCalls {
		assert.NotContains(t, call, "t0.legacy", "the completed batch's topic must never be re-promoted")
	}

	patchesBefore := len(*patchCalls)
	promotesBefore := len(*promoteCalls)
	err = orch.Execute(context.Background(), killPointDoneResult(), 10, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"})
	require.NoError(t, err)
	assert.Len(t, *patchCalls, patchesBefore)
	assert.Len(t, *promoteCalls, promotesBefore)
}

// TestTBM_Layer2_KillInjection would cover Layer 2 of the matrix — proving a
// real kill at point P (via a failure-injection harness) actually leaves
// live state S, rather than constructing S directly via fakes as every
// Layer-1 row above does. Deferred: the failure-injection harness itself is
// a later build-order plan; this task does Layer 1 only.
func TestTBM_Layer2_KillInjection(t *testing.T) {
	t.Skip("failure-injection harness is a later build-order plan")
}
