package execute

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/spf13/cobra"
)

// runStaticBranch drives a static-mode migration through
// migration.MigrationOrchestrator. Its twin is runDynamicBranch; the shared
// live reconcile that produced reconcileResult runs once, in
// runMigrationExecute.
//
// Every policy value is read from g.Spec.DefaultPolicies — the EFFECTIVE
// policy, i.e. the manifest's spec.defaultPolicies after applyPolicyOverrides
// has folded in any per-run flag overrides.
//
// Beyond its twin, it carries the static FSM's offset-sync bookends (the pause
// runs inside the FSM; the restore runs after it) and the --run-report
// recorder, which is static-only for now.
func runStaticBranch(
	cmd *cobra.Command,
	g *manifest.GatewayMigration,
	config *migration.MigrationConfig,
	reconcileResult *migplan.Result,
	deps executorDependencies,
	runReportPath string,
) error {
	ctx := context.Background()
	policy := g.Spec.DefaultPolicies

	svc, err := buildExecutorServices(g, deps)
	if err != nil {
		return err
	}
	defer func() { _ = svc.close() }()

	actions := migration.NewMigrationActionsWithOffsets(svc.gateway, svc.clusterLink, svc.sourceOffset, svc.destinationOffset)
	actions.SetRolloutTimeout(policy.RolloutTimeout)
	actions.SetHotReloadTimeout(policy.HotReloadTimeout)
	actions.SetPromoteBatchSize(policy.PromoteBatchSize)

	applyEffectivePolicy(config, policy)

	orchestrator := migration.NewMigrationOrchestrator(config, actions)

	// Stamped on the way out whatever the outcome: a failed run is a result
	// worth recording, with the stages that did complete.
	runReport := migration.NewRunReportRecorder(runReportPath, config.MigrationId,
		len(config.Topics), int64(policy.LagThreshold), orchestrator.CurrentState())
	orchestrator.SetRunReportRecorder(runReport)
	var execErr error
	defer func() { runReport.Finish(orchestrator.CurrentState(), execErr) }()

	clusterLinkConfig := migration.BuildClusterLinkConfig(config, svc.restAuth)
	if execErr = orchestrator.Execute(ctx, int64(policy.LagThreshold), svc.restAuth, reconcileResult); execErr != nil {
		migration.WarnIfPausedOnExecuteFailure(config, execErr)
		return fmt.Errorf("failed to execute migration: %w", execErr)
	}

	// Post-execute bookend: restore consumer.offset.sync.enable. Soft-fail so
	// a restore error does not roll back a successful switchover.
	migration.RestoreOffsetSync(ctx, svc.clusterLink, clusterLinkConfig, config)

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✅ Migration completed: %s\n", config.MigrationId)
	return nil
}
