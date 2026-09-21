package reconcile

import (
	"fmt"
	"sort"
)

const MaxRulesBytes = 512 * 1024

// reconcileDynamic is today's dynamic-route reconciliation, unchanged —
// renamed from the former top-level Reconcile so Reconcile itself can
// dispatch by resolved route mode. See reconcileStatic for the static-route
// counterpart.
func reconcileDynamic(in ReconcileInput, gw *GatewayConfig, sourceTopics, targetTopics []string,
	mirrors map[string]MirrorState, offsetSyncEnabled bool, ids ClusterIDs) *Plan {

	report := Report{}

	// Run-level gate: the route must be a dynamic route bound to exactly the
	// source and target domains, with coordination pinned to source and offset
	// sync off, and the clusters we read must be the real source/destination.
	// A failure here stops the run before any per-topic work.
	pcs, view, ok := CheckPreconditions(in, gw, offsetSyncEnabled, ids)
	report.Preconditions = pcs
	if !ok {
		return &Plan{Report: report, Mode: "dynamic"}
	}

	// Resolve the selector (exact names + patterns) to concrete topic names by
	// matching it against the live source topics.
	batch, err := Explode(in.Topics, in.TopicPatterns, sourceTopics)
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("selector patterns compile", err.Error()))
		return &Plan{Report: report, Mode: "dynamic"}
	}

	srcSet := toSet(sourceTopics)
	tgtSet := toSet(targetTopics)

	// Classify each resolved topic against source/target presence, mirror state
	// and current routing.
	for _, topic := range batch {
		_, onSource := srcSet[topic]
		_, onTarget := tgtSet[topic]
		mirror := mirrors[topic] // zero value MirrorNone if absent
		domain, _ := OwnerRoute(topic, view.Conditions, view.DefaultDomain)
		routesToTarget := domain == view.TargetDomain

		tv := Classify(topic, onSource, onTarget, mirror, routesToTarget)
		switch tv.Verdict {
		case Migratable:
			report.Migratable = append(report.Migratable, tv)
		case SwitchOnly:
			report.SwitchOnly = append(report.SwitchOnly, tv)
		case AwaitStopped:
			report.AwaitStopped = append(report.AwaitStopped, tv)
		case Unchanged:
			report.Unchanged = append(report.Unchanged, tv)
		default:
			report.FailFast = append(report.FailFast, tv)
		}
	}

	// Topic sets for the resume plan:
	//   promote  = topics that still need to reach STOPPED (Active + Pending)
	//   inflight = every topic to route to target this run (Active + Pending + already-Stopped)
	// Unchanged topics are already switched and appear in neither.
	promote := topicsOf(report.Migratable, report.AwaitStopped)
	inflight := topicsOf(report.Migratable, report.AwaitStopped, report.SwitchOnly)

	// Warn when a topic we are about to migrate already appears in an
	// operator-authored exact-name routing condition: our prepended entry will
	// shadow theirs. Advisory only — we never remove the operator's condition.
	report.Warnings = append(report.Warnings, shadowWarnings(inflight, view.Conditions)...)

	// Refusal gate: any failed precondition or fail-fast topic means we emit no
	// artifacts at all (all-or-nothing).
	if report.Refused() {
		return &Plan{Report: report, Mode: "dynamic"}
	}
	if len(inflight) == 0 {
		return &Plan{Report: report, Mode: "dynamic"} // nothing to do; artifacts nil (no-op)
	}

	// Build both artifacts from one pristine copy of the operator's rules, so the
	// fence and switchover derive independently from the same baseline. Both
	// derive from the whole in-flight batch (Migratable + AwaitStopped +
	// SwitchOnly) — including already-promoted SwitchOnly topics, since they
	// still need fencing ahead of their switchover and still need to switch.
	base, _ := ParseRules(gw.Route.Rules)
	fence, err := base.Clone()
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("fence rules clone", err.Error()))
		return &Plan{Report: report, Mode: "dynamic"}
	}
	fence.PrependFence(inflight)

	switchover, err := base.Clone()
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("switchover rules clone", err.Error()))
		return &Plan{Report: report, Mode: "dynamic"}
	}
	switchover.PrependCondition(inflight, view.TargetDomain)

	fenceBytes, err := fence.Serialize()
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("fence rules serialize", err.Error()))
		return &Plan{Report: report, Mode: "dynamic"}
	}
	switchBytes, err := switchover.Serialize()
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("switchover rules serialize", err.Error()))
		return &Plan{Report: report, Mode: "dynamic"}
	}

	// Guardrail: refuse if either serialized rules block exceeds the size limit.
	if len(fenceBytes) > MaxRulesBytes || len(switchBytes) > MaxRulesBytes {
		report.Preconditions = append(report.Preconditions, fail("rules block within size limit",
			fmt.Sprintf("rules block is %d bytes (> %d) — narrow the selector or migrate a smaller batch", max(len(fenceBytes), len(switchBytes)), MaxRulesBytes)))
		return &Plan{Report: report, Mode: "dynamic"}
	}

	promoteSorted := append([]string(nil), promote...)
	sort.Strings(promoteSorted)
	return &Plan{Report: report, Mode: "dynamic", Artifacts: &Artifacts{Topics: promoteSorted, FenceRules: fenceBytes, SwitchoverRules: switchBytes}}
}

// reconcileStatic is the static-route reconciliation strategy. It reuses
// Explode/Classify unchanged: topics resolve against the source cluster's
// live topic list exactly like dynamic mode, and classification uses the
// same truth table, with routesToTarget computed once at the route level
// (view.RoutesToTarget) rather than per-topic — a static route cannot bind
// different topics to different domains. Report.Refused()'s existing
// definition already produces the correct all-or-nothing policy; no new
// refusal logic is needed. Unlike dynamic mode, there is no shadow-warning
// concept (no per-topic routing conditions exist to shadow) and no
// rules-size guardrail (the fragments are a few dozen bytes, never
// realistically oversized).
func reconcileStatic(in ReconcileInput, gw *GatewayConfig, sourceTopics, targetTopics []string,
	mirrors map[string]MirrorState, ids ClusterIDs, missingSecrets []string, secretCheckSkipped string) *Plan {

	report := Report{}

	pcs, view, ok := CheckStaticPreconditions(in, gw, missingSecrets, secretCheckSkipped, ids)
	report.Preconditions = pcs
	if !ok {
		return &Plan{Report: report, Mode: "static"}
	}

	batch, err := Explode(in.Topics, in.TopicPatterns, sourceTopics)
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("selector patterns compile", err.Error()))
		return &Plan{Report: report, Mode: "static"}
	}

	srcSet := toSet(sourceTopics)
	tgtSet := toSet(targetTopics)

	for _, topic := range batch {
		_, onSource := srcSet[topic]
		_, onTarget := tgtSet[topic]
		mirror := mirrors[topic]

		tv := Classify(topic, onSource, onTarget, mirror, view.RoutesToTarget)
		switch tv.Verdict {
		case Migratable:
			report.Migratable = append(report.Migratable, tv)
		case SwitchOnly:
			report.SwitchOnly = append(report.SwitchOnly, tv)
		case AwaitStopped:
			report.AwaitStopped = append(report.AwaitStopped, tv)
		case Unchanged:
			report.Unchanged = append(report.Unchanged, tv)
		default:
			report.FailFast = append(report.FailFast, tv)
		}
	}

	// promote = topics still needing STOPPED (Active+Pending); inflight = the
	// whole in-flight batch incl. already-promoted SwitchOnly topics. The
	// static fence/switchover fragments are whole-route (no topic list), so
	// only the refusal/no-op gate and the promote set change here.
	promote := topicsOf(report.Migratable, report.AwaitStopped)
	inflight := topicsOf(report.Migratable, report.AwaitStopped, report.SwitchOnly)

	if report.Refused() {
		return &Plan{Report: report, Mode: "static"}
	}
	if len(inflight) == 0 {
		return &Plan{Report: report, Mode: "static"}
	}

	fenceFragment, err := BuildFenceFragment()
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("fence fragment builds", err.Error()))
		return &Plan{Report: report, Mode: "static"}
	}
	switchoverFragment, err := BuildSwitchoverFragment(in.TargetDomain, view.BootstrapServerID)
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("switchover fragment builds", err.Error()))
		return &Plan{Report: report, Mode: "static"}
	}

	promoteSorted := append([]string(nil), promote...)
	sort.Strings(promoteSorted)
	return &Plan{Report: report, Mode: "static", Artifacts: &Artifacts{Topics: promoteSorted, FenceRules: fenceFragment, SwitchoverRules: switchoverFragment}}
}

// Reconcile is the single entry point for both route-mode strategies. It
// resolves the route's mode (already determined by the provider layer — see
// migplan/gatewayfile.go's field-first, structural-fallback resolution) and
// dispatches to reconcileDynamic or reconcileStatic. An unresolvable route
// (gw == nil || gw.Route == nil) defaults to the dynamic path, matching this
// function's pre-existing behavior for that case — both strategies' own
// preconditions independently fail loudly on "route not found" as their
// first check either way.
func Reconcile(in ReconcileInput, gw *GatewayConfig, sourceTopics, targetTopics []string,
	mirrors map[string]MirrorState, offsetSyncEnabled bool, ids ClusterIDs, missingSecrets []string, secretCheckSkipped string) *Plan {

	if gw != nil && gw.Route != nil && gw.Route.Mode == "static" {
		return reconcileStatic(in, gw, sourceTopics, targetTopics, mirrors, ids, missingSecrets, secretCheckSkipped)
	}
	return reconcileDynamic(in, gw, sourceTopics, targetTopics, mirrors, offsetSyncEnabled, ids)
}

// topicsOf flattens the Topic field of one or more verdict buckets into a single
// slice, preserving classification order across buckets.
func topicsOf(groups ...[]TopicVerdict) []string {
	var out []string
	for _, g := range groups {
		for _, tv := range g {
			out = append(out, tv.Topic)
		}
	}
	return out
}

func toSet(ss []string) map[string]struct{} {
	m := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		m[s] = struct{}{}
	}
	return m
}

// shadowWarnings flags any migratable topic that also appears in an operator's
// existing exact-name condition (which our prepend will shadow). Never removes.
func shadowWarnings(migratable []string, conditions []Condition) []string {
	mset := toSet(migratable)
	var out []string
	for _, c := range conditions {
		for _, t := range c.Topics {
			if _, ok := mset[t]; ok {
				out = append(out, fmt.Sprintf("existing condition for %q (-> %s) is now shadowed by this migration and will not match", t, c.Domain))
			}
		}
	}
	return out
}
