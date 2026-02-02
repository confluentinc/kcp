package execute

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/types"
)

type MigrationExecutorOpts struct {
	stateFile   string
	migrationId string
	maxLag      int64
	maxWaitTime int64 // in seconds
}

type MigrationExecutor struct {
	stateFile   string
	migrationId string
	maxLag      int64
	maxWaitTime int64 // in seconds
}

func NewMigrationExecutor(opts MigrationExecutorOpts) *MigrationExecutor {
	return &MigrationExecutor{
		stateFile:   opts.stateFile,
		migrationId: opts.migrationId,
		maxLag:      opts.maxLag,
		maxWaitTime: opts.maxWaitTime,
	}
}

func (m *MigrationExecutor) Run() error {
	// load the migration from the state file
	migration, err := types.LoadMigration(m.stateFile, m.migrationId)
	if err != nil {
		return fmt.Errorf("failed to load migration: %w", err)
	}

	ctx := context.Background()
	
	// Execute the complete migration workflow
	if err := migration.Execute(ctx, m.maxLag, m.maxWaitTime); err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}

	slog.Info("migration completed", 
		"migrationId", migration.MigrationId, 
		"currentState", migration.GetCurrentState())
	return nil
}
