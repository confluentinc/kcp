package init

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/persistence"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/google/uuid"
)

type MigrationInitializerOpts struct {
	migrationStateFile string
	skipValidate       bool

	gatewayNamespace     string
	gatewayCrdName       string
	sourceName           string
	destinationName      string
	sourceRouteName      string
	destinationRouteName string
	kubeConfigPath       string

	clusterId            string
	clusterRestEndpoint  string
	clusterLinkName      string
	clusterApiKey        string
	clusterApiSecret     string
	topics               []string
	authMode             string
	ccBootstrapEndpoint  string
	loadBalancerEndpoint string
}

type MigrationInitializer struct {
	opts MigrationInitializerOpts
}

func NewMigrationInitializer(opts MigrationInitializerOpts) *MigrationInitializer {
	return &MigrationInitializer{
		opts: opts,
	}
}

func (m *MigrationInitializer) Run() error {
	// Load or create migration state
	var migrationState *types.MigrationState
	if _, err := os.Stat(m.opts.migrationStateFile); err == nil {
		// File exists, load it
		migrationState, err = types.NewMigrationStateFromFile(m.opts.migrationStateFile)
		if err != nil {
			return fmt.Errorf("failed to load migration state: %w", err)
		}
	} else {
		// File doesn't exist, create new state
		migrationState = types.NewMigrationState()
	}

	// Create MigrationConfig
	migrationId := fmt.Sprintf("migration-%s", uuid.New().String())
	config := &types.MigrationConfig{
		MigrationId:          migrationId,
		GatewayNamespace:     m.opts.gatewayNamespace,
		GatewayCrdName:       m.opts.gatewayCrdName,
		SourceName:           m.opts.sourceName,
		DestinationName:      m.opts.destinationName,
		SourceRouteName:      m.opts.sourceRouteName,
		DestinationRouteName: m.opts.destinationRouteName,
		KubeConfigPath:       m.opts.kubeConfigPath,
		ClusterId:            m.opts.clusterId,
		ClusterRestEndpoint:  m.opts.clusterRestEndpoint,
		ClusterLinkName:      m.opts.clusterLinkName,
		Topics:               m.opts.topics,
		AuthMode:             m.opts.authMode,
		CCBootstrapEndpoint:  m.opts.ccBootstrapEndpoint,
		LoadBalancerEndpoint: m.opts.loadBalancerEndpoint,
		CurrentState:         types.StateUninitialized,
	}

	// Skip validation if flag is set
	if m.opts.skipValidate {
		migrationState.UpsertMigration(*config)
		if err := migrationState.WriteToFile(m.opts.migrationStateFile); err != nil {
			return fmt.Errorf("failed to save migration state: %w", err)
		}
		slog.Info("migration created (validation skipped)",
			"migrationId", config.MigrationId,
			"currentState", config.CurrentState,
			"stateFile", m.opts.migrationStateFile)
		return nil
	}

	// Create services
	gatewayService := gateway.NewK8sService(m.opts.kubeConfigPath)
	clusterLinkService := clusterlink.NewConfluentCloudService(&http.Client{})

	// Create workflow service with injected services
	workflowService := migration.NewDefaultWorkflowService(gatewayService, clusterLinkService)

	// Create persistence service
	persistenceService := persistence.NewFileSystemService()

	// Create orchestrator with all dependencies
	orchestrator := migration.NewOrchestrator(
		config,
		workflowService,
		persistenceService,
		m.opts.migrationStateFile,
	)

	// Initialize migration
	ctx := context.Background()
	if err := orchestrator.Initialize(ctx, m.opts.clusterApiKey, m.opts.clusterApiSecret); err != nil {
		return fmt.Errorf("failed to initialize migration: %w", err)
	}

	slog.Info("migration initialized",
		"migrationId", config.MigrationId,
		"currentState", config.CurrentState,
		"stateFile", m.opts.migrationStateFile)
	return nil
}
