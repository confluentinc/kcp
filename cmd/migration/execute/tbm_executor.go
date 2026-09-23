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

// runTBMBranch drives a dynamic-mode (TBM) migration through
// tbm.TBMOrchestrator. The live reconcile that builds the run's config lives
// once, shared, in runMigrationExecute; this branch only dispatches.
//
// Every policy value applied here is read from g.Spec.DefaultPolicies — the
// EFFECTIVE policy, i.e. the manifest's spec.defaultPolicies after
// applyPolicyOverrides has already folded in any per-run flag overrides
// (runMigrationExecute applies them unconditionally before this dispatch).
// This is deliberate parity with the AAO branch, which threads the same
// effective values through buildExecutorOpts/MigrationExecutor: an unset flag
// must leave the manifest default in force, not silently reset the knob to
// zero. Reading the raw *Override package vars instead would ignore a manifest
// rolloutTimeout/hotReloadTimeout/promoteBatchSize/gatewayConfigPort whenever
// its flag was omitted — and would disagree with the LastRunPolicies snapshot
// below, which records the effective policy.
func runTBMBranch(
	cmd *cobra.Command,
	g *manifest.GatewayMigration,
	config *migration.MigrationConfig,
	reconcileResult *migplan.Result,
	buildOffsets offsetProvidersFunc,
	buildGateway gatewayServiceFunc,
	buildClusterLink clusterLinkServiceFunc,
) error {
	ctx := context.Background()

	sourceOffset, destinationOffset, closeOffsets, err := buildOffsets(g)
	if err != nil {
		return fmt.Errorf("failed to connect to source/destination clusters: %w", err)
	}
	defer func() { _ = closeOffsets() }()

	gatewayService, err := buildGateway(g)
	if err != nil {
		return fmt.Errorf("failed to build gateway service: %w", err)
	}

	clusterLinkService, err := buildClusterLink(g)
	if err != nil {
		return fmt.Errorf("failed to build cluster-link service: %w", err)
	}
	restCreds, err := g.RestCredentials()
	if err != nil {
		return fmt.Errorf("failed to resolve cluster-link REST credentials: %w", err)
	}

	actions := tbm.NewTBMActions(sourceOffset, destinationOffset, gatewayService, clusterLinkService)
	actions.SetRolloutTimeout(g.Spec.DefaultPolicies.RolloutTimeout)
	actions.SetHotReloadTimeout(g.Spec.DefaultPolicies.HotReloadTimeout)
	// Wired for both modes now — TBMActions.Promote already implements and
	// tests batched promotion; execute-tbm never exposed it.
	actions.SetPromoteBatchSize(g.Spec.DefaultPolicies.PromoteBatchSize)

	// An explicit gateway-config-port (flag or manifest) overrides the value
	// already on config — mirrors MigrationExecutor.Run's own guard.
	if g.Spec.DefaultPolicies.GatewayConfigPort > 0 {
		config.GatewayConfigPort = g.Spec.DefaultPolicies.GatewayConfigPort
	}

	// The FSM always starts at uninitialized (start-from-zero) — there is no
	// resume position: reconcile (run every invocation) + idempotent applies
	// determine what happens.
	orchestrator := tbm.NewTBMOrchestrator(config, actions)

	// Record the effective policy this run used — TBM adopts the same audit
	// snapshot AAO already has. ConsumerOffsetSyncDrainDuration
	// stays zero: TBM has no offset-sync-pause feature to record a value for.
	config.LastRunPolicies = &migration.LastRunPolicies{
		LagThreshold:                    g.Spec.DefaultPolicies.LagThreshold,
		PromoteBatchSize:                g.Spec.DefaultPolicies.PromoteBatchSize,
		RolloutTimeout:                  g.Spec.DefaultPolicies.RolloutTimeout,
		DetectUnroutedProducersDuration: g.Spec.DefaultPolicies.DetectUnroutedProducersDuration,
		HotReloadTimeout:                g.Spec.DefaultPolicies.HotReloadTimeout,
		GatewayConfigPort:               config.GatewayConfigPort,
	}

	restAuth := restCreds.Authenticator()
	if err := orchestrator.Execute(ctx, reconcileResult, int64(g.Spec.DefaultPolicies.LagThreshold),
		g.Spec.DefaultPolicies.DetectUnroutedProducersDuration, restAuth); err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}

	cmd.Printf("✅ Migration completed: %s\n", config.MigrationId)
	return nil
}
