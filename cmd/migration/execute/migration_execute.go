package migration_execute

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/types"
)

type MigrationExecuteOpts struct {
	stateFile   string
	migrationId string
}

type MigrationExecute struct {
	stateFile   string
	migrationId string
}

func NewMigrationExecute(opts MigrationExecuteOpts) *MigrationExecute {
	return &MigrationExecute{
		stateFile:   opts.stateFile,
		migrationId: opts.migrationId,
	}
}

func (m *MigrationExecute) Run() error {
	// load the migration from the state file
	migration, err := types.LoadMigration(m.stateFile, m.migrationId)
	if err != nil {
		return fmt.Errorf("failed to load migration: %w", err)
	}

	ctx := context.Background()
	
	// Execute the complete migration workflow
	if err := migration.Execute(ctx); err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}

	slog.Info("migration completed", 
		"migrationId", migration.MigrationId, 
		"currentState", migration.GetCurrentState())
	return nil
}
