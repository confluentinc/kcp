package reconcile

import (
	"strings"
	"testing"
)

// linkView routes orders and payments to the destination (cc) and everything
// else to the source (msk), as a finished topic-based migration batch leaves it.
func linkView() RouteView {
	return RouteView{
		Conditions: []Condition{
			{Topics: []string{"orders", "payments"}, Domain: "cc"},
			{TopicPatterns: []string{".*"}, Domain: "msk"},
		},
		SourceDomain: "msk",
		TargetDomain: "cc",
	}
}

func promoted(topic string) LinkMirror {
	return LinkMirror{SourceTopic: topic, MirrorTopic: topic, State: MirrorStopped, Status: "STOPPED"}
}

// linkInput is a converged link: orders and payments promoted, on both
// clusters with 3 partitions each, routed to the destination, and one group
// committing on both.
func linkInput() LinkScopeInput {
	return LinkScopeInput{
		Mirrors:      []LinkMirror{promoted("orders"), promoted("payments")},
		SourceTopics: []string{"orders", "payments"},
		TargetTopics: []string{"orders", "payments"},
		Partitions: PartitionCounts{
			Source: map[string]int{"orders": 3, "payments": 3},
			Target: map[string]int{"orders": 3, "payments": 3},
		},
		View:            linkView(),
		CommittedTopics: map[string][]string{"orders-app": {"orders", "payments"}},
		TargetStates:    map[string]string{},
	}
}

// onlyRefusal returns the single fail-fast verdict, failing unless there is
// exactly one and it is for topic.
func onlyRefusal(t *testing.T, failFast []TopicVerdict, topic string) TopicVerdict {
	t.Helper()
	if len(failFast) != 1 || failFast[0].Topic != topic {
		t.Fatalf("FailFast = %+v, want exactly %s", failFast, topic)
	}
	return failFast[0]
}

func TestCheckLinkTopics_ConvergedLinkPasses(t *testing.T) {
	unchanged, failFast := checkLinkTopics(linkInput())
	if len(failFast) != 0 {
		t.Fatalf("FailFast = %+v, want none", failFast)
	}
	if len(unchanged) != 2 || unchanged[0].Topic != "orders" || unchanged[1].Topic != "payments" {
		t.Fatalf("Unchanged = %+v, want orders then payments", unchanged)
	}
}

func TestCheckLinkTopics_RefusesAMirrorThatIsNotPromoted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  MirrorState
		status string
		want   []string
	}{
		{"active", MirrorActive, "ACTIVE", []string{"ACTIVE", "topic-based migration batch", "promote it if nothing uses it"}},
		{"pending", MirrorPending, "PENDING_STOPPED", []string{"PENDING_STOPPED", "wait for it to reach STOPPED"}},
		{"failed", MirrorBad, "FAILED", []string{"FAILED", "fix the mirror", "delete the destination topic"}},
		{"paused", MirrorBad, "PAUSED", []string{"PAUSED", "fix the mirror"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := linkInput()
			in.Mirrors[0].State, in.Mirrors[0].Status = tc.state, tc.status

			_, failFast := checkLinkTopics(in)

			tv := onlyRefusal(t, failFast, "orders")
			for _, want := range tc.want {
				if !strings.Contains(tv.Reason, want) {
					t.Errorf("reason = %q, want it to contain %q", tv.Reason, want)
				}
			}
		})
	}
}

func TestCheckLinkTopics_RefusesAPrefixedMirror(t *testing.T) {
	in := linkInput()
	in.Mirrors[0].MirrorTopic = "pfx.orders"
	in.TargetTopics = []string{"pfx.orders", "payments"}
	in.Partitions.Target = map[string]int{"pfx.orders": 3, "payments": 3}

	_, failFast := checkLinkTopics(in)

	tv := onlyRefusal(t, failFast, "orders")
	if !strings.Contains(tv.Reason, `"pfx.orders"`) || !strings.Contains(tv.Reason, "cluster.link.prefix") {
		t.Errorf("reason = %q, want it to name the mirror and cluster.link.prefix", tv.Reason)
	}
}

func TestCheckLinkTopics_RefusesALinkTopicMissingOnTheDestination(t *testing.T) {
	in := linkInput()
	in.TargetTopics = []string{"payments"}
	delete(in.Partitions.Target, "orders")

	_, failFast := checkLinkTopics(in)

	tv := onlyRefusal(t, failFast, "orders")
	if !strings.Contains(tv.Reason, "not on the destination") {
		t.Errorf("reason = %q, want it to say the topic is not on the destination", tv.Reason)
	}
	if strings.Contains(tv.Reason, "partition") {
		t.Errorf("reason = %q: a topic missing on the destination must not also report a partition problem", tv.Reason)
	}
}

func TestCheckLinkTopics_RefusesALinkTopicTheRouteStillSendsToTheSource(t *testing.T) {
	in := linkInput()
	in.View.Conditions[0].Topics = []string{"payments"} // orders falls through to msk

	_, failFast := checkLinkTopics(in)

	tv := onlyRefusal(t, failFast, "orders")
	for _, want := range []string{`"msk"`, `"cc"`, "topic-based migration batch"} {
		if !strings.Contains(tv.Reason, want) {
			t.Errorf("reason = %q, want it to contain %q", tv.Reason, want)
		}
	}
	if tv.R != "->source" {
		t.Errorf("R = %q, want ->source", tv.R)
	}
}

func TestCheckLinkTopics_RoutedByAPatternCountsAsRouted(t *testing.T) {
	in := linkInput()
	in.View.Conditions[0] = Condition{TopicPatterns: []string{"orders|payments"}, Domain: "cc"}

	if _, failFast := checkLinkTopics(in); len(failFast) != 0 {
		t.Fatalf("FailFast = %+v, want none: a pattern that sends the topic to the destination routes it there", failFast)
	}
}

func TestCheckLinkTopics_RefusesMorePartitionsOnTheDestination(t *testing.T) {
	in := linkInput()
	in.Partitions.Target["orders"] = 6

	_, failFast := checkLinkTopics(in)

	tv := onlyRefusal(t, failFast, "orders")
	for _, want := range []string{"6 partitions on the destination but 3 on the source copy", "orders-app", "Add partitions to the source copy", "auto.offset.reset"} {
		if !strings.Contains(tv.Reason, want) {
			t.Errorf("reason = %q, want it to contain %q", tv.Reason, want)
		}
	}
}

func TestCheckLinkTopics_RefusesFewerPartitionsOnTheDestination(t *testing.T) {
	in := linkInput()
	in.Partitions.Target["orders"] = 2

	_, failFast := checkLinkTopics(in)

	tv := onlyRefusal(t, failFast, "orders")
	if !strings.Contains(tv.Reason, "2 partitions on the destination but 3 on the source copy") || !strings.Contains(tv.Reason, "inconsistent") {
		t.Errorf("reason = %q, want both counts and that the link is inconsistent", tv.Reason)
	}
}

func TestCheckLinkTopics_SourceCopyDeletedSkipsThePartitionRow(t *testing.T) {
	in := linkInput()
	in.SourceTopics = []string{"payments"}
	delete(in.Partitions.Source, "orders")

	unchanged, failFast := checkLinkTopics(in)

	if len(failFast) != 0 {
		t.Fatalf("FailFast = %+v, want none: deleting the source copy purged its offsets, so there is nothing to compare", failFast)
	}
	if unchanged[0].Topic != "orders" || unchanged[0].S != "absent" {
		t.Errorf("Unchanged[0] = %+v, want orders with S=absent", unchanged[0])
	}
}

func TestCheckLinkTopics_RefusesAnUnreadablePartitionCount(t *testing.T) {
	for name, mutate := range map[string]func(*LinkScopeInput){
		"destination": func(in *LinkScopeInput) { delete(in.Partitions.Target, "orders") },
		"source":      func(in *LinkScopeInput) { delete(in.Partitions.Source, "orders") },
	} {
		t.Run(name, func(t *testing.T) {
			in := linkInput()
			mutate(&in)

			_, failFast := checkLinkTopics(in)

			tv := onlyRefusal(t, failFast, "orders")
			if !strings.Contains(tv.Reason, "could not be read") {
				t.Errorf("reason = %q, want it to say the count could not be read", tv.Reason)
			}
		})
	}
}

func TestCheckLinkTopics_ReportsEveryProblemWithATopicInOneVerdict(t *testing.T) {
	in := linkInput()
	in.Mirrors[0].State, in.Mirrors[0].Status = MirrorActive, "ACTIVE"
	in.View.Conditions[0].Topics = []string{"payments"}

	_, failFast := checkLinkTopics(in)

	tv := onlyRefusal(t, failFast, "orders")
	if !strings.Contains(tv.Reason, "ACTIVE") || !strings.Contains(tv.Reason, `"msk"`) {
		t.Errorf("reason = %q, want both the mirror state and the route in one verdict", tv.Reason)
	}
}

func TestCheckLinkTopics_VerdictsAreSortedBySourceName(t *testing.T) {
	in := linkInput()
	in.Mirrors = []LinkMirror{promoted("payments"), promoted("orders")}

	unchanged, _ := checkLinkTopics(in)

	if len(unchanged) != 2 || unchanged[0].Topic != "orders" {
		t.Fatalf("Unchanged = %+v, want orders first", unchanged)
	}
}
