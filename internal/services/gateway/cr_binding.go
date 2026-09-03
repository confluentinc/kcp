package gateway

import "fmt"

// RouteMode is a route's migration mode, resolved solely from the live initial
// CR's route-scoped binding shape (D5) — there is no manifest mode field.
type RouteMode int

const (
	// RouteModeStatic is the all-at-once workflow: the route binds a single
	// streaming domain (spec.routes[i].streamingDomain, singular object).
	RouteModeStatic RouteMode = iota
	// RouteModeDynamic is the topic-based (TBM) workflow: the route binds a
	// list of domains (spec.routes[i].streamingDomains, plural array).
	RouteModeDynamic
)

// ResolveRouteMode reads the migration mode for routeName from the initial CR's
// route-scoped binding (D5): a singular streamingDomain object ⇒ static; a
// plural streamingDomains array ⇒ dynamic. The two are mutually exclusive per
// route (the CRD enforces this with a CEL XOR). A route absent from the CR, or
// a hand-edited route carrying neither or both bindings, is an error — mode is
// never silently defaulted to static.
func ResolveRouteMode(crYAML []byte, routeName string) (RouteMode, error) {
	cr, err := parseGatewayCR("initial", crYAML)
	if err != nil {
		return 0, err
	}
	route, ok := routesByName(cr)[routeName]
	if !ok {
		return 0, fmt.Errorf("route %q is not present in the initial gateway CR's spec.routes", routeName)
	}
	_, hasSingular := mapField(route, "streamingDomain")
	_, hasPlural := sliceField(route, "streamingDomains")
	switch {
	case hasSingular && hasPlural:
		return 0, fmt.Errorf("cannot resolve mode for route %q: it declares both a singular streamingDomain and a plural streamingDomains binding", routeName)
	case hasSingular:
		return RouteModeStatic, nil
	case hasPlural:
		return RouteModeDynamic, nil
	default:
		return 0, fmt.Errorf("cannot resolve mode for route %q: it declares neither a streamingDomain nor a streamingDomains binding", routeName)
	}
}

// DeriveBootstrapServerID resolves the single bootstrap server id declared for
// domainName under the CR's spec.streamingDomains[].kafkaCluster.bootstrapServers
// (D1). The resolution is three-way: zero ids (the domain is absent from
// spec.streamingDomains — a targetStreamingDomain typo — or is declared with no
// ids, both collapsing to an empty set) is a hard error; exactly one id is used;
// more than one (a multi-homed domain, D1a) is a hard error naming the domain
// and its ids, since this manifest shape carries no disambiguator.
func DeriveBootstrapServerID(crYAML []byte, domainName string) (string, error) {
	cr, err := parseGatewayCR("initial", crYAML)
	if err != nil {
		return "", err
	}
	domains := domainBootstrapIDs(cr)
	ids := domains[domainName]
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("the initial gateway CR declares no bootstrap server id for streaming domain %q (declared domains: %s) — check spec.topicGroup[0].targetStreamingDomain against the CR's spec.streamingDomains", domainName, describeSet(domains))
	case 1:
		for id := range ids {
			return id, nil
		}
	}
	return "", fmt.Errorf("streaming domain %q declares %d bootstrap server ids (%s) — kcp cannot pick one for a multi-homed domain", domainName, len(ids), describeSet(ids))
}
