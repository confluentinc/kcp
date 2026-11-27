package reconcile

import (
	"reflect"
	"strings"
	"testing"
)

var groupLink = map[string]struct{}{"a": {}, "b": {}}

func sidesFor(t *testing.T, bound string, fenced bool) Sides {
	t.Helper()
	s, ok := ResolveSides(reversibleInput("cc"), reversibleGateway(bound, fenced))
	if !ok {
		t.Fatal("fixture sides did not resolve")
	}
	return s
}

func flipGroups(t *testing.T, g *ReversibleGroupFacts, selected, bound []string) GroupCheck {
	t.Helper()
	return CheckReversibleGroups(reversibleInput("cc"), sidesFor(t, "cp", false), g, groupLink, selected, bound)
}

func TestCheckReversibleGroups_NotMovingRunsNothing(t *testing.T) {
	g := &ReversibleGroupFacts{Committed: map[string][]string{"g1": {"a", "x"}}, EnteredStates: map[string]string{"g1": "Stable"}}
	got := CheckReversibleGroups(reversibleInput("cc"), sidesFor(t, "cc", false), g, groupLink, []string{"a", "b"}, []string{"a", "b"})
	if !reflect.DeepEqual(got, GroupCheck{}) {
		t.Errorf("nothing moves, so no group check may run: %+v", got)
	}
}

func TestCheckReversibleGroups_NilFactsFailsClosed(t *testing.T) {
	got := flipGroups(t, nil, []string{"a"}, []string{"a"})
	requireFailed(t, got.Preconditions, RSSplitBrainCheckName, "consumer group facts were not gathered")
}

func TestCheckReversibleGroups_OffLinkCommitsRefused(t *testing.T) {
	g := &ReversibleGroupFacts{Committed: map[string][]string{"g1": {"a", "x"}, "g2": {"a"}}}
	got := flipGroups(t, g, []string{"a", "b"}, []string{"a", "b", "x"})
	r := requireFailed(t, got.Preconditions, RSGroupScopeCheckName, "g1 (x)", "the target cluster (tgt-id)")
	if strings.Contains(r.Detail, "g2") {
		t.Errorf("detail = %q names a group that commits only on link topics", r.Detail)
	}
}

func TestCheckReversibleGroups_UntrackedOffLinkTopicWarns(t *testing.T) {
	g := &ReversibleGroupFacts{Committed: map[string][]string{"g1": {"a"}}}
	got := flipGroups(t, g, []string{"a", "b"}, []string{"a", "b", "orders", "_schemas"})
	if r, _ := findCheck(got.Preconditions, RSGroupScopeCheckName); !r.OK {
		t.Fatalf("topics with no commits only warn: %+v", r)
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("warnings = %q, want one", got.Warnings)
	}
	for _, want := range []string{"orders", "_schemas", "the source cluster (src-id)"} {
		if !strings.Contains(got.Warnings[0], want) {
			t.Errorf("warning = %q, want it to contain %q", got.Warnings[0], want)
		}
	}
}

func TestCheckReversibleGroups_BoundListingIncompleteRefuses(t *testing.T) {
	g := &ReversibleGroupFacts{Committed: map[string][]string{"g1": {"a"}}, ListingIncomplete: "the source credential cannot describe every group"}
	got := flipGroups(t, g, []string{"a"}, []string{"a"})
	requireFailed(t, got.Preconditions, RSGroupScopeCheckName, "the source credential cannot describe every group")
}

// A credential that can't list every group on the side being entered may hide
// an active group, so the missing states must never read as idle.
func TestCheckReversibleGroups_EnteredListingIncompleteRefuses(t *testing.T) {
	g := &ReversibleGroupFacts{
		Committed:                map[string][]string{"g1": {"a"}},
		EnteredStates:            map[string]string{},
		EnteredListingIncomplete: "the destination credential cannot describe every group",
	}
	got := flipGroups(t, g, []string{"a"}, []string{"a"})
	requireFailed(t, got.Preconditions, RSSplitBrainCheckName, "the destination credential cannot describe every group")
	requireFailed(t, got.Preconditions, RSShareClashCheckName, "the destination credential cannot describe every group")
}

func TestCheckReversibleGroups_ActiveGroupRefused(t *testing.T) {
	g := &ReversibleGroupFacts{Committed: map[string][]string{"g1": {"a"}}, EnteredStates: map[string]string{"g1": "Stable"}}
	got := flipGroups(t, g, []string{"a"}, []string{"a"})
	requireFailed(t, got.Preconditions, RSSplitBrainCheckName, "g1 (Stable)", "the target cluster (tgt-id)", "45 s by default")
}

func TestCheckReversibleGroups_UnknownStateCountsAsActive(t *testing.T) {
	g := &ReversibleGroupFacts{Committed: map[string][]string{"g1": {"a"}}, EnteredStates: map[string]string{"g1": ""}}
	got := flipGroups(t, g, []string{"a"}, []string{"a"})
	r := requireFailed(t, got.Preconditions, RSSplitBrainCheckName, "g1 (state unknown; treated as active)")
	if strings.Contains(r.Detail, "have members") {
		t.Errorf("detail = %q claims members for a state kcp could not read", r.Detail)
	}
}

func TestCheckReversibleGroups_DeadIgnoredEmptyNoWarning(t *testing.T) {
	g := &ReversibleGroupFacts{
		Committed:     map[string][]string{"g1": {"a"}, "g2": {"b"}},
		EnteredStates: map[string]string{"g1": "Dead", "g2": "Empty"},
	}
	got := flipGroups(t, g, []string{"a", "b"}, []string{"a", "b"})
	requireAllOK(t, got.Preconditions)
	if len(got.Warnings) != 0 {
		t.Errorf("an Empty group with offsets is the normal state after a flip; warnings = %q", got.Warnings)
	}
}

func TestCheckReversibleGroups_ShareClashRefused(t *testing.T) {
	g := &ReversibleGroupFacts{Committed: map[string][]string{"g1": {"a"}, "g2": {"a"}}, EnteredTypes: map[string]string{"g1": "share", "g2": "consumer"}}
	got := flipGroups(t, g, []string{"a"}, []string{"a"})
	r := requireFailed(t, got.Preconditions, RSShareClashCheckName, "g1", "the target cluster (tgt-id)", "kafka-share-groups --delete")
	if strings.Contains(r.Detail, "g2") {
		t.Errorf("detail = %q names a consumer group", r.Detail)
	}
}

// In a back-out the clients stay on the cluster they're on, so their commits
// elsewhere don't matter; the side being entered is still checked.
func TestCheckReversibleGroups_BackOutSkipsGroupScope(t *testing.T) {
	g := &ReversibleGroupFacts{Committed: map[string][]string{"g1": {"a", "x"}}, EnteredStates: map[string]string{"g1": "Stable"}}
	got := CheckReversibleGroups(reversibleInput("cc"), sidesFor(t, "cc", true), g, groupLink, []string{"a"}, []string{"a", "x"})
	if _, ok := findCheck(got.Preconditions, RSGroupScopeCheckName); ok {
		t.Errorf("a back-out must not run the group scope check: %+v", got.Preconditions)
	}
	requireFailed(t, got.Preconditions, RSSplitBrainCheckName, "g1 (Stable)")
}

func TestCheckReversibleGroups_InScopeOnlyForSelectedTopics(t *testing.T) {
	g := &ReversibleGroupFacts{
		Committed:     map[string][]string{"g2": {"b"}, "g1": {"a", "b"}, "g3": {}, "g0": {"a"}},
		EnteredStates: map[string]string{"g2": "Stable"},
	}
	got := flipGroups(t, g, []string{"a"}, []string{"a", "b"})
	if want := []string{"g0", "g1"}; !reflect.DeepEqual(got.InScope, want) {
		t.Errorf("InScope = %q, want %q", got.InScope, want)
	}
	requireAllOK(t, got.Preconditions)
}

// Without every commit on the bound side the in-scope groups can't be known,
// so neither group check on the side being entered may pass.
func TestCheckReversibleGroups_BoundListingIncompleteFailsEveryGroupCheck(t *testing.T) {
	const gap = "the source credential cannot describe arbitrary topics"
	g := &ReversibleGroupFacts{ListingIncomplete: gap, EnteredStates: map[string]string{"g1": "Stable"}}
	got := flipGroups(t, g, []string{"a"}, []string{"a"})
	for _, name := range []string{RSGroupScopeCheckName, RSSplitBrainCheckName, RSShareClashCheckName} {
		requireFailed(t, got.Preconditions, name, gap)
	}
}

func TestCheckReversibleGroups_BackOutBoundListingIncompleteRefuses(t *testing.T) {
	const gap = "the target credential cannot describe arbitrary topics"
	g := &ReversibleGroupFacts{ListingIncomplete: gap, EnteredStates: map[string]string{"g1": "Stable"}}
	got := CheckReversibleGroups(reversibleInput("cc"), sidesFor(t, "cc", true), g, groupLink, []string{"a"}, []string{"a"})
	requireFailed(t, got.Preconditions, RSSplitBrainCheckName, gap)
	requireFailed(t, got.Preconditions, RSShareClashCheckName, gap)
}
