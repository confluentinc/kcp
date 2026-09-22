package migration

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/looplab/fsm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// orchestratorOverrides allows tests to customize mock behavior before construction.
type orchestratorOverrides struct {
	getGatewayYAMLFn         func(ctx context.Context, namespace, name string) ([]byte, error)
	patchGatewayRouteFn      func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, configID string) (string, error)
	waitForGatewayReadyFn    func(ctx context.Context, namespace, name string, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error
	waitForGatewayAcceptedFn func(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration) error
	promoteMirrorTopicsFn    func(ctx context.Context, config clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error)
}

// newHappyPathOrchestrator builds an orchestrator where every workflow step
// succeeds, then positions its FSM directly at initialState via fsm.SetState
// — a test-only bypass (no callbacks fire) so a test that exercises a single
// step deep in the workflow does not have to first walk every earlier one to
// get there. NewMigrationOrchestrator itself always starts the FSM at
// StateUninitialized (there is no config field to disagree with it) — this
// positioning is unrelated to that and never happens in production.
func newHappyPathOrchestrator(t *testing.T, initialState string, topics []string, overrides ...orchestratorOverrides) (*MigrationOrchestrator, *MigrationConfig) {
	t.Helper()
	orch, config := buildHappyPathOrchestrator(t, initialState, topics, overrides...)
	if initialState != StateUninitialized {
		orch.fsm.SetState(initialState)
	}
	return orch, config
}

// buildHappyPathOrchestrator is the shared construction logic behind
// newHappyPathOrchestrator.
func buildHappyPathOrchestrator(t *testing.T, initialState string, topics []string, overrides ...orchestratorOverrides) (*MigrationOrchestrator, *MigrationConfig) {
	t.Helper()

	if len(topics) == 0 {
		topics = []string{"topic-a", "topic-b"}
	}

	config := &MigrationConfig{
		MigrationId:         "test-migration-1",
		KubeConfigPath:      "/fake/kubeconfig",
		SourceBootstrap:     "source:9092",
		ClusterBootstrap:    "dest:9092",
		ClusterId:           "lkc-test",
		ClusterRestEndpoint: "https://pkc-test.confluent.cloud",
		ClusterLinkName:     "test-link",
		Topics:              topics,
		InitialCrName:       "my-gateway",
		K8sNamespace:        "confluent",
		GatewayYAML:         testInitialCR,
		Route:               "migration-route",
		Mode:                "static",
		FenceYAML:           testFenceYAML,
		SwitchoverYAML:      testSwitchoverYAML,
	}

	// Default mock implementations. The gateway CR must be a real routed gateway
	// CR: FenceGateway derives its fence RoutePatch from it (deriveFenceRoutePatch
	// splices FenceYAML onto Route), so a scalar placeholder would fail to parse.
	getGatewayYAMLFn := func(ctx context.Context, namespace, name string) ([]byte, error) {
		return []byte(testInitialCR), nil
	}
	patchGatewayRouteFn := func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, configID string) (string, error) {
		return configID, nil
	}

	// Track promoted topics so ListMirrorTopics can model the realistic
	// ACTIVE -> STOPPED transition that PromoteTopics polls for: a topic is
	// ACTIVE until its promote request is accepted, then STOPPED once the
	// (mock) backend has processed it.
	var mirrorMu sync.Mutex
	promotedTopics := make(map[string]bool)

	promoteMirrorTopicsFn := func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
		data := make([]struct {
			MirrorTopicName string `json:"mirror_topic_name"`
			ErrorMessage    string `json:"error_message,omitempty"`
			ErrorCode       int    `json:"error_code,omitempty"`
		}, len(topicNames))
		mirrorMu.Lock()
		for i, name := range topicNames {
			data[i].MirrorTopicName = name
			promotedTopics[name] = true
		}
		mirrorMu.Unlock()
		return &clusterlink.PromoteMirrorTopicsResponse{Data: data}, nil
	}

	// Default nil → mock returns success
	var waitForGatewayReadyFn func(ctx context.Context, namespace, name string, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error
	// Default nil → mock treats the spec as accepted by the operator
	var waitForGatewayAcceptedFn func(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration) error

	// Apply overrides if provided
	if len(overrides) > 0 {
		o := overrides[0]
		if o.getGatewayYAMLFn != nil {
			getGatewayYAMLFn = o.getGatewayYAMLFn
		}
		if o.patchGatewayRouteFn != nil {
			patchGatewayRouteFn = o.patchGatewayRouteFn
		}
		if o.waitForGatewayReadyFn != nil {
			waitForGatewayReadyFn = o.waitForGatewayReadyFn
		}
		if o.waitForGatewayAcceptedFn != nil {
			waitForGatewayAcceptedFn = o.waitForGatewayAcceptedFn
		}
		if o.promoteMirrorTopicsFn != nil {
			promoteMirrorTopicsFn = o.promoteMirrorTopicsFn
		}
	}

	gw := &mockGatewayService{
		getGatewayYAMLFn: getGatewayYAMLFn,
		// checkRedundantAuthStagedFn left unset: the mock's default passes validation.
		patchGatewayRouteFn: patchGatewayRouteFn,
		getGatewayPodUIDsFn: func(ctx context.Context, namespace, name string) (map[k8stypes.UID]struct{}, error) {
			return map[k8stypes.UID]struct{}{
				"uid-1": {},
				"uid-2": {},
			}, nil
		},
		waitForGatewayPodsFn: func(ctx context.Context, namespace, name string, initialPodUIDs map[k8stypes.UID]struct{}, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.PodRolloutProgress)) error {
			return nil
		},
		waitForGatewayReadyFn:    waitForGatewayReadyFn,
		waitForGatewayAcceptedFn: waitForGatewayAcceptedFn,
	}

	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			mirrorMu.Lock()
			defer mirrorMu.Unlock()
			out := make([]clusterlink.MirrorTopic, len(topics))
			for i, name := range topics {
				status := clusterlink.MirrorStatusActive
				if promotedTopics[name] {
					status = clusterlink.MirrorStatusStopped
				}
				out[i] = clusterlink.MirrorTopic{
					MirrorTopicName: name,
					MirrorStatus:    status,
				}
			}
			return out, nil
		},
		listConfigsFn: func(ctx context.Context, cfg clusterlink.Config) (map[string]string, error) {
			return map[string]string{"consumer.offset.sync.enable": "true"}, nil
		},
		validateTopicsFn: func(reqTopics []string, clusterLinkTopics []string) error {
			return nil
		},
		promoteMirrorTopicsFn: promoteMirrorTopicsFn,
	}

	// Identical offsets => zero lag
	zeroLagOffsets := map[int32]int64{0: 100, 1: 200}
	srcOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return zeroLagOffsets, nil
		},
	}
	dstOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return zeroLagOffsets, nil
		},
	}

	actions := NewMigrationActionsWithOffsets(gw, cl, srcOffset, dstOffset)
	actions.lagPollInterval = time.Millisecond
	actions.promotePollInterval = time.Millisecond

	orch := NewMigrationOrchestrator(config, actions)

	return orch, config
}

// uninitializedReconcileResult builds the migplan.Result an orchestrator test
// starting at StateUninitialized must pass to Initialize/Execute (onInitialize
// dereferences res.Refused, so a nil res panics). topics mirrors
// newHappyPathOrchestrator's own default-resolution — nil resolves to the same
// two-topic default — so a test that drives a full run through PromoteTopics
// sees ListMirrorTopics (built from the same resolved topic set at
// construction) agree with config.Topics after Initialize overwrites it from
// res.Topics.
func uninitializedReconcileResult(topics []string) *migplan.Result {
	if len(topics) == 0 {
		topics = []string{"topic-a", "topic-b"}
	}
	return &migplan.Result{
		Route:          "migration-route",
		Topics:         topics,
		FenceYAML:      testFenceYAML,
		SwitchoverYAML: testSwitchoverYAML,
		GatewayYAML:    testInitialCR,
		Mode:           "static",
	}
}

// --- FSM transition tests ---

func TestOrchestrator_Execute_FullWorkflow(t *testing.T) {
	orch, _ := newHappyPathOrchestrator(t, StateUninitialized, nil)

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, uninitializedReconcileResult(nil))
	require.NoError(t, err)

	assert.Equal(t, StateSwitched, orch.fsm.Current())
}

// --- Error handling tests ---

func TestOrchestrator_Execute_FenceError(t *testing.T) {
	overrides := orchestratorOverrides{
		patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, _ string) (string, error) {
			return "", fmt.Errorf("apply gateway failed: forbidden")
		},
	}

	orch, _ := newHappyPathOrchestrator(t, StateUninitialized, nil, overrides)

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, uninitializedReconcileResult(nil))
	require.Error(t, err)

	// Init (uninitialized -> initialized) succeeded. CheckLags (initialized ->
	// lags_ok) succeeded. Fence (lags_ok -> fenced) failed, so the FSM should
	// rest at lags_ok.
	assert.Equal(t, StateLagsOk, orch.fsm.Current())
}

func TestOrchestrator_Execute_UnroutedProducers_AbortsFenceAndRollsBack(t *testing.T) {
	// Simulate a rogue producer: source offsets keep increasing on every call.
	var sourceCallCount int64
	var promoteCallCount int64
	var mu sync.Mutex
	var appliedPatches []gateway.RoutePatch

	overrides := orchestratorOverrides{
		patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, _ string) (string, error) {
			// Record every applied patch so the unfence (a whole-route replace,
			// distinct from the fence/switchover field-set patches) can be
			// asserted below.
			mu.Lock()
			appliedPatches = append(appliedPatches, rp)
			mu.Unlock()
			return "", nil
		},
	}

	orch, config := newHappyPathOrchestrator(t, StateFenced, nil, overrides)

	// Enable unrouted producer detection
	config.DetectUnroutedProducersDuration = time.Millisecond
	// Set valid YAML for GatewayYAML so unfenceGateway can parse it
	config.GatewayYAML = "apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\nspec:\n  routes:\n    - name: migration-route\n      endpoint: gateway:9595\n"

	// Override source offset provider to return increasing offsets (simulating rogue)
	orch.actions.sourceOffset = &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt64(&sourceCallCount, 1)
			return map[int32]int64{0: 100 + n*10}, nil
		},
	}

	// Track promote calls to verify it only runs once (not twice from duplicate callback)
	originalPromote := orch.actions.clusterLinkService
	orch.actions.clusterLinkService = &mockClusterLinkService{
		promoteMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			atomic.AddInt64(&promoteCallCount, 1)
			return originalPromote.PromoteMirrorTopics(ctx, cfg, topicNames)
		},
	}

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)

	// FSM state should have rolled back to initialized via abort_fence
	assert.Equal(t, StateInitialized, orch.fsm.Current(),
		"FSM state should be rolled back to initialized after unrouted producer detection")

	// State file should be persisted with initialized
	assert.Equal(t, StateInitialized, orch.fsm.Current(),
		"persisted state should be initialized after abort_fence transition")

	// Gateway should have been unfenced: the rollback patches the route back to
	// its captured whole-route state (Field == ""), distinct from the
	// fence/switchover field-set patches.
	mu.Lock()
	unfenced := false
	for _, rp := range appliedPatches {
		if rp.Field == "" {
			unfenced = true
		}
	}
	mu.Unlock()
	assert.True(t, unfenced, "gateway should be unfenced after detecting unrouted producers")

	// PromoteTopics should NOT have been called (detection aborts before promotion)
	assert.Equal(t, int64(0), atomic.LoadInt64(&promoteCallCount),
		"PromoteMirrorTopics should not be called when unrouted producers are detected")
}

func TestOrchestrator_Execute_UnroutedProducers_UnfenceFails_StaysAtOffsetSyncPaused(t *testing.T) {
	var applyCallCount int64

	overrides := orchestratorOverrides{
		patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, _ string) (string, error) {
			n := atomic.AddInt64(&applyCallCount, 1)
			if n == 1 {
				// First apply is the fence — succeed
				return "", nil
			}
			// Second apply is the unfence — fail
			return "", fmt.Errorf("k8s API unavailable")
		},
	}

	orch, config := newHappyPathOrchestrator(t, StateLagsOk, nil, overrides)

	// Enable unrouted producer detection
	config.DetectUnroutedProducersDuration = time.Millisecond
	config.GatewayYAML = "apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\nspec:\n  routes:\n    - name: migration-route\n      endpoint: gateway:9595\n"

	// Override source offset provider to return increasing offsets
	orch.actions.sourceOffset = &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt64(&applyCallCount, 1)
			return map[int32]int64{0: 100 + n*10}, nil
		},
	}

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.Error(t, err)
	// Unrouted producers were detected, so the surfaced error still wraps
	// ErrUnroutedProducers; the unfence happens on the abort_fence rollback and
	// its failure is logged. The safety-critical invariant is the state below.
	assert.ErrorIs(t, err, ErrUnroutedProducers)

	// State must remain at the rollback's source: the abort_fence transition is
	// cancelled when unfenceGateway fails, so it is never persisted as
	// initialized. Detection fails at offset_sync_paused (the pause stage sits
	// between fence and verify), so that is where the FSM honestly rests.
	assert.Equal(t, StateOffsetSyncPaused, orch.fsm.Current(),
		"state should remain at offset_sync_paused when unfencing fails")
}

func TestOrchestrator_Execute_UnroutedProducers_UnfenceReadinessFails_StaysAtOffsetSyncPaused(t *testing.T) {
	// The unfence CR applies cleanly but the gateway never converges to Ready.
	// The abort_fence rollback must be cancelled — persisting initialized while
	// the gateway is still mid-rollout would misrepresent reality.
	var waitCallCount int64
	var sourceCallCount int64

	overrides := orchestratorOverrides{
		// With detection enabled the fence rollout waits via WaitForGatewayPods
		// (the builder's default, which succeeds), so WaitForGatewayReady is
		// reached only by the unfence rollout — fail it to exercise the
		// "unfence never converges" path.
		waitForGatewayReadyFn: func(ctx context.Context, namespace, name string, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
			atomic.AddInt64(&waitCallCount, 1)
			return fmt.Errorf("gateway pods did not converge")
		},
	}

	orch, config := newHappyPathOrchestrator(t, StateLagsOk, nil, overrides)

	// Enable unrouted producer detection
	config.DetectUnroutedProducersDuration = time.Millisecond
	config.GatewayYAML = "apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\nspec:\n  routes:\n    - name: migration-route\n      endpoint: gateway:9595\n"

	// Override source offset provider to return increasing offsets
	orch.actions.sourceOffset = &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt64(&sourceCallCount, 1)
			return map[int32]int64{0: 100 + n*10}, nil
		},
	}

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)

	assert.Equal(t, int64(1), atomic.LoadInt64(&waitCallCount),
		"the unfence rollout readiness should be awaited exactly once (the fence rollout waits via WaitForGatewayPods when detection is enabled)")

	assert.Equal(t, StateOffsetSyncPaused, orch.fsm.Current(),
		"state should remain at offset_sync_paused when the unfence rollout never becomes ready")
}

func TestOrchestrator_Execute_VerifyFencePersistedBeforePromote(t *testing.T) {
	// A successful unrouted-producer check is its own FSM transition
	// (offset_sync_paused → fence_verified), persisted before promotion starts
	// like every other step. The persisted value is informational — bootstrap
	// demotes it back through fenced to lags_ok on the next run (see
	// TestOrchestrator_Bootstrap_DemotesFenceVerified) — but it must still
	// record the FSM's true mid-run position, never promoted.
	overrides := orchestratorOverrides{
		promoteMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			return nil, fmt.Errorf("confluent cloud API unavailable")
		},
	}

	orch, config := newHappyPathOrchestrator(t, StateFenced, nil, overrides)
	config.DetectUnroutedProducersDuration = time.Millisecond

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.Error(t, err)

	// The verify step succeeded (stable offsets); the promote transition was
	// cancelled, so fence_verified is the last good state.
	assert.Equal(t, StateFenceVerified, orch.fsm.Current())
}

// TestOrchestrator_Bootstrap_DemotesFenceVerified,
// TestOrchestrator_Bootstrap_DemotesFencedFamily, and
// TestOrchestrator_ExpireVerificationIsAnFSMEdge previously pinned
// construction-time bootstrap demotions (expire_verification,
// expire_fence) that derived a safe resume point from orch.fsm.Current().
// That mechanism no longer exists — construction always starts the FSM at
// StateUninitialized (see TestNewMigrationOrchestrator_
// AlwaysStartsUninitialized) — so these are deleted; their intent (a resume
// never trusts a stale fence/verification posture) is now covered, more
// strongly, by TestOrchestrator_Execute_FromZero_IgnoresPersistedCurrentState.

func TestOrchestrator_PauseStageIsAnFSMEdge(t *testing.T) {
	// The offset-sync pause is a first-class stage between fenced and
	// verification: pause_offset_sync enters it, verify_fence now leaves it,
	// and the abort_fence rollback covers it (rogue detection fires there).
	orch, _ := newHappyPathOrchestrator(t, StateUninitialized, nil)

	viz := fsm.Visualize(orch.fsm)
	assert.Contains(t, viz,
		`"fenced" -> "offset_sync_paused" [ label = "pause_offset_sync" ];`,
		"pause_offset_sync should be a visible edge in the state machine")
	assert.Contains(t, viz,
		`"offset_sync_paused" -> "fence_verified" [ label = "verify_fence" ];`,
		"verify_fence should leave offset_sync_paused, not fenced")
	assert.Contains(t, viz,
		`"offset_sync_paused" -> "initialized" [ label = "abort_fence" ];`,
		"abort_fence should cover offset_sync_paused, where rogue detection now fails")
}

// TestOrchestrator_Execute_ResumeFromOffsetSyncPaused_RerunsDetection and
// TestOrchestrator_Execute_ResumeFromFencedFamily_ReassertsFence previously
// exercised the removed bootstrap expire_* demotions through a full Execute
// run. Deleted — TestOrchestrator_Execute_FromZero_IgnoresPersistedCurrentState
// covers the same intent (detection and the fence never survive a stale
// persisted position) via the new from-zero contract, for every starting
// state in one pass.

func TestOrchestrator_Execute_PauseOffsetSync_FiresAfterFenceBeforeDetection(t *testing.T) {
	// AE1: with the opt-in, the disable AlterConfigs fires after the fence
	// transition completes and before the first detection snapshot — never
	// earlier (that would stretch the stale-offset window across the run).
	var mu sync.Mutex
	var order []string
	record := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, event)
	}

	overrides := orchestratorOverrides{
		patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, _ string) (string, error) {
			record("apply")
			return "", nil
		},
	}

	orch, config := newHappyPathOrchestrator(t, StateInitialized, nil, overrides)
	config.PauseConsumerOffsetSync = true
	config.DetectUnroutedProducersDuration = time.Millisecond

	// Wrap the cluster-link service to record AlterConfigs, keeping the happy
	// promote behavior from the default mock.
	originalCL := orch.actions.clusterLinkService
	orch.actions.clusterLinkService = &mockClusterLinkService{
		listMirrorTopicsFn: originalCL.ListMirrorTopics,
		listConfigsFn: func(ctx context.Context, cfg clusterlink.Config) (map[string]string, error) {
			return map[string]string{"consumer.offset.sync.enable": "true"}, nil
		},
		alterConfigsFn: func(ctx context.Context, cfg clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			record("alter")
			return nil
		},
		promoteMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			return originalCL.PromoteMirrorTopics(ctx, cfg, topicNames)
		},
	}

	zeroLagOffsets := map[int32]int64{0: 100, 1: 200}
	orch.actions.sourceOffset = &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			record("source-get")
			return zeroLagOffsets, nil
		},
	}

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	firstApply := slices.Index(order, "apply")
	firstAlter := slices.Index(order, "alter")
	require.NotEqual(t, -1, firstApply, "fence apply must have happened")
	require.NotEqual(t, -1, firstAlter, "the disable AlterConfigs must have happened")
	// CheckLags also reads source offsets pre-fence; the detection snapshot is
	// the first source read AFTER the fence apply.
	firstDetectionGet := -1
	for i := firstApply + 1; i < len(order); i++ {
		if order[i] == "source-get" {
			firstDetectionGet = i
			break
		}
	}
	require.NotEqual(t, -1, firstDetectionGet, "detection snapshots must have happened after the fence")
	assert.Less(t, firstApply, firstAlter, "pause must fire after the fence apply")
	assert.Less(t, firstAlter, firstDetectionGet, "pause must fire before the first detection snapshot")

	assert.Equal(t, StateSwitched, orch.fsm.Current())
}

func TestOrchestrator_Execute_PauseError_RollsBackToInitialized(t *testing.T) {
	// AE3: a pause failure must not hold clients fenced. The abort_fence
	// rollback unfences the gateway (with readiness wait) and lands at
	// initialized; the original pause error still surfaces. The rollback's
	// sync restore is now unconditional on PauseConsumerOffsetSync (idempotent
	// apply, not gated on whether the pause actually landed), so it still
	// attempts its own AlterConfigs even though the pause never flipped
	// anything — the mock's unconditional AlterConfigs failure makes that
	// attempt fail too, soft-failing without changing the surfaced error.
	//
	// This test is also the reentrancy pin: the rollback must fire from
	// handleStepFailure after the pause step's Event call returned — a
	// regression that fires it inside a callback deadlocks on looplab's
	// eventMu and hangs this test visibly.
	var applyCalls, waitCalls, alterCalls int64

	overrides := orchestratorOverrides{
		patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, _ string) (string, error) {
			atomic.AddInt64(&applyCalls, 1)
			return "", nil
		},
		waitForGatewayReadyFn: func(ctx context.Context, namespace, name string, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
			atomic.AddInt64(&waitCalls, 1)
			return nil
		},
	}

	orch, config := newHappyPathOrchestrator(t, StateInitialized, nil, overrides)
	config.PauseConsumerOffsetSync = true
	config.GatewayYAML = "apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\nspec:\n  routes:\n    - name: migration-route\n      endpoint: gateway:9595\n"

	orch.actions.clusterLinkService = &mockClusterLinkService{
		listConfigsFn: func(ctx context.Context, cfg clusterlink.Config) (map[string]string, error) {
			return map[string]string{"consumer.offset.sync.enable": "true"}, nil
		},
		alterConfigsFn: func(ctx context.Context, cfg clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			atomic.AddInt64(&alterCalls, 1)
			return fmt.Errorf("503 pause boom")
		},
	}

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503 pause boom", "the original pause error must surface")

	assert.Equal(t, StateInitialized, orch.fsm.Current(),
		"pause failure must roll back to initialized, not hold clients fenced")

	assert.Equal(t, int64(2), atomic.LoadInt64(&applyCalls),
		"fence apply then unfence apply")
	assert.Equal(t, int64(2), atomic.LoadInt64(&waitCalls),
		"gateway readiness awaited for both the fence and the unfence")
	assert.Equal(t, int64(2), atomic.LoadInt64(&alterCalls),
		"the failed disable attempt, plus the rollback's unconditional idempotent restore attempt")
}

// TestOrchestrator_Execute_UnconfirmedFence_RestoresInitialCR covers the state
// that per-pod verification made reachable for the first time: the fenced CR is
// live in the cluster but was never confirmed on the serving pods. Leaving it
// there means either traffic blocked on part of the fleet, or a promotion still
// in flight that lands after kcp exits. Either way kcp must put back what it
// changed.
func TestOrchestrator_Execute_UnconfirmedFence_RestoresInitialCR(t *testing.T) {
	var appliedPatches []gateway.RoutePatch
	var mu sync.Mutex
	var readyCalls int64

	overrides := orchestratorOverrides{
		patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, _ string) (string, error) {
			mu.Lock()
			appliedPatches = append(appliedPatches, rp)
			mu.Unlock()
			return "", nil
		},
		waitForGatewayReadyFn: func(ctx context.Context, namespace, name string, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
			// First call is the fence's own verification — fail it. The second is
			// the restore's, which must succeed for the compensation to complete.
			if atomic.AddInt64(&readyCalls, 1) == 1 {
				return fmt.Errorf("gateway pods did not converge")
			}
			return nil
		},
	}

	orch, config := newHappyPathOrchestrator(t, StateLagsOk, nil, overrides)

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFenceUnconfirmed)
	assert.Contains(t, err.Error(), "gateway pods did not converge",
		"the original cause must survive the compensation")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, appliedPatches, 2, "the fence apply must be followed by a restoring apply")
	wantFenceRP, err := deriveFenceRoutePatch(config)
	require.NoError(t, err)
	assert.Equal(t, wantFenceRP, appliedPatches[0])
	assert.Equal(t, "", appliedPatches[1].Field,
		"the second apply must be the whole-route restore, not the fence field-set")
	assert.NotEqual(t, wantFenceRP, appliedPatches[1])

	// The fence transition was cancelled, so the machine never left lags_ok —
	// which is already the truth once the fence has been undone.
	assert.Equal(t, StateLagsOk, orch.fsm.Current())
}

func TestOrchestrator_Execute_UnconfirmedFence_RestoreFails_ReportsBoth(t *testing.T) {
	// The worst case: the fence is unconfirmed AND cannot be undone. Silence here
	// would strand an operator with a gateway that may be fencing traffic, so the
	// surfaced error has to name both halves.
	var applyCalls int64

	overrides := orchestratorOverrides{
		patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, _ string) (string, error) {
			if atomic.AddInt64(&applyCalls, 1) == 2 {
				return "", fmt.Errorf("k8s API unavailable")
			}
			return "", nil
		},
		waitForGatewayReadyFn: func(ctx context.Context, namespace, name string, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
			return fmt.Errorf("gateway pods did not converge")
		},
	}

	orch, _ := newHappyPathOrchestrator(t, StateLagsOk, nil, overrides)

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFenceUnconfirmed)
	assert.Contains(t, err.Error(), "gateway pods did not converge", "the fence failure")
	assert.Contains(t, err.Error(), "k8s API unavailable", "the restore failure")
	assert.Contains(t, err.Error(), "inspect it before re-running")

	assert.Equal(t, int64(2), atomic.LoadInt64(&applyCalls), "the restore must have been attempted")

	assert.Equal(t, StateLagsOk, orch.fsm.Current())
}

func TestOrchestrator_Execute_FenceApplyFails_DoesNotRestore(t *testing.T) {
	// The fenced CR never reached the cluster, so there is nothing to undo and
	// kcp must not write to a gateway it did not change.
	var applyCalls int64

	overrides := orchestratorOverrides{
		patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, _ string) (string, error) {
			atomic.AddInt64(&applyCalls, 1)
			return "", fmt.Errorf("admission webhook denied the request")
		},
	}

	orch, _ := newHappyPathOrchestrator(t, StateLagsOk, nil, overrides)

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrFenceUnconfirmed)
	assert.Equal(t, int64(1), atomic.LoadInt64(&applyCalls),
		"a failed fence apply must not be followed by a restoring apply")
}

func TestOrchestrator_Execute_RejectedFence_RestoresWithRejectionMessage(t *testing.T) {
	// A definite rejection (CFK explicitly refused the fenced spec) must not be
	// dressed up as the ambiguous "could not be confirmed on every gateway pod"
	// timeout wording: the operator needs to know the fence never took effect and
	// see CFK's own reason, not be pointed at a partial-application hunt.
	rejection := &gateway.GatewayRejectedError{
		Gateway:            "my-gateway",
		ConditionType:      "platform.confluent.io/cluster-ready",
		Reason:             "ApplyFailed",
		Message:            "secretRef kcp-plain not found",
		Generation:         5,
		ObservedGeneration: 4,
	}

	var appliedPatches []gateway.RoutePatch
	var mu sync.Mutex
	var acceptCalls int64
	overrides := orchestratorOverrides{
		patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, _ string) (string, error) {
			mu.Lock()
			appliedPatches = append(appliedPatches, rp)
			mu.Unlock()
			return "", nil
		},
		waitForGatewayAcceptedFn: func(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration) error {
			// Reject the fence's acceptance wait; let the restore's own wait pass.
			if atomic.AddInt64(&acceptCalls, 1) == 1 {
				return rejection
			}
			return nil
		},
	}

	// Build inside the capture: the reporter binds os.Stdout/os.Stderr at
	// construction, so the orchestrator must be created after the pipes are swapped.
	var err error
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			orch, _ := newHappyPathOrchestrator(t, StateLagsOk, nil, overrides)
			err = orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
		})
	})
	out := stdout + stderr

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFenceUnconfirmed)
	var rejected *gateway.GatewayRejectedError
	assert.ErrorAs(t, err, &rejected, "the operator's rejection must survive in the error chain")

	// The restore still runs — the refused spec is live in etcd and would fence if
	// the objection later clears — but the message must be the definite-rejection one.
	assert.Contains(t, out, "rejected", "must say the operator rejected the spec")
	assert.Contains(t, out, "ApplyFailed", "must surface the operator's own reason")
	assert.NotContains(t, out, "could not be confirmed on every gateway pod",
		"the ambiguous-timeout wording must not be used for a definite rejection")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, appliedPatches, 2, "the rejected fence must still be followed by a restoring apply")
}

func TestOrchestrator_Execute_PauseError_UnfenceFails_StaysAtFenced(t *testing.T) {
	// AE4: the pause failed and the unfence also fails. The rollback cancels:
	// state stays fenced (memory and disk, honestly reflecting the gateway),
	// the pause error surfaces, and a re-run simply retries the pause.
	var applyCalls int64

	overrides := orchestratorOverrides{
		patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, _ string) (string, error) {
			n := atomic.AddInt64(&applyCalls, 1)
			if n == 2 {
				return "", fmt.Errorf("k8s API unavailable") // the unfence attempt
			}
			return "", nil // fence (run 1), and the re-run's fence/switch applies
		},
	}

	orch, config := newHappyPathOrchestrator(t, StateInitialized, nil, overrides)
	config.PauseConsumerOffsetSync = true
	config.GatewayYAML = "apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\nspec:\n  routes:\n    - name: migration-route\n      endpoint: gateway:9595\n"

	var alterFail int32 = 1
	originalCL := orch.actions.clusterLinkService
	orch.actions.clusterLinkService = &mockClusterLinkService{
		listMirrorTopicsFn: originalCL.ListMirrorTopics,
		listConfigsFn: func(ctx context.Context, cfg clusterlink.Config) (map[string]string, error) {
			return map[string]string{"consumer.offset.sync.enable": "true"}, nil
		},
		alterConfigsFn: func(ctx context.Context, cfg clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			if atomic.LoadInt32(&alterFail) == 1 {
				return fmt.Errorf("503 pause boom")
			}
			return nil
		},
		promoteMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			return originalCL.PromoteMirrorTopics(ctx, cfg, topicNames)
		},
	}

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503 pause boom")

	assert.Equal(t, StateFenced, orch.fsm.Current(),
		"a cancelled rollback must leave the persisted state at fenced")
	assert.Equal(t, int64(2), atomic.LoadInt64(&applyCalls),
		"the unfence must have been attempted")

	// Re-run recovery: the transient pause failure is gone; execute resumes
	// from fenced, pauses, and completes — no pending-rollback bookkeeping.
	atomic.StoreInt32(&alterFail, 0)
	err = orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.NoError(t, err, "a re-run after a failed rollback must retry the pause and proceed")
	assert.Equal(t, StateSwitched, orch.fsm.Current())
}

func TestOrchestrator_Execute_PauseError_CtxCancelledMidUnfence_NoRestore(t *testing.T) {
	// Abuse case: the context is cancelled while the rollback's unfence is in
	// flight. The rollback cancels, state stays fenced, and the restore is
	// never attempted against a gateway in an unknown rollout state.
	var applyCalls, alterCalls int64
	ctx, cancel := context.WithCancel(context.Background())

	overrides := orchestratorOverrides{
		patchGatewayRouteFn: func(c context.Context, namespace, name string, rp gateway.RoutePatch, _ string) (string, error) {
			n := atomic.AddInt64(&applyCalls, 1)
			if n == 1 {
				return "", nil // fence
			}
			cancel() // ctx dies mid-unfence
			return "", c.Err()
		},
	}

	orch, config := newHappyPathOrchestrator(t, StateInitialized, nil, overrides)
	config.PauseConsumerOffsetSync = true
	config.GatewayYAML = "apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\nspec:\n  routes:\n    - name: migration-route\n      endpoint: gateway:9595\n"

	orch.actions.clusterLinkService = &mockClusterLinkService{
		listConfigsFn: func(c context.Context, cfg clusterlink.Config) (map[string]string, error) {
			return map[string]string{"consumer.offset.sync.enable": "true"}, nil
		},
		alterConfigsFn: func(c context.Context, cfg clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			atomic.AddInt64(&alterCalls, 1)
			return fmt.Errorf("503 pause boom")
		},
	}

	err := orch.Execute(ctx, 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503 pause boom", "the original pause error must surface")

	assert.Equal(t, StateFenced, orch.fsm.Current())
	assert.Equal(t, int64(2), atomic.LoadInt64(&applyCalls), "the unfence must have been attempted")
	assert.Equal(t, int64(1), atomic.LoadInt64(&alterCalls),
		"no restore attempt after a cancelled unfence — only the failed disable")
}

func TestOrchestrator_Execute_RogueAfterPause_RestoresSyncConfig(t *testing.T) {
	// AE5: the pause succeeded, then verification detects rogue producers.
	// The rollback must restore the flipped sync config — leaving it paused
	// while clients resume on the source would stall destination offsets
	// indefinitely. Restore runs only after the unfence readiness confirms.
	var mu sync.Mutex
	var order []string
	record := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, event)
	}

	overrides := orchestratorOverrides{
		patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, _ string) (string, error) {
			record("apply")
			return "", nil
		},
		waitForGatewayReadyFn: func(ctx context.Context, namespace, name string, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
			record("wait-ready")
			return nil
		},
	}

	orch, config := newHappyPathOrchestrator(t, StateLagsOk, nil, overrides)
	config.PauseConsumerOffsetSync = true
	config.DetectUnroutedProducersDuration = time.Millisecond
	config.GatewayYAML = "apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\nspec:\n  routes:\n    - name: migration-route\n      endpoint: gateway:9595\n"

	var listCalls int64
	orch.actions.clusterLinkService = &mockClusterLinkService{
		listConfigsFn: func(ctx context.Context, cfg clusterlink.Config) (map[string]string, error) {
			if atomic.AddInt64(&listCalls, 1) == 1 {
				// Drift check before the disable: sync still enabled.
				return map[string]string{"consumer.offset.sync.enable": "true"}, nil
			}
			// Restore diff after the disable: sync is paused.
			return map[string]string{"consumer.offset.sync.enable": "false"}, nil
		},
		alterConfigsFn: func(ctx context.Context, cfg clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			for _, a := range alts {
				record("alter:" + a.Name + "=" + a.Value)
			}
			return nil
		},
	}

	// Rogue producer: source offsets keep increasing.
	var sourceCalls int64
	orch.actions.sourceOffset = &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt64(&sourceCalls, 1)
			return map[int32]int64{0: 100 + n*10}, nil
		},
	}

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)

	assert.Equal(t, StateInitialized, orch.fsm.Current())

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, order, "alter:consumer.offset.sync.enable=false", "the pause disabled sync")
	restoreIdx := slices.Index(order, "alter:consumer.offset.sync.enable=true")
	require.NotEqual(t, -1, restoreIdx, "the rollback must restore the flipped sync config")
	lastWait := -1
	for i, ev := range order {
		if ev == "wait-ready" && i < restoreIdx {
			lastWait = i
		}
	}
	require.NotEqual(t, -1, lastWait, "unfence readiness must be awaited")
	assert.Less(t, lastWait, restoreIdx,
		"restore must never start before gateway readiness confirms")
}

// TestOrchestrator_Execute_RollbackRestoreAlterFails_StillLandsInitialized
// replaces the former pair of ListConfigs-fail / AlterConfigs-fail rollback
// tests: restore no longer calls ListConfigs at all (it is a single
// idempotent AlterConfigs SET), so the only way it can fail now is the SET
// itself failing. The restore half is still soft-fail — the unfence already
// completed, so the run lands at initialized regardless, with loud
// rollback-context remediation (not the post-switchover wording).
func TestOrchestrator_Execute_RollbackRestoreAlterFails_StillLandsInitialized(t *testing.T) {
	orch, config := newHappyPathOrchestrator(t, StateLagsOk, nil)
	config.PauseConsumerOffsetSync = true
	config.DetectUnroutedProducersDuration = time.Millisecond
	config.GatewayYAML = "apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\nspec:\n  routes:\n    - name: migration-route\n      endpoint: gateway:9595\n"

	orch.actions.clusterLinkService = &mockClusterLinkService{
		alterConfigsFn: func(ctx context.Context, cfg clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			// The pause disable (=false) succeeds; the restore re-enable (=true) fails.
			for _, a := range alts {
				if a.Value == "true" {
					return fmt.Errorf("503 restore boom")
				}
			}
			return nil
		},
	}

	var sourceCalls int64
	orch.actions.sourceOffset = &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt64(&sourceCalls, 1)
			return map[int32]int64{0: 100 + n*10}, nil
		},
	}

	stderr := captureStderr(t, func() {
		err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrUnroutedProducers)
	})

	assert.Equal(t, StateInitialized, orch.fsm.Current(),
		"a failed restore alter must not cancel the completed unfence")

	assert.Contains(t, stderr, "unfenced", "guidance must carry the rollback context")
	assert.Contains(t, stderr, "re-apply manually", "a failed restore alter must point at manual remediation")
	assert.Contains(t, stderr, config.ClusterLinkName)
	assert.NotContains(t, stderr, "Migration completed",
		"the post-switchover wording is wrong for a rollback")
}

// TestOrchestrator_ExecuteFailure_EmitsGuidanceRegardlessOfLandedState
// replaces the former EmitsStateMatchedGuidance, which pinned per-state
// guidance copy (WarnIfPausedOnExecuteFailure branching on orch.fsm.Current()
// and the state-file PauseConsumerOffsetSyncFlipped marker). Both are gone:
// the guidance is now a single generic reminder gated only on
// config.PauseConsumerOffsetSync. This drives a real failed Execute into
// several different landing states an operator can actually reach with the
// pause opt-in set, then feeds the resulting config+error to the guidance
// exactly as cmd/migration/execute does, and asserts the SAME generic copy
// appears no matter which state the FSM rested in.
func TestOrchestrator_ExecuteFailure_EmitsGuidanceRegardlessOfLandedState(t *testing.T) {
	validCR := "apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\nspec:\n  routes:\n    - name: migration-route\n      endpoint: gateway:9595\n"

	// enableTrue is the drift-check/happy list response used by every case.
	enableTrue := func(ctx context.Context, cfg clusterlink.Config) (map[string]string, error) {
		return map[string]string{"consumer.offset.sync.enable": "true"}, nil
	}
	alterOK := func(ctx context.Context, cfg clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
		return nil
	}

	tests := []struct {
		name      string
		overrides orchestratorOverrides
		configure func(orch *MigrationOrchestrator, config *MigrationConfig)
		wantState string
	}{
		{
			name: "offset_sync_paused: rogue detected then unfence fails",
			overrides: orchestratorOverrides{
				// The fence patch sets the route's fence field; the rollback's
				// unfence patch whole-route-replaces it. Failing only the latter
				// (the non-fence patch) cancels abort_fence, so the FSM honestly
				// rests at offset_sync_paused.
				patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, _ string) (string, error) {
					if rp.Field != "fence" {
						return "", fmt.Errorf("k8s API unavailable")
					}
					return "", nil
				},
			},
			configure: func(orch *MigrationOrchestrator, config *MigrationConfig) {
				config.PauseConsumerOffsetSync = true
				config.DetectUnroutedProducersDuration = time.Millisecond
				config.GatewayYAML = validCR
				originalCL := orch.actions.clusterLinkService
				orch.actions.clusterLinkService = &mockClusterLinkService{
					listMirrorTopicsFn:    originalCL.ListMirrorTopics,
					validateTopicsFn:      originalCL.ValidateTopics,
					promoteMirrorTopicsFn: originalCL.PromoteMirrorTopics,
					listConfigsFn:         enableTrue,
					alterConfigsFn:        alterOK,
				}
				var sourceCalls int64
				orch.actions.sourceOffset = &mockOffsetProvider{
					getFn: func(topic string) (map[int32]int64, error) {
						n := atomic.AddInt64(&sourceCalls, 1)
						return map[int32]int64{0: 100 + n*10}, nil
					},
				}
			},
			wantState: StateOffsetSyncPaused,
		},
		{
			name:      "fence_verified: promote fails after a successful pause+verify",
			overrides: orchestratorOverrides{},
			configure: func(orch *MigrationOrchestrator, config *MigrationConfig) {
				config.PauseConsumerOffsetSync = true
				// Detection disabled: verify_fence is an immediate success, so
				// the promote failure rests the FSM at fence_verified.
				config.DetectUnroutedProducersDuration = 0
				config.GatewayYAML = validCR
				originalCL := orch.actions.clusterLinkService
				orch.actions.clusterLinkService = &mockClusterLinkService{
					listMirrorTopicsFn: originalCL.ListMirrorTopics,
					validateTopicsFn:   originalCL.ValidateTopics,
					listConfigsFn:      enableTrue,
					alterConfigsFn:     alterOK,
					promoteMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
						return nil, fmt.Errorf("promote boom")
					},
				}
			},
			wantState: StateFenceVerified,
		},
		{
			name: "promoted: switchover fails after a successful promote",
			overrides: orchestratorOverrides{
				// Fence and switchover both apply; only the switchover patch fails,
				// leaving the FSM at promoted (switch failures do not roll back).
				// The switch patch is a whole-route replace (Field == ""), while the
				// fence patch sets the fence field; because switch failures do not
				// roll back, no unfence (also a whole-route replace) runs here, so
				// Field == "" uniquely identifies the switch patch.
				patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, _ string) (string, error) {
					if rp.Field == "" {
						return "", fmt.Errorf("switchover apply failed")
					}
					return "", nil
				},
			},
			configure: func(orch *MigrationOrchestrator, config *MigrationConfig) {
				config.PauseConsumerOffsetSync = true
				config.DetectUnroutedProducersDuration = 0
				config.GatewayYAML = validCR
				originalCL := orch.actions.clusterLinkService
				orch.actions.clusterLinkService = &mockClusterLinkService{
					listMirrorTopicsFn:    originalCL.ListMirrorTopics,
					validateTopicsFn:      originalCL.ValidateTopics,
					promoteMirrorTopicsFn: originalCL.PromoteMirrorTopics,
					listConfigsFn:         enableTrue,
					alterConfigsFn:        alterOK,
				}
			},
			wantState: StatePromoted,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			orch, config := newHappyPathOrchestrator(t, StateLagsOk, nil, tc.overrides)
			tc.configure(orch, config)

			err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
			require.Error(t, err)

			// The FSM rests in the expected landed state — the value the
			// executor forwards to the guidance.
			assert.Equal(t, tc.wantState, orch.fsm.Current(), "landed state")

			// Feed the landed config + error to the guidance exactly as the
			// executor does (cmd/migration/execute/migration_executor.go). The
			// copy is the same generic reminder regardless of which state the
			// FSM landed in.
			out := captureStderr(t, func() {
				WarnIfPausedOnExecuteFailure(config, err)
			})
			assert.Contains(t, out, "test-link", "guidance for %s must name the cluster link", tc.wantState)
			assert.Contains(t, out, "consumer.offset.sync.enable", "guidance for %s must name the key", tc.wantState)
			assert.Contains(t, out, "declared baseline", "guidance for %s must point at the declared baseline", tc.wantState)
		})
	}
}

func TestOrchestrator_Execute_NoOptIn_NeverTouchesClusterLinkConfig(t *testing.T) {
	// AE2 pin: the default flow's offset_sync_unchanged guarantee. Without the
	// opt-in the run passes through offset_sync_paused to switched with zero
	// AlterConfigs calls. (ListConfigs still runs once, in Initialize.)
	var alterCalls int64

	orch, _ := newHappyPathOrchestrator(t, StateUninitialized, nil)

	originalCL := orch.actions.clusterLinkService
	orch.actions.clusterLinkService = &mockClusterLinkService{
		listMirrorTopicsFn: originalCL.ListMirrorTopics,
		listConfigsFn:      originalCL.ListConfigs,
		validateTopicsFn:   originalCL.ValidateTopics,
		promoteMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			return originalCL.PromoteMirrorTopics(ctx, cfg, topicNames)
		},
		alterConfigsFn: func(ctx context.Context, cfg clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			atomic.AddInt64(&alterCalls, 1)
			return nil
		},
	}

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, uninitializedReconcileResult(nil))
	require.NoError(t, err)

	assert.Equal(t, int64(0), atomic.LoadInt64(&alterCalls),
		"the default flow must never write cluster-link config")
	assert.Equal(t, StateSwitched, orch.fsm.Current())
}

// TestOrchestrator_Execute_ResumeAtFenced_ReappliesPauseIdempotently replaces
// the former AE7 marker-skip pin. The pause bookend is no longer gated on a
// PauseConsumerOffsetSyncFlipped marker — it is a plan-driven idempotent
// apply, so a resume from fenced simply re-applies the same AlterConfigs SET
// (a no-op against a real cluster link) rather than reading a marker to
// decide whether to skip. It never calls ListConfigs, on the first pass or a
// resume.
func TestOrchestrator_Execute_ResumeAtFenced_ReappliesPauseIdempotently(t *testing.T) {
	var alterCalls, listCalls int64

	orch, config := newHappyPathOrchestrator(t, StateFenced, nil)
	config.PauseConsumerOffsetSync = true

	originalCL := orch.actions.clusterLinkService
	orch.actions.clusterLinkService = &mockClusterLinkService{
		listMirrorTopicsFn: originalCL.ListMirrorTopics,
		listConfigsFn: func(ctx context.Context, cfg clusterlink.Config) (map[string]string, error) {
			atomic.AddInt64(&listCalls, 1)
			return map[string]string{"consumer.offset.sync.enable": "false"}, nil
		},
		alterConfigsFn: func(ctx context.Context, cfg clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			atomic.AddInt64(&alterCalls, 1)
			return nil
		},
		promoteMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			return originalCL.PromoteMirrorTopics(ctx, cfg, topicNames)
		},
	}

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.NoError(t, err)

	assert.Equal(t, int64(1), atomic.LoadInt64(&alterCalls), "the idempotent pause SET is re-applied on resume")
	assert.Equal(t, int64(0), atomic.LoadInt64(&listCalls), "never reads the live link to decide")
	assert.Equal(t, StateSwitched, orch.fsm.Current())
}

// captureStdout mirrors captureStderr (offset_sync_bookend_test.go) for the
// reporter's progress stream.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	done := make(chan string, 1)
	go func() {
		var buf strings.Builder
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	require.NoError(t, w.Close())
	return <-done
}

func TestOrchestrator_RollbackOutput_NamesKeysNotValues(t *testing.T) {
	// Log hygiene: the rollback's output names config keys, counts, and the
	// cluster-link name — never credentials. (Restore is now a baseline-driven
	// idempotent apply — it never reads live cluster-link config values at
	// all, so there is nothing config-value-shaped left to leak.)
	orch, config := newHappyPathOrchestrator(t, StateLagsOk, nil)
	config.PauseConsumerOffsetSync = true
	config.DetectUnroutedProducersDuration = time.Millisecond
	config.GatewayYAML = "apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\nspec:\n  routes:\n    - name: migration-route\n      endpoint: gateway:9595\n"

	orch.actions.clusterLinkService = &mockClusterLinkService{
		alterConfigsFn: func(ctx context.Context, cfg clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			return nil
		},
	}

	var sourceCalls int64
	orch.actions.sourceOffset = &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt64(&sourceCalls, 1)
			return map[int32]int64{0: 100 + n*10}, nil
		},
	}

	var stdout string
	stderrOut := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "super-secret-value"}, nil)
			require.Error(t, err)
		})
	})
	combined := stdout + stderrOut

	assert.Contains(t, combined, "Restoring consumer.offset.sync",
		"the rollback's restore must announce itself")
	assert.NotContains(t, combined, "super-secret-value",
		"credentials must never appear in rollback output")
}

func TestOrchestrator_Execute_VerifyFetchError_NoRollback(t *testing.T) {
	// A transient offset-fetch failure during the detection window is not a
	// detection: it must propagate without ErrUnroutedProducers so the
	// orchestrator neither unfences the gateway nor rolls the FSM back.
	// Re-running execute resumes from offset_sync_paused and retries verification.
	var applyCalls int64
	overrides := orchestratorOverrides{
		patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, _ string) (string, error) {
			atomic.AddInt64(&applyCalls, 1)
			return "", nil
		},
	}

	orch, config := newHappyPathOrchestrator(t, StateLagsOk, []string{"topic-a"}, overrides)
	config.DetectUnroutedProducersDuration = time.Millisecond

	// First snapshot succeeds; the second fails mid-window.
	var getCalls int64
	orch.actions.sourceOffset = &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			if atomic.AddInt64(&getCalls, 1) == 1 {
				return map[int32]int64{0: 100}, nil
			}
			return nil, fmt.Errorf("connection reset by peer")
		},
	}

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrUnroutedProducers,
		"a fetch failure must not be classified as a detection")
	assert.Contains(t, err.Error(), "connection reset by peer")

	assert.Equal(t, StateOffsetSyncPaused, orch.fsm.Current(),
		"state must stay at offset_sync_paused — no abort_fence rollback on a fetch error")

	assert.Equal(t, int64(1), atomic.LoadInt64(&applyCalls),
		"only the fence CR apply should occur; the gateway must not be unfenced")
}

func TestOrchestrator_Execute_PromoteError(t *testing.T) {
	overrides := orchestratorOverrides{
		promoteMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			return nil, fmt.Errorf("confluent cloud API unavailable")
		},
	}

	orch, _ := newHappyPathOrchestrator(t, StateFenced, nil, overrides)

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, nil)
	require.Error(t, err)

	// The verify_fence transition succeeded first (detection disabled → no-op),
	// then the promote transition was cancelled, so the FSM rests at
	// fence_verified — never promoted.
	assert.Equal(t, StateFenceVerified, orch.fsm.Current(),
		"should rest at fence_verified after promote failure, never promoted")
}

// --- AAO kill-point matrix (Plan 2c, Task 3) ---
//
// Each TestAAO_<row> below corresponds to one row of the AAO kill-point
// matrix (task-3-brief.md §1, in-scope rows only): it constructs, purely via
// the fakes and a hand-built *migplan.Result, the live cluster state a kill
// at that point would leave, drives one full from-zero Execute, and asserts
// (a) it converges to switched and (b) a second Execute — fed the Result a
// real migplan.Reconcile would return once nothing at all remains
// (aaoDoneResult) — is a pure no-op: zero additional gateway patches, zero
// additional PromoteMirrorTopics calls. No live-observation is added
// anywhere: every row is expressed as fake behaviour plus a constructed
// Result, and the FSM only ever consumes config.Topics/FenceYAML/
// SwitchoverYAML — set once by Initialize from that Result — never anything
// read live from the cluster to decide what to skip (see workflow.go's
// Initialize and the three plan-driven no-op guards on
// FenceGateway/PromoteTopics/SwitchGateway, all keyed on
// len(config.Topics) == 0).

// aaoDoneResult builds the migplan.Result a real migplan.Reconcile returns
// once nothing at all remains for AAO: no per-topic promote work (Topics)
// and no gateway-level work either (FenceYAML/SwitchoverYAML empty too) —
// see internal/services/migplan/reconcile/reconcile.go's reconcileStatic,
// whose `len(inflight) == 0` early return leaves Plan.Artifacts nil and
// therefore every one of these three fields empty on the resulting Result.
// This is the constructed Result every row's second ("must now be a no-op")
// Execute call below is driven with.
func aaoDoneResult() *migplan.Result {
	return &migplan.Result{
		Route:       "migration-route",
		Topics:      []string{},
		Mode:        "static",
		GatewayYAML: testInitialCR,
		// FenceYAML/SwitchoverYAML deliberately left "" — nothing in flight.
	}
}

// aaoFullResult builds the migplan.Result for a fully migratable two-topic
// plan (both topics still needing fence+promote+switch) — the Result every
// "everything still to do" row below drives its first Execute call with.
func aaoFullResult() *migplan.Result {
	return &migplan.Result{
		Route:          "migration-route",
		Topics:         []string{"topic-a", "topic-b"},
		FenceYAML:      testFenceYAML,
		SwitchoverYAML: testSwitchoverYAML,
		GatewayYAML:    testInitialCR,
		Mode:           "static",
	}
}

// newAAOKillPointOrchestrator is the shared builder behind every
// TestAAO_<row> test. Unlike buildHappyPathOrchestrator's fixed "every topic
// starts ACTIVE, only this run's own PromoteMirrorTopics call flips it to
// STOPPED" mirror model, this lets a row seed which topics' mirrors are
// ALREADY STOPPED before Execute ever runs — modelling exactly the live
// state a kill at a mid/late-promote point would leave — and it records
// every gateway-route patch and every PromoteMirrorTopics call (with its
// topic list, in call order) so each row can assert on them directly,
// including that a second Execute adds none.
//
// initialCurrentState is written onto orch.fsm.Current() purely as
// documentation of the persisted position a kill at this row's point would
// leave — construction ignores it (Task 2's start-from-zero contract,
// pinned generally by TestNewMigrationOrchestrator_AlwaysStartsUninitialized
// and per-state by TestOrchestrator_Execute_FromZero_
// IgnoresPersistedCurrentState), so it has no effect on how Execute below
// behaves; it is here only so each row's harness call reads as "the state a
// kill would leave," matching the matrix.
//
// readyProgress, when non-empty, replaces the default (immediately
// succeeding, no-progress-reported) WaitForGatewayReady fake with one that
// reports each entry via onProgress, in order, before returning nil. This is
// the harness's fake pod-waiter for the *u rows (A-S1u/A-S4u): the real
// "poll until converged" loop lives entirely inside the production
// K8sService this mock replaces, with nothing to hook in this harness, so
// "waits for convergence, then proceeds" is modelled as the single mocked
// call itself reporting an interim not-yet-converged progress tick before
// its own converged return and nil — the simplest fake that both lets the
// wait return and still proves the step observed an unconverged state
// before succeeding (see the returned readyEvents recorder). Both
// FenceGateway's and SwitchGateway's confirm step share this one mock
// (neither this build's default capability nor detection is configured to
// route either through a different wait mechanism — see confirmFence's
// default case and VerifyTransition), so a non-empty readyProgress is
// replayed on every call reaching it, fence's and switch's alike.
func newAAOKillPointOrchestrator(
	t *testing.T,
	initialCurrentState string,
	initiallyStopped []string,
	readyProgress []gateway.GatewayReadinessProgress,
) (orch *MigrationOrchestrator, config *MigrationConfig, patchCalls *int64, promoteCalls *[][]string, readyEventsOut *[]gateway.GatewayReadinessProgress) {
	t.Helper()

	topics := []string{"topic-a", "topic-b"}
	stoppedSet := make(map[string]bool, len(initiallyStopped))
	for _, tpc := range initiallyStopped {
		stoppedSet[tpc] = true
	}

	config = &MigrationConfig{
		MigrationId:         "test-migration-1",
		KubeConfigPath:      "/fake/kubeconfig",
		SourceBootstrap:     "source:9092",
		ClusterBootstrap:    "dest:9092",
		ClusterId:           "lkc-test",
		ClusterRestEndpoint: "https://pkc-test.confluent.cloud",
		ClusterLinkName:     "test-link",
		Topics:              topics,
		InitialCrName:       "my-gateway",
		K8sNamespace:        "confluent",
		GatewayYAML:         testInitialCR,
		Route:               "migration-route",
		Mode:                "static",
		FenceYAML:           testFenceYAML,
		SwitchoverYAML:      testSwitchoverYAML,
	}

	var mu sync.Mutex
	var patches int64
	var promotes [][]string
	var readyEvents []gateway.GatewayReadinessProgress
	promotedTopics := make(map[string]bool, len(stoppedSet))
	for topic := range stoppedSet {
		promotedTopics[topic] = true
	}

	gw := &mockGatewayService{
		getGatewayYAMLFn: func(ctx context.Context, namespace, name string) ([]byte, error) {
			return []byte(testInitialCR), nil
		},
		patchGatewayRouteFn: func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, configID string) (string, error) {
			atomic.AddInt64(&patches, 1)
			return configID, nil
		},
		getGatewayPodUIDsFn: func(ctx context.Context, namespace, name string) (map[k8stypes.UID]struct{}, error) {
			return map[k8stypes.UID]struct{}{
				"uid-1": {},
				"uid-2": {},
			}, nil
		},
		waitForGatewayPodsFn: func(ctx context.Context, namespace, name string, initialPodUIDs map[k8stypes.UID]struct{}, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.PodRolloutProgress)) error {
			return nil
		},
		waitForGatewayReadyFn: func(ctx context.Context, namespace, name string, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
			for _, p := range readyProgress {
				mu.Lock()
				readyEvents = append(readyEvents, p)
				mu.Unlock()
				onProgress(p)
			}
			return nil
		},
	}

	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			mu.Lock()
			defer mu.Unlock()
			out := make([]clusterlink.MirrorTopic, len(topics))
			for i, name := range topics {
				status := clusterlink.MirrorStatusActive
				if promotedTopics[name] {
					status = clusterlink.MirrorStatusStopped
				}
				out[i] = clusterlink.MirrorTopic{
					MirrorTopicName: name,
					MirrorStatus:    status,
				}
			}
			return out, nil
		},
		listConfigsFn: func(ctx context.Context, cfg clusterlink.Config) (map[string]string, error) {
			return map[string]string{"consumer.offset.sync.enable": "true"}, nil
		},
		validateTopicsFn: func(reqTopics []string, clusterLinkTopics []string) error {
			return nil
		},
		promoteMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			mu.Lock()
			promotes = append(promotes, append([]string(nil), topicNames...))
			for _, name := range topicNames {
				promotedTopics[name] = true
			}
			mu.Unlock()
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

	zeroLagOffsets := map[int32]int64{0: 100, 1: 200}
	srcOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return zeroLagOffsets, nil
		},
	}
	dstOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return zeroLagOffsets, nil
		},
	}

	actions := NewMigrationActionsWithOffsets(gw, cl, srcOffset, dstOffset)
	actions.lagPollInterval = time.Millisecond
	actions.promotePollInterval = time.Millisecond

	orch = NewMigrationOrchestrator(config, actions)

	return orch, config, &patches, &promotes, &readyEvents
}

// TestAAO_S0_FreshFullRun covers matrix row A-S0: a pristine migration, never
// fenced, mirrors ACTIVE, every topic migratable. A from-zero walk must
// fence, promote both topics, and switch — and a second run, once reconcile
// reports nothing left, must be a pure no-op.
func TestAAO_S0_FreshFullRun(t *testing.T) {
	orch, _, patchCalls, promoteCalls, _ := newAAOKillPointOrchestrator(t, StateUninitialized, nil, nil)

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, aaoFullResult())
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	assert.Equal(t, int64(2), atomic.LoadInt64(patchCalls), "one fence apply and one switch apply")
	require.Len(t, *promoteCalls, 1, "both zero-lag topics promoted in one batch")
	assert.ElementsMatch(t, []string{"topic-a", "topic-b"}, (*promoteCalls)[0])

	// A second run: a fresh reconcile now reports nothing left at all. The
	// from-zero walk still visits every step (Task 2), but every step's
	// plan-driven no-op guard (Task 1) must fire — zero new mutations.
	patchesBefore := atomic.LoadInt64(patchCalls)
	promotesBefore := len(*promoteCalls)

	err = orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, aaoDoneResult())
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	assert.Equal(t, patchesBefore, atomic.LoadInt64(patchCalls),
		"a completed migration's re-run must apply no gateway patches")
	assert.Equal(t, promotesBefore, len(*promoteCalls),
		"a completed migration's re-run must issue no promote calls")
}

// The test-only kill-point seam (killpoint.EnvVar) must interrupt a run right
// after the named checkpoint state, simulating an abrupt kcp exit (Ctrl-C) via
// a real context cancellation. A run that would otherwise reach StateSwitched
// must instead stop at StateFenced with a cancellation error, having applied
// the fence but neither promoted nor switched. This is the mechanism the live
// resume suite drives to leave a real partial world, then re-run to completion.
func TestOrchestrator_Execute_KillPointEnvCancelsAfterState(t *testing.T) {
	t.Setenv(killpoint.EnvVar, StateFenced)
	orch, _, patchCalls, promoteCalls, _ := newAAOKillPointOrchestrator(t, StateUninitialized, nil, nil)

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, aaoFullResult())

	require.Error(t, err, "execute must exit non-zero when the kill-point env var fires")
	assert.ErrorIs(t, err, context.Canceled, "the interruption must surface as a context cancellation")
	assert.Equal(t, StateFenced, orch.fsm.Current(), "the run must stop right after the fenced checkpoint")
	assert.Equal(t, int64(1), atomic.LoadInt64(patchCalls), "only the fence apply happened — the switch must not run")
	assert.Empty(t, *promoteCalls, "no promote occurred — interrupted before the promote stage")
}

// TestAAO_S1_AlreadyFencedNoReapply covers matrix row A-S1: the persisted
// CurrentState a kill right after fencing would leave. Construction ignores
// it (see newAAOKillPointOrchestrator's doc comment), so the FSM still walks
// the whole workflow — there is no live read that could tell this run "the
// route is already fenced" apart from a fresh one, so FenceGateway's apply is
// unconditionally re-issued every run (Task 2's "AAO reconciles every run").
// What this row actually pins is that re-issuing that apply is safe: exactly
// one fence patch, never doubled, and promotion/switch proceed normally from
// mirrors that are still ACTIVE (nothing was promoted by the prior, killed
// run) — landing at the same converged, idempotent-on-a-second-run place as
// a fresh migration.
func TestAAO_S1_AlreadyFencedNoReapply(t *testing.T) {
	orch, _, patchCalls, promoteCalls, _ := newAAOKillPointOrchestrator(t, StateFenced, nil, nil)

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, aaoFullResult())
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	assert.Equal(t, int64(2), atomic.LoadInt64(patchCalls),
		"one fence re-apply plus one switch apply — never doubled by resuming into a state that already says fenced")
	require.Len(t, *promoteCalls, 1)
	assert.ElementsMatch(t, []string{"topic-a", "topic-b"}, (*promoteCalls)[0])

	patchesBefore := atomic.LoadInt64(patchCalls)
	promotesBefore := len(*promoteCalls)
	err = orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, aaoDoneResult())
	require.NoError(t, err)
	assert.Equal(t, patchesBefore, atomic.LoadInt64(patchCalls))
	assert.Equal(t, promotesBefore, len(*promoteCalls))
}

// TestAAO_S1u_WaitsForConvergence covers matrix row A-S1u: fenced, but the
// serving pods have not yet converged on the fenced config. There is no
// separate "poll until ready" loop in this test harness to hook — that loop
// lives entirely inside the production K8sService the gateway mock replaces
// — so "waits for convergence, then proceeds" is modelled as the single
// mocked WaitForGatewayReady call itself reporting one not-yet-converged
// progress tick before its own converged return (see
// newAAOKillPointOrchestrator's doc comment on readyProgress). This is the
// simplest fake that both lets the wait return and still proves the step
// observed an unconverged state — not reported done while unconverged —
// before succeeding.
func TestAAO_S1u_WaitsForConvergence(t *testing.T) {
	notReady := gateway.GatewayReadinessProgress{RolloutDetected: true, InitialPodCount: 2, PodsReady: 0}
	converged := gateway.GatewayReadinessProgress{RolloutDetected: true, InitialPodCount: 2, PodsReady: 2}

	orch, _, patchCalls, promoteCalls, readyEvents := newAAOKillPointOrchestrator(
		t, StateFenced, nil, []gateway.GatewayReadinessProgress{notReady, converged})

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, aaoFullResult())
	require.NoError(t, err, "the fence step must succeed once convergence is reported, not error out on the interim tick")
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	require.NotEmpty(t, *readyEvents, "the fence/switch convergence wait must have been exercised")
	first := (*readyEvents)[0]
	assert.NotEqual(t, first.InitialPodCount, first.PodsReady,
		"the first reported tick must be the not-yet-converged one — the step must not appear done while unconverged")
	last := (*readyEvents)[len(*readyEvents)-1]
	assert.Equal(t, last.InitialPodCount, last.PodsReady, "the wait must end at convergence")

	assert.Equal(t, int64(2), atomic.LoadInt64(patchCalls))
	require.Len(t, *promoteCalls, 1)

	patchesBefore := atomic.LoadInt64(patchCalls)
	promotesBefore := len(*promoteCalls)
	err = orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, aaoDoneResult())
	require.NoError(t, err)
	assert.Equal(t, patchesBefore, atomic.LoadInt64(patchCalls))
	assert.Equal(t, promotesBefore, len(*promoteCalls))
}

// TestAAO_S2_MidPromoteMix covers matrix row A-S2: a kill mid-promotion,
// where topic-a has already reached STOPPED (SwitchOnly, per
// reconcile/rules.go's Classify) and topic-b is still ACTIVE. A real
// migplan.Reconcile excludes topic-a from the promote set entirely — the
// filtering is reconcile's job, done once, live — so the constructed Result
// here lists only topic-b, exactly as the matrix row specifies ("result
// Topics = the not-yet-STOPPED only"). This must drive PromoteTopics to
// promote topic-b alone, never re-issuing PromoteMirrorTopics for the
// already-STOPPED topic-a.
func TestAAO_S2_MidPromoteMix(t *testing.T) {
	orch, _, patchCalls, promoteCalls, _ := newAAOKillPointOrchestrator(
		t, StatePromoted, []string{"topic-a"}, nil)

	midResult := &migplan.Result{
		Route:          "migration-route",
		Topics:         []string{"topic-b"},
		FenceYAML:      testFenceYAML,
		SwitchoverYAML: testSwitchoverYAML,
		GatewayYAML:    testInitialCR,
		Mode:           "static",
	}

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, midResult)
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	require.Len(t, *promoteCalls, 1, "exactly one promote batch")
	assert.Equal(t, []string{"topic-b"}, (*promoteCalls)[0],
		"only the not-yet-stopped topic is promoted — topic-a (already STOPPED) is never re-promoted")
	assert.Equal(t, int64(2), atomic.LoadInt64(patchCalls), "fence + switch, gated by the still-nonempty (single-topic) plan")

	patchesBefore := atomic.LoadInt64(patchCalls)
	promotesBefore := len(*promoteCalls)
	err = orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, aaoDoneResult())
	require.NoError(t, err)
	assert.Equal(t, patchesBefore, atomic.LoadInt64(patchCalls))
	assert.Equal(t, promotesBefore, len(*promoteCalls))
}

// TestAAO_S3_PromotedNotSwitched covers matrix row A-S3: every mirror
// already STOPPED (nothing left to promote) but the switch not yet applied.
// A real migplan.Reconcile for this state returns Topics=[] but non-empty
// FenceYAML/SwitchoverYAML (see internal/services/migplan/reconcile/
// reconcile.go's reconcileStatic: its 'promote' set excludes SwitchOnly
// topics, but its 'inflight' set — which gates whether Artifacts are built
// at all — includes them). This was previously infeasible: FenceGateway and
// SwitchGateway both no-op'd on the same len(config.Topics)==0 check
// PromoteTopics correctly uses, so they would have BOTH incorrectly no-op'd
// too, never applying the still-owed switch — a real correctness bug (a
// kill right after the last topic's promote completes would report the
// migration falsely complete without ever switching the gateway). Fixed by
// giving Fence/Switch their own per-artifact no-op signal
// (config.FenceYAML/config.SwitchoverYAML) instead of sharing Promote's
// Topics-based one — see workflow.go's FenceGateway/SwitchGateway guard
// comments. This test pins that fix: promote makes no call at all, but
// switch still applies and the run still converges.
func TestAAO_S3_PromotedNotSwitched(t *testing.T) {
	orch, _, patchCalls, promoteCalls, _ := newAAOKillPointOrchestrator(
		t, StatePromoted, []string{"topic-a", "topic-b"}, nil)

	allPromotedResult := &migplan.Result{
		Route:          "migration-route",
		Topics:         []string{},
		FenceYAML:      testFenceYAML,
		SwitchoverYAML: testSwitchoverYAML,
		GatewayYAML:    testInitialCR,
		Mode:           "static",
	}

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, allPromotedResult)
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	assert.Empty(t, *promoteCalls, "nothing left to promote — PromoteTopics must make no call at all")
	assert.Equal(t, int64(2), atomic.LoadInt64(patchCalls),
		"fence + switch must both still apply — a non-empty artifact is owed regardless of the empty promote set")

	patchesBefore := atomic.LoadInt64(patchCalls)
	promotesBefore := len(*promoteCalls)
	err = orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, aaoDoneResult())
	require.NoError(t, err)
	assert.Equal(t, patchesBefore, atomic.LoadInt64(patchCalls))
	assert.Equal(t, promotesBefore, len(*promoteCalls))
}

// TestAAO_S4u_SwitchWaitsForConvergence covers matrix row A-S4u: the switch
// CR landed but the serving pods have not yet converged on it. Like A-S3,
// this needs Topics=[] (nothing left to promote) with a non-empty
// SwitchoverYAML for the switch to actually run — now feasible after the
// per-artifact no-op fix (see TestAAO_S3_PromotedNotSwitched). The
// convergence wait is faked the same way as TestAAO_S1u_
// WaitsForConvergence: the mocked WaitForGatewayReady call itself reports a
// not-yet-converged progress tick before its own converged return, since
// there is no separate "poll until ready" loop in this harness to hook (see
// newAAOKillPointOrchestrator's doc comment on readyProgress).
func TestAAO_S4u_SwitchWaitsForConvergence(t *testing.T) {
	notReady := gateway.GatewayReadinessProgress{RolloutDetected: true, InitialPodCount: 2, PodsReady: 0}
	converged := gateway.GatewayReadinessProgress{RolloutDetected: true, InitialPodCount: 2, PodsReady: 2}

	orch, _, patchCalls, promoteCalls, readyEvents := newAAOKillPointOrchestrator(
		t, StateSwitched, []string{"topic-a", "topic-b"}, []gateway.GatewayReadinessProgress{notReady, converged})

	allPromotedResult := &migplan.Result{
		Route:          "migration-route",
		Topics:         []string{},
		FenceYAML:      testFenceYAML,
		SwitchoverYAML: testSwitchoverYAML,
		GatewayYAML:    testInitialCR,
		Mode:           "static",
	}

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, allPromotedResult)
	require.NoError(t, err, "the switch step must succeed once convergence is reported, not error out on the interim tick")
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	require.NotEmpty(t, *readyEvents, "the fence/switch convergence wait must have been exercised")
	first := (*readyEvents)[0]
	assert.NotEqual(t, first.InitialPodCount, first.PodsReady,
		"the first reported tick must be the not-yet-converged one — the step must not appear done while unconverged")
	last := (*readyEvents)[len(*readyEvents)-1]
	assert.Equal(t, last.InitialPodCount, last.PodsReady, "the wait must end at convergence")

	assert.Empty(t, *promoteCalls)
	assert.Equal(t, int64(2), atomic.LoadInt64(patchCalls))

	patchesBefore := atomic.LoadInt64(patchCalls)
	promotesBefore := len(*promoteCalls)
	err = orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, aaoDoneResult())
	require.NoError(t, err)
	assert.Equal(t, patchesBefore, atomic.LoadInt64(patchCalls))
	assert.Equal(t, promotesBefore, len(*promoteCalls))
}

// TestAAO_S4_DoneIsNoop covers matrix row A-S4 — the important one: it pins
// the deleted HasPendingWork short-circuit's replacement. Live state: fully
// switched already, fence gone, every mirror STOPPED. A real
// migplan.Reconcile classifies every topic Unchanged and returns a Result
// with no artifacts at all (aaoDoneResult, mirroring reconcileStatic's
// len(inflight)==0 early return): Topics, FenceYAML and SwitchoverYAML all
// empty. Every one of Fence/Promote/Switch's plan-driven no-op guards (Task
// 1, all keyed on len(config.Topics)==0) must fire — zero patches, zero
// promotes — and the FSM still walks through every step to switched (Task
// 2's every-step-visited contract) rather than needing any special-cased
// short-circuit to get there.
func TestAAO_S4_DoneIsNoop(t *testing.T) {
	orch, _, patchCalls, promoteCalls, _ := newAAOKillPointOrchestrator(
		t, StateSwitched, []string{"topic-a", "topic-b"}, nil)

	err := orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, aaoDoneResult())
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current())

	assert.Equal(t, int64(0), atomic.LoadInt64(patchCalls), "zero gateway patches — fence and switch must both no-op")
	assert.Empty(t, *promoteCalls, "zero promote calls")

	// A second Execute is byte-for-byte identical: still zero mutations.
	err = orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, aaoDoneResult())
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current())
	assert.Equal(t, int64(0), atomic.LoadInt64(patchCalls))
	assert.Empty(t, *promoteCalls)
}

// TestAAO_S1p_OffsetSyncPaused would cover matrix row A-S1p (fenced, with
// consumer offset sync paused). Deferred: offset-sync is out of 2c's scope.
func TestAAO_S1p_OffsetSyncPaused(t *testing.T) {
	t.Skip("offset-sync is Plan 2e")
}

// TestAAO_Layer2_KillInjection would cover Layer 2 of the matrix — proving a
// real kill at point P (via a failure-injection harness) actually leaves
// live state S, rather than constructing S directly via fakes as every
// Layer-1 row above does. Deferred: the failure-injection harness itself is
// a later build-order plan; 2c does Layer 1 only.
func TestAAO_Layer2_KillInjection(t *testing.T) {
	t.Skip("failure-injection harness is a later build-order plan")
}

// --- Plan 2e, Task 3: no state file, still idempotent ---

// TestOrchestrator_Execute_NoStateFileWritten is the finale for Plan 2e: it
// drives a full from-zero AAO run, and its idempotent second run once
// reconcile reports nothing left, in a real temporary working directory —
// mirroring TestAAO_S0_FreshFullRun's exact harness (newAAOKillPointOrchestrator)
// and Result fixtures (aaoFullResult/aaoDoneResult) rather than inventing a
// new one — and asserts that neither run ever creates a <name>-state.json,
// or any other file, on disk. Tasks 1-2 already deleted every line of code
// that used to write one (MigrationState, PersistState, CurrentState,
// ClusterLinkConfigs, PauseConsumerOffsetSyncFlipped); this test guards the
// resulting property directly — in the directory a real `kcp migration
// execute` invocation would run in — rather than trusting that the absence
// of code implies the absence of files. The idempotency half is already
// covered thoroughly by the kill-point matrix above; asserting it again here
// (zero additional patches/promotes on the second run) just confirms the
// no-file guarantee holds across the same two-run shape those tests use.
func TestOrchestrator_Execute_NoStateFileWritten(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	orch, _, patchCalls, promoteCalls, _ := newAAOKillPointOrchestrator(t, StateUninitialized, nil, nil)

	err = orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, aaoFullResult())
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current())
	assertNoFilesWritten(t, dir)

	patchesBefore := atomic.LoadInt64(patchCalls)
	promotesBefore := len(*promoteCalls)

	// Second, idempotent run: a fresh reconcile now reports nothing left at
	// all (aaoDoneResult). No new gateway patch or promote call is issued —
	// same contract TestAAO_S0_FreshFullRun pins — and still no file appears.
	err = orch.Execute(context.Background(), 0, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, aaoDoneResult())
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, orch.fsm.Current())
	assert.Equal(t, patchesBefore, atomic.LoadInt64(patchCalls),
		"a completed migration's re-run must apply no gateway patches")
	assert.Equal(t, promotesBefore, len(*promoteCalls),
		"a completed migration's re-run must issue no promote calls")

	assertNoFilesWritten(t, dir)
}

// assertNoFilesWritten fails if dir contains anything at all. In particular
// this catches a "*-state.json" migration state file — the artifact Plan 2e
// deleted entirely — reappearing by regression, but it deliberately checks
// for ANY file: there is no longer any legitimate reason for a migration
// execute run to write anything to its working directory.
func assertNoFilesWritten(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "no file should be written to the working directory: %v", entries)
}
