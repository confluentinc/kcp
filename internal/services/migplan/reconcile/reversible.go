package reconcile

import (
	"fmt"
	"sort"
)

// ReconcileReversibleStatic plans a reversible static migration from facts read
// on both clusters, both copies of the link and the gateway CR. It runs the
// gateway, identity and link checks, then places every selected topic in
// writable-side and mirror-side terms, then runs the group checks when a
// reversal or switch can follow. A refusal carries no artifacts and no
// reversal.
func ReconcileReversibleStatic(in ReversibleInput, f ReversibleFacts) *Plan {
	return applyGatewaySync(reconcileReversible(in, f), f.Gateway)
}

func reconcileReversible(in ReversibleInput, f ReversibleFacts) *Plan {
	report := Report{}
	refused := func() *Plan { return &Plan{Report: report, Mode: ModeReversibleStatic} }

	report.Preconditions = append(checkReversibleRoute(in, f), checkReversibleLink(in, f)...)
	sides, resolved := ResolveSides(in, f.Gateway)
	if !resolved && !report.Refused() {
		report.Preconditions = append(report.Preconditions, fail("writable side resolves", fmt.Sprintf(
			"writableOn %q is neither route.sourceStreamingDomain (%q) nor route.targetStreamingDomain (%q)", in.WritableOn, in.SourceDomain, in.TargetDomain)))
	}
	if report.Refused() {
		return refused()
	}

	topics, err := Explode(in.Topics, in.TopicPatterns, f.SourceTopics)
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("selector patterns compile", err.Error()))
		return refused()
	}

	link := linkTopicSet(f)
	verdicts, clients := placeTopics(in, sides, f, topics)
	groups := CheckReversibleGroups(in, sides, f.Groups, link, topics, f.Topics(sides.Bound))
	report.Preconditions = append(report.Preconditions, checkSelectionCoversLink(topics, link), clients)
	report.Preconditions = append(report.Preconditions, groups.Preconditions...)
	report.Warnings = append(report.Warnings, groups.Warnings...)
	for _, tv := range verdicts {
		switch tv.Verdict {
		case Migratable:
			report.Migratable = append(report.Migratable, tv)
		case AwaitStopped:
			report.AwaitStopped = append(report.AwaitStopped, tv)
		case SwitchOnly:
			report.SwitchOnly = append(report.SwitchOnly, tv)
		case Unchanged:
			report.Unchanged = append(report.Unchanged, tv)
		default:
			report.FailFast = append(report.FailFast, tv)
		}
	}
	if report.Refused() {
		return refused()
	}

	// A fenced route is never done: even with nothing reversed, the switch
	// still has to lift the fence.
	if len(report.Unchanged) == len(verdicts) && !sides.Fenced {
		return &Plan{Report: report, Mode: ModeReversibleStatic, NothingToDo: true, Reversal: &Reversal{Sides: sides}}
	}

	raw := f.Gateway.Route.Raw
	var bootstrapID string
	for id := range staticDomainBootstrapIDs(f.Gateway)[sides.WritableDomain] {
		bootstrapID = id
	}
	fence, err := BuildFenceFragment()
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("fence fragment builds", err.Error()))
		return refused()
	}
	switchover, err := BuildSwitchoverRoute(raw, sides.WritableDomain, bootstrapID)
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("switchover route builds", err.Error()))
		return refused()
	}
	rollback, err := BuildRollbackFenceRoute(raw)
	if err != nil {
		report.Preconditions = append(report.Preconditions, fail("rollback route builds", err.Error()))
		return refused()
	}

	reverse := sortedCopy(topicsOf(report.Migratable))
	awaitStopped := sortedCopy(topicsOf(report.AwaitStopped))
	switchOnly := sortedCopy(topicsOf(report.SwitchOnly))
	migrate := append(append(append([]string(nil), reverse...), awaitStopped...), switchOnly...)
	sort.Strings(migrate)
	return &Plan{
		Report: report,
		Mode:   ModeReversibleStatic,
		Artifacts: &Artifacts{
			FenceRules:         fence,
			SwitchoverRules:    switchover,
			RollbackFenceRules: rollback,
			FencedAtStart:      sides.Fenced,
			RollbackAllowed:    rollbackAllowed(report),
			MigrateTopics:      migrate,
			AwaitStopped:       awaitStopped,
		},
		Reversal: &Reversal{
			Sides:         sides,
			ReverseOn:     sides.Writable,
			Reverse:       reverse,
			AwaitStopped:  awaitStopped,
			SwitchOnly:    switchOnly,
			InScopeGroups: groups.InScope,
		},
	}
}
