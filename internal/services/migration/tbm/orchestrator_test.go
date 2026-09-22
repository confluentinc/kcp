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
// construction read config.CurrentState (here, fenced) to decide where the
// FSM started, bootstrap-demoted via the also-removed expire_fence edge. The
// FSM now always starts at StateUninitialized regardless of
// config.CurrentState (see TestNewTBMOrchestrator_AlwaysStartsUninitialized),
// so a stale persisted "fenced" here changes nothing: Execute walks the whole
// canonical workflow from Initialize, harmlessly re-running WaitForLags and
// Fence for real — config.Topics is empty (an empty *migplan.Result is
// passed), and both have their own no-topics guard making them a no-op
// success.
func TestTBMOrchestrator_Execute_FromZero_IgnoresPersistedCurrentState(t *testing.T) {
	orchestrator, config, _ := newTestOrchestrator(t, StateFenced)

	require.NoError(t, orchestrator.Execute(context.Background(), &migplan.Result{}, 10, 0, clusterlink.BasicAuth{}))

	assert.Equal(t, StateSwitched, config.CurrentState,
		"a from-zero walk must reach switched regardless of the stale persisted state")
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
