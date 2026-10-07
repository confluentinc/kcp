package reconcile

import (
	"fmt"
	"sort"
	"strings"
)

// GroupFacts is the consumer-group data a conversion reads, gathered by the I/O
// layer: every group id on the source, each destination group's state (as
// ListGroups reports it), and, per source group, the topics it has committed
// offsets on. The split-brain check reads the first two; the convergence check
// is scoped to the third.
//
// SourceListingIncomplete and TargetListingIncomplete are empty when that
// cluster's listing is known to be complete. A non-empty value says why it may
// not be (the listing credential can only see the groups it can describe
// individually), and refuses the conversion: a source group the listing hides
// is a set of topics never verified, and a destination group it hides is a
// split-brain the check cannot see.
//
// SourceTopicsIncomplete and TargetTopicsIncomplete are the same for topics: a
// credential that may not describe every topic gets an offset fetch that
// silently leaves out the topics it can't see (so a tracked topic is never
// verified), and a topic list that omits them.
type GroupFacts struct {
	SourceGroups            []string
	TargetStates            map[string]string
	TrackedTopics           map[string][]string
	SourceListingIncomplete string
	TargetListingIncomplete string
	SourceTopicsIncomplete  string
	TargetTopicsIncomplete  string
}

// The visibility preconditions ReconcileConvert adds: whether each cluster's
// credential can see every consumer group and every topic.
const (
	SourceGroupVisibilityCheckName = "source credential can list every consumer group"
	TargetGroupVisibilityCheckName = "destination credential can list every consumer group"
	SourceTopicVisibilityCheckName = "source credential can describe every topic"
	TargetTopicVisibilityCheckName = "destination credential can describe every topic"
)

// checkVisibility refuses with reason when a credential's view may be partial.
func checkVisibility(name, reason string) PreconditionResult {
	if reason != "" {
		return fail(name, reason)
	}
	return pass(name)
}

// GroupSplitBrainCheckName names the precondition CheckGroupSplitBrain returns.
const GroupSplitBrainCheckName = "no in-scope consumer group is active on the destination"

// CheckGroupSplitBrain refuses when one of groups (the conversion's in-scope
// source groups) is active on the destination: after the switch, coordination
// for the route's clients moves to the destination, so the group's members
// would join a group that already has members, and kcp's admin offset commit
// for it would be refused. Groups only on the destination are ignored. A group
// sitting Empty on the destination (retained only while it has committed
// offsets) passes with a warning, since the conversion will overwrite those
// offsets. Dead is ignored. Any other state — including an empty or
// unrecognised one — counts as active, so an unreadable state can't let a
// split-brain through.
func CheckGroupSplitBrain(groups []string, targetStates map[string]string) (PreconditionResult, []string) {
	var active, idle []string
	anyUnknown := false
	for _, g := range groups {
		state, onTarget := targetStates[g]
		if !onTarget {
			continue
		}
		switch strings.ToLower(state) {
		case "empty":
			idle = append(idle, g)
		case "dead":
		case "":
			anyUnknown = true
			active = append(active, g+" (state unknown; treated as active)")
		default:
			active = append(active, fmt.Sprintf("%s (%s)", g, state))
		}
	}
	sort.Strings(active)
	sort.Strings(idle)

	var warnings []string
	if len(idle) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"consumer group(s) %s exist on the destination with committed offsets but no members; the conversion will overwrite their offsets with the source's — check nothing consumed on the destination under these names",
			strings.Join(idle, ", ")))
	}
	if len(active) > 0 {
		// Only claim members when every listed state was actually read.
		onDest := "already have members on the destination"
		if anyUnknown {
			onDest = "are active, or of unreadable state, on the destination"
		}
		return fail(GroupSplitBrainCheckName, fmt.Sprintf(
			"consumer group(s) %s exist on the source and %s; after the switch the source members would join the same group — stop the destination members first",
			strings.Join(active, ", "), onDest)), warnings
	}
	return pass(GroupSplitBrainCheckName), warnings
}
