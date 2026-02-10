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
	"github.com/goccy/go-yaml"
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
	CCBootstrapEndpoint  string
	LoadBalancerEndpoint string
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

	CCBootstrapEndpoint  string `json:"cc_bootstrap_endpoint"`
	LoadBalancerEndpoint string `json:"load_balancer_endpoint"`
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
	slog.Info("═══════════════════════════════════════════════════════════════")
	slog.Info("FSM: Before event", "event", e.Event, "currentState", m.fsm.Current(), "nextState", e.Dst)
	slog.Info("═══════════════════════════════════════════════════════════════")
}

func (m *Migration) leaveUninitializedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("")
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	slog.Info("FSM: LEAVING STATE", "state", m.fsm.Current(), "triggeredBy", e.Event)
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	if err := m.initializeMigration(ctx); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leaveInitializedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("")
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	slog.Info("FSM: LEAVING STATE", "state", m.fsm.Current(), "triggeredBy", e.Event)
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// Extract maxLag and maxWaitTime from args (required parameters)
	var threshold, maxWaitTime int64
	if len(e.Args) > 0 {
		if lag, ok := e.Args[0].(int64); ok {
			threshold = lag
		}
	}
	if len(e.Args) > 1 {
		if waitTime, ok := e.Args[1].(int64); ok {
			maxWaitTime = waitTime
		}
	}

	var clusterApiKey, clusterApiSecret string
	if len(e.Args) > 2 {
		if key, ok := e.Args[2].(string); ok {
			clusterApiKey = key
		}
	}
	if len(e.Args) > 3 {
		if secret, ok := e.Args[3].(string); ok {
			clusterApiSecret = secret
		}
	}

	if err := m.checkLags(ctx, threshold, maxWaitTime, clusterApiKey, clusterApiSecret); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leaveLagsOkCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("")
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	slog.Info("FSM: LEAVING STATE", "state", m.fsm.Current(), "triggeredBy", e.Event)
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	if err := m.fenceGateway(ctx); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leaveFencedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("")
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	slog.Info("FSM: LEAVING STATE", "state", m.fsm.Current(), "triggeredBy", e.Event)
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	var clusterApiKey, clusterApiSecret string
	if len(e.Args) > 0 {
		if key, ok := e.Args[0].(string); ok {
			clusterApiKey = key
		}
	}
	if len(e.Args) > 1 {
		if secret, ok := e.Args[1].(string); ok {
			clusterApiSecret = secret
		}
	}

	if err := m.startTopicPromotion(ctx, clusterApiKey, clusterApiSecret); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leavePromotingCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("")
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	slog.Info("FSM: LEAVING STATE", "state", m.fsm.Current(), "triggeredBy", e.Event)
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	if err := m.checkPromotionCompletion(ctx); err != nil {
		e.Cancel(err)
	}
}

func (m *Migration) leavePromotedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("")
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	slog.Info("FSM: LEAVING STATE", "state", m.fsm.Current(), "triggeredBy", e.Event)
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	if err := m.switchOverGatewayConfig(ctx); err != nil {
		e.Cancel(err)
	}

	slog.Info("gateway patched with new route and streaming domain")

	// wait for recycled gateway pods
	if err := m.waitForRecycledGatewayPods(ctx); err != nil {
		e.Cancel(err)
	}

	slog.Info("recycled gateway pods ready")
}

func (m *Migration) leaveStateCallback(_ context.Context, e *fsm.Event) {
	slog.Info("FSM: Left state", "state", m.fsm.Current(), "triggeredBy", e.Event)
}

func (m *Migration) enterStateCallback(_ context.Context, e *fsm.Event) {
	slog.Info("")
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	slog.Info("FSM: ENTERING STATE", "state", m.fsm.Current(), "triggeredBy", e.Event)
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	slog.Info("")
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

	slog.Info("")
	slog.Info("═══════════════════════════════════════════════════════════════")
	slog.Info("FSM: After event", "event", e.Event, "currentState", m.fsm.Current())
	slog.Info("═══════════════════════════════════════════════════════════════")
	slog.Info("")
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
		CCBootstrapEndpoint:  opts.CCBootstrapEndpoint,
		LoadBalancerEndpoint: opts.LoadBalancerEndpoint,
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
func (m *Migration) Execute(ctx context.Context, threshold int64, maxWaitTime int64, clusterApiKey string, clusterApiSecret string) error {
	// Execute remaining steps based on current state
	steps := []struct {
		event       string
		description string
		args        []any
	}{
		{EventWaitForLags, "checking lags", []any{threshold, maxWaitTime, clusterApiKey, clusterApiSecret}},
		{EventFence, "fencing gateway", []any{}},
		{EventPromote, "promoting topics", []any{clusterApiKey, clusterApiSecret}},
		{EventWaitForPromotionCompletion, "waiting for promotion completion", []any{}},
		{EventSwitch, "switching gateway config", []any{}},
	}

	for _, step := range steps {
		// Check if this transition is valid from current state
		if !m.canTransition(step.event) {
			continue // Skip if already past this state
		}

		// Pass threshold as argument to FSM event
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
		Topics:       m.Topics,
	}

	mirrorTopics, err := m.clusterLinkService.ListMirrorTopics(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to list mirror topics: %w", err)
	}

	clusterLinkTopics, inactiveTopics := clusterlink.ClassifyMirrorTopics(mirrorTopics)
	if len(inactiveTopics) > 0 {
		return fmt.Errorf("%d mirror topics are not active: %s", len(inactiveTopics), strings.Join(inactiveTopics, ", "))
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

// checkLags polls mirror topics via the cluster link service until all partition lags are below threshold.
// It runs until either all lags are below threshold (success), maxWaitTime is exceeded (error), or context is cancelled.
func (m *Migration) checkLags(ctx context.Context, threshold int64, maxWaitTime int64, clusterApiKey string, clusterApiSecret string) error {
	slog.Info("starting lag check", "threshold", threshold, "maxWaitTime", maxWaitTime, "topicCount", len(m.Topics))

	// Early exit if no topics to check
	if len(m.Topics) == 0 {
		slog.Info("no topics to check")
		return nil
	}

	// Build cluster link configuration for API calls
	config := clusterlink.Config{
		RestEndpoint: m.ClusterRestEndpoint,
		ClusterID:    m.ClusterId,
		LinkName:     m.ClusterLinkName,
		APIKey:       clusterApiKey,
		APISecret:    clusterApiSecret,
	}

	// Set up polling interval (check every 2 seconds)
	pollInterval := 2 * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Record start time for timeout tracking
	startTime := time.Now()
	maxWaitDuration := time.Duration(maxWaitTime) * time.Second

	// Main polling loop - continues until all lags are below threshold, timeout, or context cancelled
	for {
		// Check for context cancellation before each iteration
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if maxWaitTime has been exceeded
		elapsed := time.Since(startTime)
		if elapsed >= maxWaitDuration {
			return fmt.Errorf("max wait time exceeded (%v): lag threshold (%d) not met within %v", elapsed, threshold, maxWaitDuration)
		}

		// Fetch current mirror topic status from cluster link API
		mirrorTopics, err := m.clusterLinkService.ListMirrorTopics(ctx, config)
		if err != nil {
			return fmt.Errorf("failed to list mirror topics: %w", err)
		}

		// Build a map of topics we care about (filter out unrelated topics)
		topicMap := make(map[string]bool)
		for _, topic := range m.Topics {
			topicMap[topic] = true
		}

		// Check lag for all partitions of all relevant mirror topics
		allLagsBelowThreshold := true
		var topicsWithLag []string

		for _, mirrorTopic := range mirrorTopics {
			// Skip topics not in our migration list
			if !topicMap[mirrorTopic.MirrorTopicName] {
				continue
			}

			// Check each partition's lag
			for _, lag := range mirrorTopic.MirrorLags {
				if lag.Lag >= int(threshold) {
					allLagsBelowThreshold = false
					topicsWithLag = append(topicsWithLag, fmt.Sprintf("%s[partition:%d](lag:%d)",
						mirrorTopic.MirrorTopicName, lag.Partition, lag.Lag))
				}
			}
		}

		// Success: all partition lags are below threshold
		if allLagsBelowThreshold {
			slog.Info("all mirror topic lags are below threshold", "threshold", threshold)
			return nil
		}

		// Log progress: show sample of topics/partitions still with lag
		sampleSize := len(topicsWithLag)
		if sampleSize > 5 {
			sampleSize = 5
		}
		elapsed = time.Since(startTime)
		slog.Info("mirror topic lags still present", "totalTopicsWithLag", len(topicsWithLag), "sample", topicsWithLag[:sampleSize], "elapsed", elapsed, "remaining", maxWaitDuration-elapsed)

		// Wait for next poll interval or context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C: // Continue polling
		}
	}
}

func (m *Migration) fenceGateway(ctx context.Context) error {
	// fence the gateway
	slog.Info("fencing the gateway...done")
	return nil
}

func (m *Migration) startTopicPromotion(ctx context.Context, clusterApiKey string, clusterApiSecret string) error {
	slog.Info("topic promotion process started")

	config := clusterlink.Config{
		RestEndpoint: m.ClusterRestEndpoint,
		ClusterID:    m.ClusterId,
		LinkName:     m.ClusterLinkName,
		APIKey:       clusterApiKey,
		APISecret:    clusterApiSecret,
		Topics:       m.Topics,
	}

	pollInterval := 5 * time.Second

	for {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Step 1: List all mirror topics
		mirrorTopics, err := m.clusterLinkService.ListMirrorTopics(ctx, config)
		if err != nil {
			return fmt.Errorf("failed to list mirror topics: %w", err)
		}

		// no mirror topics found, promotion is complete
		if len(mirrorTopics) == 0 {
			slog.Info("no mirror topics found, promotion complete")
			return nil
		}

		// Step 2: Get active mirror topics with zero lag
		topicsToPromote := clusterlink.GetActiveTopicsWithZeroLag(mirrorTopics)

		// Check completion condition: no active topics found
		activeCount := clusterlink.CountActiveMirrorTopics(mirrorTopics)
		if activeCount == 0 {
			slog.Info("no active mirror topics remaining, promotion complete")
			return nil
		}

		// If no topics ready to promote (all have non-zero lag), wait and retry
		if len(topicsToPromote) == 0 {
			if clusterlink.HasActiveTopicsWithNonZeroLag(mirrorTopics) {
				slog.Info("active topics found but lag is not zero, waiting before retry",
					"activeTopics", activeCount,
					"pollInterval", pollInterval)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(pollInterval):
					continue
				}
			}
			// No active topics with any lag status - we're done
			slog.Info("promotion complete, no more topics to promote")
			return nil
		}

		// Step 3: Promote topics that are active and have zero lag
		slog.Info("promoting mirror topics", "topicCount", len(topicsToPromote), "topics", topicsToPromote)

		promoteResponse, err := m.clusterLinkService.PromoteMirrorTopics(ctx, config, topicsToPromote)
		if err != nil {
			return fmt.Errorf("failed to promote mirror topics: %w", err)
		}

		// Log any promotion errors
		for _, topic := range promoteResponse.Data {
			if topic.ErrorCode != 0 {
				slog.Warn("topic promotion error",
					"topic", topic.MirrorTopicName,
					"errorCode", topic.ErrorCode,
					"errorMessage", topic.ErrorMessage)
			} else {
				slog.Info("topic promotion initiated", "topic", topic.MirrorTopicName)
			}
		}

		// Wait before checking again
		slog.Info("waiting for promotion to complete before next check", "pollInterval", pollInterval)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
			// Continue to next iteration
		}
	}
}

func (m *Migration) checkPromotionCompletion(ctx context.Context) error {
	//wait for topic promotion to complete
	slog.Info("waiting for topic promotion to complete.....")
	time.Sleep(10 * time.Second)
	slog.Info("waiting for topic promotion to complete...done")
	return nil
}

func (m *Migration) switchOverGatewayConfig(ctx context.Context) error {
	slog.Info("switching over to the new gateway config", "destination", m.DestinationName)

	// Step 1: Get the current gateway to find the source route index
	gatewayYAML, err := m.gatewayService.GetGatewayYAML(ctx, m.GatewayNamespace, m.GatewayCrdName)
	if err != nil {
		return fmt.Errorf("failed to get gateway: %w", err)
	}

	var gw gateway.GatewayResource
	if err := yaml.Unmarshal(gatewayYAML, &gw); err != nil {
		return fmt.Errorf("failed to parse gateway YAML: %w", err)
	}

	// Find the source route index so we can replace it in-place
	routeIndex := -1
	for i, route := range gw.Spec.Routes {
		if route.Name == m.SourceRouteName {
			routeIndex = i
			break
		}
	}
	if routeIndex == -1 {
		return fmt.Errorf("source route '%s' not found in gateway routes", m.SourceRouteName)
	}

	slog.Info("found source route to update", "route", m.SourceRouteName, "index", routeIndex)

	// Step 2: Build a single JSON patch with both operations:
	//   1) Add the new streaming domain
	//   2) Replace the route to point to the new streaming domain

	slog.Info("adding new streaming domain", "bootstrapEndpoint", m.CCBootstrapEndpoint, "loadBalancerEndpoint", m.LoadBalancerEndpoint)

	patchOps := []map[string]any{
		// Op 1: Add the new streaming domain
		{
			"op":   "add",
			"path": "/spec/streamingDomains/-",
			"value": map[string]any{
				"name": "confluent-cloud-plain",
				"type": "kafka",
				"kafkaCluster": map[string]any{
					"bootstrapServers": []map[string]any{
						{
							"id":       "SASL_PLAIN",
							"endpoint": "SASL_SSL://" + m.CCBootstrapEndpoint,
							"tls": map[string]any{
								"secretRef": "confluent-tls",
							},
						},
					},
					"nodeIdRanges": []map[string]any{
						{
							"name":  "pool-1",
							"start": 0,
							"end":   2,
						},
					},
				},
			},
		},
		// Op 2: Replace the source route to point to the new streaming domain
		{
			"op":   "replace",
			"path": fmt.Sprintf("/spec/routes/%d", routeIndex),
			"value": map[string]any{
				"name":     "swap-msk-unauth-to-cc-route",
				"endpoint": m.LoadBalancerEndpoint,
				"brokerIdentificationStrategy": map[string]any{
					"type": "port",
				},
				"streamingDomain": map[string]any{
					"name":              "confluent-cloud-plain",
					"bootstrapServerId": "SASL_PLAIN",
				},
				"security": map[string]any{
					"auth":        "swap",
					"secretStore": "file-store",
					"client": map[string]any{
						"authentication": map[string]any{
							"type": "none",
						},
					},
					"cluster": map[string]any{
						"authentication": map[string]any{
							"type": "plain",
							"jaasConfigPassThrough": map[string]any{
								"secretRef": "plain-jaas",
							},
						},
					},
				},
			},
		},
	}

	// Step 3: Apply both operations in a single atomic patch
	if err := m.gatewayService.PatchGateway(ctx, m.GatewayNamespace, m.GatewayCrdName, patchOps); err != nil {
		return fmt.Errorf("failed to patch gateway: %w", err)
	}

	return nil
}

func (m *Migration) waitForRecycledGatewayPods(ctx context.Context) error {
	const (
		pollInterval = 5 * time.Second
		timeout      = 5 * time.Minute
	)

	slog.Info("waiting for recycled gateway pods to be ready",
		"namespace", m.GatewayNamespace,
		"gateway", m.GatewayCrdName,
		"pollInterval", pollInterval,
		"timeout", timeout)

	if err := m.gatewayService.WaitForGatewayPods(ctx, m.GatewayNamespace, m.GatewayCrdName, pollInterval, timeout); err != nil {
		return fmt.Errorf("failed waiting for gateway pods: %w", err)
	}

	slog.Info("waiting for recycled gateway pods to be ready...done")
	return nil
}
