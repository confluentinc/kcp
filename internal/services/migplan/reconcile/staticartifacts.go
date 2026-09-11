package reconcile

import "github.com/goccy/go-yaml"

// staticFenceScope and staticFenceErrorCode are the fence parameters this
// migration injects — the same values gateway.FenceRoutes hardcodes today
// (see internal/services/gateway/fence.go). Duplicated here per the migplan
// static-route-strategy design doc's decision 13 (no new dependency from
// reconcile on internal/services/gateway), not imported.
const (
	staticFenceScope     = "ALL"
	staticFenceErrorCode = "BROKER_NOT_AVAILABLE"
)

// BuildFenceFragment returns the fence block's value as a small,
// route-agnostic fragment — {fence: {scope, errorCode}} — mirroring
// RulesTree.Serialize's shape (a wrapped value, not a whole CR). Splicing
// this onto the named route's fence key in a whole CR, and applying the
// result, is deferred to later FSM-integration work — not done here. See
// the migplan static-route-strategy design doc, decision 11.
func BuildFenceFragment() ([]byte, error) {
	return yaml.Marshal(map[string]any{"fence": map[string]any{
		"scope":     staticFenceScope,
		"errorCode": staticFenceErrorCode,
	}})
}

// BuildSwitchoverFragment returns the streamingDomain block's value as a
// small, route-agnostic fragment — {streamingDomain: {name, bootstrapServerId}}.
// bootstrapServerID is CheckStaticPreconditions' own derived id
// (StaticRouteView.BootstrapServerID), resolved exactly once there.
func BuildSwitchoverFragment(targetDomain, bootstrapServerID string) ([]byte, error) {
	return yaml.Marshal(map[string]any{"streamingDomain": map[string]any{
		"name":              targetDomain,
		"bootstrapServerId": bootstrapServerID,
	}})
}
