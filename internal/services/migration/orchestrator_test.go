package migration

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
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

	// ListMirrorTopics returns ACTIVE topics matching config.Topics
	mirrorTopics := make([]clusterlink.MirrorTopic, len(topics))
	for i, name := range topics {
		mirrorTopics[i] = clusterlink.MirrorTopic{
			MirrorTopicName: name,
			MirrorStatus:    clusterlink.MirrorStatusActive,
		}
	}

	promoteMirrorTopicsFn := func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
		data := make([]struct {
			MirrorTopicName string `json:"mirror_topic_name"`
			ErrorMessage    string `json:"error_message,omitempty"`
			ErrorCode       int    `json:"error_code,omitempty"`
		}, len(topicNames))
		for i, name := range topicNames {
			data[i].MirrorTopicName = name
		}
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
			return mirrorTopics, nil
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
	for _, startState := range []string{StateInitialized, StateLagsOk, StateFenced, StatePromoted} {
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

func TestOrchestrator_Execute_PromoteError(t *testing.T) {
	overrides := orchestratorOverrides{
		promoteMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			return nil, fmt.Errorf("confluent cloud API unavailable")
		},
	}

	orch, config, stateFilePath := newHappyPathOrchestrator(t, StateFenced, nil, overrides)

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, err)

	// State should remain at fenced — the promote transition was cancelled
	assert.Equal(t, StateFenced, config.CurrentState)

	// No state file should have been written for this run since no transition succeeded.
	// (We started at fenced and the first attempted transition fenced->promoted failed.)
	_, loadErr := NewMigrationStateFromFile(stateFilePath)
	if loadErr == nil {
		state, _ := NewMigrationStateFromFile(stateFilePath)
		if state != nil {
			m, getErr := state.GetMigrationById(config.MigrationId)
			if getErr == nil {
				assert.NotEqual(t, StatePromoted, m.CurrentState,
					"state file should NOT contain migration at promoted state after promote failure")
			}
		}
	}
}
