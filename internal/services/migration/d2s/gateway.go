package d2s

import (
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration"
)

// The fence and its rollback set config.Route's rules; the switch replaces
// config.Route whole with the converted static route, as AAO's static switch
// does (a field patch cannot remove streamingDomains and rules). Each change
// yields the RoutePatch a write applies and the whole CR the capability probe
// inspects.
func fenceChange(config *migration.MigrationConfig) gateway.RouteChange {
	return gateway.RouteChange{Edit: gateway.EditRouteRules, Artifact: []byte(config.FenceYAML)}
}

func switchChange(config *migration.MigrationConfig) gateway.RouteChange {
	return gateway.RouteChange{Edit: gateway.EditRoute, Artifact: []byte(config.SwitchoverYAML)}
}

func unfenceChange(config *migration.MigrationConfig) gateway.RouteChange {
	return gateway.RouteChange{Edit: gateway.EditRouteRules, Artifact: []byte(config.RollbackFenceYAML)}
}

func deriveFenceRoutePatch(config *migration.MigrationConfig) (gateway.RoutePatch, error) {
	return fenceChange(config).Patch(config.Route)
}

func deriveSwitchRoutePatch(config *migration.MigrationConfig) (gateway.RoutePatch, error) {
	return switchChange(config).Patch(config.Route)
}

func deriveUnfenceRoutePatch(config *migration.MigrationConfig) (gateway.RoutePatch, error) {
	return unfenceChange(config).Patch(config.Route)
}
