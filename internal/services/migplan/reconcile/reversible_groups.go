package reconcile

import (
	"fmt"
	"sort"
	"strings"
)

const (
	RSGroupScopeCheckName = "every consumer group on the side the route is bound to commits only on link topics"
	RSSplitBrainCheckName = "no in-scope consumer group is active on the side being entered"
	RSShareClashCheckName = "no in-scope consumer group is a share group on the side being entered"
)

// GroupCheck is CheckReversibleGroups' verdict. InScope is sorted.
type GroupCheck struct {
	Preconditions []PreconditionResult
	Warnings      []string
	InScope       []string
}

// CheckReversibleGroups runs the consumer-group checks of a reversible run, and
// only when a reversal or switch can follow. In-scope groups are the bound
// side's groups with a commit on a selected topic. On a normal flip it first
// refuses a bound-side group committing off the link, since those topics don't
// follow the switch; a back-out keeps the clients where they are, so it skips
// that. Both then refuse an in-scope group that is active, or a share group,
// on the side being entered. It is exported so the state machine can re-run it
// on fresh facts before reversing.
func CheckReversibleGroups(in ReversibleInput, s Sides, g *ReversibleGroupFacts,
	linkTopics map[string]struct{}, selected, boundTopics []string) GroupCheck {

	var c GroupCheck
	if !s.Moving {
		return c
	}
	if g == nil {
		c.Preconditions = []PreconditionResult{fail(RSSplitBrainCheckName, "consumer group facts were not gathered")}
		return c
	}
	bound, entered := in.ClusterLabel(s.Bound), in.ClusterLabel(s.Writable)

	if s.Bound == s.Mirror {
		switch offending, _ := offLinkCommits(linkTopics, g.Committed); {
		case g.ListingIncomplete != "":
			c.Preconditions = append(c.Preconditions, fail(RSGroupScopeCheckName, g.ListingIncomplete))
		case len(offending) > 0:
			c.Preconditions = append(c.Preconditions, fail(RSGroupScopeCheckName, fmt.Sprintf(
				"consumer group(s) %s on %s have committed offsets on topics that are not on the cluster link; those topics don't follow the switch to %s, so the route's clients would lose them there. For each: add the topic to the link and to this migration; or, if the commits are stale, delete the group's offsets on those topics (kafka-consumer-groups --delete-offsets) or delete the group; or, if an app outside this route owns the group, move it, then delete the group",
				joinCapped(offending, 20), bound, entered)))
		default:
			c.Preconditions = append(c.Preconditions, pass(RSGroupScopeCheckName))
		}
		if g.ListingIncomplete == "" {
			if w := untrackedBoundWarning(boundTopics, linkTopics, g.Committed, bound, entered); w != "" {
				c.Warnings = append(c.Warnings, w)
			}
		}
	}

	c.InScope = groupsCommittingOn(selected, g.Committed)

	if g.EnteredListingIncomplete != "" {
		c.Preconditions = append(c.Preconditions,
			fail(RSSplitBrainCheckName, g.EnteredListingIncomplete),
			fail(RSShareClashCheckName, g.EnteredListingIncomplete))
		return c
	}

	if active, _, unknown := splitByState(c.InScope, g.EnteredStates); len(active) > 0 {
		have := "have members on"
		if unknown {
			have = "are active, or of unreadable state, on"
		}
		c.Preconditions = append(c.Preconditions, fail(RSSplitBrainCheckName, fmt.Sprintf(
			"consumer group(s) %s %s %s, the side being entered; the reversal would wait behind the fence until they leave. The route's own members clear on their own within one session timeout (45 s by default) of a flip or a fence, so rerun shortly; otherwise stop the clients using these groups directly on that cluster",
			strings.Join(active, ", "), have, entered)))
	} else {
		c.Preconditions = append(c.Preconditions, pass(RSSplitBrainCheckName))
	}

	if share := shareGroupsAmong(c.InScope, g.EnteredTypes); len(share) > 0 {
		c.Preconditions = append(c.Preconditions, fail(RSShareClashCheckName, fmt.Sprintf(
			"consumer group(s) %s are share groups on %s; the route's consumers could not join them after the switch — delete those share groups (kafka-share-groups --delete) or use other group names",
			strings.Join(share, ", "), entered)))
	} else {
		c.Preconditions = append(c.Preconditions, pass(RSShareClashCheckName))
	}
	return c
}

// groupsCommittingOn returns, sorted, the groups with a commit on any of topics.
func groupsCommittingOn(topics []string, committed map[string][]string) []string {
	want := toSet(topics)
	var out []string
	for g, ts := range committed {
		for _, t := range ts {
			if _, ok := want[t]; ok {
				out = append(out, g)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// untrackedBoundWarning lists the bound side's topics that are off the link and
// that no group commits on. Nothing tells a forgotten produce-only topic from a
// system topic, so this warns and never refuses.
func untrackedBoundWarning(topics []string, link map[string]struct{}, committed map[string][]string, bound, entered string) string {
	ordinary, internal := offLinkTopics(topics, link, committed)
	var names []string
	if len(ordinary) > 0 {
		names = append(names, joinCapped(ordinary, 20))
	}
	if len(internal) > 0 {
		names = append(names, joinCapped(internal, 20)+" (probably internal: name starts with '_')")
	}
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("topic(s) %s on %s are not on the cluster link and no consumer group has committed offsets on them, so they are not checked; after the switch the route sends their traffic to %s — add them to the link first if anything on this route still reads or writes them",
		strings.Join(names, "; "), bound, entered)
}
