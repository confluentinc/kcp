package reconcile

import "fmt"

const convertMode = "convert"

// ReconcileConvert reconciles a dynamic-to-static route conversion
// (spec.route.convertTo: static). It reuses CheckPreconditions for the route
// checks, adds the conversion's own (the target binding's bootstrap id, auth
// for the target domain, no fence kcp didn't write, no live route-level fence,
// no source group active on the destination), and classifies every live source topic — there is no
// selector — refusing unless all of them are Unchanged: the topic-based
// migration must already be finished. On success the artifacts are the route's
// rules with kcp's convert fence, the route converted to static, and the rules
// without the fence for a rollback.
//
// A route that is already static and bound to the target is a completed
// conversion: nothing to do — unless it carries a live route-level fence, which
// is refused. A static route bound anywhere else is refused.
func ReconcileConvert(in ReconcileInput, gw *GatewayConfig, sourceTopics, targetTopics []string,
	mirrors map[string]MirrorState, offsetSyncEnabled bool, ids ClusterIDs, groups GroupFacts) *Plan {

	report := Report{}

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

	groupCheck, groupWarnings := CheckGroupSplitBrain(groups)
	report.Preconditions = append(report.Preconditions, groupCheck)
	report.Warnings = append(report.Warnings, groupWarnings...)

	if report.Refused() {
		return &Plan{Report: report, Mode: convertMode}
	}

	tgtSet := toSet(targetTopics)
	for _, topic := range sourceTopics {
		_, onTarget := tgtSet[topic]
		domain, _ := OwnerRoute(topic, view.Conditions, view.DefaultDomain)
		tv := Classify(topic, true, onTarget, mirrors[topic], domain == view.TargetDomain)
		switch tv.Verdict {
		case Unchanged:
			report.Unchanged = append(report.Unchanged, tv)
		case FailFast:
			report.FailFast = append(report.FailFast, tv)
		default:
			// Migratable, SwitchOnly and AwaitStopped are in-flight states a
			// topic migration accepts; a conversion requires the migration done.
			tv.Reason = fmt.Sprintf("%s is not migrated yet (%s); finish the topic-based migration before converting the route", topic, tv.Verdict)
			tv.Verdict = FailFast
			report.FailFast = append(report.FailFast, tv)
		}
	}
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
