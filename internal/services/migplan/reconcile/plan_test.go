package reconcile

import "testing"

func TestVerdictString(t *testing.T) {
	cases := map[Verdict]string{
		Migratable:   "migratable",
		Unchanged:    "unchanged",
		FailFast:     "fail-fast",
		SwitchOnly:   "switch-only",
		AwaitStopped: "await-stopped",
	}
	for v, want := range cases {
		if got := v.String(); got != want {
			t.Fatalf("Verdict(%d).String() = %q, want %q", int(v), got, want)
		}
	}
}

func TestReportRefused(t *testing.T) {
	ok := Report{Migratable: []TopicVerdict{{Topic: "a"}}, Unchanged: []TopicVerdict{{Topic: "b"}}}
	if ok.Refused() {
		t.Fatal("report with only migratable+unchanged must not be refused")
	}
	ff := Report{FailFast: []TopicVerdict{{Topic: "c", Reason: "not on the cluster link"}}}
	if !ff.Refused() {
		t.Fatal("report with a fail-fast topic must be refused")
	}
	pc := Report{Preconditions: []PreconditionResult{{Name: "route dynamic", OK: false}}}
	if !pc.Refused() {
		t.Fatal("report with a failed precondition must be refused")
	}
	// Resume verdicts must NOT refuse.
	resume := Report{
		SwitchOnly:   []TopicVerdict{{Topic: "a", Verdict: SwitchOnly}},
		AwaitStopped: []TopicVerdict{{Topic: "b", Verdict: AwaitStopped}},
	}
	if resume.Refused() {
		t.Fatal("Refused() = true for a report with only SwitchOnly/AwaitStopped topics, want false")
	}
	// A fail-fast still refuses even alongside resume verdicts.
	resume.FailFast = []TopicVerdict{{Topic: "c", Verdict: FailFast, Reason: "x"}}
	if !resume.Refused() {
		t.Fatal("Refused() = false with a FailFast topic, want true")
	}
}
