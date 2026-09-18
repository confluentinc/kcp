package tbm

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/goccy/go-yaml"
)

// deriveFencedCRYAML builds the fenced CR bytes from the captured gateway CR
// snapshot by replacing config.Route's rules subtree with config.FenceYAML,
// applied unmodified — migplan.Reconcile's artifact already satisfies the
// Gateway CRD (PrependFence sets rules.fencing[].blocked itself; see
// migplan/reconcile/rules.go), so there is nothing to patch here. There is no
// separately-snapshotted fenced CR: this and resolveGatewayCapability's
// detection both derive from the same source, so they can never drift from
// each other. config.GatewayYAML is already clean (migplan strips
// server-managed metadata once, centrally — see gatewayfile.go's
// cleanGatewayDoc), so this only needs to parse it.
func deriveFencedCRYAML(config *migration.MigrationConfig) ([]byte, error) {
	var base map[string]interface{}
	if err := yaml.Unmarshal([]byte(config.GatewayYAML), &base); err != nil {
		return nil, fmt.Errorf("failed to parse gateway CR YAML: %w", err)
	}
	return gateway.ReplaceRouteRulesObj(base, config.Route, []byte(config.FenceYAML))
}

// deriveSwitchedCRYAML builds the switched CR bytes the same way, from
// config.SwitchoverYAML, also applied unmodified — this is what Switch
// itself applies. It also independently serves resolveGatewayCapability's
// detection, which needs to see both the fenced and switched CR to correctly
// infer verification mode — a detector shown only the live gateway could
// pick rollout verification for a migration that will hot-reload, and then
// observe nothing (see migration.ResolveGatewayCapability's comment).
func deriveSwitchedCRYAML(config *migration.MigrationConfig) ([]byte, error) {
	var base map[string]interface{}
	if err := yaml.Unmarshal([]byte(config.GatewayYAML), &base); err != nil {
		return nil, fmt.Errorf("failed to parse gateway CR YAML: %w", err)
	}
	return gateway.ReplaceRouteRulesObj(base, config.Route, []byte(config.SwitchoverYAML))
}

// deriveRulesRoutePatch builds the RoutePatch that grafts yamlSrc's rules
// fragment onto config.Route. TBM's fence and switch write paths both patch
// the same "rules" field — they differ only in which captured YAML the
// fragment comes from — unlike AAO's fence/switch pair, which patch distinct
// fields ("fence" vs "streamingDomain").
func deriveRulesRoutePatch(config *migration.MigrationConfig, yamlSrc string) (gateway.RoutePatch, error) {
	v, err := gateway.FragmentValue([]byte(yamlSrc), "rules")
	if err != nil {
		return gateway.RoutePatch{}, err
	}
	return gateway.RoutePatch{RouteName: config.Route, Field: "rules", Value: v}, nil
}

// deriveFenceRoutePatch builds the RoutePatch that grafts config.FenceYAML's
// rules fragment onto config.Route. Consumed by Fence's write path;
// resolveGatewayCapability's probe still uses deriveFencedCRYAML/full CR
// bytes since capability detection needs a complete CR to apply, not a route
// patch.
func deriveFenceRoutePatch(config *migration.MigrationConfig) (gateway.RoutePatch, error) {
	return deriveRulesRoutePatch(config, config.FenceYAML)
}

// deriveSwitchRoutePatch builds the RoutePatch that grafts
// config.SwitchoverYAML's rules fragment onto config.Route. Consumed by
// Switch's write path.
func deriveSwitchRoutePatch(config *migration.MigrationConfig) (gateway.RoutePatch, error) {
	return deriveRulesRoutePatch(config, config.SwitchoverYAML)
}

// deriveUnfenceRoutePatch builds the RoutePatch that restores config.Route to
// its captured state in config.GatewayYAML — a whole-route replace (Field ==
// "") rather than a single-key mutation.
func deriveUnfenceRoutePatch(config *migration.MigrationConfig) (gateway.RoutePatch, error) {
	route, err := gateway.RouteObject([]byte(config.GatewayYAML), config.Route)
	if err != nil {
		return gateway.RoutePatch{}, err
	}
	return gateway.RoutePatch{RouteName: config.Route, Value: route}, nil
}

// verifier builds the shared gateway apply/wait/verify mechanism, seeded
// with this run's current capability and timeouts. migration's own Actions
// type builds the identical thing (see migration.MigrationActions.verifier)
// — only how each derives CR bytes and adopts a newly resolved capability
// differs, which stays here rather than in gateway.TransitionVerifier itself.
func (a *TBMActions) verifier() *gateway.TransitionVerifier {
	return &gateway.TransitionVerifier{
		Service:          a.gatewayService,
		Reporter:         a.reporter,
		Capability:       a.gatewayCapability,
		RolloutTimeout:   a.rolloutTimeout,
		HotReloadTimeout: a.hotReloadTimeout,
	}
}

// ensureGatewayCapability resolves gatewayCapability at most once per
// process: the first of Fence or Switch to run this call actually resolves
// it (and smoke-tests hot-reload); whichever runs second, if any, in the
// same process is then a no-op. This fixes a real bug: gateway capability
// used to be resolved only inside Fence, so a run resuming directly at
// switch (fence/verify_fence/promote already done in an earlier, separate
// execute-tbm process) would use the unresolved zero-value capability
// (VerifyRollout) instead of the live cluster's real one. Mirrors, at
// smaller scope, migration's own "Execute re-derives [capability]
// authoritatively" comment on ResolveGatewayCapability — but only when a
// gateway-touching step is about to run, not unconditionally on every
// invocation (TBM's single command can legitimately resume at wait_for_lags
// or promote alone, neither of which touches the gateway).
func (a *TBMActions) ensureGatewayCapability(ctx context.Context, config *migration.MigrationConfig) error {
	if a.capabilityResolved {
		return nil
	}
	if err := a.resolveGatewayCapability(ctx, config); err != nil {
		return err
	}
	if err := a.verifyHotReloadCapability(ctx, config); err != nil {
		return err
	}
	a.capabilityResolved = true
	return nil
}

// resolveGatewayCapability determines how gateway state transitions will be
// verified on the live cluster, and adopts it for this run. Called once,
// authoritatively, from Fence itself — unlike migration.ResolveGatewayCapability
// there is no separate advisory call at initialize: TBM has one command, not
// migration's separate init/execute split with a review step in between.
func (a *TBMActions) resolveGatewayCapability(ctx context.Context, config *migration.MigrationConfig) error {
	// Settle the port first: the capability probe below needs it.
	if config.GatewayConfigPort == 0 {
		config.GatewayConfigPort = gateway.DefaultGatewayConfigPort
	}

	fencedCrYAML, err := deriveFencedCRYAML(config)
	if err != nil {
		return fmt.Errorf("failed to derive fenced gateway CR: %w", err)
	}
	switchedCrYAML, err := deriveSwitchedCRYAML(config)
	if err != nil {
		return fmt.Errorf("failed to derive switched gateway CR: %w", err)
	}

	capability, err := a.verifier().ResolveCapability(ctx, config.K8sNamespace, config.InitialCrName,
		config.GatewayConfigPort, fencedCrYAML, switchedCrYAML)
	if err != nil {
		return err
	}
	a.gatewayCapability = capability
	if capability.Mode == gateway.VerifyPerPodConfigID {
		a.reporter.Success("Gateway transitions will be verified per pod via %s", gateway.GatewayConfigEndpointPath)
	}
	return nil
}

// verifyHotReloadCapability proves the gateway really does apply config
// revisions, before fencing touches any traffic. See
// gateway.TransitionVerifier.VerifyHotReloadCapability's doc comment for why
// this matters and why patching only spec.configId — touching no other
// field — makes it safe to call at any point, including a resume.
func (a *TBMActions) verifyHotReloadCapability(ctx context.Context, config *migration.MigrationConfig) error {
	return a.verifier().VerifyHotReloadCapability(ctx, config.K8sNamespace, config.InitialCrName, config.GatewayConfigPort)
}

// patchGatewayRoute patches one route mutation onto the gateway CR, attaching
// a fresh config revision id when the cluster supports one. See
// gateway.TransitionVerifier.PatchCR.
func (a *TBMActions) patchGatewayRoute(ctx context.Context, config *migration.MigrationConfig, rp gateway.RoutePatch, step string) (gateway.ApplyResult, error) {
	return a.verifier().PatchCR(ctx, config.K8sNamespace, config.InitialCrName, rp, step)
}

// waitForGatewayAccepted blocks until the Confluent operator has accepted
// the gateway CR just applied. See
// gateway.TransitionVerifier.WaitForAccepted's doc comment for why this
// matters.
func (a *TBMActions) waitForGatewayAccepted(ctx context.Context, config *migration.MigrationConfig, step string) error {
	return a.verifier().WaitForAccepted(ctx, config.K8sNamespace, config.InitialCrName, step)
}

// verifyGatewayTransition confirms a transition landed, by whichever means
// the cluster supports.
func (a *TBMActions) verifyGatewayTransition(ctx context.Context, config *migration.MigrationConfig, applied gateway.ApplyResult, step string) error {
	return a.verifier().VerifyTransition(ctx, config.K8sNamespace, config.InitialCrName, config.GatewayConfigPort, applied, step)
}
