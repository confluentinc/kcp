package reconcile

import (
	"fmt"
	"sort"
)

// placement is where a topic stands on a bidirectional link, in writable-side
// (W) and mirror-side (M) terms.
type placement int

const (
	placeInvalid    placement = iota
	placeReversible           // W ACTIVE, M STOPPED or none: writable on M, reversible toward W
	placeReversing            // a reversal toward W is in flight
	placeReversed             // W STOPPED or none, M ACTIVE: already writable on W
)

const (
	rsCoverageCheckName = "selection covers every mirror on the link"
	rsClientsCheckName  = "the unfenced route's clients are on the writable copy of every topic"
)

func (p placement) String() string {
	switch p {
	case placeReversible:
		return "reversible"
	case placeReversing:
		return "reversing"
	case placeReversed:
		return "reversed"
	}
	return "invalid"
}

// movingVerdict is the verdict of a movable placement when a reversal or switch
// follows.
func (p placement) movingVerdict() Verdict {
	switch p {
	case placeReversing:
		return AwaitStopped
	case placeReversed:
		return SwitchOnly
	}
	return Migratable
}

func rowIn(r MirrorRow, statuses ...RowStatus) bool {
	for _, s := range statuses {
		if r.Status == s {
			return true
		}
	}
	return false
}

func rowText(r MirrorRow) string {
	if r.Status == RowNone {
		return "none"
	}
	return r.Raw
}

// placeRow places a topic from its W and M rows, and whether it exists on each
// cluster. Anything that is not one of the three movable placements is
// placeInvalid, with the reason (the topic name not included).
func placeRow(w, m MirrorRow, onW, onM bool, wLabel, mLabel string) (placement, string) {
	// A reversal toward `to` is in flight when `to` is synchronizing next to a
	// stopped, absent or not-yet-mirroring row, or `from` has started mirroring
	// next to a stopped or stopping row.
	reversingToward := func(to, from MirrorRow) bool {
		return (to.Status == RowPendingSynchronize && rowIn(from, RowStopped, RowNone, RowPendingMirror)) ||
			(rowIn(to, RowPendingStopped, RowStopped) && from.Status == RowPendingMirror)
	}
	switch {
	case w.Status == RowNone && m.Status == RowNone:
		return placeInvalid, "is not a mirror on the cluster link on either cluster"
	case (w.Status == RowPendingStopped && m.Status == RowNone) || (m.Status == RowPendingStopped && w.Status == RowNone):
		return placeInvalid, "is being promoted; once that finishes the link can't flip it back"
	case reversingToward(m, w):
		return placeInvalid, fmt.Sprintf("is mid-reversal toward %s, the mirror side; wait for it to settle (about 10 s) and rerun — the rerun reverses it back", mLabel)
	case reversingToward(w, m):
		return placeReversing, ""
	case w.Status == RowActive && rowIn(m, RowStopped, RowNone):
		if m.Status == RowNone && !onM {
			return placeInvalid, "does not exist on " + mLabel
		}
		return placeReversible, ""
	case rowIn(w, RowStopped, RowNone) && m.Status == RowActive:
		if w.Status == RowNone && !onW {
			return placeInvalid, "does not exist on " + wLabel
		}
		return placeReversed, ""
	case rowIn(w, RowStopped, RowNone) && rowIn(m, RowStopped, RowNone):
		return placeInvalid, "is writable on both clusters; someone promoted or failed it over, so the link no longer gives a way back"
	}
	return placeInvalid, fmt.Sprintf("has mirror statuses kcp does not act on (%s: %s, %s: %s)", wLabel, rowText(w), mLabel, rowText(m))
}

// linkTopicSet is every topic with a mirror row on either copy of the link.
func linkTopicSet(f ReversibleFacts) map[string]struct{} {
	out := map[string]struct{}{}
	for _, c := range []LinkCopy{f.SourceLink, f.TargetLink} {
		for t := range c.Rows {
			out[t] = struct{}{}
		}
	}
	return out
}

// checkSelectionCoversLink refuses a selection that leaves out a mirror on the
// link: the link reverses topic by topic, so a topic left behind would be
// writable on the side the clients leave.
func checkSelectionCoversLink(selected []string, link map[string]struct{}) PreconditionResult {
	sel := toSet(selected)
	var uncovered []string
	for t := range link {
		if _, ok := sel[t]; !ok {
			uncovered = append(uncovered, t)
		}
	}
	if len(uncovered) == 0 {
		return pass(rsCoverageCheckName)
	}
	sort.Strings(uncovered)
	return fail(rsCoverageCheckName, fmt.Sprintf(
		"topic(s) %s have a mirror row on the cluster link but are not selected; the route moves every client, so every topic on the link must move with it — add them to topics or topicPatterns",
		joinCapped(uncovered, 20)))
}

// placeTopics gives each topic its verdict and returns the check that the
// unfenced route's clients are on the writable copy of every topic. When the
// route is on the mirror side, or in a back-out, reversible topics migrate,
// reversing ones are awaited and reversed ones only switch; unfenced on the
// mirror side, only reversible topics fit where the clients are. Unfenced on
// the writable side, only reversed topics fit, and they are unchanged. Outside a
// flip the clients are always on the writable copy, so a misfit there most
// likely means the two domain names are swapped.
func placeTopics(in ReversibleInput, s Sides, f ReversibleFacts, topics []string) ([]TopicVerdict, PreconditionResult) {
	wRows, mRows := f.Link(s.Writable).Rows, f.Link(s.Mirror).Rows
	onW, onM := toSet(f.Topics(s.Writable)), toSet(f.Topics(s.Mirror))
	wLabel, mLabel, boundLabel := in.ClusterLabel(s.Writable), in.ClusterLabel(s.Mirror), in.ClusterLabel(s.Bound)
	onMirrorSide := s.Bound == s.Mirror || s.BackOut
	notWhereClientsAre := "is not writable on " + boundLabel + ", the cluster the unfenced route points at"

	var verdicts []TopicVerdict
	var misplaced []string
	for _, t := range topics {
		w, m := wRows[t], mRows[t]
		_, tOnW := onW[t]
		_, tOnM := onM[t]
		p, reason := placeRow(w, m, tOnW, tOnM, wLabel, mLabel)
		tv := TopicVerdict{
			Topic: t,
			S:     rowText(f.SourceLink.Rows[t]),
			M:     p.String(),
			T:     rowText(f.TargetLink.Rows[t]),
			R:     "->" + string(s.Bound),
		}
		switch {
		case p == placeInvalid:
			tv.Verdict, tv.Reason = FailFast, t+" "+reason
		case onMirrorSide && (s.Fenced || p == placeReversible):
			tv.Verdict = p.movingVerdict()
		case !onMirrorSide && p == placeReversed:
			tv.Verdict = Unchanged
		default:
			tv.Verdict, tv.Reason = FailFast, t+" "+notWhereClientsAre
			misplaced = append(misplaced, t)
		}
		verdicts = append(verdicts, tv)
	}

	if len(misplaced) == 0 {
		return verdicts, pass(rsClientsCheckName)
	}
	return verdicts, fail(rsClientsCheckName, fmt.Sprintf(
		"topic(s) %s are not writable on %s, where the unfenced route sends clients. Outside a flip the clients are always on the writable copy, so route.sourceStreamingDomain and route.targetStreamingDomain may be swapped — check that each names the cluster in spec.source and spec.target",
		joinCapped(misplaced, 20), boundLabel))
}
