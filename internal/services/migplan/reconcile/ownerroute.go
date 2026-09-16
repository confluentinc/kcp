package reconcile

import "github.com/confluentinc/kcp/internal/regexanchor"

// OwnerRoute resolves the domain that owns topic per the routing conditions:
// the first condition it matches (exact Topics OR anchored TopicPatterns, in
// list order), else defaultDomain. Returns ok=false only on a strict route
// (empty defaultDomain) with no matching condition.
//
// Mirrors the Gateway's DynamicRouterState.ownerRoute. Patterns are compiled
// here anchored full-match. A pattern that does not compile is treated as
// non-matching (fail closed) — the "gateway routing patterns compile"
// precondition refuses the run before this point if any operator pattern is
// uncompilable, so silent mis-routing cannot ship.
func OwnerRoute(topic string, conditions []Condition, defaultDomain string) (string, bool) {
	for _, c := range conditions {
		for _, t := range c.Topics {
			if t == topic {
				return c.Domain, true
			}
		}
		for _, p := range c.TopicPatterns {
			re, err := regexanchor.Compile(p)
			if err != nil {
				continue
			}
			if re.MatchString(topic) {
				return c.Domain, true
			}
		}
	}
	if defaultDomain == "" {
		return "", false
	}
	return defaultDomain, true
}

// OwnerRouteFromView is a small test helper that resolves a topic's owning
// domain directly from a projected RouteView.
func OwnerRouteFromView(v RouteView, topic string) string {
	d, _ := OwnerRoute(topic, v.Conditions, v.DefaultDomain)
	return d
}
