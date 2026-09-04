package reconcile

import "testing"

func TestVerdictString(t *testing.T) {
	cases := map[Verdict]string{Migratable: "migratable", Unchanged: "unchanged", FailFast: "fail-fast"}
	for v, want := range cases {
		if got := v.String(); got != want {
			t.Fatalf("Verdict(%d).String() = %q, want %q", v, got, want)
		}
	}
}

func TestReportRefused(t *testing.T) {
	ok := Report{Migratable: []TopicVerdict{{Topic: "a"}}, Unchanged: []TopicVerdict{{Topic: "b"}}}
	if ok.Refused() {
		t.Fatal("report with only migratable+unchanged must not be refused")
	}
	ff := Report{FailFast: []TopicVerdict{{Topic: "c", Reason: "F3"}}}
	if !ff.Refused() {
		t.Fatal("report with a fail-fast topic must be refused")
	}
	pc := Report{Preconditions: []PreconditionResult{{Name: "route dynamic", OK: false}}}
	if !pc.Refused() {
		t.Fatal("report with a failed precondition must be refused")
	}
}
