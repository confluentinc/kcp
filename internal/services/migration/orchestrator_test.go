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
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// orchestratorOverrides allows tests to customize mock behavior before construction.
type orchestratorOverrides struct {
	getGatewayYAMLFn      func(ctx context.Context, namespace, name string) ([]byte, error)
	applyGatewayYAMLFn    func(ctx context.Context, namespace, name string, yaml []byte) error
	promoteMirrorTopicsFn func(ctx context.Context, config clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error)
}

// newHappyPathOrchestrator builds an orchestrator where every workflow step succeeds.
// The returned config starts at the given initialState.
// Optional overrides allow customizing mock behavior before construction.
func newHappyPathOrchestrator(t *testing.T, initialState string, topics []string, overrides ...orchestratorOverrides) (*MigrationOrchestrator, *types.MigrationConfig, string) {
	t.Helper()

	if len(topics) == 0 {
		topics = []string{"topic-a", "topic-b"}
	}

	config := &types.MigrationConfig{
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

	// Apply overrides if provided
	if len(overrides) > 0 {
		o := overrides[0]
		if o.getGatewayYAMLFn != nil {
			getGatewayYAMLFn = o.getGatewayYAMLFn
		}
		if o.applyGatewayYAMLFn != nil {
			applyGatewayYAMLFn = o.applyGatewayYAMLFn
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

	workflow := NewMigrationWorkflowWithOffsets(gw, cl, srcOffset, dstOffset)

	stateDir := t.TempDir()
	stateFilePath := filepath.Join(stateDir, "migration-state.json")

	migrationState := types.NewMigrationState()

	orch := NewMigrationOrchestrator(config, workflow, migrationState, stateFilePath)

	return orch, config, stateFilePath
}

// loadPersistedMigration reads the state file and returns the migration config by ID.
func loadPersistedMigration(t *testing.T, stateFilePath, migrationID string) *types.MigrationConfig {
	t.Helper()
	state, err := types.NewMigrationStateFromFile(stateFilePath)
	require.NoError(t, err, "failed to load state file")
	m, err := state.GetMigrationById(migrationID)
	require.NoError(t, err, "migration %q not found in state file", migrationID)
	return m
}

// --- FSM transition tests ---

func TestOrchestrator_Initialize_FromUninitialized(t *testing.T) {
	orch, config, stateFilePath := newHappyPathOrchestrator(t, types.StateUninitialized, nil)

	err := orch.Initialize(context.Background(), "api-key", "api-secret")
	require.NoError(t, err)

	assert.Equal(t, types.StateInitialized, config.CurrentState)

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, types.StateInitialized, persisted.CurrentState)
}

func TestOrchestrator_Execute_FullWorkflow(t *testing.T) {
	orch, config, stateFilePath := newHappyPathOrchestrator(t, types.StateUninitialized, nil)

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.NoError(t, err)

	assert.Equal(t, types.StateSwitched, config.CurrentState)

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, types.StateSwitched, persisted.CurrentState)
}

func TestOrchestrator_Execute_ResumesFromState(t *testing.T) {
	for _, startState := range []string{types.StateInitialized, types.StateLagsOk, types.StateFenced, types.StatePromoted} {
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

			assert.Equal(t, types.StateSwitched, config.CurrentState)

			persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
			assert.Equal(t, types.StateSwitched, persisted.CurrentState)

			// For initialized and later states, init step should be skipped
			// (GetGatewayYAML is called during init, so 0 calls means init was skipped)
			if startState == types.StateInitialized {
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

	orch, config, stateFilePath := newHappyPathOrchestrator(t, types.StateUninitialized, nil, overrides)

	err := orch.Initialize(context.Background(), "api-key", "api-secret")
	require.Error(t, err)

	// Config state should NOT have advanced
	assert.Equal(t, types.StateUninitialized, config.CurrentState)

	// State file should not have been written (persistState is called after fsm.Event,
	// and fsm.Event returns error when the callback cancels)
	_, loadErr := types.NewMigrationStateFromFile(stateFilePath)
	if loadErr == nil {
		// If file exists, verify the migration is NOT at initialized
		state, _ := types.NewMigrationStateFromFile(stateFilePath)
		if state != nil {
			m, getErr := state.GetMigrationById(config.MigrationId)
			if getErr == nil {
				assert.NotEqual(t, types.StateInitialized, m.CurrentState,
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

	orch, config, stateFilePath := newHappyPathOrchestrator(t, types.StateUninitialized, nil, overrides)

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, err)

	// The orchestrator should have persisted state after each successful step.
	// Init (uninitialized -> initialized) succeeded and was persisted.
	// CheckLags (initialized -> lags_ok) succeeded and was persisted.
	// Fence (lags_ok -> fenced) failed, so the last persisted state should be lags_ok.
	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	assert.Equal(t, types.StateLagsOk, persisted.CurrentState)
}

func TestOrchestrator_Execute_PromoteError(t *testing.T) {
	overrides := orchestratorOverrides{
		promoteMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			return nil, fmt.Errorf("confluent cloud API unavailable")
		},
	}

	orch, config, stateFilePath := newHappyPathOrchestrator(t, types.StateFenced, nil, overrides)

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, err)

	// State should remain at fenced — the promote transition was cancelled
	assert.Equal(t, types.StateFenced, config.CurrentState)

	// No state file should have been written for this run since no transition succeeded.
	// (We started at fenced and the first attempted transition fenced->promoted failed.)
	_, loadErr := types.NewMigrationStateFromFile(stateFilePath)
	if loadErr == nil {
		state, _ := types.NewMigrationStateFromFile(stateFilePath)
		if state != nil {
			m, getErr := state.GetMigrationById(config.MigrationId)
			if getErr == nil {
				assert.NotEqual(t, types.StatePromoted, m.CurrentState,
					"state file should NOT contain migration at promoted state after promote failure")
			}
		}
	}
}
