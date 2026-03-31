package migration

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/types"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// newHappyPathOrchestrator builds an orchestrator where every workflow step succeeds.
// The returned config starts at the given initialState.
func newHappyPathOrchestrator(t *testing.T, initialState string, topics []string) (*MigrationOrchestrator, *types.MigrationConfig, string) {
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

	gw := &mockGatewayService{
		getGatewayYAMLFn: func(ctx context.Context, namespace, name string) ([]byte, error) {
			return []byte("initial-yaml"), nil
		},
		validateGatewayCRsFn: func(initial, fenced, switchover []byte) error {
			return nil
		},
		applyGatewayYAMLFn: func(ctx context.Context, namespace, name string, yaml []byte) error {
			return nil
		},
		getGatewayPodUIDsFn: func(ctx context.Context, namespace, name string) (map[k8stypes.UID]struct{}, error) {
			return map[k8stypes.UID]struct{}{
				"uid-1": {},
				"uid-2": {},
			}, nil
		},
		waitForGatewayPodsFn: nil, // nil returns nil (success)
	}

	// ListMirrorTopics returns ACTIVE topics matching config.Topics
	mirrorTopics := make([]clusterlink.MirrorTopic, len(topics))
	for i, name := range topics {
		mirrorTopics[i] = clusterlink.MirrorTopic{
			MirrorTopicName: name,
			MirrorStatus:    clusterlink.MirrorStatusActive,
		}
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
		promoteMirrorTopicsFn: func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
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
	if err != nil {
		t.Fatalf("failed to load state file: %v", err)
	}
	m, err := state.GetMigrationById(migrationID)
	if err != nil {
		t.Fatalf("migration %q not found in state file: %v", migrationID, err)
	}
	return m
}

// --- FSM transition tests ---

func TestOrchestrator_Initialize_FromUninitialized(t *testing.T) {
	orch, config, stateFilePath := newHappyPathOrchestrator(t, types.StateUninitialized, nil)

	if err := orch.Initialize(context.Background(), "api-key", "api-secret"); err != nil {
		t.Fatalf("Initialize returned error: %v", err)
	}

	if config.CurrentState != types.StateInitialized {
		t.Errorf("expected current state %q, got %q", types.StateInitialized, config.CurrentState)
	}

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	if persisted.CurrentState != types.StateInitialized {
		t.Errorf("persisted state: expected %q, got %q", types.StateInitialized, persisted.CurrentState)
	}
}

func TestOrchestrator_Execute_FullWorkflow(t *testing.T) {
	orch, config, stateFilePath := newHappyPathOrchestrator(t, types.StateUninitialized, nil)

	if err := orch.Execute(context.Background(), 0, "api-key", "api-secret"); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if config.CurrentState != types.StateSwitched {
		t.Errorf("expected final state %q, got %q", types.StateSwitched, config.CurrentState)
	}

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	if persisted.CurrentState != types.StateSwitched {
		t.Errorf("persisted state: expected %q, got %q", types.StateSwitched, persisted.CurrentState)
	}
}

func TestOrchestrator_Execute_ResumesFromInitialized(t *testing.T) {
	orch, config, stateFilePath := newHappyPathOrchestrator(t, types.StateInitialized, nil)

	// Track calls to GetGatewayYAML — the init step calls this, so if it's called the
	// orchestrator did NOT skip the init step.
	var getYAMLCalls int32
	orch.workflow.gatewayService.(*mockGatewayService).getGatewayYAMLFn = func(ctx context.Context, namespace, name string) ([]byte, error) {
		atomic.AddInt32(&getYAMLCalls, 1)
		return []byte("initial-yaml"), nil
	}

	if err := orch.Execute(context.Background(), 0, "api-key", "api-secret"); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if config.CurrentState != types.StateSwitched {
		t.Errorf("expected final state %q, got %q", types.StateSwitched, config.CurrentState)
	}

	if atomic.LoadInt32(&getYAMLCalls) != 0 {
		t.Errorf("expected GetGatewayYAML not to be called (init step should be skipped), but it was called %d times", getYAMLCalls)
	}

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	if persisted.CurrentState != types.StateSwitched {
		t.Errorf("persisted state: expected %q, got %q", types.StateSwitched, persisted.CurrentState)
	}
}

func TestOrchestrator_Execute_ResumesFromLagsOk(t *testing.T) {
	orch, config, stateFilePath := newHappyPathOrchestrator(t, types.StateLagsOk, nil)

	if err := orch.Execute(context.Background(), 0, "api-key", "api-secret"); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if config.CurrentState != types.StateSwitched {
		t.Errorf("expected final state %q, got %q", types.StateSwitched, config.CurrentState)
	}

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	if persisted.CurrentState != types.StateSwitched {
		t.Errorf("persisted state: expected %q, got %q", types.StateSwitched, persisted.CurrentState)
	}
}

func TestOrchestrator_Execute_ResumesFromFenced(t *testing.T) {
	orch, config, stateFilePath := newHappyPathOrchestrator(t, types.StateFenced, nil)

	if err := orch.Execute(context.Background(), 0, "api-key", "api-secret"); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if config.CurrentState != types.StateSwitched {
		t.Errorf("expected final state %q, got %q", types.StateSwitched, config.CurrentState)
	}

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	if persisted.CurrentState != types.StateSwitched {
		t.Errorf("persisted state: expected %q, got %q", types.StateSwitched, persisted.CurrentState)
	}
}

func TestOrchestrator_Execute_ResumesFromPromoted(t *testing.T) {
	orch, config, stateFilePath := newHappyPathOrchestrator(t, types.StatePromoted, nil)

	if err := orch.Execute(context.Background(), 0, "api-key", "api-secret"); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if config.CurrentState != types.StateSwitched {
		t.Errorf("expected final state %q, got %q", types.StateSwitched, config.CurrentState)
	}

	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	if persisted.CurrentState != types.StateSwitched {
		t.Errorf("persisted state: expected %q, got %q", types.StateSwitched, persisted.CurrentState)
	}
}

// --- Error handling tests ---

func TestOrchestrator_Initialize_WorkflowError(t *testing.T) {
	orch, config, stateFilePath := newHappyPathOrchestrator(t, types.StateUninitialized, nil)

	// Make GetGatewayYAML fail — this is the first call in Initialize workflow step
	orch.workflow.gatewayService.(*mockGatewayService).getGatewayYAMLFn = func(ctx context.Context, namespace, name string) ([]byte, error) {
		return nil, fmt.Errorf("k8s connection refused")
	}

	err := orch.Initialize(context.Background(), "api-key", "api-secret")
	if err == nil {
		t.Fatal("expected Initialize to return an error, got nil")
	}

	// Config state should NOT have advanced
	if config.CurrentState != types.StateUninitialized {
		t.Errorf("expected state to remain %q after error, got %q", types.StateUninitialized, config.CurrentState)
	}

	// State file should not have been written (persistState is called after fsm.Event,
	// and fsm.Event returns error when the callback cancels)
	_, loadErr := types.NewMigrationStateFromFile(stateFilePath)
	if loadErr == nil {
		// If file exists, verify the migration is NOT at initialized
		state, _ := types.NewMigrationStateFromFile(stateFilePath)
		if state != nil {
			m, getErr := state.GetMigrationById(config.MigrationId)
			if getErr == nil && m.CurrentState == types.StateInitialized {
				t.Error("state file should NOT contain migration at initialized state after init failure")
			}
		}
	}
	// If file doesn't exist, that's fine — no state was persisted
}

func TestOrchestrator_Execute_FenceError(t *testing.T) {
	orch, config, stateFilePath := newHappyPathOrchestrator(t, types.StateUninitialized, nil)

	// Make ApplyGatewayYAML fail — this is called during FenceGateway (lags_ok -> fenced)
	// GetGatewayPodUIDs is called first in FenceGateway, so let that succeed.
	// Then ApplyGatewayYAML fails.
	orch.workflow.gatewayService.(*mockGatewayService).applyGatewayYAMLFn = func(ctx context.Context, namespace, name string, yaml []byte) error {
		return fmt.Errorf("apply gateway failed: forbidden")
	}

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	if err == nil {
		t.Fatal("expected Execute to return an error, got nil")
	}

	// The orchestrator should have persisted state after each successful step.
	// Init (uninitialized -> initialized) succeeded and was persisted.
	// CheckLags (initialized -> lags_ok) succeeded and was persisted.
	// Fence (lags_ok -> fenced) failed, so the last persisted state should be lags_ok.
	persisted := loadPersistedMigration(t, stateFilePath, config.MigrationId)
	if persisted.CurrentState != types.StateLagsOk {
		t.Errorf("expected persisted state %q (last successful), got %q", types.StateLagsOk, persisted.CurrentState)
	}
}

func TestOrchestrator_Execute_PromoteError(t *testing.T) {
	orch, config, stateFilePath := newHappyPathOrchestrator(t, types.StateFenced, nil)

	// Make PromoteMirrorTopics fail
	orch.workflow.clusterLinkService.(*mockClusterLinkService).promoteMirrorTopicsFn = func(ctx context.Context, cfg clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
		return nil, fmt.Errorf("confluent cloud API unavailable")
	}

	err := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	if err == nil {
		t.Fatal("expected Execute to return an error, got nil")
	}

	// State should remain at fenced — the promote transition was cancelled
	if config.CurrentState != types.StateFenced {
		t.Errorf("expected config state to remain %q, got %q", types.StateFenced, config.CurrentState)
	}

	// No state file should have been written for this run since no transition succeeded.
	// (We started at fenced and the first attempted transition fenced->promoted failed.)
	_, loadErr := types.NewMigrationStateFromFile(stateFilePath)
	if loadErr == nil {
		state, _ := types.NewMigrationStateFromFile(stateFilePath)
		if state != nil {
			m, getErr := state.GetMigrationById(config.MigrationId)
			if getErr == nil && m.CurrentState == types.StatePromoted {
				t.Error("state file should NOT contain migration at promoted state after promote failure")
			}
		}
	}
}
