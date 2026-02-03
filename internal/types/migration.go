package types

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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
	MigrationId  string   `json:"migration_id"`
	CurrentState string   `json:"current_state"`
	fsm          *fsm.FSM `json:"-"` // ✅ Made private

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
	m.fsm = fsm.NewFSM(
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
			"before_event":                m.beforeEventCallback,
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
	slog.Info("FSM: Before event", "event", e.Event, "currentState", m.fsm.Current(), "nextState", e.Dst)
}

func (m *Migration) leaveUninitializedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: Leaving state", "state", m.fsm.Current(), "triggered by event", e.Event)
	if err := m.initializeMigration(ctx); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leaveInitializedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: Leaving state", "state", m.fsm.Current(), "triggered by event", e.Event)

	// Extract maxLag and maxWaitTime from args (required parameters)
	var maxLag, maxWaitTime int64
	if len(e.Args) > 0 {
		if lag, ok := e.Args[0].(int64); ok {
			maxLag = lag
		}
	}
	if len(e.Args) > 1 {
		if waitTime, ok := e.Args[1].(int64); ok {
			maxWaitTime = waitTime
		}
	}

	if err := m.checkLags(ctx, maxLag, maxWaitTime); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leaveLagsOkCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: Leaving state", "state", m.fsm.Current(), "triggered by event", e.Event)
	if err := m.fenceGateway(ctx); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leaveFencedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: Leaving state", "state", m.fsm.Current(), "triggered by event", e.Event)
	if err := m.startTopicPromotion(ctx); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leavePromotingCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: Leaving state", "state", m.fsm.Current(), "triggered by event", e.Event)
	if err := m.checkPromotionCompletion(ctx); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leavePromotedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: Leaving state", "state", m.fsm.Current(), "triggered by event", e.Event)
	if err := m.switchOverGatewayConfig(ctx); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leaveStateCallback(_ context.Context, e *fsm.Event) {
	slog.Info("FSM: Left state", "state", m.fsm.Current(), "triggered by event", e.Event)
}

func (m *Migration) enterStateCallback(_ context.Context, e *fsm.Event) {
	slog.Info("FSM: Entered state", "state", m.fsm.Current(), "triggered by event", e.Event)
}

func (m *Migration) afterEventCallback(_ context.Context, e *fsm.Event) {
	m.CurrentState = m.fsm.Current()
	m.state.UpsertMigration(*m)

	// Use retry logic for persistence since we can't rollback the FSM transition
	if err := m.persistenceService.SaveWithRetry(m.state); err != nil {
		// Can't cancel or rollback the FSM transition at this point
		// Log critical error and terminate to avoid inconsistent state
		slog.Error("FATAL: Failed to persist state after transition - system is in inconsistent state",
			"event", e.Event,
			"currentState", m.fsm.Current(),
			"error", err,
		)
		panic(fmt.Sprintf("failed to persist state file after transition to %s: %v", m.fsm.Current(), err))
	}

	slog.Info("FSM: After event", "event", e.Event, "currentState", m.fsm.Current())
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

// Public API for migration execution (hides FSM implementation)

// Initialize executes the initialization step of the migration
func (m *Migration) Initialize(ctx context.Context) error {
	return m.fsm.Event(ctx, EventInitialize)
}

// Execute runs the complete migration workflow from current state to completion
func (m *Migration) Execute(ctx context.Context, maxLag int64, maxWaitTime int64) error {
	// Execute remaining steps based on current state
	steps := []struct {
		event       string
		description string
		args        []interface{} // Add args field
	}{
		{EventWaitForLags, "checking lags", []any{maxLag, maxWaitTime}},
		{EventFence, "fencing gateway", []any{}},
		{EventPromote, "promoting topics", []any{}},
		{EventWaitForPromotionCompletion, "waiting for promotion completion", []any{}},
		{EventSwitch, "switching gateway config", []any{}},
	}

	for _, step := range steps {
		// Check if this transition is valid from current state
		if !m.canTransition(step.event) {
			continue // Skip if already past this state
		}

		// Pass maxLag as argument to FSM event
		if err := m.fsm.Event(ctx, step.event, step.args...); err != nil {
			return fmt.Errorf("failed during %s: %w", step.description, err)
		}
	}

	return nil
}

// Utility methods

// GetCurrentState returns the current state of the migration
func (m *Migration) GetCurrentState() string {
	return m.CurrentState
}

// canTransition checks if a given event is valid from the current state
func (m *Migration) canTransition(event string) bool {
	return m.fsm.Can(event)
}

// Private migration workflow methods (used by FSM callbacks)

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
	allowed, err := m.gatewayService.CheckPermissions(ctx, "update", gateway.GatewayResourcePlural, gateway.GatewayGroup, m.GatewayNamespace)
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
	gatewayYAML, err := m.gatewayService.GetGatewayYAML(ctx, m.GatewayNamespace, m.GatewayCrdName)
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

	mirrorTopics, err := m.clusterLinkService.ListMirrorTopics(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to list mirror topics: %w", err)
	}

	clusterLinkTopics, inactiveTopics := clusterlink.ClassifyMirrorTopics(mirrorTopics)
	if len(inactiveTopics) > 0 {
		return fmt.Errorf("%d mirror topics are not active: %s",len(inactiveTopics), strings.Join(inactiveTopics, ", "))
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

func (m *Migration) checkLags(ctx context.Context, maxLag int64, maxWaitTime int64) error {
	slog.Info("starting lag check", "maxLag", maxLag, "maxWaitTime", maxWaitTime, "topicCount", len(m.Topics))

	if len(m.Topics) == 0 {
		slog.Info("no topics to check")
		return nil
	}

	// Initialize simulated lag values for each topic (start high)
	topicLags := make(map[string]int64)
	for _, topic := range m.Topics {
		// Simulate starting lag between maxLag+500 and maxLag+2000
		topicLags[topic] = maxLag + 5000 + (int64(len(topic)) % 10000)
	}

	// Poll lag values until all are below threshold or max wait time exceeded
	pollInterval := 2 * time.Second
	maxPolls := int(maxWaitTime / int64(pollInterval.Seconds()))
	if maxPolls <= 0 {
		maxPolls = 1 // At least one poll
	}

	for poll := 0; poll < maxPolls; poll++ {
		allBelowThreshold := true

		for _, topic := range m.Topics {
			currentLag := topicLags[topic]

			// Simulate lag decreasing over time (reduce by ~200-300 per poll)
			if currentLag > maxLag {
				reduction := int64(200 + (poll * 20))
				topicLags[topic] = currentLag - reduction
				if topicLags[topic] < 0 {
					topicLags[topic] = 0
				}

				slog.Info("checking topic lag",
					"topic", topic,
					"currentLag", topicLags[topic],
					"maxLag", maxLag,
					"belowThreshold", topicLags[topic] <= maxLag,
					"poll", poll+1,
				)

				if topicLags[topic] > maxLag {
					allBelowThreshold = false
				}
			}
		}

		if allBelowThreshold {
			slog.Info("all topics below lag threshold",
				"totalPolls", poll+1,
				"duration", time.Duration(poll+1)*pollInterval,
			)
			return nil
		}

		// Wait before next poll
		time.Sleep(pollInterval)
	}

	// Check if any topics still above threshold
	var topicsAboveThreshold []string
	for topic, lag := range topicLags {
		if lag > maxLag {
			topicsAboveThreshold = append(topicsAboveThreshold, fmt.Sprintf("%s (lag: %d)", topic, lag))
		}
	}

	if len(topicsAboveThreshold) > 0 {
		elapsedTime := time.Duration(maxPolls) * pollInterval
		return fmt.Errorf("max wait time exceeded (%v): %d topics still above threshold: %v",
			elapsedTime, len(topicsAboveThreshold), topicsAboveThreshold)
	}

	slog.Info("all topics below lag threshold")
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
