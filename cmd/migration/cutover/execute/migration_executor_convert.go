package execute

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/migration/d2s"
	"github.com/confluentinc/kcp/internal/tracing"
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

// Skips buildExecutorServices: a conversion uses neither its offset providers nor the link client.
func runConvertBranch(
	cmd *cobra.Command,
	g *manifest.GatewayMigration,
	config *migration.MigrationConfig,
	reconcileResult *migplan.Result,
	deps executorDependencies,
) error {
	ctx := commandContext(cmd)
	policy := g.Spec.DynamicToStaticConversion.Policies

	if deps.convert == nil {
		return fmt.Errorf("no route-conversion services are configured for this command")
	}
	_, buildSpan := tracing.Start(ctx, tracerName, "migration.build_services")
	gatewayService, err := deps.gateway(g)
	if err != nil {
		tracing.End(buildSpan, err)
		return fmt.Errorf("failed to build gateway service: %w", err)
	}
	svc, err := deps.convert(g)
	tracing.End(buildSpan, err)
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

	applyCommonPolicy(config, policy.CommonPolicies)

	orchestrator := d2s.NewD2SOrchestrator(config, actions)
	if err := orchestrator.Execute(ctx, reconcileResult, d2s.Policy{
		DetectUnroutedCommitsDuration: policy.DetectUnroutedCommitsDuration,
		Workers:                       policy.EffectiveOffsetSyncConcurrency(),
	}); err != nil {
		return fmt.Errorf("failed to execute route conversion: %w", err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✅ Route conversion completed: %s\n", config.MigrationId)
	return nil
}
