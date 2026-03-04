package init

import (
	"context"
	"fmt"
	"net/http"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/persistence"
	"github.com/confluentinc/kcp/internal/types"
)

type MigrationInitializerOpts struct {
	MigrationStateFile string
	MigrationState     types.MigrationState
	MigrationConfig    types.MigrationConfig
	ClusterApiKey      string
	ClusterApiSecret   string
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
	// Use pre-loaded config from opts
	config := &m.opts.MigrationConfig

	// Create services
	gatewayService := gateway.NewK8sService(config.KubeConfigPath)
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
		m.opts.MigrationStateFile,
	)

	// Initialize migration (validate infrastructure)
	ctx := context.Background()
	if err := orchestrator.Initialize(ctx, m.opts.ClusterApiKey, m.opts.ClusterApiSecret); err != nil {
		return fmt.Errorf("failed to initialize migration: %w", err)
	}

	return nil
}
