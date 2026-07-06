package migration

import (
	"context"
	"fmt"
	"path/filepath"
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
	for _, startState := range []string{StateInitialized, StateLagsOk, StateFenced, StateFenceVerified, StatePromoted} {
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
	var unfenceCalled bool

	overrides := orchestratorOverrides{
		applyGatewayYAMLFn: func(ctx context.Context, namespace, name string, yaml []byte) error {
			// Track unfence calls (the second apply is the unfence after detection)
			unfenceCalled = true
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

	// Gateway should have been unfenced
	assert.True(t, unfenceCalled, "gateway should be unfenced after detecting unrouted producers")

	// PromoteTopics should NOT have been called (detection aborts before promotion)
	assert.Equal(t, int64(0), atomic.LoadInt64(&promoteCallCount),
		"PromoteMirrorTopics should not be called when unrouted producers are detected")
}

func TestOrchestrator_Execute_UnroutedProducers_UnfenceFails_StaysAtFenced(t *testing.T) {
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

	// State must remain at fenced: the abort_fence transition is cancelled when
	// unfenceGateway fails, so it is never persisted as initialized. The last
	// successful transition was lags_ok → fenced.
	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateFenced, persisted.CurrentState,
		"state should remain at fenced when unfencing fails")
}

func TestOrchestrator_Execute_UnroutedProducers_UnfenceReadinessFails_StaysAtFenced(t *testing.T) {
	// The unfence CR applies cleanly but the gateway never converges to Ready.
	// The abort_fence rollback must be cancelled — persisting initialized while
	// the gateway is still mid-rollout would misrepresent reality.
	var waitCallCount int64
	var sourceCallCount int64

	overrides := orchestratorOverrides{
		waitForGatewayReadyFn: func(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
			n := atomic.AddInt64(&waitCallCount, 1)
			if n == 1 {
				// First wait is the fence rollout — succeed
				return nil
			}
			// Second wait is the unfence rollout — fail
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

	assert.Equal(t, int64(2), atomic.LoadInt64(&waitCallCount),
		"gateway readiness should be awaited for both the fence and the unfence rollout")

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, StateFenced, persisted.CurrentState,
		"state should remain at fenced when the unfence rollout never becomes ready")
}

func TestOrchestrator_Execute_VerifyFencePersistedBeforePromote(t *testing.T) {
	// A successful unrouted-producer check is its own FSM transition
	// (fenced → fence_verified), persisted before promotion starts like every
	// other step. The persisted value is informational — bootstrap demotes it
	// back to fenced on the next run (see TestOrchestrator_Bootstrap_DemotesFenceVerified)
	// — but it must still record the FSM's true mid-run position, never promoted.
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
	// restart: construction must demote it to fenced so detection re-runs.
	_, config, _ := newHappyPathOrchestrator(t, StateFenceVerified, nil)

	assert.Equal(t, StateFenced, config.CurrentState,
		"bootstrap should demote a persisted fence_verified to fenced")
}

func TestOrchestrator_ExpireVerificationIsAnFSMEdge(t *testing.T) {
	// The bootstrap demotion must be modelled as an FSM transition
	// (expire_verification: fence_verified → fenced), not a config mutation
	// the machine never sees — so it fires through the FSM callbacks and
	// appears in fsm.Visualize output alongside abort_fence.
	orch, _, _ := newHappyPathOrchestrator(t, StateUninitialized, nil)

	assert.Contains(t, fsm.Visualize(orch.fsm),
		`"fence_verified" -> "fenced" [ label = "expire_verification" ];`,
		"expire_verification should be a visible edge in the state machine")
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
	// Re-running execute resumes from fenced and retries verification.
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
	assert.Equal(t, StateFenced, persisted.CurrentState,
		"state must stay fenced — no abort_fence rollback on a fetch error")

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
