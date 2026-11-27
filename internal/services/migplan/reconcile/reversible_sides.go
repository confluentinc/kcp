package reconcile

// ResolveSides works out each cluster's role from the live static route. ok is
// false when the route is missing or not static, is bound to neither declared
// domain, or WritableOn names neither domain; the route checks report why.
func ResolveSides(in ReversibleInput, gw *GatewayConfig) (Sides, bool) {
	if gw == nil || gw.Route == nil || gw.Route.Name != in.Route || gw.Route.Mode != RouteModeStatic {
		return Sides{}, false
	}
	side := func(domain string) (Side, bool) {
		switch domain {
		case "":
			return "", false
		case in.SourceDomain:
			return SideSource, true
		case in.TargetDomain:
			return SideTarget, true
		}
		return "", false
	}
	bound, ok := side(boundDomain(gw.Route))
	if !ok {
		return Sides{}, false
	}
	writable, ok := side(in.WritableOn)
	if !ok {
		return Sides{}, false
	}
	s := Sides{
		Writable:       writable,
		Mirror:         writable.Other(),
		WritableDomain: in.Domain(writable),
		MirrorDomain:   in.Domain(writable.Other()),
		Bound:          bound,
		Fenced:         hasKcpStaticFence(gw.Route.Raw),
	}
	s.BackOut = s.Bound == s.Writable && s.Fenced
	s.Moving = s.Bound == s.Mirror || s.BackOut
	return s, true
}

// boundDomain is the static route's streamingDomain name, "" when it has none.
func boundDomain(rc *RouteConfig) string {
	sd, _ := mapField(rc.Raw, "streamingDomain")
	return stringField(sd, "name")
}
