package reconcile

import "testing"

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
