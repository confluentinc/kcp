package tbm

import (
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
// separately-snapshotted fenced CR: this and the capability probe's
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
// itself applies. It also independently serves the capability
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
// fragment onto config.Route. TBM's fence, switch and rollback all patch the
// same "rules" field — they differ only in which reconcile artifact the
// fragment comes from.
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

// deriveUnfenceRoutePatch builds the RoutePatch a rollback applies: reconcile's
// rollback target (config.RollbackFenceYAML), set on config.Route's rules like
// the fence and the switch.
func deriveUnfenceRoutePatch(config *migration.MigrationConfig) (gateway.RoutePatch, error) {
	return deriveRulesRoutePatch(config, config.RollbackFenceYAML)
}
