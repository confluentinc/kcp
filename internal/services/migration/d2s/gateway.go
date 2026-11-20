package d2s

import (
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration/migrun"
)

// The fence and its rollback set config.Route's rules; the switch replaces
// config.Route whole with the converted static route, as AAO's static switch
// does (a field patch cannot remove streamingDomains and rules). Each change
// yields the route patch a write applies and the whole CR the gateway capability check
// inspects.
func fenceChange(config *migrun.MigrationConfig) gateway.RouteChange {
	return gateway.RouteChange{Edit: gateway.EditRouteRules, Artifact: []byte(config.FenceYAML)}
}

func switchChange(config *migrun.MigrationConfig) gateway.RouteChange {
	return gateway.RouteChange{Edit: gateway.EditRoute, Artifact: []byte(config.SwitchoverYAML)}
}

func unfenceChange(config *migrun.MigrationConfig) gateway.RouteChange {
	return gateway.RouteChange{Edit: gateway.EditRouteRules, Artifact: []byte(config.RollbackFenceYAML)}
}
