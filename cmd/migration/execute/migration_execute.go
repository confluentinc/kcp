package migration_execute

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/types"
)

type MigrationExecuteOpts struct {
	stateFile string
	state     types.State
	migrationId string
}

type MigrationExecute struct {
	stateFile string
	state     types.State
	migrationId string
}

func NewMigrationExecute(opts MigrationExecuteOpts) *MigrationExecute {
	return &MigrationExecute{
		stateFile:            opts.stateFile,
		state:                opts.state,
		migrationId:          opts.migrationId,
	}
}

func (m *MigrationExecute) Run() error {

	migration, err := types.LoadMigration(m.state, m.migrationId)
	if err != nil {
		return fmt.Errorf("failed to load migration: %v", err)
	}

	// Execute the migration
	err = migration.FSM.Event(context.Background(), types.EventKcpExecute)
	if err != nil {
		return fmt.Errorf("failed to execute migration: %v", err)
	}
	slog.Info("migration executed", "migrationId", migration.MigrationId, "currentState", migration.CurrentState, "fsm", migration.FSM.Current())
	m.state.UpsertMigration(*migration)
	err = m.state.PersistStateFile(m.stateFile)
	if err != nil {
		return fmt.Errorf("failed to persist state file: %v", err)
	}

	// Promote topics
	err = migration.FSM.Event(context.Background(), types.EventTopicsPromoted)
	if err != nil {
		return fmt.Errorf("failed to promote topics: %v", err)
	}
	slog.Info("topics promoted", "migrationId", migration.MigrationId, "currentState", migration.CurrentState, "fsm", migration.FSM.Current())
	m.state.UpsertMigration(*migration)
	return m.state.PersistStateFile(m.stateFile)
}
