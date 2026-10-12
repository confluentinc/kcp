package execute

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/migration/tbm"
	"github.com/spf13/cobra"
)

// runDynamicBranch drives a dynamic-mode migration through tbm.TBMOrchestrator.
// Its twin is runStaticBranch; the shared live reconcile that produced
// reconcileResult runs once, in runMigrationExecute.
//
// Every policy value is read from g.Spec.DefaultPolicies — the EFFECTIVE
// policy, i.e. the manifest's spec.defaultPolicies after applyPolicyOverrides
// has folded in any per-run flag overrides — so an unset flag leaves the
// manifest default in force rather than resetting the knob to zero.
func runDynamicBranch(
	cmd *cobra.Command,
	g *manifest.GatewayMigration,
	config *migration.MigrationConfig,
	reconcileResult *migplan.Result,
	deps executorDependencies,
) error {
	ctx := context.Background()
	policy := g.Spec.DefaultPolicies

	svc, err := buildExecutorServices(g, deps)
	if err != nil {
		return err
	}
	defer func() { _ = svc.close() }()

	actions := tbm.NewTBMActions(svc.sourceOffset, svc.destinationOffset, svc.gateway, svc.clusterLink)
	actions.SetRolloutTimeout(policy.RolloutTimeout)
	actions.SetHotReloadTimeout(policy.HotReloadTimeout)
	actions.SetPromoteBatchSize(policy.PromoteBatchSize)

	applyEffectivePolicy(config, policy)

	orchestrator := tbm.NewTBMOrchestrator(config, actions)
	if err := orchestrator.Execute(ctx, reconcileResult, int64(policy.LagThreshold),
		policy.DetectUnroutedProducersDuration, svc.restAuth); err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✅ Migration completed: %s\n", config.MigrationId)
	return nil
}
