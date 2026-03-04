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
	migrationStateFile string
	migrationId        string
	threshold          int64
	maxWaitTime        int64 // in seconds
	clusterApiKey      string
	clusterApiSecret   string
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
	// Load migration state
	migrationState, err := types.NewMigrationStateFromFile(m.opts.migrationStateFile)
	if err != nil {
		return fmt.Errorf("migration state file not found: %s\nRun 'kcp migration init' to create a new migration first", m.opts.migrationStateFile)
	}

	// Get MigrationConfig by ID
	config, err := migrationState.GetMigrationById(m.opts.migrationId)
	if err != nil {
		return fmt.Errorf("migration '%s' not found in %s\nRun 'kcp migration list' to see available migrations", m.opts.migrationId, m.opts.migrationStateFile)
	}

	// Create services
	gatewayService := gateway.NewK8sService(config.KubeConfigPath)
	clusterLinkService := clusterlink.NewConfluentCloudService(&http.Client{})

	// Create workflow service
	workflowService := migration.NewDefaultWorkflowService(gatewayService, clusterLinkService)

	// Create persistence service
	persistenceService := persistence.NewFileSystemService()

	// Create orchestrator
	orchestrator := migration.NewOrchestrator(
		config,
		workflowService,
		persistenceService,
		m.opts.migrationStateFile,
	)

	// Execute migration
	ctx := context.Background()
	if err := orchestrator.Execute(ctx, m.opts.threshold, m.opts.maxWaitTime, m.opts.clusterApiKey, m.opts.clusterApiSecret); err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}

	slog.Info("migration completed",
		"migrationId", config.MigrationId,
		"currentState", config.CurrentState)
	return nil
}
