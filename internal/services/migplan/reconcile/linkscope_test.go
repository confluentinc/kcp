package reconcile

import (
	"fmt"
	"reflect"
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

func scopePrecondition(t *testing.T, s LinkScope, name string) PreconditionResult {
	t.Helper()
	for _, p := range s.Preconditions {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no precondition %q in %+v", name, s.Preconditions)
	return PreconditionResult{}
}

func TestCheckLinkScope_GroupOnLinkTopicsIsInScope(t *testing.T) {
	s := CheckLinkScope(linkInput())

	if s.Refused() {
		t.Fatalf("got %+v, want no refusal", s)
	}
	if !reflect.DeepEqual(s.InScopeGroups, []string{"orders-app"}) {
		t.Errorf("InScopeGroups = %v, want [orders-app]", s.InScopeGroups)
	}
	if pc := scopePrecondition(t, s, GroupScopeCheckName); !pc.OK {
		t.Errorf("group rule = %+v, want a pass", pc)
	}
}

func TestCheckLinkScope_RefusesAGroupWithACommitOutsideTheLink(t *testing.T) {
	for name, topics := range map[string][]string{
		"alongside link topics": {"orders", "refunds"},
		"outside only":          {"refunds"},
	} {
		t.Run(name, func(t *testing.T) {
			in := linkInput()
			in.SourceTopics = append(in.SourceTopics, "refunds")
			in.CommittedTopics["refunds-app"] = topics

			s := CheckLinkScope(in)

			pc := scopePrecondition(t, s, GroupScopeCheckName)
			if pc.OK {
				t.Fatal("a group committing on a topic outside the link must refuse")
			}
			for _, want := range []string{"refunds-app (refunds)", "add it to the link", "--delete-offsets", "stop or move it"} {
				if !strings.Contains(pc.Detail, want) {
					t.Errorf("detail = %q, want it to contain %q", pc.Detail, want)
				}
			}
			if strings.Contains(pc.Detail, "orders-app") {
				t.Errorf("detail = %q must not name a group whose commits are all on the link", pc.Detail)
			}
			if s.InScopeGroups != nil {
				t.Errorf("InScopeGroups = %v, want none on a refusal", s.InScopeGroups)
			}
		})
	}
}

func TestCheckLinkScope_GroupWithNoCommitsIsNotTracked(t *testing.T) {
	in := linkInput()
	in.CommittedTopics["idle-app"] = nil

	s := CheckLinkScope(in)

	if s.Refused() || !reflect.DeepEqual(s.InScopeGroups, []string{"orders-app"}) {
		t.Fatalf("got %+v, want no refusal and only orders-app in scope", s)
	}
}

func TestCheckLinkScope_TopicRefusalStopsBeforeTheGroupRule(t *testing.T) {
	in := linkInput()
	in.Mirrors[0].State, in.Mirrors[0].Status = MirrorActive, "ACTIVE"
	in.CommittedTopics["refunds-app"] = []string{"refunds"}

	s := CheckLinkScope(in)

	if !s.Refused() || len(s.FailFast) != 1 {
		t.Fatalf("got %+v, want the topic refusal", s)
	}
	for _, pc := range s.Preconditions {
		if pc.Name == GroupScopeCheckName || pc.Name == GroupSplitBrainCheckName {
			t.Errorf("precondition %q ran: the group stages run only once every link topic passes", pc.Name)
		}
	}
}

func TestCheckLinkScope_SplitBrainCoversInScopeGroupsOnly(t *testing.T) {
	in := linkInput()
	in.TargetStates = map[string]string{"orders-app": "Stable"}
	if pc := scopePrecondition(t, CheckLinkScope(in), GroupSplitBrainCheckName); pc.OK {
		t.Error("an in-scope group active on the destination must refuse")
	}

	in = linkInput()
	in.CommittedTopics["idle-app"] = nil
	in.TargetStates = map[string]string{"idle-app": "Stable"}
	if pc := scopePrecondition(t, CheckLinkScope(in), GroupSplitBrainCheckName); !pc.OK {
		t.Errorf("split-brain = %+v, want a pass: a group with no commits is not in scope", pc)
	}
}

func TestCheckLinkScope_IdleInScopeGroupOnTheDestinationWarns(t *testing.T) {
	in := linkInput()
	in.TargetStates = map[string]string{"orders-app": "Empty"}

	s := CheckLinkScope(in)

	if s.Refused() || len(s.Warnings) != 1 || !strings.Contains(s.Warnings[0], "orders-app") {
		t.Fatalf("got %+v, want no refusal and one warning naming orders-app", s)
	}
}

func TestCheckLinkScope_WarnsAboutUntrackedTopicsOffTheLink(t *testing.T) {
	in := linkInput()
	in.SourceTopics = append(in.SourceTopics, "_schemas", "clicks")

	s := CheckLinkScope(in)

	if s.Refused() {
		t.Fatalf("got %+v, want no refusal", s)
	}
	if !strings.HasPrefix(s.UntrackedTopicsWarning, "topic(s) clicks are not on the cluster link") {
		t.Fatalf("UntrackedTopicsWarning = %q, want it to lead with the ordinary topic clicks", s.UntrackedTopicsWarning)
	}
	if !strings.HasSuffix(s.UntrackedTopicsWarning, "Also not on the link, and probably internal (name starts with '_'): _schemas") {
		t.Fatalf("UntrackedTopicsWarning = %q, want _schemas listed separately as probably internal", s.UntrackedTopicsWarning)
	}
	if len(s.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want the untracked warning kept out of it", s.Warnings)
	}
}

// Decision S6 holds: a topic named like an internal one is still listed, never dropped, even
// when it is the only untracked topic.
func TestCheckLinkScope_UntrackedInternalTopicsAloneStillWarn(t *testing.T) {
	in := linkInput()
	in.SourceTopics = append(in.SourceTopics, "_confluent-metrics", "_confluent-command")

	s := CheckLinkScope(in)

	want := "topic(s) _confluent-command, _confluent-metrics are not on the cluster link and are probably internal (name starts with '_'); no source consumer group has committed offsets on them"
	if !strings.HasPrefix(s.UntrackedTopicsWarning, want) {
		t.Fatalf("UntrackedTopicsWarning = %q, want prefix %q", s.UntrackedTopicsWarning, want)
	}
	if !strings.Contains(s.UntrackedTopicsWarning, "migrate them first if anything on this route still reads or writes them") {
		t.Fatalf("UntrackedTopicsWarning = %q, want the migrate-first guidance kept", s.UntrackedTopicsWarning)
	}
}

func TestCheckLinkScope_CapsTheOutsideTopicsPerGroup(t *testing.T) {
	in := linkInput()
	var off []string
	for i := 0; i < 12; i++ {
		off = append(off, fmt.Sprintf("off-%02d", i))
	}
	in.CommittedTopics["wide-app"] = off

	s := CheckLinkScope(in)

	pc := scopePrecondition(t, s, GroupScopeCheckName)
	if pc.OK || !strings.Contains(pc.Detail, "and 2 more") {
		t.Fatalf("group scope = %+v, want a refusal whose detail contains \"and 2 more\"", pc)
	}
}

func TestCheckLinkScope_LinkTopicNobodyCommitsOnIsSilent(t *testing.T) {
	in := linkInput()
	in.CommittedTopics = map[string][]string{}

	s := CheckLinkScope(in)

	if s.Refused() || len(s.Warnings) != 0 || s.UntrackedTopicsWarning != "" || len(s.InScopeGroups) != 0 {
		t.Fatalf("got %+v, want a clean result with nothing in scope", s)
	}
}

// The topics in scope are the promoted topics: with none, nothing was migrated
// and every other rule would pass vacuously, so the conversion refuses — even
// with no group commits at all.
func TestCheckLinkScope_RefusesALinkWithNoPromotedTopic(t *testing.T) {
	for name, mirrors := range map[string][]LinkMirror{
		"empty link": nil,
		"nothing promoted": {
			{SourceTopic: "orders", MirrorTopic: "orders", State: MirrorActive, Status: "ACTIVE"},
			{SourceTopic: "payments", MirrorTopic: "payments", State: MirrorActive, Status: "ACTIVE"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := linkInput()
			in.Mirrors = mirrors
			in.CommittedTopics = map[string][]string{}

			s := CheckLinkScope(in)

			pc := scopePrecondition(t, s, PromotedTopicsCheckName)
			if pc.OK || !strings.Contains(pc.Detail, "migrate and promote") {
				t.Fatalf("promoted-topics check = %+v, want a refusal telling the operator to migrate and promote first", pc)
			}
			if s.InScopeGroups != nil {
				t.Errorf("InScopeGroups = %v, want none", s.InScopeGroups)
			}
		})
	}
}

func TestCheckLinkScope_OnePromotedTopicPassesThePromotedCheck(t *testing.T) {
	if pc := scopePrecondition(t, CheckLinkScope(linkInput()), PromotedTopicsCheckName); !pc.OK {
		t.Errorf("promoted-topics check = %+v, want a pass", pc)
	}
}

func TestCheckLinkScope_ManyOffendingGroupsAreCapped(t *testing.T) {
	in := linkInput()
	in.SourceTopics = append(in.SourceTopics, "refunds")
	for i := 0; i < 25; i++ {
		in.CommittedTopics[fmt.Sprintf("app-%02d", i)] = []string{"refunds"}
	}

	pc := scopePrecondition(t, CheckLinkScope(in), GroupScopeCheckName)

	if pc.OK || !strings.Contains(pc.Detail, "and 5 more") || strings.Contains(pc.Detail, "app-24") {
		t.Errorf("detail = %q, want 20 groups named and \"and 5 more\"", pc.Detail)
	}
}
