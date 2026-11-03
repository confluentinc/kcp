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
		{"switch-only", true, true, MirrorStopped, false, SwitchOnly, ""},
		{"await-stopped", true, true, MirrorPending, false, AwaitStopped, ""},
		{"routes-to-target-but-unpromoted", true, true, MirrorActive, true, FailFast, "not yet promoted"},
		{"stopped-not-on-target", true, false, MirrorStopped, false, FailFast, "not switched over"},
		{"not-on-link", true, false, MirrorNone, false, FailFast, "not on the cluster link"},
		{"absent-on-source", false, false, MirrorNone, false, FailFast, "not found on the source"},
		{"mirror-in-bad-state", true, true, MirrorBad, false, FailFast, "failed"},
		{"pending-unexpected", true, true, MirrorPending, true, FailFast, "promotion is still in progress"},
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

// TestClassifyExhaustive pins every cell of onSource(2) × onTarget(2) ×
// mirror(5) × routesToTarget(2) = 40. Resume verdicts (SwitchOnly, AwaitStopped)
// require onSource && onTarget && !routesToTarget; every other mid-migration or
// inconsistent cell fails closed. reasonHas is empty for the non-fail-fast
// verdicts and a distinctive fragment for each fail-fast (also verifying WHICH
// switch case fired, locking ordering).
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
		{F, F, MirrorNone, F, FailFast, "not found on the source"},
		{F, F, MirrorNone, T, FailFast, "not found on the source"},
		{F, F, MirrorActive, F, FailFast, "not found on the source"},
		{F, F, MirrorActive, T, FailFast, "not yet promoted"},
		{F, F, MirrorStopped, F, FailFast, "not switched over"},
		{F, F, MirrorStopped, T, FailFast, "not found on the source"},
		{F, F, MirrorBad, F, FailFast, "failed"},
		{F, F, MirrorBad, T, FailFast, "failed"},
		{F, F, MirrorPending, F, FailFast, "promotion is still in progress"},
		{F, F, MirrorPending, T, FailFast, "promotion is still in progress"},
		{F, T, MirrorNone, F, FailFast, "not found on the source"},
		{F, T, MirrorNone, T, FailFast, "not found on the source"},
		{F, T, MirrorActive, F, FailFast, "not found on the source"},
		{F, T, MirrorActive, T, FailFast, "not yet promoted"},
		{F, T, MirrorStopped, F, FailFast, "not switched over"},
		{F, T, MirrorStopped, T, Unchanged, ""}, // Unchanged matches without source presence
		{F, T, MirrorBad, F, FailFast, "failed"},
		{F, T, MirrorBad, T, FailFast, "failed"},
		{F, T, MirrorPending, F, FailFast, "promotion is still in progress"},
		{F, T, MirrorPending, T, FailFast, "promotion is still in progress"},
		// onSource = true.
		{T, F, MirrorNone, F, FailFast, "not on the cluster link"},
		{T, F, MirrorNone, T, FailFast, "not on the cluster link"},
		{T, F, MirrorActive, F, Migratable, ""},
		{T, F, MirrorActive, T, FailFast, "not yet promoted"},
		{T, F, MirrorStopped, F, FailFast, "not switched over"}, // Stopped but not on target → inconsistent
		{T, F, MirrorStopped, T, FailFast, "unclassified"},      // reachable default
		{T, F, MirrorBad, F, FailFast, "failed"},
		{T, F, MirrorBad, T, FailFast, "failed"},
		{T, F, MirrorPending, F, FailFast, "promotion is still in progress"}, // Pending but not on target
		{T, F, MirrorPending, T, FailFast, "promotion is still in progress"},
		{T, T, MirrorNone, F, FailFast, "not a mirror of the source"},
		{T, T, MirrorNone, T, FailFast, "not a mirror of the source"},
		{T, T, MirrorActive, F, Migratable, ""},
		{T, T, MirrorActive, T, FailFast, "not yet promoted"},
		{T, T, MirrorStopped, F, SwitchOnly, ""}, // CHANGED: was FailFast "not switched over"
		{T, T, MirrorStopped, T, Unchanged, ""},
		{T, T, MirrorBad, F, FailFast, "failed"},
		{T, T, MirrorBad, T, FailFast, "failed"},
		{T, T, MirrorPending, F, AwaitStopped, ""}, // NEW: the await-then-switch resume case
		{T, T, MirrorPending, T, FailFast, "promotion is still in progress"},
	}
	if len(cells) != 40 {
		t.Fatalf("table must enumerate all 40 combinations, got %d", len(cells))
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
