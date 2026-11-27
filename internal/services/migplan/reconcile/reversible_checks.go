package reconcile

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	rsBoundDomainCheckName   = "route is bound to one of the two declared domains"
	rsDomainsCheckName       = "both domains are declared with exactly one bootstrap server id"
	rsStagedAuthCheckName    = "route carries pre-staged auth for both domains"
	rsSecretsCheckName       = "staged auth secrets exist"
	rsLinkExistsCheckName    = "cluster link exists on both clusters"
	rsTargetIDCheckName      = "target cluster matches the manifest"
	rsSourceIDCheckName      = "source cluster matches the manifest"
	rsRemoteIDsCheckName     = "each link copy points at the other cluster"
	rsOneLinkCheckName       = "both link copies are one link"
	rsHealthyCheckName       = "cluster link is healthy on both copies"
	rsBidirectionalCheckName = "cluster link is bidirectional on both copies"
	rsNoPrefixCheckName      = "cluster link has no prefix on either copy"
	rsOffsetSyncCheckName    = "consumer offset sync is on for both copies"
	rsGroupFilterCheckName   = "consumer offset group filter is the single * LOCAL_MIRROR entry on both copies"
	rsGroupFilterFix         = `set consumer.offset.group.filters to {"groupFilters":[{"name":"*","patternType":"LITERAL","filterType":"INCLUDE","topicTypes":["LOCAL_MIRROR"]}]} on both copies (configs:alter; takes effect without a link restart)`
	rsSectionKey             = "reversibleStaticMigration"
	rsBidirectionalLinkMode  = "BIDIRECTIONAL"
)

var bothSides = []Side{SideSource, SideTarget}

func copyLabel(s Side) string { return fmt.Sprintf("the %s copy of the link", s) }

// failOrPass fails name with the problems joined, or passes it when there are
// none.
func failOrPass(name string, problems []string) PreconditionResult {
	if len(problems) == 0 {
		return pass(name)
	}
	return fail(name, strings.Join(problems, "; "))
}

// checkReversibleRoute runs the gateway checks, in order: the route exists, is
// static, is bound to a declared domain, carries no foreign fence; both
// domains are declared with one bootstrap server id each; the route stages auth
// for both, and those secrets exist. A missing route stops it there.
func checkReversibleRoute(in ReversibleInput, f ReversibleFacts) []PreconditionResult {
	gw := f.Gateway
	if gw == nil || gw.Route == nil || gw.Route.Name != in.Route {
		return []PreconditionResult{fail("route exists", fmt.Sprintf("route %q not found in the gateway CR", in.Route))}
	}
	rc := gw.Route
	res := []PreconditionResult{pass("route exists")}

	if rc.Mode == RouteModeStatic {
		res = append(res, pass("route is static"))
	} else {
		res = append(res, fail("route is static", fmt.Sprintf("%s is declared but route %q is %q; it requires a static (all-at-once) route", rsSectionKey, rc.Name, rc.Mode)))
	}

	switch bound := boundDomain(rc); {
	case bound == "":
		res = append(res, fail(rsBoundDomainCheckName, fmt.Sprintf("route %q has no streamingDomain binding", rc.Name)))
	case bound != in.SourceDomain && bound != in.TargetDomain:
		res = append(res, fail(rsBoundDomainCheckName, fmt.Sprintf("route %q is bound to %q, which is neither route.sourceStreamingDomain (%q) nor route.targetStreamingDomain (%q)",
			rc.Name, bound, in.SourceDomain, in.TargetDomain)))
	default:
		res = append(res, pass(rsBoundDomainCheckName))
	}

	if fence, ok := mapField(rc.Raw, "fence"); ok && !isUnfencedStatic(fence) && !isKcpStaticFence(fence) {
		res = append(res, fail(staticFenceCheckName, fmt.Sprintf("route %q already carries a fence kcp didn't write (%s); remove it before migrating", rc.Name, renderFence(fence))))
	} else {
		res = append(res, pass(staticFenceCheckName))
	}

	domains := staticDomainBootstrapIDs(gw)
	var domainProblems, authProblems []string
	for _, s := range bothSides {
		d := in.Domain(s)
		ids, declared := domains[d]
		switch {
		case !declared:
			domainProblems = append(domainProblems, fmt.Sprintf("streaming domain %q is not declared in the gateway CR's spec.streamingDomains", d))
		case len(ids) != 1:
			domainProblems = append(domainProblems, fmt.Sprintf("streaming domain %q declares %d bootstrap server ids; kcp cannot pick one for a multi-homed domain", d, len(ids)))
		}
		if p := stagedAuthProblem(in.Route, rc.Raw, d); p != "" {
			authProblems = append(authProblems, p)
		}
	}
	res = append(res, failOrPass(rsDomainsCheckName, domainProblems), failOrPass(rsStagedAuthCheckName, authProblems))

	switch {
	case f.SecretCheckSkipped != "":
		res = append(res, skip(rsSecretsCheckName, f.SecretCheckSkipped))
	case len(f.MissingSecrets) > 0:
		res = append(res, fail(rsSecretsCheckName, fmt.Sprintf("secret(s) %s referenced by route %q's staged auth do not exist", strings.Join(f.MissingSecrets, ", "), in.Route)))
	default:
		res = append(res, pass(rsSecretsCheckName))
	}
	return res
}

// checkReversibleLink runs the identity and link checks, in order: both copies
// exist; each cluster is the one the manifest pins; each copy points at the
// other cluster and both are one link; then each copy is healthy,
// bidirectional, unprefixed, syncs offsets and carries the * group filter. A
// missing copy stops it after the cluster checks. Every ID check fails closed
// on an empty value.
func checkReversibleLink(in ReversibleInput, f ReversibleFacts) []PreconditionResult {
	var missing []string
	for _, s := range bothSides {
		if !f.Link(s).Found {
			missing = append(missing, fmt.Sprintf("the cluster link %q was never created on the %s cluster", in.LinkName, s))
		}
	}
	res := []PreconditionResult{
		failOrPass(rsLinkExistsCheckName, missing),
		clusterIDCheck(rsTargetIDCheckName, SideTarget, in.TargetClusterID, f.LiveTargetClusterID),
		clusterIDCheck(rsSourceIDCheckName, SideSource, in.SourceClusterID, f.LiveSourceClusterID),
	}
	if len(missing) > 0 {
		return res
	}

	var remote []string
	for _, s := range bothSides {
		want := in.TargetClusterID
		if s == SideTarget {
			want = in.SourceClusterID
		}
		switch got := f.Link(s).RemoteClusterID; {
		case got == "":
			remote = append(remote, fmt.Sprintf("%s reports no remote_cluster_id, so kcp cannot prove it points at the %s cluster", copyLabel(s), s.Other()))
		case got != want:
			remote = append(remote, fmt.Sprintf("%s points at cluster %q, not the %s cluster (%q)", copyLabel(s), got, s.Other(), want))
		}
	}
	res = append(res, failOrPass(rsRemoteIDsCheckName, remote))

	switch src, tgt := f.SourceLink.LinkID, f.TargetLink.LinkID; {
	case src == "" || tgt == "":
		res = append(res, fail(rsOneLinkCheckName, fmt.Sprintf("a link copy reports no link_id (source copy %q, target copy %q), so kcp cannot prove both copies are one link", src, tgt)))
	case src != tgt:
		res = append(res, fail(rsOneLinkCheckName, fmt.Sprintf("the source copy of the link has link_id %q and the target copy %q; they are two links that share the name %q", src, tgt, in.LinkName)))
	default:
		res = append(res, pass(rsOneLinkCheckName))
	}

	perCopy := func(name string, problem func(c LinkCopy) string) PreconditionResult {
		var problems []string
		for _, s := range bothSides {
			if p := problem(f.Link(s)); p != "" {
				problems = append(problems, copyLabel(s)+" "+p)
			}
		}
		return failOrPass(name, problems)
	}
	res = append(res,
		perCopy(rsHealthyCheckName, func(c LinkCopy) string {
			if linkHealthy(c.LinkError) {
				return ""
			}
			return fmt.Sprintf("reports link_error=%s, so nothing is being replicated on it; fix the link (credentials, connectivity) and rerun", c.LinkError)
		}),
		perCopy(rsBidirectionalCheckName, func(c LinkCopy) string {
			switch c.Mode {
			case rsBidirectionalLinkMode:
				return ""
			case "":
				return "reports no link.mode; " + rsSectionKey + " needs link.mode=BIDIRECTIONAL on both copies"
			}
			return fmt.Sprintf("has link.mode=%s; %s needs link.mode=BIDIRECTIONAL on both copies", c.Mode, rsSectionKey)
		}),
		perCopy(rsNoPrefixCheckName, func(c LinkCopy) string {
			if c.Prefix == "" {
				return ""
			}
			return fmt.Sprintf("has cluster.link.prefix=%q; %s doesn't support a prefixed link, because the Gateway routes by topic name", c.Prefix, rsSectionKey)
		}),
		perCopy(rsOffsetSyncCheckName, func(c LinkCopy) string {
			if c.OffsetSyncEnabled {
				return ""
			}
			return "does not have consumer.offset.sync.enable=true, so consumer offsets would not follow a reversal"
		}),
	)

	filter := perCopy(rsGroupFilterCheckName, func(c LinkCopy) string {
		if p := groupFilterProblem(c.GroupFilters); p != "" {
			return "has a consumer.offset.group.filters that " + p
		}
		return ""
	})
	if !filter.OK {
		filter.Detail += "; " + rsGroupFilterFix
	}
	return append(res, filter)
}

// clusterIDCheck compares a side's live Kafka-protocol cluster ID with the
// manifest's pin, failing closed when either is empty.
func clusterIDCheck(name string, s Side, pinned, live string) PreconditionResult {
	switch {
	case pinned == "":
		return fail(name, fmt.Sprintf("spec.%s.clusterId is empty, so kcp cannot prove which cluster it read", s))
	case live == "":
		return fail(name, fmt.Sprintf("the %s cluster's ID could not be read over the Kafka protocol, so kcp cannot prove it is spec.%s (%q)", s, s, pinned))
	case live != pinned:
		return fail(name, fmt.Sprintf("spec.%s.clusterId is %q, but the %s cluster reports %q", s, pinned, s, live))
	}
	return pass(name)
}

type groupFilterEntry struct {
	Name        string   `json:"name"`
	PatternType string   `json:"patternType"`
	FilterType  string   `json:"filterType"`
	TopicTypes  []string `json:"topicTypes"`
}

// groupFilterProblem says how raw, a copy's consumer.offset.group.filters, falls
// short of exactly one {"name":"*","patternType":"LITERAL","filterType":
// "INCLUDE","topicTypes":["LOCAL_MIRROR"]} entry; "" when it is that entry. The
// comparison is semantic: key order, whitespace and the enums' case don't
// matter, but the name must be exactly *. Only the * entry is accepted: it
// covers every group, including ones created after reconcile, and LOCAL_MIRROR
// alone keeps each copy syncing offsets only for the topics it mirrors.
func groupFilterProblem(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "is empty, so no consumer group's offsets are synced"
	}
	var doc struct {
		GroupFilters []groupFilterEntry `json:"groupFilters"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return fmt.Sprintf("is not valid JSON (%v)", err)
	}
	if len(doc.GroupFilters) == 0 {
		return "is empty, so no consumer group's offsets are synced"
	}
	e := doc.GroupFilters[0]
	if len(doc.GroupFilters) > 1 || e.Name != "*" || !strings.EqualFold(e.PatternType, "LITERAL") || !strings.EqualFold(e.FilterType, "INCLUDE") {
		return "has a named, prefixed or exclusion entry; kcp currently supports only the * filter"
	}
	if len(e.TopicTypes) == 0 {
		return "includes every group (*) without topicTypes, so offsets sync in both directions and each copy overwrites the positions the other syncs"
	}
	for _, t := range e.TopicTypes {
		if strings.EqualFold(t, "REMOTE_MIRROR") {
			return "includes REMOTE_MIRROR in topicTypes, so offsets also sync for the topics this copy is the source of"
		}
	}
	if len(e.TopicTypes) != 1 || !strings.EqualFold(e.TopicTypes[0], "LOCAL_MIRROR") {
		return fmt.Sprintf("has topicTypes %v; only [\"LOCAL_MIRROR\"] is supported", e.TopicTypes)
	}
	return ""
}
