package grouping

import (
	"reflect"
	"testing"
)

func TestBuild_PRFAQSample(t *testing.T) {
	// From the design doc / PRFAQ:
	//   Client A: t1,t2,t3   Client B: t2,t4   Client C: t5,t6
	// A and B share t2 -> merge into {t1,t2,t3,t4}; C stays {t5,t6}.
	res := Build([]Transaction{
		{ID: "A", Topics: []string{"t1", "t2", "t3"}},
		{ID: "B", Topics: []string{"t2", "t4"}},
		{ID: "C", Topics: []string{"t5", "t6"}},
	}, Options{})

	if len(res.Groups) != 2 {
		t.Fatalf("want 2 groups, got %d: %+v", len(res.Groups), res.Groups)
	}
	if got := res.Groups[0].Topics; !reflect.DeepEqual(got, []string{"t1", "t2", "t3", "t4"}) {
		t.Errorf("group 1 topics = %v", got)
	}
	if got := res.Groups[1].Topics; !reflect.DeepEqual(got, []string{"t5", "t6"}) {
		t.Errorf("group 2 topics = %v", got)
	}
	if len(res.IndividualTopics) != 0 {
		t.Errorf("want no individual topics, got %v", res.IndividualTopics)
	}
}

func TestBuild_TransitiveMerge(t *testing.T) {
	// a-b, b-c, c-d across separate transactions collapse into one group.
	res := Build([]Transaction{
		{ID: "1", Topics: []string{"a", "b"}},
		{ID: "2", Topics: []string{"b", "c"}},
		{ID: "3", Topics: []string{"c", "d"}},
	}, Options{})

	if len(res.Groups) != 1 {
		t.Fatalf("want 1 group, got %d: %+v", len(res.Groups), res.Groups)
	}
	if got := res.Groups[0].Topics; !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("group topics = %v", got)
	}
	if len(res.Groups[0].TxnIDs) != 3 {
		t.Errorf("want 3 contributing txns, got %v", res.Groups[0].TxnIDs)
	}
}

func TestBuild_DropsInternalTopics_PreventsChaining(t *testing.T) {
	// Two unrelated EOS apps both commit offsets, so both footprints include
	// __consumer_offsets. If we didn't drop it, transitive closure would chain them
	// into one bogus group.
	res := Build([]Transaction{
		{ID: "app1", Topics: []string{"orders", "__consumer_offsets"}, ReadProcessWrite: true},
		{ID: "app2", Topics: []string{"payments", "shipments", "__consumer_offsets"}, ReadProcessWrite: true},
	}, Options{})

	if len(res.Groups) != 1 {
		t.Fatalf("want 1 group (app2 only), got %d: %+v", len(res.Groups), res.Groups)
	}
	if got := res.Groups[0].Topics; !reflect.DeepEqual(got, []string{"payments", "shipments"}) {
		t.Errorf("group topics = %v", got)
	}
	if !contains(res.IndividualTopics, "orders") {
		t.Errorf("want 'orders' as an individual topic, got %v", res.IndividualTopics)
	}
	// The lone produced topic 'orders' is still a read-process-write app: its input
	// topics are unknown, so it must remain flagged even though it grouped alone.
	if !contains(res.ReadProcessWriteTopics, "orders") {
		t.Errorf("want 'orders' flagged read-process-write, got %v", res.ReadProcessWriteTopics)
	}
	if contains(res.ReadProcessWriteTopics, "__consumer_offsets") {
		t.Errorf("internal topic leaked into read-process-write topics: %v", res.ReadProcessWriteTopics)
	}
}

func TestBuild_IncludeInternalTopicsChainsEverything(t *testing.T) {
	// Guard rail proving WHY we drop internal topics: with the debug flag on, the
	// shared __consumer_offsets fuses the two apps into one group.
	res := Build([]Transaction{
		{ID: "app1", Topics: []string{"orders", "__consumer_offsets"}},
		{ID: "app2", Topics: []string{"payments", "__consumer_offsets"}},
	}, Options{IncludeInternalTopics: true})

	if len(res.Groups) != 1 || len(res.Groups[0].Topics) != 3 {
		t.Fatalf("want everything chained into 1 group of 3, got %+v", res.Groups)
	}
}

func TestIsInternalTopic(t *testing.T) {
	cases := map[string]bool{
		"__consumer_offsets":  true,
		"__transaction_state": true,
		"orders":              false,
		"_schemas":            false, // single underscore is not a Kafka-internal topic
	}
	for in, want := range cases {
		if got := IsInternalTopic(in); got != want {
			t.Errorf("IsInternalTopic(%q) = %v, want %v", in, got, want)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
