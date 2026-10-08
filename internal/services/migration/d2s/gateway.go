package d2s

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/goccy/go-yaml"
)

// parseGatewayYAML parses the cleaned gateway CR migplan pulled (config.GatewayYAML).
func parseGatewayYAML(config *migration.MigrationConfig) (map[string]any, error) {
	var base map[string]any
	if err := yaml.Unmarshal([]byte(config.GatewayYAML), &base); err != nil {
		return nil, fmt.Errorf("failed to parse gateway CR YAML: %w", err)
	}
	return base, nil
}

// deriveFencedCRYAML is the whole CR with config.Route's rules replaced by the fence artifact. It serves only
// the capability probe, which needs complete CRs; the fence itself is a route patch.
func deriveFencedCRYAML(config *migration.MigrationConfig) ([]byte, error) {
	base, err := parseGatewayYAML(config)
	if err != nil {
		return nil, err
	}
	return gateway.ReplaceRouteRulesObj(base, config.Route, []byte(config.FenceYAML))
}

// deriveSwitchedCRYAML is the whole CR with config.Route replaced by the converted static route, for the
// capability probe.
func deriveSwitchedCRYAML(config *migration.MigrationConfig) ([]byte, error) {
	base, err := parseGatewayYAML(config)
	if err != nil {
		return nil, err
	}
	return gateway.ReplaceRouteObj(base, config.Route, []byte(config.SwitchoverYAML))
}

// rulesRoutePatch sets config.Route's rules to artifact's rules block. The fence and its rollback are both
// one.
func rulesRoutePatch(config *migration.MigrationConfig, artifact string) (gateway.RoutePatch, error) {
	v, err := gateway.FragmentValue([]byte(artifact), "rules")
	if err != nil {
		return gateway.RoutePatch{}, err
	}
	return gateway.RoutePatch{RouteName: config.Route, Field: "rules", Value: v}, nil
}

func deriveFenceRoutePatch(config *migration.MigrationConfig) (gateway.RoutePatch, error) {
	return rulesRoutePatch(config, config.FenceYAML)
}

func deriveUnfenceRoutePatch(config *migration.MigrationConfig) (gateway.RoutePatch, error) {
	return rulesRoutePatch(config, config.RollbackFenceYAML)
}

// deriveSwitchRoutePatch replaces config.Route whole with the converted route, as AAO's static switch does
// (migration.wholeRoutePatch): a field patch cannot remove streamingDomains and rules.
func deriveSwitchRoutePatch(config *migration.MigrationConfig) (gateway.RoutePatch, error) {
	route, err := gateway.FragmentValue([]byte(config.SwitchoverYAML), "route")
	if err != nil {
		return gateway.RoutePatch{}, err
	}
	return gateway.RoutePatch{RouteName: config.Route, Value: route}, nil
}

// verifier builds the shared gateway apply/wait/verify mechanism with this run's capability and timeouts.
func (a *D2SActions) verifier() *gateway.TransitionVerifier {
	return &gateway.TransitionVerifier{
		Service:          a.gatewayService,
		Reporter:         a.reporter,
		Capability:       a.gatewayCapability,
		RolloutTimeout:   a.rolloutTimeout,
		HotReloadTimeout: a.hotReloadTimeout,
	}
}

// ensureGatewayCapability resolves the gateway capability at most once per process and proves hot-reload
// works when the gateway claims it; the first gateway-touching step (Fence, Switch, or a rollback's unfence)
// does it.
func (a *D2SActions) ensureGatewayCapability(ctx context.Context, config *migration.MigrationConfig) error {
	if a.capabilityResolved {
		return nil
	}
	if err := a.resolveGatewayCapability(ctx, config); err != nil {
		return err
	}
	if err := a.verifier().VerifyHotReloadCapability(ctx, config.K8sNamespace, config.InitialCrName, config.GatewayConfigPort); err != nil {
		return err
	}
	a.capabilityResolved = true
	return nil
}

// resolveGatewayCapability probes the live gateway with the fenced CR and the switched CR, so the detector
// sees the spec.hotReload either one would put in force.
func (a *D2SActions) resolveGatewayCapability(ctx context.Context, config *migration.MigrationConfig) error {
	if config.GatewayConfigPort == 0 {
		config.GatewayConfigPort = gateway.DefaultGatewayConfigPort
	}
	fencedCR, err := deriveFencedCRYAML(config)
	if err != nil {
		return fmt.Errorf("failed to derive fenced gateway CR: %w", err)
	}
	switchedCR, err := deriveSwitchedCRYAML(config)
	if err != nil {
		return fmt.Errorf("failed to derive switched gateway CR: %w", err)
	}
	capability, err := a.verifier().ResolveCapability(ctx, config.K8sNamespace, config.InitialCrName, config.GatewayConfigPort, fencedCR, switchedCR)
	if err != nil {
		return err
	}
	a.gatewayCapability = capability
	if capability.Mode == gateway.VerifyPerPodConfigID {
		a.reporter.Success("Gateway transitions will be verified per pod via %s", gateway.GatewayConfigEndpointPath)
	}
	return nil
}

func (a *D2SActions) patchGatewayRoute(ctx context.Context, config *migration.MigrationConfig, rp gateway.RoutePatch, step string) (gateway.ApplyResult, error) {
	return a.verifier().PatchCR(ctx, config.K8sNamespace, config.InitialCrName, rp, step)
}

func (a *D2SActions) waitForGatewayAccepted(ctx context.Context, config *migration.MigrationConfig, step string) error {
	return a.verifier().WaitForAccepted(ctx, config.K8sNamespace, config.InitialCrName, step)
}

func (a *D2SActions) verifyGatewayTransition(ctx context.Context, config *migration.MigrationConfig, applied gateway.ApplyResult, step string) error {
	return a.verifier().VerifyTransition(ctx, config.K8sNamespace, config.InitialCrName, config.GatewayConfigPort, applied, step)
}
