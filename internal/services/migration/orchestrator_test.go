package migration

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/looplab/fsm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// orchestratorOverrides allows tests to customize mock behavior before construction.
type orchestratorOverrides struct {
	getGatewayYAMLFn      func(ctx context.Context, namespace, name string) ([]byte, error)
	applyGatewayYAMLFn    func(ctx context.Context, namespace, name string, yaml []byte) error
	waitForGatewayReadyFn func(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error
	promoteMirrorTopicsFn func(ctx context.Context, config clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error)
}

// newHappyPathOrchestrator builds an orchestrator where every workflow step succeeds.
// The returned config starts at the given initialState.
// Optional overrides allow customizing mock behavior before construction.
func newHappyPathOrchestrator(t *testing.T, initialState string, topics []string, overrides ...orchestratorOverrides) (*MigrationOrchestrator, *MigrationConfig, string) {
	t.Helper()

	if len(topics) == 0 {
		topics = []string{"topic-a", "topic-b"}
	}

	config := &MigrationConfig{
		MigrationId:         "test-migration-1",
		CurrentState:        initialState,
		KubeConfigPath:      "/fake/kubeconfig",
		SourceBootstrap:     "source:9092",
		ClusterBootstrap:    "dest:9092",
		ClusterId:           "lkc-test",
		ClusterRestEndpoint: "https://pkc-test.confluent.cloud",
		ClusterLinkName:     "test-link",
		Topics:              topics,
		InitialCrName:       "my-gateway",
		K8sNamespace:        "confluent",
		InitialCrYAML:       []byte("initial-yaml"),
		FencedCrYAML:        []byte("fenced-yaml"),
		SwitchoverCrYAML:    []byte("switchover-yaml"),
	}

	// Default mock implementations
	getGatewayYAMLFn := func(ctx context.Context, namespace, name string) ([]byte, error) {
		return []byte("initial-yaml"), nil
	}
	applyGatewayYAMLFn := func(ctx context.Context, namespace, name string, yaml []byte) error {
		return nil
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
	var waitForGatewayReadyFn func(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error

	// Apply overrides if provided
	if len(overrides) > 0 {
		o := overrides[0]
		if o.getGatewayYAMLFn != nil {
			getGatewayYAMLFn = o.getGatewayYAMLFn
		}
		if o.applyGatewayYAMLFn != nil {
			applyGatewayYAMLFn = o.applyGatewayYAMLFn
		}
		if o.waitForGatewayReadyFn != nil {
			waitForGatewayReadyFn = o.waitForGatewayReadyFn
		}
		if o.promoteMirrorTopicsFn != nil {
			promoteMirrorTopicsFn = o.promoteMirrorTopicsFn
		}
	}

	gw := &mockGatewayService{
		getGatewayYAMLFn: getGatewayYAMLFn,
		validateGatewayCRsFn: func(initial, fenced, switchover []byte) error {
			return nil
		},
		applyGatewayYAMLFn: applyGatewayYAMLFn,
		getGatewayPodUIDsFn: func(ctx context.Context, namespace, name string) (map[k8stypes.UID]struct{}, error) {
			return map[k8stypes.UID]struct{}{
				"uid-1": {},
				"uid-2": {},
			}, nil
		},
		waitForGatewayPodsFn: func(ctx context.Context, namespace, name string, initialPodUIDs map[k8stypes.UID]struct{}, pollInterval, timeout time.Duration, onProgress func(gateway.PodRolloutProgress)) error {
			return nil
		},
		waitForGatewayReadyFn: waitForGatewayReadyFn,
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

	stateDir := t.TempDir()
	stateFilePath := filepath.Join(stateDir, "migration-state.json")

	migrationState := NewMigrationState()

	orch := NewMigrationOrchestrator(config, actions, migrationState, stateFilePath)

	return orch, config, stateFilePath
}

// loadPersistedMigration reads the state file and returns the migration config by ID.
func loadPersistedMigration(t *testing.T, stateFilePath, migrationID string) *MigrationConfig {
	t.Helper()
	state, err := NewMigrationStateFromFile(stateFilePath)
	require.NoError(t, err, "failed to load state file")
	m, err := state.GetMigrationById(migrationID)
	require.NoError(t, err, "migration %q not found in state file", migrationID)
	return m
}

// --- FSM transition tests ---

func TestOrchestrator_Initialize_FromUninitialized(t *testing.T) {
	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateUninitialized, nil)

	err := orch.Initialize(context.Background(), "api-key", "api-secret")
	require.NoError(t, err)

	assert.Equal(t, StateInitialized, config.CurrentState)

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateInitialized, persisted.CurrentState)
}

func TestOrchestrator_Execute_FullWorkflow(t *testing.T) {
	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateUninitialized, nil)

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.NoError(t, err)

	assert.Equal(t, StateSwitched, config.CurrentState)

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateSwitched, persisted.CurrentState)
}

func TestOrchestrator_Execute_ResumesFromState(t *testing.T) {
	for _, startState := range []string{StateInitialized, StateLagsOk, StateFenced, StateOffsetSyncPaused, StateFenceVerified, StatePromoted} {
		t.Run("from_"+startState, func(t *testing.T) {
			var getYAMLCalls int32
			overrides := orchestratorOverrides{
				getGatewayYAMLFn: func(ctx context.Context, namespace, name string) ([]byte, error) {
					atomic.AddInt32(&getYAMLCalls, 1)
					return []byte("initial-yaml"), nil
				},
			}

			orch, config, stateFilePath := newHappyPathOrchestrator(t, startState, nil, overrides)

			err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
			require.NoError(t, err)

			assert.Equal(t, StateSwitched, config.CurrentState)

			persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
			assert.Equal(t, StateSwitched, persisted.CurrentState)

			// For initialized and later states, init step should be skipped
			// (GetGatewayYAML is called during init, so 0 calls means init was skipped)
			if startState == StateInitialized {
				assert.Equal(t, int32(0), atomic.LoadInt32(&getYAMLCalls),
					"GetGatewayYAML should not be called when resuming from initialized (init step should be skipped)")
			}
		})
	}
}

// --- Error handling tests ---

func TestOrchestrator_Initialize_WorkflowError(t *testing.T) {
	overrides := orchestratorOverrides{
		getGatewayYAMLFn: func(ctx context.Context, namespace, name string) ([]byte, error) {
			return nil, fmt.Errorf("k8s connection refused")
		},
	}

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateUninitialized, nil, overrides)

	err := orch.Initialize(context.Background(), "api-key", "api-secret")
	require.Error(t, err)

	// Config state should NOT have advanced
	assert.Equal(t, StateUninitialized, config.CurrentState)

	// State file should not have been written (PersistState is called after fsm.Event,
	// and fsm.Event returns error when the callback cancels)
	_, loadErr := NewMigrationStateFromFile(stateFilePath)
	if loadErr == nil {
		// If file exists, verify the migration is NOT at initialized
		state, _ := NewMigrationStateFromFile(stateFilePath)
		if state != nil {
			m, getErr := state.GetMigrationById(config.MigrationId)
			if getErr == nil {
				assert.NotEqual(t, StateInitialized, m.CurrentState,
					"state file should NOT contain migration at initialized state after init failure")
			}
		}
	}
	// If file doesn't exist, that's fine — no state was persisted
}

func TestOrchestrator_Execute_FenceError(t *testing.T) {
	overrides := orchestratorOverrides{
		applyGatewayYAMLFn: func(ctx context.Context, namespace, name string, yaml []byte) error {
			return fmt.Errorf("apply gateway failed: forbidden")
		},
	}

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateUninitialized, nil, overrides)

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, err)

	// The orchestrator should have persisted state after each successful step.
	// Init (uninitialized -> initialized) succeeded and was persisted.
	// CheckLags (initialized -> lags_ok) succeeded and was persisted.
	// Fence (lags_ok -> fenced) failed, so the last persisted state should be lags_ok.
	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateLagsOk, persisted.CurrentState)
}

func TestOrchestrator_Execute_UnroutedProducers_AbortsFenceAndRollsBack(t *testing.T) {
	// Simulate a rogue producer: source offsets keep increasing on every call.
	var sourceCallCount int64
	var promoteCallCount int64
	var mu sync.Mutex
	var appliedYAMLs []string

	overrides := orchestratorOverrides{
		applyGatewayYAMLFn: func(ctx context.Context, namespace, name string, yaml []byte) error {
			// Record every applied CR so the unfence (the round-tripped initial
			// CR, distinct from the literal fenced-yaml) can be asserted below.
			mu.Lock()
			appliedYAMLs = append(appliedYAMLs, string(yaml))
			mu.Unlock()
			return nil
		},
	}

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateFenced, nil, overrides)

	// Enable unrouted producer detection
	config.DetectUnroutedProducersDuration = time.Millisecond
	// Set valid YAML for InitialCrYAML so unfenceGateway can parse it
	config.InitialCrYAML = []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\n")

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

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)

	// FSM state should have rolled back to initialized via abort_fence
	assert.Equal(t, StateInitialized, config.CurrentState,
		"FSM state should be rolled back to initialized after unrouted producer detection")

	// State file should be persisted with initialized
	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateInitialized, persisted.CurrentState,
		"persisted state should be initialized after abort_fence transition")

	// Gateway should have been unfenced: the rollback applies the initial CR
	// (round-tripped through YAML, so it carries the kind), which is neither
	// the fenced nor the switchover literal.
	mu.Lock()
	unfenced := false
	for _, y := range appliedYAMLs {
		if strings.Contains(y, "kind: Gateway") {
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
		applyGatewayYAMLFn: func(ctx context.Context, namespace, name string, yaml []byte) error {
			n := atomic.AddInt64(&applyCallCount, 1)
			if n == 1 {
				// First apply is the fence — succeed
				return nil
			}
			// Second apply is the unfence — fail
			return fmt.Errorf("k8s API unavailable")
		},
	}

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateLagsOk, nil, overrides)

	// Enable unrouted producer detection
	config.DetectUnroutedProducersDuration = time.Millisecond
	config.InitialCrYAML = []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\n")

	// Override source offset provider to return increasing offsets
	orch.actions.sourceOffset = &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt64(&applyCallCount, 1)
			return map[int32]int64{0: 100 + n*10}, nil
		},
	}

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, err)
	// Unrouted producers were detected, so the surfaced error still wraps
	// ErrUnroutedProducers; the unfence happens on the abort_fence rollback and
	// its failure is logged. The safety-critical invariant is the state below.
	assert.ErrorIs(t, err, ErrUnroutedProducers)

	// State must remain at the rollback's source: the abort_fence transition is
	// cancelled when unfenceGateway fails, so it is never persisted as
	// initialized. Detection fails at offset_sync_paused (the pause stage sits
	// between fence and verify), so that is where the FSM honestly rests.
	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateOffsetSyncPaused, persisted.CurrentState,
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
		waitForGatewayReadyFn: func(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
			atomic.AddInt64(&waitCallCount, 1)
			return fmt.Errorf("gateway pods did not converge")
		},
	}

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateLagsOk, nil, overrides)

	// Enable unrouted producer detection
	config.DetectUnroutedProducersDuration = time.Millisecond
	config.InitialCrYAML = []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\n")

	// Override source offset provider to return increasing offsets
	orch.actions.sourceOffset = &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt64(&sourceCallCount, 1)
			return map[int32]int64{0: 100 + n*10}, nil
		},
	}

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)

	assert.Equal(t, int64(1), atomic.LoadInt64(&waitCallCount),
		"the unfence rollout readiness should be awaited exactly once (the fence rollout waits via WaitForGatewayPods when detection is enabled)")

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateOffsetSyncPaused, persisted.CurrentState,
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

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateFenced, nil, overrides)
	config.DetectUnroutedProducersDuration = time.Millisecond

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, err)

	// The verify step succeeded (stable offsets) and was persisted; the promote
	// transition was cancelled, so fence_verified is the last good state.
	assert.Equal(t, StateFenceVerified, config.CurrentState)
	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateFenceVerified, persisted.CurrentState,
		"successful fence verification should be persisted even when promotion later fails")
}

func TestOrchestrator_Bootstrap_DemotesFenceVerified(t *testing.T) {
	// fence_verified is a point-in-time attestation and never survives a
	// restart: construction demotes it to fenced (expire_verification) so
	// detection re-runs, and the fence demotion then carries it to lags_ok so
	// the resume re-asserts the fenced CR before re-verifying.
	_, config, _ := newHappyPathOrchestrator(t, StateFenceVerified, nil)

	assert.Equal(t, StateLagsOk, config.CurrentState,
		"bootstrap should demote a persisted fence_verified through fenced to lags_ok")
}

func TestOrchestrator_Bootstrap_DemotesFencedFamily(t *testing.T) {
	// Whether the live gateway still holds the fenced CR is also a
	// point-in-time fact: a crash or a partially-completed abort_fence
	// rollback can leave the gateway unfenced while the state file still says
	// fenced or offset_sync_paused. Construction demotes both to lags_ok so
	// the resume re-applies the fenced CR (a no-op rollout when the gateway
	// never diverged) instead of promoting behind a fence that may not exist.
	for _, state := range []string{StateFenced, StateOffsetSyncPaused} {
		t.Run(state, func(t *testing.T) {
			_, config, _ := newHappyPathOrchestrator(t, state, nil)
			assert.Equal(t, StateLagsOk, config.CurrentState,
				"bootstrap should demote a persisted %s to lags_ok", state)
		})
	}
}

func TestOrchestrator_ExpireVerificationIsAnFSMEdge(t *testing.T) {
	// The bootstrap demotions must be modelled as FSM transitions
	// (expire_verification: fence_verified → fenced; expire_fence:
	// {fenced, offset_sync_paused} → lags_ok), not config mutations the
	// machine never sees — so they fire through the FSM callbacks and appear
	// in fsm.Visualize output alongside abort_fence.
	orch, _, _ := newHappyPathOrchestrator(t, StateUninitialized, nil)

	viz := fsm.Visualize(orch.fsm)
	assert.Contains(t, viz,
		`"fence_verified" -> "fenced" [ label = "expire_verification" ];`,
		"expire_verification should be a visible edge in the state machine")
	assert.Contains(t, viz,
		`"fenced" -> "lags_ok" [ label = "expire_fence" ];`,
		"expire_fence should be a visible edge from fenced")
	assert.Contains(t, viz,
		`"offset_sync_paused" -> "lags_ok" [ label = "expire_fence" ];`,
		"expire_fence should be a visible edge from offset_sync_paused")
}

func TestOrchestrator_PauseStageIsAnFSMEdge(t *testing.T) {
	// The offset-sync pause is a first-class stage between fenced and
	// verification: pause_offset_sync enters it, verify_fence now leaves it,
	// and the abort_fence rollback covers it (rogue detection fires there).
	orch, _, _ := newHappyPathOrchestrator(t, StateUninitialized, nil)

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

func TestOrchestrator_Execute_ResumeFromOffsetSyncPaused_RerunsDetection(t *testing.T) {
	// A resume from offset_sync_paused must re-run fence verification before
	// promoting. The bootstrap fence demotion sends it back through the fence
	// step first; verification then follows as the forward walk's next
	// attestation, so the rogue-producer check never survives a restart.
	var getCalls int64

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateOffsetSyncPaused, nil)
	config.DetectUnroutedProducersDuration = time.Millisecond

	zeroLagOffsets := map[int32]int64{0: 100, 1: 200}
	orch.actions.sourceOffset = &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			atomic.AddInt64(&getCalls, 1)
			return zeroLagOffsets, nil
		},
	}

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.NoError(t, err)

	assert.GreaterOrEqual(t, atomic.LoadInt64(&getCalls), int64(2),
		"resume from offset_sync_paused should take both detection snapshots before promoting")

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateSwitched, persisted.CurrentState)
}

func TestOrchestrator_Execute_ResumeFromFencedFamily_ReassertsFence(t *testing.T) {
	// The divergence this pins closed: a previous run's rollback applied the
	// initial (unfenced) CR but died before the rolled-back state reached disk
	// (or its readiness wait failed after the apply landed). The state file
	// says fenced/offset_sync_paused while the live gateway is unfenced —
	// without a re-fence the resume would sample a quiet source through
	// verify_fence and promote behind a fence that does not exist.
	for _, state := range []string{StateFenced, StateOffsetSyncPaused} {
		t.Run(state, func(t *testing.T) {
			var mu sync.Mutex
			var appliedYAMLs []string
			overrides := orchestratorOverrides{
				applyGatewayYAMLFn: func(ctx context.Context, namespace, name string, yaml []byte) error {
					mu.Lock()
					appliedYAMLs = append(appliedYAMLs, string(yaml))
					mu.Unlock()
					return nil
				},
			}

			orch, config, stateFilePath := newHappyPathOrchestrator(t, state, nil, overrides)

			err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
			require.NoError(t, err)

			mu.Lock()
			defer mu.Unlock()
			require.NotEmpty(t, appliedYAMLs, "the resume must apply gateway CRs")
			assert.Equal(t, "fenced-yaml", appliedYAMLs[0],
				"resume must re-apply the fenced CR before verifying or promoting behind it")

			persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
			assert.Equal(t, StateSwitched, persisted.CurrentState)
		})
	}
}

func TestOrchestrator_Execute_RollbackPersistFails_SurfacesBothErrors(t *testing.T) {
	// The rollback completed (gateway unfenced) but persisting initialized
	// failed: disk still claims a fenced-family state while reality is
	// unfenced. A log line alone is not actionable — the returned error must
	// carry the persist failure and the true gateway state, and the step
	// error must keep its sentinel classification through the extra wrap.
	var applyCalls int64
	var stateDir string

	overrides := orchestratorOverrides{
		applyGatewayYAMLFn: func(ctx context.Context, namespace, name string, yaml []byte) error {
			if atomic.AddInt64(&applyCalls, 1) == 2 {
				// The unfence apply: remove the state directory so every
				// subsequent persist fails while the unfence itself succeeds.
				require.NoError(t, os.RemoveAll(stateDir))
			}
			return nil
		},
	}

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateLagsOk, nil, overrides)
	stateDir = filepath.Dir(stateFilePath)
	config.DetectUnroutedProducersDuration = time.Millisecond
	config.InitialCrYAML = []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\n")

	// Rogue producer: source offsets keep increasing.
	var sourceCalls int64
	orch.actions.sourceOffset = &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt64(&sourceCalls, 1)
			return map[int32]int64{0: 100 + n*10}, nil
		},
	}

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers,
		"the step error must keep its classification through the persist-failure wrap")
	assert.Contains(t, err.Error(), "persisting the rolled-back state failed",
		"a swallowed persist failure after a completed rollback is not actionable")
	assert.Contains(t, err.Error(), "unfenced",
		"the error must name the true gateway state")
	assert.Equal(t, StateInitialized, config.CurrentState,
		"in-memory state reflects the completed rollback")
}

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
		applyGatewayYAMLFn: func(ctx context.Context, namespace, name string, yaml []byte) error {
			record("apply")
			return nil
		},
	}

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateInitialized, nil, overrides)
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

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
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

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateSwitched, persisted.CurrentState)
	assert.True(t, persisted.PauseConsumerOffsetSyncFlipped,
		"the flipped marker must be persisted (restore is owed by the post-execute bookend)")
}

func TestOrchestrator_Execute_PauseError_RollsBackToInitialized(t *testing.T) {
	// AE3: a pause failure must not hold clients fenced. The abort_fence
	// rollback unfences the gateway (with readiness wait) and lands at
	// initialized; the original pause error still surfaces. Nothing was
	// flipped, so the rollback's sync restore is a no-op.
	//
	// This test is also the reentrancy pin: the rollback must fire from
	// handleStepFailure after the pause step's Event call returned — a
	// regression that fires it inside a callback deadlocks on looplab's
	// eventMu and hangs this test visibly.
	var applyCalls, waitCalls, alterCalls int64

	overrides := orchestratorOverrides{
		applyGatewayYAMLFn: func(ctx context.Context, namespace, name string, yaml []byte) error {
			atomic.AddInt64(&applyCalls, 1)
			return nil
		},
		waitForGatewayReadyFn: func(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
			atomic.AddInt64(&waitCalls, 1)
			return nil
		},
	}

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateInitialized, nil, overrides)
	config.PauseConsumerOffsetSync = true
	config.InitialCrYAML = []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\n")

	orch.actions.clusterLinkService = &mockClusterLinkService{
		listConfigsFn: func(ctx context.Context, cfg clusterlink.Config) (map[string]string, error) {
			return map[string]string{"consumer.offset.sync.enable": "true"}, nil
		},
		alterConfigsFn: func(ctx context.Context, cfg clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			atomic.AddInt64(&alterCalls, 1)
			return fmt.Errorf("503 pause boom")
		},
	}

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503 pause boom", "the original pause error must surface")

	assert.Equal(t, StateInitialized, config.CurrentState,
		"pause failure must roll back to initialized, not hold clients fenced")
	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateInitialized, persisted.CurrentState)
	assert.False(t, persisted.PauseConsumerOffsetSyncFlipped)

	assert.Equal(t, int64(2), atomic.LoadInt64(&applyCalls),
		"fence apply then unfence apply")
	assert.Equal(t, int64(2), atomic.LoadInt64(&waitCalls),
		"gateway readiness awaited for both the fence and the unfence")
	assert.Equal(t, int64(1), atomic.LoadInt64(&alterCalls),
		"only the failed disable attempt — no restore call when nothing was flipped")
}

func TestOrchestrator_Execute_PauseError_UnfenceFails_StaysAtFenced(t *testing.T) {
	// AE4: the pause failed and the unfence also fails. The rollback cancels:
	// state stays fenced (memory and disk, honestly reflecting the gateway),
	// the pause error surfaces, and a re-run simply retries the pause.
	var applyCalls int64

	overrides := orchestratorOverrides{
		applyGatewayYAMLFn: func(ctx context.Context, namespace, name string, yaml []byte) error {
			n := atomic.AddInt64(&applyCalls, 1)
			if n == 2 {
				return fmt.Errorf("k8s API unavailable") // the unfence attempt
			}
			return nil // fence (run 1), and the re-run's fence/switch applies
		},
	}

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateInitialized, nil, overrides)
	config.PauseConsumerOffsetSync = true
	config.InitialCrYAML = []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\n")

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

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503 pause boom")

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateFenced, persisted.CurrentState,
		"a cancelled rollback must leave the persisted state at fenced")
	assert.Equal(t, int64(2), atomic.LoadInt64(&applyCalls),
		"the unfence must have been attempted")

	// Re-run recovery: the transient pause failure is gone; execute resumes
	// from fenced, pauses, and completes — no pending-rollback bookkeeping.
	atomic.StoreInt32(&alterFail, 0)
	err = orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.NoError(t, err, "a re-run after a failed rollback must retry the pause and proceed")
	persisted = loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateSwitched, persisted.CurrentState)
}

func TestOrchestrator_Execute_PauseError_CtxCancelledMidUnfence_NoRestore(t *testing.T) {
	// Abuse case: the context is cancelled while the rollback's unfence is in
	// flight. The rollback cancels, state stays fenced, and the restore is
	// never attempted against a gateway in an unknown rollout state.
	var applyCalls, alterCalls int64
	ctx, cancel := context.WithCancel(context.Background())

	overrides := orchestratorOverrides{
		applyGatewayYAMLFn: func(c context.Context, namespace, name string, yaml []byte) error {
			n := atomic.AddInt64(&applyCalls, 1)
			if n == 1 {
				return nil // fence
			}
			cancel() // ctx dies mid-unfence
			return c.Err()
		},
	}

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateInitialized, nil, overrides)
	config.PauseConsumerOffsetSync = true
	config.InitialCrYAML = []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\n")

	orch.actions.clusterLinkService = &mockClusterLinkService{
		listConfigsFn: func(c context.Context, cfg clusterlink.Config) (map[string]string, error) {
			return map[string]string{"consumer.offset.sync.enable": "true"}, nil
		},
		alterConfigsFn: func(c context.Context, cfg clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			atomic.AddInt64(&alterCalls, 1)
			return fmt.Errorf("503 pause boom")
		},
	}

	err := orch.Execute(ctx, 0, "api-key", "api-secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503 pause boom", "the original pause error must surface")

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateFenced, persisted.CurrentState)
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
		applyGatewayYAMLFn: func(ctx context.Context, namespace, name string, yaml []byte) error {
			record("apply")
			return nil
		},
		waitForGatewayReadyFn: func(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
			record("wait-ready")
			return nil
		},
	}

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateLagsOk, nil, overrides)
	config.PauseConsumerOffsetSync = true
	config.DetectUnroutedProducersDuration = time.Millisecond
	config.InitialCrYAML = []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\n")
	config.ClusterLinkConfigs = map[string]string{"consumer.offset.sync.enable": "true"}

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

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateInitialized, persisted.CurrentState)
	assert.False(t, persisted.PauseConsumerOffsetSyncFlipped,
		"the rollback's restore must clear the flipped marker")

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

func TestOrchestrator_Execute_RollbackRestoreFails_StillLandsInitialized(t *testing.T) {
	// The restore half of the rollback is soft-fail: unfencing succeeded, so
	// clients are safe; a failed restore lands at initialized anyway with the
	// flipped marker kept and loud rollback-context guidance (not the
	// post-switchover wording).
	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateLagsOk, nil)
	config.PauseConsumerOffsetSync = true
	config.DetectUnroutedProducersDuration = time.Millisecond
	config.InitialCrYAML = []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\n")
	config.ClusterLinkConfigs = map[string]string{"consumer.offset.sync.enable": "true"}

	var listCalls int64
	orch.actions.clusterLinkService = &mockClusterLinkService{
		listConfigsFn: func(ctx context.Context, cfg clusterlink.Config) (map[string]string, error) {
			if atomic.AddInt64(&listCalls, 1) == 1 {
				return map[string]string{"consumer.offset.sync.enable": "true"}, nil
			}
			return nil, fmt.Errorf("network error during restore")
		},
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

	stderr := captureStderr(t, func() {
		err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrUnroutedProducers)
	})

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateInitialized, persisted.CurrentState,
		"a failed restore must not cancel the completed unfence")
	assert.True(t, persisted.PauseConsumerOffsetSyncFlipped,
		"the flipped marker stays set — a restore is still owed")

	assert.Contains(t, stderr, "unfenced", "guidance must carry the rollback context")
	assert.Contains(t, stderr, config.ClusterLinkName)
	assert.NotContains(t, stderr, "Migration completed",
		"the post-switchover wording is wrong for a rollback")
}

func TestOrchestrator_Execute_RollbackRestoreAlterFails_StillLandsInitialized(t *testing.T) {
	// Companion to the ListConfigs-fail case above: here the restore's diff read
	// succeeds but the AlterConfigs that re-enables sync fails. The restore half
	// is still soft-fail — the unfence already completed, so the run lands at
	// initialized with the flipped marker kept (a restore is still owed) and the
	// rollback-context remediation names the still-owed keys.
	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateLagsOk, nil)
	config.PauseConsumerOffsetSync = true
	config.DetectUnroutedProducersDuration = time.Millisecond
	config.InitialCrYAML = []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\n")
	config.ClusterLinkConfigs = map[string]string{"consumer.offset.sync.enable": "true"}

	var listCalls int64
	orch.actions.clusterLinkService = &mockClusterLinkService{
		listConfigsFn: func(ctx context.Context, cfg clusterlink.Config) (map[string]string, error) {
			if atomic.AddInt64(&listCalls, 1) == 1 {
				// Drift check before the pause disable: sync still enabled.
				return map[string]string{"consumer.offset.sync.enable": "true"}, nil
			}
			// Restore diff after the disable: sync is paused, so the restore
			// wants to set it back to true.
			return map[string]string{"consumer.offset.sync.enable": "false"}, nil
		},
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
		err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrUnroutedProducers)
	})

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateInitialized, persisted.CurrentState,
		"a failed restore alter must not cancel the completed unfence")
	assert.True(t, persisted.PauseConsumerOffsetSyncFlipped,
		"the flipped marker stays set — a restore is still owed")

	assert.Contains(t, stderr, "unfenced", "guidance must carry the rollback context")
	assert.Contains(t, stderr, "Still owed", "a failed restore alter must name the owed keys")
	assert.Contains(t, stderr, config.ClusterLinkName)
	assert.NotContains(t, stderr, "Migration completed",
		"the post-switchover wording is wrong for a rollback")
}

// TestOrchestrator_ExecuteFailure_EmitsStateMatchedGuidance joins the two halves
// that are otherwise only tested apart: WHERE a failed Execute leaves the FSM
// (config.CurrentState — the value the executor forwards) and WHAT
// WarnIfPausedOnExecuteFailure emits for that state. It drives a real failed
// Execute into each urgent fenced-family landing an operator can actually reach
// with the pause already flipped, then feeds the resulting config+error to the
// guidance exactly as cmd/migration/execute does. Guards against a landing-state
// change that would silently mis-shape the operator guidance while both isolated
// unit tests still pass.
func TestOrchestrator_ExecuteFailure_EmitsStateMatchedGuidance(t *testing.T) {
	validCR := []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\n")

	// enableTrue is the drift-check/happy list response used by every case.
	enableTrue := func(ctx context.Context, cfg clusterlink.Config) (map[string]string, error) {
		return map[string]string{"consumer.offset.sync.enable": "true"}, nil
	}
	alterOK := func(ctx context.Context, cfg clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
		return nil
	}

	tests := []struct {
		name            string
		overrides       orchestratorOverrides
		configure       func(orch *MigrationOrchestrator, config *MigrationConfig)
		wantState       string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "offset_sync_paused: rogue detected then unfence fails",
			overrides: orchestratorOverrides{
				// The fence applies the (literal) fenced CR; the rollback's
				// unfence applies the round-tripped initial CR carrying
				// "kind: Gateway". Failing only the latter cancels abort_fence,
				// so the FSM honestly rests at offset_sync_paused.
				applyGatewayYAMLFn: func(ctx context.Context, namespace, name string, yaml []byte) error {
					if strings.Contains(string(yaml), "kind: Gateway") {
						return fmt.Errorf("k8s API unavailable")
					}
					return nil
				},
			},
			configure: func(orch *MigrationOrchestrator, config *MigrationConfig) {
				config.PauseConsumerOffsetSync = true
				config.DetectUnroutedProducersDuration = time.Millisecond
				config.InitialCrYAML = validCR
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
			wantState:       StateOffsetSyncPaused,
			wantContains:    []string{"still fenced", "blocked", "kcp migration execute", "test-link", "consumer.offset.sync.enable"},
			wantNotContains: []string{"restore will run after a successful switchover", "rollback failed"},
		},
		{
			name:      "fence_verified: promote fails after a successful pause+verify",
			overrides: orchestratorOverrides{},
			configure: func(orch *MigrationOrchestrator, config *MigrationConfig) {
				config.PauseConsumerOffsetSync = true
				// Detection disabled: verify_fence is an immediate success, so
				// the promote failure rests the FSM at fence_verified.
				config.DetectUnroutedProducersDuration = 0
				config.InitialCrYAML = validCR
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
			wantState:       StateFenceVerified,
			wantContains:    []string{"still fenced", "blocked", "re-apply the initial gateway CR", "test-link"},
			wantNotContains: []string{"restore will run after a successful switchover", "complete the switchover", "Do not re-apply"},
		},
		{
			name: "promoted: switchover fails after a successful promote",
			overrides: orchestratorOverrides{
				// Fence and switchover both apply; only the switchover CR fails,
				// leaving the FSM at promoted (switch failures do not roll back).
				applyGatewayYAMLFn: func(ctx context.Context, namespace, name string, yaml []byte) error {
					if strings.Contains(string(yaml), "switchover") {
						return fmt.Errorf("switchover apply failed")
					}
					return nil
				},
			},
			configure: func(orch *MigrationOrchestrator, config *MigrationConfig) {
				config.PauseConsumerOffsetSync = true
				config.DetectUnroutedProducersDuration = 0
				config.InitialCrYAML = validCR
				originalCL := orch.actions.clusterLinkService
				orch.actions.clusterLinkService = &mockClusterLinkService{
					listMirrorTopicsFn:    originalCL.ListMirrorTopics,
					validateTopicsFn:      originalCL.ValidateTopics,
					promoteMirrorTopicsFn: originalCL.PromoteMirrorTopics,
					listConfigsFn:         enableTrue,
					alterConfigsFn:        alterOK,
				}
			},
			wantState:       StatePromoted,
			wantContains:    []string{"still fenced", "blocked", "complete the switchover", "Do not re-apply the initial gateway CR"},
			wantNotContains: []string{"restore will run after a successful switchover"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			orch, config, stateFilePath := newHappyPathOrchestrator(t, StateLagsOk, nil, tc.overrides)
			tc.configure(orch, config)

			err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
			require.Error(t, err)

			// The FSM rests in the expected urgent state, in memory (the value
			// the executor forwards to the guidance) and on disk.
			assert.Equal(t, tc.wantState, config.CurrentState, "in-memory landed state")
			persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
			assert.Equal(t, tc.wantState, persisted.CurrentState, "persisted landed state")
			require.True(t, config.PauseConsumerOffsetSyncFlipped,
				"the urgent guidance only fires when the pause was flipped")

			// Feed the landed config + error to the guidance exactly as the
			// executor does (cmd/migration/execute/migration_executor.go), and
			// assert the emitted copy matches the landed state.
			out := captureStderr(t, func() {
				WarnIfPausedOnExecuteFailure(config, err)
			})
			for _, want := range tc.wantContains {
				assert.Contains(t, out, want, "guidance for %s must mention %q", tc.wantState, want)
			}
			for _, notWant := range tc.wantNotContains {
				assert.NotContains(t, out, notWant, "guidance for %s must not mention %q", tc.wantState, notWant)
			}
		})
	}
}

func TestOrchestrator_Execute_NoOptIn_NeverTouchesClusterLinkConfig(t *testing.T) {
	// AE2 pin: the default flow's offset_sync_unchanged guarantee. Without the
	// opt-in the run passes through offset_sync_paused to switched with zero
	// AlterConfigs calls. (ListConfigs still runs once, in Initialize.)
	var alterCalls int64

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateUninitialized, nil)

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

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.NoError(t, err)

	assert.Equal(t, int64(0), atomic.LoadInt64(&alterCalls),
		"the default flow must never write cluster-link config")
	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateSwitched, persisted.CurrentState)
	assert.False(t, persisted.PauseConsumerOffsetSyncFlipped)
}

func TestOrchestrator_Execute_LegacyFlippedAtFenced_SkipsPauseAndProceeds(t *testing.T) {
	// AE7 pin: an in-flight state file from a release where the pause ran
	// pre-FSM (flipped marker set, state fenced) resumes without a second
	// pause: the stage passes through on the marker and promotion proceeds.
	var alterCalls, listCalls int64

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateFenced, nil)
	config.PauseConsumerOffsetSync = true
	config.PauseConsumerOffsetSyncFlipped = true

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

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.NoError(t, err)

	assert.Equal(t, int64(0), atomic.LoadInt64(&alterCalls), "no second pause")
	assert.Equal(t, int64(0), atomic.LoadInt64(&listCalls), "no drift re-check on the already-flipped path")
	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateSwitched, persisted.CurrentState)
	assert.True(t, persisted.PauseConsumerOffsetSyncFlipped, "marker stays set until the restore bookend clears it")
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
	// cluster-link name — never config values or credentials.
	orch, config, _ := newHappyPathOrchestrator(t, StateLagsOk, nil)
	config.PauseConsumerOffsetSync = true
	config.DetectUnroutedProducersDuration = time.Millisecond
	config.InitialCrYAML = []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\n")
	config.ClusterLinkConfigs = map[string]string{
		"consumer.offset.sync.enable":   "true",
		"consumer.offset.group.filters": `{"groups":["SENSITIVE-GROUP-FILTER"]}`,
	}

	var listCalls int64
	orch.actions.clusterLinkService = &mockClusterLinkService{
		listConfigsFn: func(ctx context.Context, cfg clusterlink.Config) (map[string]string, error) {
			if atomic.AddInt64(&listCalls, 1) == 1 {
				return map[string]string{"consumer.offset.sync.enable": "true"}, nil
			}
			return map[string]string{"consumer.offset.sync.enable": "false"}, nil
		},
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
			err := orch.Execute(context.Background(), 0, "api-key", "super-secret-value")
			require.Error(t, err)
		})
	})
	combined := stdout + stderrOut

	assert.Contains(t, combined, "Restoring consumer.offset.sync",
		"the rollback's restore must announce itself")
	assert.NotContains(t, combined, "SENSITIVE-GROUP-FILTER",
		"config values must never appear in rollback output")
	assert.NotContains(t, combined, "super-secret-value",
		"credentials must never appear in rollback output")
}

func TestOrchestrator_Execute_UnknownState_Fails(t *testing.T) {
	// A state value this binary does not know (corrupted file, or a file
	// written by a newer kcp) must fail loudly. Silently skipping every step
	// and printing "Migration complete!" is the failure mode this guards.
	orch, config, _ := newHappyPathOrchestrator(t, "bogus_state", nil)

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, err, "an unrecognized persisted state must not execute as a silent no-op")
	assert.Contains(t, err.Error(), "bogus_state")
	assert.Equal(t, "bogus_state", config.CurrentState,
		"the unknown state must be left untouched for the operator to inspect")
}

func TestOrchestrator_Execute_ResumeFromFenceVerified_RerunsDetection(t *testing.T) {
	// A rogue producer may appear between the run that verified the fence and
	// a later resume (e.g. promote failed, operator re-runs hours later).
	// fence_verified must not survive the restart: detection re-runs, catches
	// the rogue producer, and the abort_fence rollback fires.
	var sourceCallCount int64

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateFenceVerified, nil)
	config.DetectUnroutedProducersDuration = time.Millisecond
	config.InitialCrYAML = []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\n")

	// Rogue producer: source offsets keep increasing on every call
	orch.actions.sourceOffset = &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt64(&sourceCallCount, 1)
			return map[int32]int64{0: 100 + n*10}, nil
		},
	}

	// Deadline bounds the failure mode where detection is skipped and promote
	// polls forever on never-zero lag; the happy path finishes in milliseconds.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := orch.Execute(ctx, 0, "api-key", "api-secret")
	require.Error(t, err,
		"resume from fence_verified must re-run detection and catch the rogue producer")
	assert.ErrorIs(t, err, ErrUnroutedProducers)

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateInitialized, persisted.CurrentState,
		"detection on resume should roll back to initialized via abort_fence")
}

func TestOrchestrator_Execute_VerifyFetchError_NoRollback(t *testing.T) {
	// A transient offset-fetch failure during the detection window is not a
	// detection: it must propagate without ErrUnroutedProducers so the
	// orchestrator neither unfences the gateway nor rolls the FSM back.
	// Re-running execute resumes from offset_sync_paused and retries verification.
	var applyCalls int64
	overrides := orchestratorOverrides{
		applyGatewayYAMLFn: func(ctx context.Context, namespace, name string, yaml []byte) error {
			atomic.AddInt64(&applyCalls, 1)
			return nil
		},
	}

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateLagsOk, []string{"topic-a"}, overrides)
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

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrUnroutedProducers,
		"a fetch failure must not be classified as a detection")
	assert.Contains(t, err.Error(), "connection reset by peer")

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateOffsetSyncPaused, persisted.CurrentState,
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

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateFenced, nil, overrides)

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, err)

	// The verify_fence transition succeeded first (detection disabled → no-op),
	// then the promote transition was cancelled, so the FSM rests at
	// fence_verified — never promoted.
	assert.Equal(t, StateFenceVerified, config.CurrentState)

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateFenceVerified, persisted.CurrentState,
		"state file should rest at fence_verified after promote failure, never promoted")
}
