package execute

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/persistence"
	"github.com/confluentinc/kcp/internal/types"
)

type MigrationExecutorOpts struct {
	MigrationStateFile string
	MigrationState     types.MigrationState
	MigrationConfig    types.MigrationConfig
	Threshold          int64
	MaxWaitTime        int64 // in seconds
	ClusterApiKey      string
	ClusterApiSecret   string
}

type MigrationExecutor struct {
	opts MigrationExecutorOpts
}

func NewMigrationExecutor(opts MigrationExecutorOpts) *MigrationExecutor {
	return &MigrationExecutor{
		opts: opts,
	}
}

func (m *MigrationExecutor) Run() error {
	// Use pre-loaded config from opts
	config := m.opts.MigrationConfig

	// Create services
	gatewayService := gateway.NewK8sService(config.KubeConfigPath)
	clusterLinkService := clusterlink.NewConfluentCloudService(&http.Client{})

	// Create workflow service
	workflowService := migration.NewDefaultWorkflowService(gatewayService, clusterLinkService)

	// Create persistence service
	persistenceService := persistence.NewFileSystemService()

	// Create orchestrator
	orchestrator := migration.NewOrchestrator(
		&config,
		workflowService,
		persistenceService,
		m.opts.MigrationStateFile,
	)

	// Execute migration
	ctx := context.Background()
	if err := orchestrator.Execute(ctx, m.opts.Threshold, m.opts.MaxWaitTime, m.opts.ClusterApiKey, m.opts.ClusterApiSecret); err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}

	slog.Info("migration completed",
		"migrationId", config.MigrationId,
		"currentState", config.CurrentState)
	return nil
}
