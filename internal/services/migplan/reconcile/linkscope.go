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
	Partitions   PartitionCounts
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
