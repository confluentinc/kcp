package execute

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/tracing"
	"github.com/spf13/cobra"
)

// runStaticBranch drives a staticMigration (AAO) through
// migration.MigrationOrchestrator. Its twin is runDynamicBranch; the shared
// live reconcile that produced reconcileResult runs once, in
// runMigrationExecute.
//
// Every policy value is read from g.Spec.StaticMigration.Policies — the
// EFFECTIVE policy, i.e. the manifest's staticMigration.policies after
// applyPolicyOverrides has folded in any per-run flag overrides.
//
// Beyond its twin, it prints the offset-sync guidance when a run with the pause
// opted in fails.
func runStaticBranch(
	cmd *cobra.Command,
	g *manifest.GatewayMigration,
	config *migration.MigrationConfig,
	reconcileResult *migplan.Result,
	deps executorDependencies,
) error {
	ctx := commandContext(cmd)
	policy := g.Spec.StaticMigration.Policies

	_, buildSpan := tracing.Start(ctx, tracerName, "migration.build_services")
	svc, err := buildExecutorServices(g, deps)
	tracing.End(buildSpan, err)
	if err != nil {
		return err
	}
	defer func() { _ = svc.close() }()

	actions := migration.NewMigrationActionsWithOffsets(svc.gateway, svc.clusterLink, svc.sourceOffset, svc.destinationOffset)
	actions.SetRolloutTimeout(policy.RolloutTimeout)
	actions.SetHotReloadTimeout(policy.HotReloadTimeout)
	actions.SetPromoteBatchSize(policy.PromoteBatchSize)

	applyCommonPolicy(config, policy.CommonPolicies)
	config.DetectUnroutedProducersDuration = policy.DetectUnroutedProducersDuration
	config.ConsumerOffsetSyncDrainDuration = policy.ConsumerOffsetSyncDrainDuration

	orchestrator := migration.NewMigrationOrchestrator(config, actions)

	if err := orchestrator.Execute(ctx, int64(policy.LagThreshold), svc.restAuth, reconcileResult); err != nil {
		migration.WarnIfPausedOnExecuteFailure(config, err)
		return fmt.Errorf("failed to execute migration: %w", err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✅ Migration completed: %s\n", config.MigrationId)
	return nil
}
