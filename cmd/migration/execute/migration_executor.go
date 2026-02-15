package execute

import (
	"context"
	"fmt"
	"log/slog"

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

	// Load the specific migration
	migration, err := types.LoadMigration(migrationState, m.opts.migrationId)
	if err != nil {
		return fmt.Errorf("migration '%s' not found in %s\nRun 'kcp migration list' to see available migrations", m.opts.migrationId, m.opts.migrationStateFile)
	}

	// Provide save function for FSM callbacks (will save after each state transition)
	migration.SetSaveStateFunc(func() error {
		migrationState.UpsertMigration(*migration)
		return migrationState.WriteToFile(m.opts.migrationStateFile)
	})

	ctx := context.Background()

	if err := migration.Execute(ctx, m.opts.threshold, m.opts.maxWaitTime, m.opts.clusterApiKey, m.opts.clusterApiSecret); err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}

	slog.Info("migration completed",
		"migrationId", migration.MigrationId,
		"currentState", migration.GetCurrentState())
	return nil
}
