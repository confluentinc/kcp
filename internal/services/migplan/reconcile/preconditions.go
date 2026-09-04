package reconcile

import "fmt"

// ReconcileInput is the engine-owned request, decoupled from the manifest.
type ReconcileInput struct {
	Topics        []string
	TopicPatterns []string
	Route         string
	TargetDomain  string
}

type GatewayConfig struct {
	Route *RouteConfig // the single route named by the input, resolved by the provider
}

type RouteConfig struct {
	Name         string
	Mode         string   // "static" | "dynamic"
	BoundDomains []string // the route's bound streaming-domain names
	Rules        map[string]any
}

func pass(name string) PreconditionResult { return PreconditionResult{Name: name, OK: true} }
func fail(name, detail string) PreconditionResult {
	return PreconditionResult{Name: name, OK: false, Detail: detail}
}

// CheckPreconditions runs the dynamic-route run-level checks. ok is true
// only if every check passed; on success view carries the resolved domains.
func CheckPreconditions(in ReconcileInput, gw *GatewayConfig, offsetSyncEnabled bool) ([]PreconditionResult, RouteView, bool) {
	var res []PreconditionResult
	var view RouteView

	if gw == nil || gw.Route == nil {
		return []PreconditionResult{fail("route exists", fmt.Sprintf("route %q not found in the gateway CR", in.Route))}, view, false
	}
	rc := gw.Route

	if rc.Mode == "dynamic" {
		res = append(res, pass("route is dynamic"))
	} else {
		res = append(res, fail("route is dynamic", fmt.Sprintf("route %q is %q; this engine handles dynamic routes only", rc.Name, rc.Mode)))
	}

	if len(rc.BoundDomains) == 2 {
		res = append(res, pass("route binds exactly two domains"))
	} else {
		res = append(res, fail("route binds exactly two domains", fmt.Sprintf("route binds %d domains %v; v1 requires exactly two", len(rc.BoundDomains), rc.BoundDomains)))
	}

	bound := false
	var source string
	for _, d := range rc.BoundDomains {
		if d == in.TargetDomain {
			bound = true
		} else {
			source = d
		}
	}
	if bound {
		res = append(res, pass("target domain is bound"))
	} else {
		res = append(res, fail("target domain is bound", fmt.Sprintf("targetStreamingDomain %q is not bound to route %q (bound: %v)", in.TargetDomain, rc.Name, rc.BoundDomains)))
	}

	rt, _ := ParseRules(routingParent(rc.Rules))
	group := rt.CoordinationGroup()
	if bound && len(rc.BoundDomains) == 2 && group == source && source != "" {
		res = append(res, pass("coordination.group pinned on source"))
	} else {
		res = append(res, fail("coordination.group pinned on source", fmt.Sprintf("coordination.group is %q; must be the source domain %q", group, source)))
	}

	if !offsetSyncEnabled {
		res = append(res, pass("consumer offset sync disabled on link"))
	} else {
		res = append(res, fail("consumer offset sync disabled on link", "consumer offset sync is enabled on the cluster link; disable it for a dynamic-route migration"))
	}

	ok := true
	for _, r := range res {
		if !r.OK {
			ok = false
		}
	}
	if ok {
		view = rt.Project()
		view.SourceDomain = source
		view.TargetDomain = in.TargetDomain
	}
	return res, view, ok
}

// routingParent adapts a RouteConfig.Rules map (which has the rules subtree
// directly, i.e. {routing:{...}, fencing:[...]}) for ParseRules.
func routingParent(rules map[string]any) map[string]any {
	if rules == nil {
		return map[string]any{}
	}
	return rules
}
