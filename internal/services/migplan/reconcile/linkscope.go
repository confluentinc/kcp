package reconcile

import (
	"fmt"
	"sort"
	"strings"
)

// LinkMirror is one mirror topic on the cluster link as a conversion reads it:
// the source topic's name, the mirror's own name on the destination (the two
// differ only on a link with cluster.link.prefix), the mapped state, and the
// link's own mirror_status string, kept for messages.
type LinkMirror struct {
	SourceTopic string
	MirrorTopic string
	State       MirrorState
	Status      string
}

// PartitionCounts is each link topic's partition count per cluster. Source is
// keyed by source topic name, Target by mirror topic name. A topic absent from
// a map has no copy on that cluster.
type PartitionCounts struct {
	Source map[string]int
	Target map[string]int
}

// LinkScopeInput is everything the link-scoped checks read. It is plain data
// so that reconcile and verify_fence apply exactly the same rules.
type LinkScopeInput struct {
	// Mirrors is every mirror topic on the cluster link: the topics in scope.
	Mirrors []LinkMirror
	// SourceTopics and TargetTopics are every non-internal topic on each cluster.
	SourceTopics []string
	TargetTopics []string
	// Partitions must be filled from a live read of every link topic on both
	// clusters; a missing count is reported as "could not be read; rerun",
	// not as an error.
	Partitions PartitionCounts
	// View is the dynamic route's routing, for OwnerRoute.
	View RouteView
	// CommittedTopics is, per source group, the topics it has committed an
	// offset (>= 0) on. A group with no commits maps to an empty list.
	CommittedTopics map[string][]string
	// TargetStates is each destination group's state, as ListGroups reports it.
	TargetStates map[string]string
}

// checkLinkTopics checks every link topic: promoted, unprefixed name, on the
// destination, routed there, and the same partition count on both clusters.
// Each topic gets one verdict; every problem with it is joined into its Reason,
// with the fix for each. Verdicts are sorted by source topic name.
func checkLinkTopics(in LinkScopeInput) (unchanged, failFast []TopicVerdict) {
	srcSet, tgtSet := toSet(in.SourceTopics), toSet(in.TargetTopics)
	committers := groupsByTopic(in.CommittedTopics)
	mirrors := append([]LinkMirror(nil), in.Mirrors...)
	sort.Slice(mirrors, func(i, j int) bool { return mirrors[i].SourceTopic < mirrors[j].SourceTopic })

	for _, m := range mirrors {
		_, onSource := srcSet[m.SourceTopic]
		_, onTarget := tgtSet[m.MirrorTopic]
		// The Gateway routes by the name clients ask for, which is the source name.
		domain, _ := OwnerRoute(m.SourceTopic, in.View.Conditions, in.View.DefaultDomain)
		routed := domain == in.View.TargetDomain
		tv := TopicVerdict{
			Topic:   m.SourceTopic,
			Verdict: Unchanged,
			S:       boolStr(onSource, "present", "absent"),
			M:       m.State.String(),
			T:       boolStr(onTarget, "present", "absent"),
			R:       boolStr(routed, "->target", "->source"),
		}

		var reasons []string
		if r := mirrorStateProblem(m); r != "" {
			reasons = append(reasons, r)
		}
		if m.MirrorTopic != m.SourceTopic {
			reasons = append(reasons, fmt.Sprintf(
				"its mirror is named %q on the destination; conversions don't support a cluster link with cluster.link.prefix, because the Gateway routes by topic name", m.MirrorTopic))
		}
		if !onTarget {
			reasons = append(reasons, fmt.Sprintf("its mirror %q is not on the destination", m.MirrorTopic))
		}
		if !routed {
			to := "no streaming domain"
			if domain != "" {
				to = fmt.Sprintf("%q", domain)
			}
			reasons = append(reasons, fmt.Sprintf(
				"the route sends it to %s, not the destination %q, so its producers still write to the source copy: finish the topic-based migration batch (rerun kcp migration execute on its manifest) before converting",
				to, in.View.TargetDomain))
		}
		if onTarget {
			if r := partitionProblem(m, onSource, in.Partitions, committers[m.SourceTopic]); r != "" {
				reasons = append(reasons, r)
			}
		}

		if len(reasons) > 0 {
			tv.Verdict = FailFast
			tv.Reason = fmt.Sprintf("%s cannot be converted: %s", m.SourceTopic, strings.Join(reasons, "; "))
			failFast = append(failFast, tv)
			continue
		}
		unchanged = append(unchanged, tv)
	}
	return unchanged, failFast
}

// mirrorStateProblem says why a mirror that is not STOPPED blocks the
// conversion, and how to clear it; "" for a promoted mirror.
func mirrorStateProblem(m LinkMirror) string {
	status := m.Status
	if status == "" {
		status = m.State.String()
	}
	switch m.State {
	case MirrorStopped:
		return ""
	case MirrorActive:
		return fmt.Sprintf("its mirror is %s, not promoted: migrate it in a topic-based migration batch, or promote it if nothing uses it", status)
	case MirrorPending:
		return fmt.Sprintf("its promotion is still in progress (%s): wait for it to reach STOPPED, then rerun", status)
	default:
		return fmt.Sprintf("its mirror is %s: fix the mirror and promote it, or delete the destination topic if it isn't wanted", status)
	}
}

// partitionProblem compares a link topic's partition counts; "" when they
// match or the source copy no longer exists (deleting it purged its offsets,
// so no group can track it). groups are the source groups committing on it.
func partitionProblem(m LinkMirror, onSource bool, parts PartitionCounts, groups []string) string {
	dst, hasDst := parts.Target[m.MirrorTopic]
	if !hasDst {
		return "its partition count on the destination could not be read; rerun"
	}
	src, hasSrc := parts.Source[m.SourceTopic]
	if !hasSrc {
		if onSource {
			return "its partition count on the source could not be read; rerun"
		}
		return ""
	}
	switch {
	case dst > src:
		committing := "no consumer group commits on it"
		if len(groups) > 0 {
			committing = fmt.Sprintf("consumer group(s) %s commit on it", joinCapped(groups, 20))
		}
		return fmt.Sprintf(
			"it has %d partitions on the destination but %d on the source copy; the source rejects commits on a partition it doesn't have, so no position exists for the added ones and their consumers would resume from auto.offset.reset after the switch (%s). Add partitions to the source copy to match, set those groups' offsets on the new source partitions, then rerun",
			dst, src, committing)
	case dst < src:
		return fmt.Sprintf(
			"it has %d partitions on the destination but %d on the source copy; a mirror keeps its partition count and partitions can't be removed, so the link is inconsistent",
			dst, src)
	}
	return ""
}

// groupsByTopic inverts group -> committed topics into topic -> sorted groups.
func groupsByTopic(committed map[string][]string) map[string][]string {
	out := map[string][]string{}
	for g, topics := range committed {
		for _, t := range topics {
			out[t] = append(out[t], g)
		}
	}
	for t := range out {
		sort.Strings(out[t])
	}
	return out
}

// GroupScopeCheckName names the group rule's precondition.
const GroupScopeCheckName = "every source consumer group commits only on cluster-link topics"

// PromotedTopicsCheckName names the precondition that the link has at least
// one promoted topic: the topics in scope are the promoted topics.
const PromotedTopicsCheckName = "the cluster link has promoted topics"

// checkPromotedTopics refuses a link with no promoted (STOPPED) mirror,
// including an empty link: nothing was migrated over it, and every other
// link-scoped rule would pass vacuously.
func checkPromotedTopics(mirrors []LinkMirror) PreconditionResult {
	for _, m := range mirrors {
		if m.State == MirrorStopped {
			return pass(PromotedTopicsCheckName)
		}
	}
	return fail(PromotedTopicsCheckName,
		"no topic on the cluster link is promoted, so nothing has been migrated over it; a conversion closes a topic-based migration — migrate and promote the route's topics first")
}

// LinkScope is CheckLinkScope's verdict, in the shape ReconcileConvert folds
// into its Report.
type LinkScope struct {
	Unchanged     []TopicVerdict
	FailFast      []TopicVerdict
	Preconditions []PreconditionResult
	Warnings      []string
	// UntrackedTopicsWarning lists source topics not on the link that no group
	// commits on ("" when there are none). It is separate from Warnings because
	// only reconcile reports it; verify_fence does not repeat it.
	UntrackedTopicsWarning string
	// InScopeGroups is every source group whose commits are all on link
	// topics, sorted. Set only when the group rule passed.
	InScopeGroups []string
}

// Refused reports whether any link topic or precondition refused.
func (s LinkScope) Refused() bool {
	if len(s.FailFast) > 0 {
		return true
	}
	for _, p := range s.Preconditions {
		if !p.OK {
			return true
		}
	}
	return false
}

// CheckLinkScope applies every link-scoped rule of a route conversion, in
// stages: the link topic checks (with the at-least-one-promoted-topic rule),
// then the group rule (with the warning about untracked topics off the link),
// then the split-brain check over the in-scope groups. A refusing stage returns
// before the next: the group rule trusts a link the topic checks passed, and
// the split-brain check needs the in-scope groups the group rule produces.
// Reconcile and verify_fence both call it, so their verdicts can't drift.
func CheckLinkScope(in LinkScopeInput) LinkScope {
	var s LinkScope
	s.Unchanged, s.FailFast = checkLinkTopics(in)
	s.Preconditions = append(s.Preconditions, checkPromotedTopics(in.Mirrors))
	if s.Refused() {
		return s
	}

	link := make(map[string]struct{}, len(in.Mirrors))
	for _, m := range in.Mirrors {
		link[m.SourceTopic] = struct{}{}
	}
	groupCheck, inScope := checkGroupScope(link, in.CommittedTopics)
	s.Preconditions = append(s.Preconditions, groupCheck)
	s.UntrackedTopicsWarning = untrackedOffLinkWarning(in.SourceTopics, link, in.CommittedTopics)
	if !groupCheck.OK {
		return s
	}
	s.InScopeGroups = inScope

	splitBrain, warnings := CheckGroupSplitBrain(inScope, in.TargetStates)
	s.Preconditions = append(s.Preconditions, splitBrain)
	s.Warnings = append(s.Warnings, warnings...)
	return s
}

// checkGroupScope is the group rule: a source group whose commits are all on
// link topics is in scope; a group with any commit on a topic outside the link
// refuses, even alongside link topics; a group with no commits is not tracked.
// It returns the sorted in-scope groups, or nil on a refusal.
func checkGroupScope(link map[string]struct{}, committed map[string][]string) (PreconditionResult, []string) {
	var inScope, offending []string
	for g, topics := range committed {
		if len(topics) == 0 {
			continue
		}
		var outside []string
		for _, t := range topics {
			if _, ok := link[t]; !ok {
				outside = append(outside, t)
			}
		}
		if len(outside) > 0 {
			sort.Strings(outside)
			offending = append(offending, fmt.Sprintf("%s (%s)", g, joinCapped(outside, 10)))
			continue
		}
		inScope = append(inScope, g)
	}
	if len(offending) > 0 {
		sort.Strings(offending)
		return fail(GroupScopeCheckName, fmt.Sprintf(
			"consumer group(s) %s have committed offsets on topics that are not on the cluster link; after the switch those topics are not reachable on the destination, or the group belongs to a client outside this route. For each: migrate the topic (add it to the link and run a topic-based migration batch); or, if the commits are stale, delete the group's offsets on those topics (kafka-consumer-groups --delete-offsets) or delete the group; or, if a live app outside this route owns the group, stop or move it, then delete the group",
			joinCapped(offending, 20))), nil
	}
	sort.Strings(inScope)
	return pass(GroupScopeCheckName), inScope
}

// untrackedOffLinkWarning lists source topics that are not on the link and that
// no group commits on. Nothing tells a forgotten produce-only topic from a
// system topic (_schemas, the MSK canary, Connect internals), so this is a
// warning, never a refusal, and every such topic stays in it. Names starting
// with '_' (the usual internal-topic convention) go in a separate clause so the
// ordinary topics stand out.
func untrackedOffLinkWarning(sourceTopics []string, link map[string]struct{}, committed map[string][]string) string {
	tracked := map[string]struct{}{}
	for _, topics := range committed {
		for _, t := range topics {
			tracked[t] = struct{}{}
		}
	}
	var ordinary, internal []string
	for _, t := range sourceTopics {
		_, onLink := link[t]
		_, isTracked := tracked[t]
		if onLink || isTracked {
			continue
		}
		if strings.HasPrefix(t, "_") {
			internal = append(internal, t)
		} else {
			ordinary = append(ordinary, t)
		}
	}
	sort.Strings(ordinary)
	sort.Strings(internal)
	const consequence = "no source consumer group has committed offsets on them, so they are not checked; after the switch the route sends their traffic to the destination — migrate them first if anything on this route still reads or writes them"
	switch {
	case len(ordinary) == 0 && len(internal) == 0:
		return ""
	case len(ordinary) == 0:
		return fmt.Sprintf("topic(s) %s are not on the cluster link and are probably internal (name starts with '_'); %s",
			joinCapped(internal, 20), consequence)
	case len(internal) == 0:
		return fmt.Sprintf("topic(s) %s are not on the cluster link and %s", joinCapped(ordinary, 20), consequence)
	default:
		return fmt.Sprintf("topic(s) %s are not on the cluster link and %s. Also not on the link, and probably internal (name starts with '_'): %s",
			joinCapped(ordinary, 20), consequence, joinCapped(internal, 20))
	}
}
