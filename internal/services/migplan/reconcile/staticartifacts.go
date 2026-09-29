package reconcile

import "github.com/goccy/go-yaml"

// staticFenceScope and staticFenceErrorCode are the fence parameters this
// migration injects — the same values gateway.FenceRoutes hardcodes today
// (see internal/services/gateway/fence.go). Duplicated here rather than
// imported, to avoid a new dependency from reconcile on
// internal/services/gateway.
const (
	staticFenceScope     = "ALL"
	staticFenceErrorCode = "BROKER_NOT_AVAILABLE"
)

// BuildFenceFragment returns the fence block's value as a small,
// route-agnostic fragment — {fence: {scope, errorCode}} — mirroring
// RulesTree.Serialize's shape (a wrapped value, not a whole CR). Splicing
// this onto the named route's fence key in a whole CR, and applying the
// result, is deferred to later FSM-integration work — not done here.
func BuildFenceFragment() ([]byte, error) {
	return yaml.Marshal(map[string]any{"fence": map[string]any{
		"scope":     staticFenceScope,
		"errorCode": staticFenceErrorCode,
	}})
}

// BuildSwitchoverRoute returns the whole switched route, wrapped as
// {route: …}: the start-of-run route (raw, as pulled from the live CR) with its
// fence removed and its streamingDomain bound to the target. It is a whole
// route because the switched route must carry no fence, and a route patch can
// set a field but not remove one. bootstrapServerID is CheckStaticPreconditions'
// own derived id (StaticRouteView.BootstrapServerID). raw is not modified.
func BuildSwitchoverRoute(raw map[string]any, targetDomain, bootstrapServerID string) ([]byte, error) {
	route := routeWithoutFence(raw)
	route["streamingDomain"] = map[string]any{
		"name":              targetDomain,
		"bootstrapServerId": bootstrapServerID,
	}
	return yaml.Marshal(map[string]any{"route": route})
}

// BuildRollbackFenceRoute returns the route as a rollback leaves it, wrapped as
// {route: …}: the start-of-run route (raw) with its fence removed and every
// other field, including its source streamingDomain, unchanged. On a resume the
// start-of-run route already carries the interrupted run's fence; this is what
// takes it out. raw is not modified.
func BuildRollbackFenceRoute(raw map[string]any) ([]byte, error) {
	return yaml.Marshal(map[string]any{"route": routeWithoutFence(raw)})
}

// routeWithoutFence returns a copy of raw without its fence key. Only
// top-level keys are set or deleted on the copy, so a shallow copy is enough.
func routeWithoutFence(raw map[string]any) map[string]any {
	route := make(map[string]any, len(raw))
	for k, v := range raw {
		route[k] = v
	}
	delete(route, "fence")
	return route
}
