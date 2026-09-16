package reconcile

import (
	"fmt"
	"sort"
)

// StaticRouteView carries the static-mode facts CheckStaticPreconditions
// resolves for its caller to reuse downstream: whether the route's current
// binding already equals the target domain (RoutesToTarget, applied
// uniformly to every candidate topic's Classify call — a static route
// cannot bind different topics to different domains, unlike a dynamic
// route's per-topic OwnerRoute lookup), and the single bootstrap server id
// the switchover fragment needs (DeriveBootstrapServerID-equivalent,
// resolved once here rather than duplicated by the artifact builder).
type StaticRouteView struct {
	RoutesToTarget    bool
	BootstrapServerID string
}

// CheckStaticPreconditions runs the static-route run-level checks — the
// static-mode analog of CheckPreconditions. ok is true only if every check
// passed. missingSecrets is a plain-data fact already gathered by the I/O
// layer (via ResolveStagedSecretNames + a SecretExistenceChecker) — this
// pure function never does I/O itself. See the migplan
// static-route-strategy design doc, decision 10, for the exact check list.
func CheckStaticPreconditions(in ReconcileInput, gw *GatewayConfig, missingSecrets []string, ids ClusterIDs) ([]PreconditionResult, StaticRouteView, bool) {
	var res []PreconditionResult
	var view StaticRouteView

	if gw == nil || gw.Route == nil || gw.Route.Name != in.Route {
		return []PreconditionResult{fail("route exists", fmt.Sprintf("route %q not found in the gateway CR", in.Route))}, view, false
	}
	rc := gw.Route

	if rc.Mode == "static" {
		res = append(res, pass("route is static"))
	} else {
		res = append(res, fail("route is static", fmt.Sprintf("route %q is %q; the static (all-at-once) strategy requires a static route", rc.Name, rc.Mode)))
	}

	domains := staticDomainBootstrapIDs(gw)
	ids2, declared := domains[in.TargetDomain]
	var bootstrapServerID string
	switch {
	case !declared:
		res = append(res, fail("target domain is declared", fmt.Sprintf("targetStreamingDomain %q is not declared in the gateway CR's spec.streamingDomains", in.TargetDomain)))
	case len(ids2) == 1:
		for id := range ids2 {
			bootstrapServerID = id
		}
		res = append(res, pass("target domain declares exactly one bootstrap server id"))
	default:
		res = append(res, fail("target domain declares exactly one bootstrap server id", fmt.Sprintf("streaming domain %q declares %d bootstrap server ids; kcp cannot pick one for a multi-homed domain", in.TargetDomain, len(ids2))))
	}
	view.BootstrapServerID = bootstrapServerID

	routesToTarget := false
	if sd, ok := mapField(rc.Raw, "streamingDomain"); ok {
		routesToTarget = stringField(sd, "name") == in.TargetDomain
	}
	view.RoutesToTarget = routesToTarget
	if routesToTarget {
		res = append(res, fail("route is not already bound to the target domain", fmt.Sprintf("route %q is already bound to streaming domain %q — the switch would be a no-op", in.Route, in.TargetDomain)))
	} else {
		res = append(res, pass("route is not already bound to the target domain"))
	}

	staged, hasStaged := stagedAuthFor(rc.Raw, in.TargetDomain)
	switch {
	case !hasStaged:
		res = append(res, fail("route carries pre-staged auth for the target domain", fmt.Sprintf("route %q has no pre-staged security.cluster.%s block — the redundant auth this switch depends on is not configured", in.Route, in.TargetDomain)))
	case stringField(staged, "secretStore") == "":
		res = append(res, fail("route carries pre-staged auth for the target domain", fmt.Sprintf("route %q's security.cluster.%s has no secretStore", in.Route, in.TargetDomain)))
	default:
		if auth, ok := mapField(staged, "authentication"); !ok || len(auth) == 0 {
			res = append(res, fail("route carries pre-staged auth for the target domain", fmt.Sprintf("route %q's security.cluster.%s has no authentication block", in.Route, in.TargetDomain)))
		} else {
			res = append(res, pass("route carries pre-staged auth for the target domain"))
		}
	}

	if len(missingSecrets) > 0 {
		res = append(res, fail("staged auth secrets exist", fmt.Sprintf("secret(s) %s referenced by route %q's staged auth for %q do not exist", joinNames(missingSecrets), in.Route, in.TargetDomain)))
	} else {
		res = append(res, pass("staged auth secrets exist"))
	}

	res = checkClusterIdentities(res, in, ids)

	ok := true
	for _, r := range res {
		if !r.OK {
			ok = false
		}
	}
	return res, view, ok
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}

// staticDomainBootstrapIDs maps each streaming domain the CR declares (at
// spec.streamingDomains[]) to the bootstrap server ids it defines under
// kafkaCluster.bootstrapServers. Mirrors gateway.domainBootstrapIDs.
func staticDomainBootstrapIDs(gw *GatewayConfig) map[string]map[string]struct{} {
	domains := map[string]map[string]struct{}{}
	if gw == nil || gw.RawObj == nil {
		return domains
	}
	spec, ok := mapField(gw.RawObj, "spec")
	if !ok {
		return domains
	}
	rawDomains, ok := sliceField(spec, "streamingDomains")
	if !ok {
		return domains
	}
	for _, raw := range rawDomains {
		domain, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := stringField(domain, "name")
		if name == "" {
			continue
		}
		ids := map[string]struct{}{}
		cluster, _ := mapField(domain, "kafkaCluster")
		servers, _ := sliceField(cluster, "bootstrapServers")
		for _, rawServer := range servers {
			server, ok := rawServer.(map[string]any)
			if !ok {
				continue
			}
			if id := stringField(server, "id"); id != "" {
				ids[id] = struct{}{}
			}
		}
		domains[name] = ids
	}
	return domains
}

// stagedAuthFor returns route's pre-staged security.cluster.<domainName>
// block, if present. Mirrors gateway.stagedAuthFor.
func stagedAuthFor(route map[string]any, domainName string) (map[string]any, bool) {
	security, _ := mapField(route, "security")
	cluster, _ := mapField(security, "cluster")
	return mapField(cluster, domainName)
}

// staticSecretRefKeys are the Gateway CRD's Secret-naming field names —
// mirrors internal/services/gateway/validate.go's own secretRefKeys,
// duplicated per decision 13, not imported.
var staticSecretRefKeys = map[string]struct{}{
	"secretRef":            {},
	"configSecretRef":      {},
	"clientCredentialsRef": {},
}

// collectStaticSecretRefs recursively walks v and adds every
// staticSecretRefKeys-named string value found to names. Scoped by its
// caller (ResolveStagedSecretNames) to a single staged security.cluster.
// <domain> block, not the whole CR — a small, bounded subtree, so unlike
// gateway.collectSecretRefsGuarded (which walks a whole hand-authored CR and
// must guard against a YAML-alias "anchor bomb"), no cycle guard is needed
// here.
func collectStaticSecretRefs(v any, names map[string]struct{}) {
	switch node := v.(type) {
	case map[string]any:
		for key, child := range node {
			if _, known := staticSecretRefKeys[key]; known {
				if s, ok := child.(string); ok && s != "" {
					names[s] = struct{}{}
					continue
				}
			}
			collectStaticSecretRefs(child, names)
		}
	case []any:
		for _, child := range node {
			collectStaticSecretRefs(child, names)
		}
	}
}

// ResolveStagedSecretNames finds every Kubernetes Secret name gw.Route's
// pre-staged security.cluster.<targetDomain> block references — the
// secret(s) whose existence the redundant-auth switch depends on but has
// never exercised (see the migplan static-route-strategy design doc,
// decision 4). Exported for the I/O layer (engine.go) to call before
// building missingSecrets to pass into Reconcile — CheckStaticPreconditions
// itself never calls this; it only consumes the already-gathered
// missingSecrets fact. Takes no route name: gw.Route is already the one
// route this GatewayConfig was resolved for (see RouteConfig.Raw, Task 1) —
// there is nothing left to look up by name. Returns nil if the staged block
// isn't present; that absence is itself a precondition failure
// CheckStaticPreconditions reports separately, not an error here.
func ResolveStagedSecretNames(gw *GatewayConfig, targetDomain string) []string {
	if gw == nil || gw.Route == nil {
		return nil
	}
	staged, ok := stagedAuthFor(gw.Route.Raw, targetDomain)
	if !ok {
		return nil
	}
	names := map[string]struct{}{}
	collectStaticSecretRefs(staged, names)
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
