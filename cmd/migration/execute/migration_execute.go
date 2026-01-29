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

	migration, err := types.LoadMigration(m.stateFile, m.state, m.migrationId)
	if err != nil {
		return fmt.Errorf("failed to load migration: %v", err)
	}

	// fence the gateway
	err = migration.FSM.Event(context.Background(), types.EventFence)
	if err != nil {
		return fmt.Errorf("failed to start migration: %v", err)
	}
	
	// Promote topics
	err = migration.FSM.Event(context.Background(), types.EventPromote)
	if err != nil {
		return fmt.Errorf("failed to comlete migration: %v", err)
	}

	// Switch over to the new gateway config
	err = migration.FSM.Event(context.Background(), types.EventSwitch)
	if err != nil {
		return fmt.Errorf("failed to switch over to the new gateway config: %v", err)
	}

	slog.Info("migration completed", "migrationId", migration.MigrationId, "currentState", migration.CurrentState, "fsm", migration.FSM.Current())
	return nil
}
