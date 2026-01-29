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
		return fmt.Errorf("failed to load migration: %v", err)
	}

	// check the lags are below threshold
	err = migration.FSM.Event(context.Background(), types.EventWaitForLags)
	if err != nil {
		return fmt.Errorf("failed during lag check: %v", err)
	}

	// fence the gateway
	err = migration.FSM.Event(context.Background(), types.EventFence)
	if err != nil {
		return fmt.Errorf("failed to fence gateway: %v", err)
	}

	// Promote topics
	err = migration.FSM.Event(context.Background(), types.EventPromote)
	if err != nil {
		return fmt.Errorf("failed to start topic promotion: %v", err)
	}

	// check the promotion completion
	err = migration.FSM.Event(context.Background(), types.EventWaitForPromotionCompletion)
	if err != nil {
		return fmt.Errorf("failed during promotion completion check: %v", err)
	}

	// Switch over to the new gateway config
	err = migration.FSM.Event(context.Background(), types.EventSwitch)
	if err != nil {
		return fmt.Errorf("failed to switch over to the new gateway config: %v", err)
	}

	slog.Info("migration completed", "migrationId", migration.MigrationId, "currentState", migration.CurrentState, "fsm", migration.FSM.Current())
	return nil
}
