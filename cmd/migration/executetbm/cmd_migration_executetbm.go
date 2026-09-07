package executetbm

import (
	"context"
	"fmt"
	"os"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration/tbm"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

var (
	manifestFile string
	tbmStateFile string
)

// reconcileFn is the engine entry point the command calls to produce the
// reconcile plan. A package var so the command's own tests, which exercise the
// FSM scaffold with unreachable placeholder endpoints, can stub it; production
// always uses the real engine.
var reconcileFn = migplan.Reconcile

const executeTBMLong = `Execute a Topic-Batch Migration (TBM) run.

This is a scaffold: every FSM transition is currently a noop (it sleeps to simulate
real execution timing, then logs). It exists to validate the state-machine shape and
command wiring ahead of the real per-batch migration logic described in the TBM design
proposal.

The migration is identified by metadata.name in the GatewayMigration manifest at
--migration-yaml — there is no separate init step and no --migration-id flag. The first
run for a given name creates a fresh entry in the TBM state file; a later run with the
SAME manifest content resumes from the last completed step. A later run with a CHANGED
manifest for the SAME name is refused outright, with no override — a genuinely new
migration needs a new metadata.name.`

// NewMigrationExecuteTBMCmd builds the `execute-tbm` command.
func NewMigrationExecuteTBMCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "execute-tbm",
		Short:         "Execute a Topic-Batch Migration run (scaffold: noop transitions)",
		Long:          executeTBMLong,
		Example:       `  kcp migration execute-tbm --migration-yaml gateway-migration.yaml --tbm-state-file tbm-state.json`,
		Hidden:        true, // noop scaffold pending real transition logic; kept in the binary but not user-facing (cascades to --help and gen-docs)
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE:       func(c *cobra.Command, _ []string) error { return utils.BindEnvToFlags(c) },
		RunE:          runMigrationExecuteTBM,
	}

	cmd.Flags().StringVar(&manifestFile, "migration-yaml", "", "Path to the GatewayMigration manifest describing this migration.")
	cmd.Flags().StringVar(&tbmStateFile, "tbm-state-file", "", "Path to the TBM state file. Created if it doesn't exist.")

	_ = cmd.MarkFlagRequired("migration-yaml")
	_ = cmd.MarkFlagRequired("tbm-state-file")

	return cmd
}

func runMigrationExecuteTBM(cmd *cobra.Command, args []string) error {
	g, err := manifest.LoadGatewayMigrationFile(manifestFile)
	if err != nil {
		return err
	}

	manifestBytes, err := os.ReadFile(manifestFile)
	if err != nil {
		return fmt.Errorf("failed to read migration manifest: %w", err)
	}
	hash := tbm.HashManifest(manifestBytes)

	var tbmState *tbm.TBMState
	if _, statErr := os.Stat(tbmStateFile); statErr == nil {
		tbmState, err = tbm.NewTBMStateFromFile(tbmStateFile)
		if err != nil {
			return fmt.Errorf("failed to load tbm state: %w", err)
		}
	} else if os.IsNotExist(statErr) {
		tbmState = tbm.NewTBMState()
	} else {
		return fmt.Errorf("failed to check tbm state file: %w", statErr)
	}

	config, err := resolveTBMConfig(tbmState, g.Metadata.Name, hash)
	if err != nil {
		return err
	}

	// Early write, mirroring kcp migration init's Phase 3: the file must exist
	// (and record a freshly-created migration) before the orchestrator runs.
	tbmState.UpsertMigration(*config)
	if err := tbmState.WriteToFile(tbmStateFile); err != nil {
		return fmt.Errorf("failed to write tbm state file: %w", err)
	}

	actions := tbm.NewTBMActions()

	// Produce the reconcile plan for this migration: the engine reads the live
	// Gateway CR + source/target/cluster-link state, renders its own report, and
	// returns the promote topic list plus the fence and switchover rules
	// artifacts. err here is an I/O failure (the plan could not be produced); an
	// infeasible-but-reachable plan is not an error — it surfaces as res.Refused
	// with res.Reasons.
	//
	// The state machine does not consume res yet: wiring res.Topics /
	// res.FenceYAML / res.SwitchoverYAML (and honouring res.Refused) into the TBM
	// transitions is the next step.
	res, err := reconcileFn(cmd.Context(), g)
	if err != nil {
		return fmt.Errorf("failed to produce the reconcile plan: %w", err)
	}
	_ = res

	orchestrator := tbm.NewTBMOrchestrator(config, actions, tbmState, tbmStateFile)

	if !orchestrator.HasPendingWork() {
		cmd.Printf("✅ TBM migration already complete: %s\n", config.MigrationId)
		return nil
	}

	if err := orchestrator.Execute(context.Background()); err != nil {
		return fmt.Errorf("failed to execute tbm migration: %w", err)
	}

	cmd.Printf("✅ TBM migration completed: %s\n", config.MigrationId)
	return nil
}

// resolveTBMConfig implements the identity & drift rule: no entry for this
// name -> create fresh at uninitialized; hash matches -> resume from the
// persisted state; hash differs -> refuse unconditionally, regardless of
// CurrentState. There is no override.
func resolveTBMConfig(state *tbm.TBMState, migrationId, hash string) (*tbm.TBMConfig, error) {
	existing, err := state.GetMigrationById(migrationId)
	if err != nil {
		return &tbm.TBMConfig{
			MigrationId:  migrationId,
			CurrentState: tbm.StateUninitialized,
			ManifestHash: hash,
		}, nil
	}

	if existing.ManifestHash != hash {
		return nil, fmt.Errorf( //nolint:staticcheck // multi-line operator guidance
			"the manifest for migration %q has changed since it was last run (recorded hash %s, current hash %s).\n"+
				"A migration's manifest must not change once started. Use a new metadata.name for a new migration, "+
				"or revert this file to match the run already in progress.",
			migrationId, existing.ManifestHash, hash)
	}

	return existing, nil
}
