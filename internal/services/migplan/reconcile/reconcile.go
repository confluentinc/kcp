package reconcile

import (
	"fmt"
	"sort"
)

const MaxRulesBytes = 512 * 1024

func Reconcile(in ReconcileInput, gw *GatewayConfig, sourceTopics, targetTopics []string,
	mirrors map[string]MirrorState, offsetSyncEnabled bool) *Plan {

	report := Report{}

	// Run-level gate: the route must be a dynamic route bound to exactly the
	// source and target domains, with coordination pinned to source and offset
	// sync off. A failure here stops the run before any per-topic work.
	pcs, view, ok := CheckPreconditions(in, gw, offsetSyncEnabled)
	report.Preconditions = pcs
	if !ok {
		return &Plan{Report: report}
	}

	// Resolve the selector (exact names + patterns) to concrete topic names by
	// matching it against the live source topics.
	batch, err := Explode(in.Topics, in.TopicPatterns, sourceTopics)
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("selector patterns compile", err.Error()))
		return &Plan{Report: report}
	}

	srcSet := toSet(sourceTopics)
	tgtSet := toSet(targetTopics)

	// Classify each resolved topic against source/target presence, mirror state
	// and current routing.
	var migratable []string
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
			migratable = append(migratable, topic)
		case Unchanged:
			report.Unchanged = append(report.Unchanged, tv)
		default:
			report.FailFast = append(report.FailFast, tv)
		}
	}

	// Warn when a topic we are about to migrate already appears in an
	// operator-authored exact-name routing condition: our prepended entry will
	// shadow theirs. Advisory only — we never remove the operator's condition.
	report.Warnings = append(report.Warnings, shadowWarnings(migratable, view.Conditions)...)

	// Refusal gate: any failed precondition or fail-fast topic means we emit no
	// artifacts at all (all-or-nothing).
	if report.Refused() {
		return &Plan{Report: report}
	}
	if len(migratable) == 0 {
		return &Plan{Report: report} // nothing to do; artifacts nil (no-op)
	}

	// Build both artifacts from one pristine copy of the operator's rules, so the
	// fence and switchover derive independently from the same baseline.
	base, _ := ParseRules(gw.Route.Rules)
	fence := base.Clone()
	fence.PrependFence(migratable)
	switchover := base.Clone()
	switchover.PrependCondition(migratable, view.TargetDomain)

	fenceBytes, _ := fence.Serialize()
	switchBytes, _ := switchover.Serialize()

	// Guardrail: refuse if either serialized rules block exceeds the size limit.
	if len(fenceBytes) > MaxRulesBytes || len(switchBytes) > MaxRulesBytes {
		report.Preconditions = append(report.Preconditions, fail("rules block within size limit",
			fmt.Sprintf("rules block is %d bytes (> %d) — narrow the selector or migrate a smaller batch", max(len(fenceBytes), len(switchBytes)), MaxRulesBytes)))
		return &Plan{Report: report}
	}

	promote := append([]string(nil), migratable...)
	sort.Strings(promote)
	return &Plan{Report: report, Artifacts: &Artifacts{Topics: promote, FenceRules: fenceBytes, SwitchoverRules: switchBytes}}
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
