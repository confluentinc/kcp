package reconcile

import (
	"fmt"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name               string
		onSource, onTarget bool
		mirror             MirrorState
		routesToTarget     bool
		wantVerdict        Verdict
		wantReasonHas      string
	}{
		{"migratable", true, true, MirrorActive, false, Migratable, ""},
		{"unchanged", true, true, MirrorStopped, true, Unchanged, ""},
		{"F1 routes-target-unpromoted", true, true, MirrorActive, true, FailFast, "F1"},
		{"F2 promoted-not-switched", true, true, MirrorStopped, false, FailFast, "F2"},
		{"F3 not-on-link", true, false, MirrorNone, false, FailFast, "F3"},
		{"F4 absent-source", false, false, MirrorNone, false, FailFast, "F4"},
		{"F5 mirror-bad", true, true, MirrorBad, false, FailFast, "F5"},
		{"F6 independent-target", true, true, MirrorNone, false, FailFast, "F6"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := Classify("topic", c.onSource, c.onTarget, c.mirror, c.routesToTarget)
			if v.Verdict != c.wantVerdict {
				t.Fatalf("verdict = %v, want %v", v.Verdict, c.wantVerdict)
			}
			if c.wantReasonHas != "" && !contains(v.Reason, c.wantReasonHas) {
				t.Fatalf("reason = %q, want it to contain %q", v.Reason, c.wantReasonHas)
			}
		})
	}
}

// TestClassifyExhaustive pins EVERY cell of the input space — onSource(2) ×
// onTarget(2) × mirror(4) × routesToTarget(2) = 32 combinations — to its exact
// verdict. This locks not just the eight outcomes but the first-match ORDERING
// of the switch (e.g. F6-before-F3, F1-before-F5, the Stopped∧onTarget Unchanged
// quirk) against future edits. The one cell that reaches the `default` arm
// (onSource, Stopped, routes-to-target, NOT on target — promoted+switched yet
// absent on target) is asserted here so that reachable inconsistency stays
// classified as fail-fast rather than silently changing shape.
func TestClassifyExhaustive(t *testing.T) {
	const F, T = false, true
	type cell struct {
		onSource, onTarget bool
		mirror             MirrorState
		routesToTarget     bool
		want               Verdict
		reasonHas          string
	}
	cells := []cell{
		// onSource = false — nothing to migrate; case order still matters.
		{F, F, MirrorNone, F, FailFast, "F4"},
		{F, F, MirrorNone, T, FailFast, "F4"},
		{F, F, MirrorActive, F, FailFast, "F4"},
		{F, F, MirrorActive, T, FailFast, "F1"},
		{F, F, MirrorStopped, F, FailFast, "F2"},
		{F, F, MirrorStopped, T, FailFast, "F4"},
		{F, F, MirrorBad, F, FailFast, "F5"},
		{F, F, MirrorBad, T, FailFast, "F5"},
		{F, T, MirrorNone, F, FailFast, "F4"},
		{F, T, MirrorNone, T, FailFast, "F4"},
		{F, T, MirrorActive, F, FailFast, "F4"},
		{F, T, MirrorActive, T, FailFast, "F1"},
		{F, T, MirrorStopped, F, FailFast, "F2"},
		{F, T, MirrorStopped, T, Unchanged, ""}, // case2 matches even without source presence
		{F, T, MirrorBad, F, FailFast, "F5"},
		{F, T, MirrorBad, T, FailFast, "F5"},
		// onSource = true.
		{T, F, MirrorNone, F, FailFast, "F3"},
		{T, F, MirrorNone, T, FailFast, "F3"},
		{T, F, MirrorActive, F, Migratable, ""},
		{T, F, MirrorActive, T, FailFast, "F1"},
		{T, F, MirrorStopped, F, FailFast, "F2"},
		{T, F, MirrorStopped, T, FailFast, "unclassified"}, // the reachable default arm
		{T, F, MirrorBad, F, FailFast, "F5"},
		{T, F, MirrorBad, T, FailFast, "F5"},
		{T, T, MirrorNone, F, FailFast, "F6"},
		{T, T, MirrorNone, T, FailFast, "F6"},
		{T, T, MirrorActive, F, Migratable, ""},
		{T, T, MirrorActive, T, FailFast, "F1"},
		{T, T, MirrorStopped, F, FailFast, "F2"},
		{T, T, MirrorStopped, T, Unchanged, ""},
		{T, T, MirrorBad, F, FailFast, "F5"},
		{T, T, MirrorBad, T, FailFast, "F5"},
	}
	if len(cells) != 32 {
		t.Fatalf("table must enumerate all 32 combinations, got %d", len(cells))
	}
	for _, c := range cells {
		name := fmt.Sprintf("S=%v/T=%v/M=%s/R=%v", c.onSource, c.onTarget, c.mirror, c.routesToTarget)
		t.Run(name, func(t *testing.T) {
			v := Classify("topic", c.onSource, c.onTarget, c.mirror, c.routesToTarget)
			if v.Verdict != c.want {
				t.Fatalf("verdict = %v (%q), want %v", v.Verdict, v.Reason, c.want)
			}
			if !contains(v.Reason, c.reasonHas) {
				t.Fatalf("reason = %q, want it to contain %q", v.Reason, c.reasonHas)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
