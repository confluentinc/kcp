package reconcile

import (
	"reflect"
	"strings"
	"testing"
)

var none MirrorRow

func TestPlaceRow(t *testing.T) {
	paused := row(RowOther, "PAUSED")
	for _, tc := range []struct {
		name     string
		w, m     MirrorRow
		onW, onM bool
		want     placement
		reason   string
	}{
		{"no row on either copy", none, none, true, true, placeInvalid, "is not a mirror on the cluster link on either cluster"},
		{"promote in progress on W", pendingStopped, none, true, true, placeInvalid, "is being promoted; once that finishes the link can't flip it back"},
		{"promote in progress on M", none, pendingStopped, true, true, placeInvalid, "is being promoted"},
		{"toward M: M synchronizing, W stopped", stopped, pendingSynchronize, true, true, placeInvalid, "is mid-reversal toward M-cluster, the mirror side; wait for it to settle (about 10 s) and rerun — the rerun reverses it back"},
		{"toward M: M synchronizing, W none", none, pendingSynchronize, true, true, placeInvalid, "is mid-reversal toward M-cluster"},
		{"toward M: M synchronizing, W pending mirror", pendingMirror, pendingSynchronize, true, true, placeInvalid, "is mid-reversal toward M-cluster"},
		{"toward M: M pending stopped, W pending mirror", pendingMirror, pendingStopped, true, true, placeInvalid, "is mid-reversal toward M-cluster"},
		{"toward M: M stopped, W pending mirror", pendingMirror, stopped, true, true, placeInvalid, "is mid-reversal toward M-cluster"},
		{"reversing: W synchronizing, M stopped", pendingSynchronize, stopped, true, true, placeReversing, ""},
		{"reversing: W synchronizing, M none", pendingSynchronize, none, true, true, placeReversing, ""},
		{"reversing: W synchronizing, M pending mirror", pendingSynchronize, pendingMirror, true, true, placeReversing, ""},
		{"reversing: W pending stopped, M pending mirror", pendingStopped, pendingMirror, true, true, placeReversing, ""},
		{"reversing: W stopped, M pending mirror", stopped, pendingMirror, true, true, placeReversing, ""},
		{"reversible: W active, M stopped", active, stopped, true, true, placeReversible, ""},
		{"reversible: W active, M none", active, none, true, true, placeReversible, ""},
		{"W active, M none, missing on M", active, none, true, false, placeInvalid, "does not exist on M-cluster"},
		{"reversed: W stopped, M active", stopped, active, true, true, placeReversed, ""},
		{"reversed: W none, M active", none, active, true, true, placeReversed, ""},
		{"W none, M active, missing on W", none, active, false, true, placeInvalid, "does not exist on W-cluster"},
		{"writable on both: both stopped", stopped, stopped, true, true, placeInvalid, "is writable on both clusters; someone promoted or failed it over, so the link no longer gives a way back"},
		{"writable on both: W stopped, M none", stopped, none, true, true, placeInvalid, "is writable on both clusters"},
		{"both active", active, active, true, true, placeInvalid, "has mirror statuses kcp does not act on (W-cluster: ACTIVE, M-cluster: ACTIVE)"},
		{"paused", paused, stopped, true, true, placeInvalid, "PAUSED"},
		{"unknown next to none", paused, none, true, true, placeInvalid, "(W-cluster: PAUSED, M-cluster: none)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := placeRow(tc.w, tc.m, tc.onW, tc.onM, "W-cluster", "M-cluster")
			if got != tc.want {
				t.Errorf("placement = %d, want %d (reason %q)", got, tc.want, reason)
			}
			switch {
			case tc.reason == "" && reason != "":
				t.Errorf("reason = %q, want none", reason)
			case !strings.Contains(reason, tc.reason):
				t.Errorf("reason = %q, want it to contain %q", reason, tc.reason)
			}
		})
	}
}

// placedFacts builds facts whose source and target copies carry the given
// rows; every named topic exists on both clusters.
func placedFacts(gw *GatewayConfig, source, target map[string]MirrorRow, topics ...string) ReversibleFacts {
	f := neverReversedFacts()
	f.Gateway = gw
	f.SourceTopics, f.TargetTopics = topics, topics
	f.SourceLink.Rows, f.TargetLink.Rows = source, target
	return f
}

func verdictsOf(tvs []TopicVerdict) map[string]Verdict {
	out := map[string]Verdict{}
	for _, tv := range tvs {
		out[tv.Topic] = tv.Verdict
	}
	return out
}

func place(t *testing.T, in ReversibleInput, f ReversibleFacts, topics ...string) ([]TopicVerdict, PreconditionResult) {
	t.Helper()
	s, ok := ResolveSides(in, f.Gateway)
	if !ok {
		t.Fatal("fixture sides did not resolve")
	}
	return placeTopics(in, s, f, topics)
}

func TestPlaceTopics_NormalFlip(t *testing.T) {
	tvs, check := place(t, reversibleInput("cc"), neverReversedFacts(), "a", "b")
	if want := map[string]Verdict{"a": Migratable, "b": Migratable}; !reflect.DeepEqual(verdictsOf(tvs), want) {
		t.Errorf("verdicts = %v, want %v", verdictsOf(tvs), want)
	}
	if !check.OK || check.Name != rsClientsCheckName {
		t.Errorf("clients check = %+v", check)
	}
}

func TestPlaceTopics_ResumeFenced(t *testing.T) {
	f := placedFacts(reversibleGateway("cp", true),
		map[string]MirrorRow{"b": stopped, "c": active},
		map[string]MirrorRow{"a": active, "b": pendingSynchronize, "c": stopped},
		"a", "b", "c")
	tvs, check := place(t, reversibleInput("cc"), f, "a", "b", "c")
	if want := map[string]Verdict{"a": Migratable, "b": AwaitStopped, "c": SwitchOnly}; !reflect.DeepEqual(verdictsOf(tvs), want) {
		t.Errorf("verdicts = %v, want %v", verdictsOf(tvs), want)
	}
	if !check.OK {
		t.Errorf("a fenced route has no clients to misplace: %+v", check)
	}
}

func TestPlaceTopics_UnfencedMixFailsClientsCheck(t *testing.T) {
	f := placedFacts(reversibleGateway("cp", false),
		map[string]MirrorRow{"b": active},
		map[string]MirrorRow{"a": active, "b": stopped},
		"a", "b")
	tvs, check := place(t, reversibleInput("cc"), f, "a", "b")
	if want := map[string]Verdict{"a": Migratable, "b": FailFast}; !reflect.DeepEqual(verdictsOf(tvs), want) {
		t.Errorf("verdicts = %v, want %v", verdictsOf(tvs), want)
	}
	for _, tv := range tvs {
		if tv.Topic == "b" && !strings.Contains(tv.Reason, "is not writable on the source cluster (src-id), the cluster the unfenced route points at") {
			t.Errorf("b reason = %q", tv.Reason)
		}
	}
	if check.OK || !strings.Contains(check.Detail, "b") || !strings.Contains(check.Detail, "swapped") || strings.Contains(check.Detail, "a,") {
		t.Errorf("clients check = %+v, want a failure naming b only", check)
	}
}

func TestPlaceTopics_WritableSideUnfencedReversedIsUnchanged(t *testing.T) {
	f := placedFacts(reversibleGateway("cc", false),
		map[string]MirrorRow{"a": active, "b": active},
		map[string]MirrorRow{"a": stopped, "b": stopped},
		"a", "b")
	tvs, check := place(t, reversibleInput("cc"), f, "a", "b")
	if want := map[string]Verdict{"a": Unchanged, "b": Unchanged}; !reflect.DeepEqual(verdictsOf(tvs), want) {
		t.Errorf("verdicts = %v, want %v", verdictsOf(tvs), want)
	}
	if !check.OK {
		t.Errorf("clients check = %+v", check)
	}
}

func TestPlaceTopics_WritableSideUnfencedReversibleFailsClientsCheck(t *testing.T) {
	f := neverReversedFacts()
	f.Gateway = reversibleGateway("cc", false)
	tvs, check := place(t, reversibleInput("cc"), f, "a", "b")
	if want := map[string]Verdict{"a": FailFast, "b": FailFast}; !reflect.DeepEqual(verdictsOf(tvs), want) {
		t.Errorf("verdicts = %v, want %v", verdictsOf(tvs), want)
	}
	if check.OK || !strings.Contains(check.Detail, "the target cluster (tgt-id)") {
		t.Errorf("clients check = %+v", check)
	}
}

// A flip toward cc stopped part way, fenced, with the route still on cp; the
// operator set writableOn back to cp. a was reversed by the stopped run, b
// never was.
func TestPlaceTopics_BackOut(t *testing.T) {
	f := placedFacts(reversibleGateway("cp", true),
		map[string]MirrorRow{"a": active},
		map[string]MirrorRow{"a": stopped, "b": active},
		"a", "b")
	in := reversibleInput("cp")
	s, _ := ResolveSides(in, f.Gateway)
	if !s.BackOut {
		t.Fatalf("fixture is not a back-out: %+v", s)
	}
	tvs, check := placeTopics(in, s, f, []string{"a", "b"})
	if want := map[string]Verdict{"a": Migratable, "b": SwitchOnly}; !reflect.DeepEqual(verdictsOf(tvs), want) {
		t.Errorf("verdicts = %v, want %v", verdictsOf(tvs), want)
	}
	if !check.OK {
		t.Errorf("clients check = %+v", check)
	}
}

func TestPlaceTopics_SwappedDomainsCaught(t *testing.T) {
	in := reversibleInput("cc")
	in.SourceDomain, in.TargetDomain = "cc", "cp"
	_, check := place(t, in, neverReversedFacts(), "a", "b")
	if check.OK || !strings.Contains(check.Detail, "swapped") {
		t.Errorf("swapped domains must fail the clients check: %+v", check)
	}
}

// topicPatterns ['.*'] on a Confluent source also selects its internal topics,
// which are on neither copy of the link.
func TestPlaceTopics_InternalTopicSelectedIsNamed(t *testing.T) {
	f := neverReversedFacts()
	f.SourceTopics = append(f.SourceTopics, "_confluent-command")
	tvs, _ := place(t, reversibleInput("cc"), f, "_confluent-command", "a", "b")
	got := verdictsOf(tvs)
	if got["_confluent-command"] != FailFast || got["a"] != Migratable {
		t.Fatalf("verdicts = %v", got)
	}
	for _, tv := range tvs {
		if tv.Topic == "_confluent-command" && tv.Reason != "_confluent-command is not a mirror on the cluster link on either cluster" {
			t.Errorf("reason = %q", tv.Reason)
		}
	}
}

func TestCheckSelectionCoversLink_NamesUncovered(t *testing.T) {
	link := map[string]struct{}{"a": {}, "b": {}, "c": {}}
	r := checkSelectionCoversLink([]string{"a"}, link)
	if r.OK || r.Name != rsCoverageCheckName || !strings.Contains(r.Detail, "b, c") {
		t.Errorf("got %+v, want a failure naming b, c", r)
	}
	if r := checkSelectionCoversLink([]string{"a", "b", "c", "d"}, link); !r.OK {
		t.Errorf("a selection covering the link passes: %+v", r)
	}
}

func TestLinkTopicSet_UnionOfBothCopies(t *testing.T) {
	f := placedFacts(reversibleGateway("cp", false), map[string]MirrorRow{"x": active}, map[string]MirrorRow{"a": active}, "a", "x")
	if got := linkTopicSet(f); !reflect.DeepEqual(got, map[string]struct{}{"a": {}, "x": {}}) {
		t.Errorf("linkTopicSet = %v", got)
	}
}
