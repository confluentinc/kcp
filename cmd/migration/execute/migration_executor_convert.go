package execute

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/migration/d2s"
	"github.com/spf13/cobra"
)

// convertServices is everything a route conversion drives its state machine with besides the gateway
// service, which comes from executorDependencies.gateway like every branch's. close releases every client
// the services hold.
type convertServices struct {
	deps  d2s.Dependencies
	close func() error
}

// convertServicesFunc builds a conversion's services from the manifest.
type convertServicesFunc func(g *manifest.GatewayMigration) (convertServices, error)

// runConvertBranch drives a route conversion (spec.route.convertTo: static) through d2s.D2SOrchestrator.
// Its siblings are runStaticBranch and runDynamicBranch; the live reconcile that produced reconcileResult ran
// once, in runMigrationExecute. It builds its own services rather than calling buildExecutorServices, which
// always builds both offset providers and the cluster-link client, neither of which a conversion uses.
//
// Every policy is read from g.Spec.DefaultPolicies, the effective policy after applyPolicyOverrides. A
// conversion reads detectUnroutedCommitsDuration and offsetSyncConcurrency (resolved to their defaults), and
// the rollout and hot-reload timeouts and the gateway config port like every branch; lagThreshold,
// promoteBatchSize and --run-report are AAO/TBM-only.
func runConvertBranch(
	cmd *cobra.Command,
	g *manifest.GatewayMigration,
	config *migration.MigrationConfig,
	reconcileResult *migplan.Result,
	deps executorDependencies,
) error {
	ctx := context.Background()
	policy := g.Spec.DefaultPolicies

	if deps.convert == nil {
		return fmt.Errorf("no route-conversion services are configured for this command")
	}
	gatewayService, err := deps.gateway(g)
	if err != nil {
		return fmt.Errorf("failed to build gateway service: %w", err)
	}
	svc, err := deps.convert(g)
	if err != nil {
		return fmt.Errorf("failed to build route-conversion services: %w", err)
	}
	defer func() { _ = svc.close() }()
	if svc.deps.Out == nil {
		svc.deps.Out = cmd.OutOrStdout()
	}

	actions := d2s.NewD2SActions(gatewayService, svc.deps)
	actions.SetRolloutTimeout(policy.RolloutTimeout)
	actions.SetHotReloadTimeout(policy.HotReloadTimeout)

	applyEffectivePolicy(config, policy)

	orchestrator := d2s.NewD2SOrchestrator(config, actions)
	if err := orchestrator.Execute(ctx, reconcileResult, d2s.Policy{
		DetectUnroutedCommitsDuration: policy.EffectiveDetectUnroutedCommitsDuration(),
		Workers:                       policy.EffectiveOffsetSyncConcurrency(),
	}); err != nil {
		return fmt.Errorf("failed to execute route conversion: %w", err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✅ Route conversion completed: %s\n", config.MigrationId)
	return nil
}
