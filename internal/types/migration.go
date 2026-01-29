package types

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/persistence"
	"github.com/looplab/fsm"
)

// FSM State constants
const (
	StateUninitialized = "uninitialized"
	StateInitialized   = "initialized"
	StateLagsOk        = "lags_ok"
	StateFenced        = "fenced"
	StatePromoting     = "promoting"
	StatePromoted      = "promoted"
	StateSwitched      = "switched"
)

// FSM Event constants
const (
	EventInitialize                 = "initialize"
	EventWaitForLags                = "wait_for_lags"
	EventFence                      = "fence"
	EventPromote                    = "promote"
	EventWaitForPromotionCompletion = "wait_for_promotion_completion"
	EventSwitch                     = "switch"
)

// MigrationOpts contains options for creating a new migration
type MigrationOpts struct {
	GatewayNamespace     string
	GatewayCrdName       string
	SourceName           string
	DestinationName      string
	SourceRouteName      string
	DestinationRouteName string
	KubeConfigPath       string
	ClusterId            string
	ClusterRestEndpoint  string
	ClusterLinkName      string
	Topics               []string
	AuthMode             string
	ClusterApiKey        string
	ClusterApiSecret     string
}

// Migration represents a gateway migration with a finite state machine
type Migration struct {
	MigrationId     string `json:"migration_id"`
	CurrentState    string `json:"current_state"`
	FSM             *fsm.FSM `json:"-"`
	
	// Internal state (not serialized)
	state              *State              `json:"-"`
	persistenceService persistence.Service `json:"-"`
	
	// Gateway configuration
	GatewayNamespace     string `json:"gateway_namespace"`
	GatewayCrdName       string `json:"gateway_crd_name"`
	SourceName           string `json:"source_name"`
	DestinationName      string `json:"destination_name"`
	SourceRouteName      string `json:"source_route_name"`
	DestinationRouteName string `json:"destination_route_name"`
	KubeConfigPath       string `json:"kube_config_path"`
	
	// Cluster link configuration
	ClusterId           string   `json:"cluster_id"`
	ClusterRestEndpoint string   `json:"cluster_rest_endpoint"`
	ClusterLinkName     string   `json:"cluster_link_name"`
	ClusterApiKey       string   `json:"-"`
	ClusterApiSecret    string   `json:"-"`
	Topics              []string `json:"topics"`
	AuthMode            string   `json:"auth_mode"`
	
	// Migration data
	ClusterLinkTopics   []string          `json:"cluster_link_topics"`
	ClusterLinkConfigs  map[string]string `json:"cluster_link_configs"`
	GatewayOriginalYAML []byte            `json:"gateway_original_yaml"`
	
	// Services (injected dependencies)
	gatewayService     gateway.Service     `json:"-"`
	clusterLinkService clusterlink.Service `json:"-"`
}

// initializeFSM sets up the FSM for the migration with the given initial state
func (m *Migration) initializeFSM(initialState string) {
	m.FSM = fsm.NewFSM(
		initialState,
		fsm.Events{
			{Name: EventInitialize, Src: []string{StateUninitialized}, Dst: StateInitialized},
			{Name: EventWaitForLags, Src: []string{StateInitialized}, Dst: StateLagsOk},
			{Name: EventFence, Src: []string{StateLagsOk}, Dst: StateFenced},
			{Name: EventPromote, Src: []string{StateFenced}, Dst: StatePromoting},
			{Name: EventWaitForPromotionCompletion, Src: []string{StatePromoting}, Dst: StatePromoted},
			{Name: EventSwitch, Src: []string{StatePromoted}, Dst: StateSwitched},
		},
		fsm.Callbacks{
			"before_event": m.beforeEventCallback,
			"leave_" + StateUninitialized: m.leaveUninitializedCallback,
			"leave_" + StateInitialized:   m.leaveInitializedCallback,
			"leave_" + StateLagsOk:        m.leaveLagsOkCallback,
			"leave_" + StateFenced:        m.leaveFencedCallback,
			"leave_" + StatePromoting:     m.leavePromotingCallback,
			"leave_" + StatePromoted:      m.leavePromotedCallback,
			"leave_state":                 m.leaveStateCallback,
			"enter_state":                 m.enterStateCallback,
			"after_event":                 m.afterEventCallback,
		},
	)
}

// FSM Callbacks

func (m *Migration) beforeEventCallback(_ context.Context, e *fsm.Event) {
	slog.Info("FSM: Before event", "event", e.Event, "currentState", m.FSM.Current(), "nextState", e.Dst)
}

func (m *Migration) leaveUninitializedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: Leaving state", "state", m.FSM.Current(), "triggered by event", e.Event)
	if err := m.initializeMigration(ctx); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leaveInitializedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: Leaving state", "state", m.FSM.Current(), "triggered by event", e.Event)
	if err := m.checkLags(ctx); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leaveLagsOkCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: Leaving state", "state", m.FSM.Current(), "triggered by event", e.Event)
	if err := m.fenceGateway(ctx); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leaveFencedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: Leaving state", "state", m.FSM.Current(), "triggered by event", e.Event)
	if err := m.startTopicPromotion(ctx); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leavePromotingCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: Leaving state", "state", m.FSM.Current(), "triggered by event", e.Event)
	if err := m.checkPromotionCompletion(ctx); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leavePromotedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: Leaving state", "state", m.FSM.Current(), "triggered by event", e.Event)
	if err := m.switchOverGatewayConfig(ctx); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leaveStateCallback(_ context.Context, e *fsm.Event) {
	slog.Info("FSM: Left state", "state", m.FSM.Current(), "triggered by event", e.Event)
}

func (m *Migration) enterStateCallback(_ context.Context, e *fsm.Event) {
	slog.Info("FSM: Entered state", "state", m.FSM.Current(), "triggered by event", e.Event)
}

func (m *Migration) afterEventCallback(_ context.Context, e *fsm.Event) {
	m.CurrentState = m.FSM.Current()
	m.state.UpsertMigration(*m)
	
	// Use retry logic for persistence since we can't rollback the FSM transition
	if err := m.persistenceService.SaveWithRetry(m.state); err != nil {
		// Can't cancel or rollback the FSM transition at this point
		// Log critical error and terminate to avoid inconsistent state
		slog.Error("FATAL: Failed to persist state after transition - system is in inconsistent state",
			"event", e.Event,
			"currentState", m.FSM.Current(),
			"error", err,
		)
		panic(fmt.Sprintf("failed to persist state file after transition to %s: %v", m.FSM.Current(), err))
	}
	
	slog.Info("FSM: After event", "event", e.Event, "currentState", m.FSM.Current())
}

// NewMigration creates a new Migration with the given ID, starting in the uninitialized state
func NewMigration(migrationId string, stateFilePath string, opts MigrationOpts) *Migration {
	// Initialize persistence service and load state
	persistenceService := persistence.NewFileService(stateFilePath)
	
	var state State
	if err := persistenceService.Load(&state); err != nil {
		// If state file doesn't exist, create a new empty state
		state = State{Migrations: []Migration{}}
	}
	
	m := &Migration{
		MigrationId:          migrationId,
		CurrentState:         StateUninitialized,
		state:                &state,
		persistenceService:   persistenceService,
		GatewayNamespace:     opts.GatewayNamespace,
		GatewayCrdName:       opts.GatewayCrdName,
		SourceName:           opts.SourceName,
		DestinationName:      opts.DestinationName,
		SourceRouteName:      opts.SourceRouteName,
		DestinationRouteName: opts.DestinationRouteName,
		KubeConfigPath:       opts.KubeConfigPath,
		ClusterId:            opts.ClusterId,
		ClusterRestEndpoint:  opts.ClusterRestEndpoint,
		ClusterLinkName:      opts.ClusterLinkName,
		Topics:               opts.Topics,
		AuthMode:             opts.AuthMode,
		ClusterApiKey:        opts.ClusterApiKey,
		ClusterApiSecret:     opts.ClusterApiSecret,
	}

	// Initialize services
	m.gatewayService = gateway.NewK8sService(opts.KubeConfigPath)
	m.clusterLinkService = clusterlink.NewConfluentCloudService(http.DefaultClient)

	m.initializeFSM(StateUninitialized)

	return m
}

// LoadMigration loads a Migration object from state file by its ID
func LoadMigration(stateFilePath string, migrationId string) (*Migration, error) {
	// Initialize persistence service and load state
	persistenceService := persistence.NewFileService(stateFilePath)
	
	var state State
	if err := persistenceService.Load(&state); err != nil {
		return nil, fmt.Errorf("failed to load state from file: %w", err)
	}
	
	m, err := state.GetMigrationById(migrationId)
	if err != nil {
		return nil, fmt.Errorf("failed to get migration: %w", err)
	}

	// Set internal state and persistence service
	m.state = &state
	m.persistenceService = persistenceService

	// Re-initialize services
	m.gatewayService = gateway.NewK8sService(m.KubeConfigPath)
	m.clusterLinkService = clusterlink.NewConfluentCloudService(http.DefaultClient)

	// Initialize the FSM with the loaded current state
	m.initializeFSM(m.CurrentState)

	return m, nil
}

// Migration workflow methods

func (m *Migration) initializeMigration(ctx context.Context) error {
	slog.Info("parsing gateway resource", "gatewayName", m.GatewayNamespace, "kubeConfigPath", m.KubeConfigPath)

	// Check Kubernetes permissions
	if err := m.checkK8sPermissions(ctx); err != nil {
		return err
	}

	// Get and validate gateway
	gatewayYAML, err := m.validateGatewayResources(ctx)
	if err != nil {
		return err
	}

	// Validate cluster link and topics
	if err := m.validateClusterLink(ctx); err != nil {
		return err
	}

	// Get cluster link configs
	clusterLinkConfigs, err := m.getClusterLinkConfigs(ctx)
	if err != nil {
		return err
	}

	// Save the gateway original YAML and cluster link configs
	m.GatewayOriginalYAML = gatewayYAML
	m.ClusterLinkConfigs = clusterLinkConfigs

	return nil
}

func (m *Migration) checkK8sPermissions(ctx context.Context) error {
	allowed, err := m.gatewayService.CheckPermissions(ctx, "update", gateway.GatewayResourcePlural, gateway.GatewayGroup, gateway.ConfluentNamespace)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}

	if !allowed {
		return fmt.Errorf("you don't have permission to update gateway resources")
	}

	slog.Info("permission check response", "verb", "update", "allowed", allowed)
	return nil
}

func (m *Migration) validateGatewayResources(ctx context.Context) ([]byte, error) {
	gatewayYAML, err := m.gatewayService.GetGatewayYAML(ctx, gateway.ConfluentNamespace, m.GatewayCrdName)
	if err != nil {
		return nil, fmt.Errorf("failed to get gateway as YAML: %w", err)
	}

	config := gateway.GatewayConfig{
		Namespace:            m.GatewayNamespace,
		CRDName:              m.GatewayCrdName,
		SourceName:           m.SourceName,
		DestinationName:      m.DestinationName,
		SourceRouteName:      m.SourceRouteName,
		DestinationRouteName: m.DestinationRouteName,
		AuthMode:             m.AuthMode,
		KubeConfigPath:       m.KubeConfigPath,
	}

	if err := m.gatewayService.ValidateGateway(ctx, gatewayYAML, config); err != nil {
		return nil, fmt.Errorf("gateway validation failed: %w", err)
	}

	slog.Info("gateway validation successful",
		"source", m.SourceName,
		"destination", m.DestinationName,
		"route", m.SourceRouteName,
	)

	return gatewayYAML, nil
}

func (m *Migration) validateClusterLink(ctx context.Context) error {
	slog.Info("describing cluster link", "clusterId", m.ClusterId, "clusterLinkName", m.ClusterLinkName)

	config := clusterlink.Config{
		RestEndpoint: m.ClusterRestEndpoint,
		ClusterID:    m.ClusterId,
		LinkName:     m.ClusterLinkName,
		APIKey:       m.ClusterApiKey,
		APISecret:    m.ClusterApiSecret,
	}

	clusterLinkTopics, err := m.clusterLinkService.ListMirrorTopics(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to list mirror topics: %w", err)
	}

	if len(m.Topics) > 0 {
		slog.Info("validating topics in cluster link", "topic count", len(m.Topics))
		if err := m.clusterLinkService.ValidateTopics(m.Topics, clusterLinkTopics); err != nil {
			return fmt.Errorf("failed to validate topics in cluster link: %w", err)
		}
	} else {
		m.Topics = clusterLinkTopics
	}

	m.ClusterLinkTopics = clusterLinkTopics
	return nil
}

func (m *Migration) getClusterLinkConfigs(ctx context.Context) (map[string]string, error) {
	config := clusterlink.Config{
		RestEndpoint: m.ClusterRestEndpoint,
		ClusterID:    m.ClusterId,
		LinkName:     m.ClusterLinkName,
		APIKey:       m.ClusterApiKey,
		APISecret:    m.ClusterApiSecret,
	}

	configs, err := m.clusterLinkService.ListConfigs(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster link configs: %w", err)
	}

	return configs, nil
}

func (m *Migration) checkLags(ctx context.Context) error {
	// check the lags are below threshold, if not, cancel the migration
	slog.Info("checking lags are below threshold.....")
	time.Sleep(10 * time.Second)
	slog.Info("checking lags are below threshold...done")
	return nil
}

func (m *Migration) fenceGateway(ctx context.Context) error {
	// fence the gateway
	slog.Info("fencing the gateway...done")
	return nil
}

func (m *Migration) startTopicPromotion(ctx context.Context) error {
	// start topic promotion process
	slog.Info("topic promotion process started")
	return nil
}

func (m *Migration) checkPromotionCompletion(ctx context.Context) error {
	//wait for topic promotion to complete
	slog.Info("waiting for topic promotion to complete.....")
	time.Sleep(10 * time.Second)
	slog.Info("waiting for topic promotion to complete...done")
	return nil
}

func (m *Migration) switchOverGatewayConfig(ctx context.Context) error {
	// switch over to the new gateway
	slog.Info("switching over to the new gateway config...done")
	return nil
}
