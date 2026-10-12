package reconcile

import "github.com/goccy/go-yaml"

// staticFenceScope and staticFenceErrorCode are the fence kcp writes on a
// static route (BuildFenceFragment), and the exact value it recognises as its
// own fence (isKcpStaticFence).
const (
	staticFenceScope     = "ALL"
	staticFenceErrorCode = "BROKER_NOT_AVAILABLE"
)

// BuildFenceFragment returns the fence block's value as a small,
// route-agnostic fragment — {fence: {scope, errorCode}} — mirroring
// RulesTree.Serialize's shape (a wrapped value, not a whole CR). The static
// FSM's fence step sets it on the named route's fence key.
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
