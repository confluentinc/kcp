package tbm

import (
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration/migrun"
)

// The fence, switch and rollback each set config.Route's rules from a
// reconcile artifact, applied unmodified (migplan.Reconcile's artifacts already
// satisfy the Gateway CRD). Each change yields the route patch a write applies
// and the whole CR the gateway capability check inspects, so they cannot drift.
func fenceChange(config *migrun.MigrationConfig) gateway.RouteChange {
	return gateway.RouteChange{Edit: gateway.EditRouteRules, Artifact: []byte(config.FenceYAML)}
}

func switchChange(config *migrun.MigrationConfig) gateway.RouteChange {
	return gateway.RouteChange{Edit: gateway.EditRouteRules, Artifact: []byte(config.SwitchoverYAML)}
}

func unfenceChange(config *migrun.MigrationConfig) gateway.RouteChange {
	return gateway.RouteChange{Edit: gateway.EditRouteRules, Artifact: []byte(config.RollbackFenceYAML)}
}
