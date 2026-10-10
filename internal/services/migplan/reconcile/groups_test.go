package reconcile

import (
	"strings"
	"testing"
)

func TestCheckGroupSplitBrain_RefusesActiveGroups(t *testing.T) {
	for _, state := range []string{"Stable", "PreparingRebalance", "CompletingRebalance", "Assigning", "Reconciling", "STABLE"} {
		t.Run(state, func(t *testing.T) {
			res, _ := CheckGroupSplitBrain([]string{"orders-app"}, map[string]string{"orders-app": state})
			if res.OK {
				t.Fatalf("state %q on destination must refuse, got pass", state)
			}
			if !strings.Contains(res.Detail, "orders-app ("+state+")") {
				t.Errorf("detail = %q, want it to name the group and its state", res.Detail)
			}
		})
	}
}

func TestCheckGroupSplitBrain_UnknownStateRefuses(t *testing.T) {
	for _, state := range []string{"", "NotReady"} {
		res, _ := CheckGroupSplitBrain([]string{"g"}, map[string]string{"g": state})
		if res.OK {
			t.Fatalf("state %q must be treated as active and refuse", state)
		}
	}
}

// A group whose destination state could not be read is refused as active, but
// the message must not claim it has members: it says the state is unknown.
func TestCheckGroupSplitBrain_UnknownStateDetailDoesNotClaimMembers(t *testing.T) {
	res, _ := CheckGroupSplitBrain([]string{"g", "orders-app"}, map[string]string{"g": "", "orders-app": "Stable"})
	if res.OK {
		t.Fatal("an unknown state must refuse")
	}
	for _, want := range []string{"g (state unknown; treated as active)", "orders-app (Stable)"} {
		if !strings.Contains(res.Detail, want) {
			t.Errorf("detail = %q, want it to contain %q", res.Detail, want)
		}
	}
	if strings.Contains(res.Detail, "already have members") {
		t.Errorf("detail = %q must not claim an unknown-state group already has members", res.Detail)
	}
}

func TestCheckGroupSplitBrain_EmptyGroupWarnsOnly(t *testing.T) {
	res, warnings := CheckGroupSplitBrain([]string{"billing"}, map[string]string{"billing": "Empty"})
	if !res.OK {
		t.Fatalf("an Empty destination group must not refuse, got %+v", res)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "billing") || !strings.Contains(warnings[0], "overwrite") {
		t.Fatalf("warnings = %v, want one naming billing and the overwrite", warnings)
	}
}

func TestCheckGroupSplitBrain_IgnoresDeadAndDestinationOnlyGroups(t *testing.T) {
	res, warnings := CheckGroupSplitBrain([]string{"gone"}, map[string]string{"gone": "Dead", "dest-only-app": "Stable"})
	if !res.OK || len(warnings) != 0 {
		t.Fatalf("got %+v / %v, want a pass and no warnings", res, warnings)
	}
}

func TestCheckGroupSplitBrain_DetailIsSorted(t *testing.T) {
	res, _ := CheckGroupSplitBrain([]string{"zeta", "alpha"}, map[string]string{"zeta": "Stable", "alpha": "Stable"})
	if strings.Index(res.Detail, "alpha") > strings.Index(res.Detail, "zeta") {
		t.Errorf("detail = %q, want groups sorted", res.Detail)
	}
}

func TestConvertCredentialChecks_AllFiveInOrder(t *testing.T) {
	want := []string{
		SourceGroupVisibilityCheckName,
		TargetGroupVisibilityCheckName,
		SourceTopicVisibilityCheckName,
		TargetTopicVisibilityCheckName,
		TargetOffsetCommitCheckName,
	}
	got := ConvertCredentialChecks(GroupFacts{})
	if len(got) != len(want) {
		t.Fatalf("got %d checks %+v, want %d", len(got), got, len(want))
	}
	for i, pc := range got {
		if pc.Name != want[i] || !pc.OK {
			t.Errorf("check %d = %+v, want %q passing", i, pc, want[i])
		}
	}
}

func TestConvertCredentialChecks_EachReasonRefusesItsOwnCheck(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*GroupFacts)
	}{
		{SourceGroupVisibilityCheckName, func(g *GroupFacts) { g.SourceListingIncomplete = "why" }},
		{TargetGroupVisibilityCheckName, func(g *GroupFacts) { g.TargetListingIncomplete = "why" }},
		{SourceTopicVisibilityCheckName, func(g *GroupFacts) { g.SourceTopicsIncomplete = "why" }},
		{TargetTopicVisibilityCheckName, func(g *GroupFacts) { g.TargetTopicsIncomplete = "why" }},
		{TargetOffsetCommitCheckName, func(g *GroupFacts) { g.TargetCommitDenied = "why" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var g GroupFacts
			tc.set(&g)
			for _, pc := range ConvertCredentialChecks(g) {
				if wantOK := pc.Name != tc.name; pc.OK != wantOK {
					t.Errorf("%q OK = %v, want %v", pc.Name, pc.OK, wantOK)
				}
				if !pc.OK && pc.Detail != "why" {
					t.Errorf("%q detail = %q, want the reason carried as is", pc.Name, pc.Detail)
				}
			}
		})
	}
}
