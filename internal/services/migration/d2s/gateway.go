package d2s

import (
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
