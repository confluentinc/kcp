package reconcile

import (
	"fmt"
	"strings"
)

const convertMode = "convert"

// convertToStatic is the only ReconcileInput.ConvertTo this strategy runs. It
// mirrors manifest.RouteConvertToStatic (the core does not import manifest).
const convertToStatic = "static"

// convertTargetCheckName names the precondition that refuses any other target.
const convertTargetCheckName = "conversion target is static"

// ReconcileConvert reconciles a dynamic-to-static route conversion
// (spec.route.convertTo: static). The cluster link defines its scope: every
// mirror topic on the link, with no selector. It runs in stages and returns at
// the first that refuses: the route checks (CheckPreconditions, then the
// conversion's own: the target binding's bootstrap id, auth for the target
// domain, no fence kcp didn't write, no live route-level fence); the four
// visibility probes; then CheckLinkScope — every link topic promoted,
// unprefixed, on the destination, routed there and partition-matched; every
// source group committing only on link topics; no in-scope group active on the
// destination. On success the artifacts are the route's rules with kcp's
// convert fence, the route converted to static, and the rules without the
// fence for a rollback.
//
// A route that is already static and bound to the target is a completed
// conversion: nothing to do — unless it carries a live route-level fence, which
// is refused. A static route bound anywhere else is refused.
func ReconcileConvert(in ReconcileInput, gw *GatewayConfig, sourceTopics, targetTopics []string,
	mirrors []LinkMirror, partitions PartitionCounts, offsetSyncEnabled bool, ids ClusterIDs, groups GroupFacts) *Plan {

	report := Report{}

	// This strategy is the conversion to static only; a future
	// convertTo: dynamic (or a caller dispatching here without ConvertTo) must
	// never run it, nor be told a static route is "nothing to do".
	if in.ConvertTo != convertToStatic {
		report.Preconditions = []PreconditionResult{fail(convertTargetCheckName, fmt.Sprintf(
			"convertTo is %q; this strategy converts a route to %q only", in.ConvertTo, convertToStatic))}
		return &Plan{Report: report, Mode: convertMode}
	}

	if gw != nil && gw.Route != nil && gw.Route.Mode == "static" {
		if sd, ok := mapField(gw.Route.Raw, "streamingDomain"); ok && stringField(sd, "name") == in.TargetDomain {
			// A completed conversion is nothing to do only while the static
			// route blocks nothing: a live fence there is not "done".
			fenceCheck := routeLevelFenceCheck(gw.Route)
			report.Preconditions = []PreconditionResult{pass("route is already static on the target domain"), fenceCheck}
			if !fenceCheck.OK {
				return &Plan{Report: report, Mode: convertMode}
			}
			return &Plan{Report: report, Mode: convertMode, NothingToDo: true}
		}
		report.Preconditions = []PreconditionResult{fail("route is dynamic", fmt.Sprintf(
			"route %q is static but not bound to targetStreamingDomain %q; there is nothing to convert", gw.Route.Name, in.TargetDomain))}
		return &Plan{Report: report, Mode: convertMode}
	}

	pcs, view, ok := CheckPreconditions(in, gw, offsetSyncEnabled, ids)
	report.Preconditions = pcs
	if !ok {
		return &Plan{Report: report, Mode: convertMode}
	}
	rc := gw.Route

	bootstrapID, bound := targetBinding(rc.Raw, in.TargetDomain)
	if bound {
		report.Preconditions = append(report.Preconditions, pass("target binding carries a bootstrap server id"))
	} else {
		report.Preconditions = append(report.Preconditions, fail("target binding carries a bootstrap server id", fmt.Sprintf(
			"route %q's streamingDomains entry for %q has no bootstrapServerId; the static route needs one to bind to", rc.Name, in.TargetDomain)))
	}

	if _, has := stagedAuthFor(rc.Raw, in.TargetDomain); has {
		report.Preconditions = append(report.Preconditions, pass("route carries auth for the target domain"))
	} else {
		report.Preconditions = append(report.Preconditions, fail("route carries auth for the target domain", fmt.Sprintf(
			"route %q has no security.cluster.%s block; the static route needs it to reach the target", rc.Name, in.TargetDomain)))
	}

	base, _ := ParseRules(rc.Rules)
	if foreign := base.ForeignFences(); len(foreign) > 0 {
		report.Preconditions = append(report.Preconditions, fail("route has no fence kcp didn't write", fmt.Sprintf(
			"route %q's rules.fencing has %d entr(ies) kcp didn't write (%s); the conversion drops the rules tree, so remove them first",
			rc.Name, len(foreign), renderFences(foreign))))
	} else {
		report.Preconditions = append(report.Preconditions, pass("route has no fence kcp didn't write"))
	}

	// BuildConvertedStaticRoute keeps every other route key, so a route-level
	// fence on the dynamic route would ride into the static route, where the
	// gateway enforces it.
	report.Preconditions = append(report.Preconditions, routeLevelFenceCheck(rc))
	if report.Refused() {
		return &Plan{Report: report, Mode: convertMode}
	}

	report.Preconditions = append(report.Preconditions,
		checkVisibility(SourceGroupVisibilityCheckName, groups.SourceListingIncomplete),
		checkVisibility(TargetGroupVisibilityCheckName, groups.TargetListingIncomplete),
		checkVisibility(SourceTopicVisibilityCheckName, groups.SourceTopicsIncomplete),
		checkVisibility(TargetTopicVisibilityCheckName, groups.TargetTopicsIncomplete))
	// A refusal here returns before the link checks, on purpose: they read the
	// group and topic listings, and the visibility preconditions say those may
	// be partial, so their verdict would not be trustworthy. The cost is that
	// an operator who fixes a credential can then meet a second refusal.
	if report.Refused() {
		return &Plan{Report: report, Mode: convertMode}
	}

	scope := CheckLinkScope(LinkScopeInput{
		Mirrors:         mirrors,
		SourceTopics:    sourceTopics,
		TargetTopics:    targetTopics,
		Partitions:      partitions,
		View:            view,
		CommittedTopics: groups.TrackedTopics,
		TargetStates:    groups.TargetStates,
	})
	report.Unchanged = append(report.Unchanged, scope.Unchanged...)
	report.FailFast = append(report.FailFast, scope.FailFast...)
	report.Preconditions = append(report.Preconditions, scope.Preconditions...)
	report.Warnings = append(report.Warnings, scope.Warnings...)
	if report.Refused() {
		return &Plan{Report: report, Mode: convertMode}
	}

	fence, err := base.Clone()
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("fence rules clone", err.Error()))
		return &Plan{Report: report, Mode: convertMode}
	}
	fence.PrependConvertFence()
	rollback, err := base.Clone()
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("rollback rules clone", err.Error()))
		return &Plan{Report: report, Mode: convertMode}
	}
	rollback.DropConvertFence()

	fenceBytes, err := fence.Serialize()
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("fence rules serialize", err.Error()))
		return &Plan{Report: report, Mode: convertMode}
	}
	rollbackBytes, err := rollback.Serialize()
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("rollback rules serialize", err.Error()))
		return &Plan{Report: report, Mode: convertMode}
	}
	switchBytes, err := BuildConvertedStaticRoute(rc.Raw, in.TargetDomain, bootstrapID)
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("converted route builds", err.Error()))
		return &Plan{Report: report, Mode: convertMode}
	}
	// Only the fence artifact is size-checked: the rollback is the same rules
	// minus kcp's fence, so never larger, and the switchover is a route, not a
	// rules block.
	if len(fenceBytes) > MaxRulesBytes {
		report.Preconditions = append(report.Preconditions, fail("rules block within size limit",
			fmt.Sprintf("rules block is %d bytes (> %d)", len(fenceBytes), MaxRulesBytes)))
		return &Plan{Report: report, Mode: convertMode}
	}

	report.ConvertToStatic = true
	return &Plan{Report: report, Mode: convertMode, Artifacts: &Artifacts{
		FenceRules:         fenceBytes,
		SwitchoverRules:    switchBytes,
		RollbackFenceRules: rollbackBytes,
		RollbackAllowed:    true,
		FencedAtStart:      base.HasConvertFence(),
	}}
}

// routeLevelFenceCheckName names the precondition that refuses a route-level
// fence on a route being converted (or already converted) to static.
const routeLevelFenceCheckName = "route has no route-level fence"

// routeLevelFenceCheck passes when the route carries no route-level fence, or
// an inert one (empty, or scope NONE as the CRD may default it — see
// isUnfencedStatic). Any other fence — including one that is not a map — would
// be enforced by the gateway once the route is static, so it refuses. kcp never
// writes a route-level fence for a conversion (its fence is a rules.fencing
// entry), so there is no kcp-owned exception here.
func routeLevelFenceCheck(rc *RouteConfig) PreconditionResult {
	v, present := rc.Raw["fence"]
	if !present || v == nil {
		return pass(routeLevelFenceCheckName)
	}
	f, isMap := v.(map[string]any)
	if isMap && isUnfencedStatic(f) {
		return pass(routeLevelFenceCheckName)
	}
	rendered := fmt.Sprintf("%v", v)
	if isMap {
		rendered = renderFence(f)
	}
	return fail(routeLevelFenceCheckName, fmt.Sprintf(
		"route %q carries a route-level fence (%s) that the static route would enforce; remove it before converting",
		rc.Name, rendered))
}

// renderFences renders fencing entries for a refusal message.
func renderFences(entries []any) string {
	out := ""
	for i, e := range entries {
		if i > 0 {
			out += ", "
		}
		if m, ok := e.(map[string]any); ok {
			out += renderFence(m)
		} else {
			out += fmt.Sprintf("%v", e)
		}
	}
	return out
}

// joinCapped joins names for a message, showing at most max and a count of the
// rest.
func joinCapped(names []string, max int) string {
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:max], ", "), len(names)-max)
}
