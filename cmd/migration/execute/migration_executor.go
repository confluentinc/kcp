package execute

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/types"
)

type MigrationExecutorOpts struct {
	stateFile        string
	migrationId      string
	threshold        int64
	maxWaitTime      int64 // in seconds
	clusterApiKey    string
	clusterApiSecret string
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
	// load the migration from the state file
	migration, err := types.LoadMigration(m.opts.stateFile, m.opts.migrationId)
	if err != nil {
		return fmt.Errorf("failed to load migration: %w", err)
	}

	ctx := context.Background()

	if err := migration.Execute(ctx, m.opts.threshold, m.opts.maxWaitTime, m.opts.clusterApiKey, m.opts.clusterApiSecret); err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}

	slog.Info("migration completed",
		"migrationId", migration.MigrationId,
		"currentState", migration.GetCurrentState())
	return nil
}
