package tbm

import (
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration"
)

// The fence, switch and rollback each set config.Route's rules from a
// reconcile artifact, applied unmodified (migplan.Reconcile's artifacts already
// satisfy the Gateway CRD). Each change yields the RoutePatch a write applies
// and the whole CR the capability probe inspects, so they cannot drift.
func fenceChange(config *migration.MigrationConfig) gateway.RouteChange {
	return gateway.RouteChange{Edit: gateway.EditRouteRules, Artifact: []byte(config.FenceYAML)}
}

func switchChange(config *migration.MigrationConfig) gateway.RouteChange {
	return gateway.RouteChange{Edit: gateway.EditRouteRules, Artifact: []byte(config.SwitchoverYAML)}
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
