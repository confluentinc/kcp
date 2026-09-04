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
		{"routes-to-target-but-unpromoted", true, true, MirrorActive, true, FailFast, "not yet promoted"},
		{"promoted-not-switched", true, true, MirrorStopped, false, FailFast, "not switched over"},
		{"not-on-link", true, false, MirrorNone, false, FailFast, "not on the cluster link"},
		{"absent-on-source", false, false, MirrorNone, false, FailFast, "not found on the source"},
		{"mirror-in-bad-state", true, true, MirrorBad, false, FailFast, "failed/transitional"},
		{"independent-target-topic", true, true, MirrorNone, false, FailFast, "not a mirror of the source"},
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
// verdict, asserting on a distinctive fragment of each fail-fast's message. This
// locks not just the eight outcomes but the first-match ORDERING of the switch —
// e.g. an independent same-named target topic is caught before "not on the link",
// and a bad mirror before either — against future edits. The one cell that
// reaches the `default` case (onSource, Stopped, routes-to-target, NOT on target —
// promoted and switched yet absent on target) is asserted here so that reachable
// inconsistency stays classified as fail-fast rather than silently changing shape.
func TestClassifyExhaustive(t *testing.T) {
	const F, T = false, true
	type cell struct {
		onSource, onTarget bool
		mirror             MirrorState
		routesToTarget     bool
		want               Verdict
		reasonHas          string
	}
	// reasonHas is a substring unique to the message that the matching switch
	// case produces, so it also verifies WHICH case fired, not merely that some
	// fail-fast did.
	cells := []cell{
		// onSource = false — nothing to migrate; case order still matters.
		{F, F, MirrorNone, F, FailFast, "not found on the source"},
		{F, F, MirrorNone, T, FailFast, "not found on the source"},
		{F, F, MirrorActive, F, FailFast, "not found on the source"},
		{F, F, MirrorActive, T, FailFast, "not yet promoted"},
		{F, F, MirrorStopped, F, FailFast, "not switched over"},
		{F, F, MirrorStopped, T, FailFast, "not found on the source"},
		{F, F, MirrorBad, F, FailFast, "failed/transitional"},
		{F, F, MirrorBad, T, FailFast, "failed/transitional"},
		{F, T, MirrorNone, F, FailFast, "not found on the source"},
		{F, T, MirrorNone, T, FailFast, "not found on the source"},
		{F, T, MirrorActive, F, FailFast, "not found on the source"},
		{F, T, MirrorActive, T, FailFast, "not yet promoted"},
		{F, T, MirrorStopped, F, FailFast, "not switched over"},
		{F, T, MirrorStopped, T, Unchanged, ""}, // Unchanged case matches even without source presence
		{F, T, MirrorBad, F, FailFast, "failed/transitional"},
		{F, T, MirrorBad, T, FailFast, "failed/transitional"},
		// onSource = true.
		{T, F, MirrorNone, F, FailFast, "not on the cluster link"},
		{T, F, MirrorNone, T, FailFast, "not on the cluster link"},
		{T, F, MirrorActive, F, Migratable, ""},
		{T, F, MirrorActive, T, FailFast, "not yet promoted"},
		{T, F, MirrorStopped, F, FailFast, "not switched over"},
		{T, F, MirrorStopped, T, FailFast, "unclassified"}, // the reachable default case
		{T, F, MirrorBad, F, FailFast, "failed/transitional"},
		{T, F, MirrorBad, T, FailFast, "failed/transitional"},
		{T, T, MirrorNone, F, FailFast, "not a mirror of the source"},
		{T, T, MirrorNone, T, FailFast, "not a mirror of the source"},
		{T, T, MirrorActive, F, Migratable, ""},
		{T, T, MirrorActive, T, FailFast, "not yet promoted"},
		{T, T, MirrorStopped, F, FailFast, "not switched over"},
		{T, T, MirrorStopped, T, Unchanged, ""},
		{T, T, MirrorBad, F, FailFast, "failed/transitional"},
		{T, T, MirrorBad, T, FailFast, "failed/transitional"},
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
