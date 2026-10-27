package execute

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/migration/tbm"
	"github.com/confluentinc/kcp/internal/tracing"
	"github.com/spf13/cobra"
)

// runDynamicBranch drives a dynamicMigration (TBM) through tbm.TBMOrchestrator.
// Its twin is runStaticBranch; the shared live reconcile that produced
// reconcileResult runs once, in runMigrationExecute.
//
// Every policy value is read from g.Spec.DynamicMigration.Policies — the
// EFFECTIVE policy, i.e. the manifest's dynamicMigration.policies after
// applyPolicyOverrides has folded in any per-run flag overrides — so an unset flag leaves the
// manifest default in force rather than resetting the knob to zero.
func runDynamicBranch(
	cmd *cobra.Command,
	g *manifest.GatewayMigration,
	config *migration.MigrationConfig,
	reconcileResult *migplan.Result,
	deps executorDependencies,
) error {
	ctx := commandContext(cmd)
	policy := g.Spec.DynamicMigration.Policies

	_, buildSpan := tracing.Start(ctx, tracerName, "migration.build_services")
	svc, err := buildExecutorServices(g, deps)
	tracing.End(buildSpan, err)
	if err != nil {
		return err
	}
	defer func() { _ = svc.close() }()

	actions := tbm.NewTBMActions(svc.sourceOffset, svc.destinationOffset, svc.gateway, svc.clusterLink)
	actions.SetRolloutTimeout(policy.RolloutTimeout)
	actions.SetHotReloadTimeout(policy.HotReloadTimeout)
	actions.SetPromoteBatchSize(policy.PromoteBatchSize)

	applyCommonPolicy(config, policy.CommonPolicies)
	config.DetectUnroutedProducersDuration = policy.DetectUnroutedProducersDuration

	orchestrator := tbm.NewTBMOrchestrator(config, actions)
	if err := orchestrator.Execute(ctx, reconcileResult, int64(policy.LagThreshold),
		policy.DetectUnroutedProducersDuration, svc.restAuth); err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✅ Migration completed: %s\n", config.MigrationId)
	return nil
}
