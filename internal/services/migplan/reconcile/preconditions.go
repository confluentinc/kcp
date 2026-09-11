package reconcile

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/regexanchor"
)

// ReconcileInput is the engine-owned request, decoupled from the manifest.
type ReconcileInput struct {
	Topics        []string
	TopicPatterns []string
	Route         string
	TargetDomain  string
	// TargetClusterID is the operator's declared destination cluster
	// (spec.target.clusterId), checked against the cluster we actually read.
	TargetClusterID string
}

// ClusterIDs carries the live cluster identities gathered by the I/O layer, used
// to prove the clusters we read really are the migration's source and target.
// Any field may be empty when it could not be determined (e.g. a destination
// that does not report source_cluster_id) — an empty value skips its check
// rather than failing.
type ClusterIDs struct {
	Source     string // the source cluster's own Kafka cluster id (live)
	Target     string // the target cluster's own Kafka cluster id (live)
	LinkSource string // the cluster link's source_cluster_id (live)
}

type GatewayConfig struct {
	Route *RouteConfig // the single route named by the input, resolved by the provider

	// RawYAML is the whole gateway CR, cleaned of server-managed metadata
	// (see cleanGatewayDoc in migplan/gatewayfile.go) but otherwise exactly as
	// pulled. The reconcile core does not read it — it is provenance a caller
	// can diff against a later re-pull to detect drift before mutating; once
	// cleaned, the whole document is stable enough to diff directly (the
	// volatile fields that made a caller carve out just `spec` before are
	// gone).
	RawYAML string

	// RawObj is the same cleaned tree RawYAML re-marshals from, already
	// parsed — surfaced for the static-route strategy's precondition reads
	// that are CR-level, not route-level (declared spec.streamingDomains[]).
	// The reconcile core still does not mutate it.
	RawObj map[string]any
}

type RouteConfig struct {
	Name         string
	Mode         string   // "static" | "dynamic"
	BoundDomains []string // the route's bound streaming-domain names
	Rules        map[string]any

	// Raw is the route's own raw map exactly as found in spec.routes[] —
	// the same object findRoute already extracts Name/Mode/BoundDomains/Rules
	// from, surfaced here rather than re-derived by a second by-name lookup
	// into GatewayConfig.RawObj. Static preconditions need fields (security.
	// cluster, streamingDomain) none of this struct's other fields carry.
	Raw map[string]any
}

func pass(name string) PreconditionResult { return PreconditionResult{Name: name, OK: true} }
func fail(name, detail string) PreconditionResult {
	return PreconditionResult{Name: name, OK: false, Detail: detail}
}

// CheckPreconditions runs the dynamic-route run-level checks. ok is true
// only if every check passed; on success view carries the resolved domains.
func CheckPreconditions(in ReconcileInput, gw *GatewayConfig, offsetSyncEnabled bool, ids ClusterIDs) ([]PreconditionResult, RouteView, bool) {
	var res []PreconditionResult
	var view RouteView

	if gw == nil || gw.Route == nil {
		return []PreconditionResult{fail("route exists", fmt.Sprintf("route %q not found in the gateway CR", in.Route))}, view, false
	}
	rc := gw.Route

	if rc.Mode == "dynamic" {
		res = append(res, pass("route is dynamic"))
	} else {
		res = append(res, fail("route is dynamic", fmt.Sprintf("route %q is %q; only dynamic (topic-based) routes are currently supported", rc.Name, rc.Mode)))
	}

	if len(rc.BoundDomains) == 2 {
		res = append(res, pass("route binds exactly two domains"))
	} else {
		res = append(res, fail("route binds exactly two domains", fmt.Sprintf("route binds %d domains %v; a migration route must bind exactly two (source and target)", len(rc.BoundDomains), rc.BoundDomains)))
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

	// Cluster identity: the clusters we read must be the migration's real source
	// and destination. An empty id (destination omits source_cluster_id, or the
	// metadata could not be read) can't prove a mismatch, so it passes.
	switch {
	case ids.LinkSource == "" || ids.Source == "":
		res = append(res, pass("source cluster matches the cluster link"))
	case ids.LinkSource == ids.Source:
		res = append(res, pass("source cluster matches the cluster link"))
	default:
		res = append(res, fail("source cluster matches the cluster link",
			fmt.Sprintf("the cluster link mirrors from cluster %q, but spec.source is cluster %q", ids.LinkSource, ids.Source)))
	}

	switch {
	case in.TargetClusterID == "" || ids.Target == "":
		res = append(res, pass("target cluster matches the manifest"))
	case ids.Target == in.TargetClusterID:
		res = append(res, pass("target cluster matches the manifest"))
	default:
		res = append(res, fail("target cluster matches the manifest",
			fmt.Sprintf("spec.target.clusterId is %q, but the target cluster reports %q", in.TargetClusterID, ids.Target)))
	}

	// The operator's routing-condition patterns are used by OwnerRoute to resolve
	// which domain owns a topic. A pattern RE2 cannot compile (e.g. a Java-only
	// construct) would be silently treated as non-matching and mis-route the
	// topic, flipping its verdict. Validate them up front and refuse rather than
	// mis-classify.
	projected := rt.Project()
	if bad, patErr := firstUncompilablePattern(projected.Conditions); patErr != nil {
		res = append(res, fail("gateway routing patterns compile",
			fmt.Sprintf("routing condition pattern %q does not compile as an anchored full-match: %v", bad, patErr)))
	} else {
		res = append(res, pass("gateway routing patterns compile"))
	}

	ok := true
	for _, r := range res {
		if !r.OK {
			ok = false
		}
	}
	if ok {
		view = projected
		view.SourceDomain = source
		view.TargetDomain = in.TargetDomain
	}
	return res, view, ok
}

// firstUncompilablePattern returns the first routing-condition pattern that does
// not compile as an anchored full-match, and the compile error. It compiles via
// the same hardened anchoring OwnerRoute uses, so a pattern that passes here
// cannot escape its anchor there.
func firstUncompilablePattern(conditions []Condition) (string, error) {
	for _, c := range conditions {
		for _, p := range c.TopicPatterns {
			if _, err := regexanchor.Compile(p); err != nil {
				return p, err
			}
		}
	}
	return "", nil
}

// routingParent adapts a RouteConfig.Rules map (which has the rules subtree
// directly, i.e. {routing:{...}, fencing:[...]}) for ParseRules.
func routingParent(rules map[string]any) map[string]any {
	if rules == nil {
		return map[string]any{}
	}
	return rules
}
