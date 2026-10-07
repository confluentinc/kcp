package migplan

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
)

type fakeStrictClient struct {
	groups        []types.ConsumerGroupListing
	err           error
	tracked       map[string][]string
	canDescribe   bool
	canTopics     bool
	partitions    map[string]int
	partitionsFor []string
}

func (f *fakeStrictClient) CanDescribeAnyGroup() (bool, error) {
	return f.canDescribe, f.err
}

func (f *fakeStrictClient) CanDescribeAnyTopic() (bool, error) {
	return f.canTopics, f.err
}

func (f *fakeStrictClient) ListGroupsAllBrokers() ([]types.ConsumerGroupListing, error) {
	return f.groups, f.err
}

func (f *fakeStrictClient) CommittedTopics([]string) (map[string][]string, error) {
	return f.tracked, f.err
}

func TestKafkaGroupLister_UsesTheStrictListing(t *testing.T) {
	want := []types.ConsumerGroupListing{{GroupID: "g1", State: "Stable"}}
	got, err := NewKafkaGroupLister(&fakeStrictClient{groups: want}).ListGroups(context.Background())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ListGroups = %v, %v; want %v", got, err, want)
	}
	boom := errors.New("broker b2 failed")
	if _, err := NewKafkaGroupLister(&fakeStrictClient{err: boom}).ListGroups(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("ListGroups error = %v, want the strict listing's error", err)
	}
}

func TestKafkaGroupLister_CommittedTopicsUsesTheClient(t *testing.T) {
	want := map[string][]string{"g1": {"orders"}}
	got, err := NewKafkaGroupLister(&fakeStrictClient{tracked: want}).CommittedTopics(context.Background(), []string{"g1"})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("CommittedTopics = %v, %v; want %v", got, err, want)
	}
	boom := errors.New("coordinator not available")
	if _, err := NewKafkaGroupLister(&fakeStrictClient{err: boom}).CommittedTopics(context.Background(), []string{"g1"}); !errors.Is(err, boom) {
		t.Fatalf("CommittedTopics error = %v, want the client's error", err)
	}
}

func TestKafkaGroupLister_CanDescribeAnyGroupUsesTheClient(t *testing.T) {
	got, err := NewKafkaGroupLister(&fakeStrictClient{canDescribe: true}).CanDescribeAnyGroup(context.Background())
	if err != nil || !got {
		t.Fatalf("CanDescribeAnyGroup = %v, %v; want true", got, err)
	}
	boom := errors.New("describe failed")
	if _, err := NewKafkaGroupLister(&fakeStrictClient{err: boom}).CanDescribeAnyGroup(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("CanDescribeAnyGroup error = %v, want the client's error", err)
	}
}

func TestKafkaGroupLister_CanDescribeAnyTopicUsesTheClient(t *testing.T) {
	got, err := NewKafkaGroupLister(&fakeStrictClient{canTopics: true}).CanDescribeAnyTopic(context.Background())
	if err != nil || !got {
		t.Fatalf("CanDescribeAnyTopic = %v, %v; want true", got, err)
	}
	boom := errors.New("probe failed")
	if _, err := NewKafkaGroupLister(&fakeStrictClient{err: boom}).CanDescribeAnyTopic(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("CanDescribeAnyTopic error = %v, want the client's error", err)
	}
}

func TestTopicGap(t *testing.T) {
	for _, side := range []string{"source", "destination"} {
		t.Run(side, func(t *testing.T) {
			if got := topicGap(side, true); got != "" {
				t.Errorf("allowed: gap = %q, want none", got)
			}
			denied := topicGap(side, false)
			if !strings.Contains(denied, "the "+side+" credential") || !strings.Contains(denied, "topics") || !strings.Contains(denied, "DESCRIBE") {
				t.Errorf("denied: gap = %q, want it to name the %s credential and the missing topic DESCRIBE", denied, side)
			}
			if strings.Contains(denied, "consumer group") && !strings.Contains(denied, "topics") {
				t.Errorf("denied: gap = %q is about groups, not topics", denied)
			}
		})
	}
}

func TestListingGap(t *testing.T) {
	for _, side := range []string{"source", "destination"} {
		t.Run(side, func(t *testing.T) {
			if got := listingGap(side, true); got != "" {
				t.Errorf("allowed: gap = %q, want none", got)
			}
			denied := listingGap(side, false)
			if !strings.Contains(denied, "the "+side+" credential") || !strings.Contains(denied, "DESCRIBE") || !strings.Contains(denied, "group") {
				t.Errorf("denied: gap = %q, want it to name the %s credential and the missing group DESCRIBE", denied, side)
			}
			if strings.Contains(denied, "cluster") {
				t.Errorf("denied: gap = %q must not tell the operator to grant cluster DESCRIBE: that is not what makes a listing complete", denied)
			}
		})
	}
}

func TestGroupFacts(t *testing.T) {
	tracked := map[string][]string{"a": {"orders"}}
	f := groupFacts(
		[]types.ConsumerGroupListing{{GroupID: "a"}, {GroupID: "b"}},
		[]types.ConsumerGroupListing{{GroupID: "b", State: "Stable"}, {GroupID: "c", State: "Empty"}},
		tracked,
		listingGaps{sourceGroups: "sg", targetGroups: "tg", sourceTopics: "st", targetTopics: "tt"},
	)
	if f.SourceListingIncomplete != "sg" || f.TargetListingIncomplete != "tg" ||
		f.SourceTopicsIncomplete != "st" || f.TargetTopicsIncomplete != "tt" {
		t.Errorf("gaps = %q/%q/%q/%q, want all four carried to their own fields",
			f.SourceListingIncomplete, f.TargetListingIncomplete, f.SourceTopicsIncomplete, f.TargetTopicsIncomplete)
	}
	if !reflect.DeepEqual(f.TrackedTopics, tracked) {
		t.Errorf("TrackedTopics = %v, want %v", f.TrackedTopics, tracked)
	}
	if !reflect.DeepEqual(f.SourceGroups, []string{"a", "b"}) {
		t.Errorf("SourceGroups = %v", f.SourceGroups)
	}
	if !reflect.DeepEqual(f.TargetStates, map[string]string{"b": "Stable", "c": "Empty"}) {
		t.Errorf("TargetStates = %v", f.TargetStates)
	}
}

func (f *fakeStrictClient) PartitionCounts(topics []string) (map[string]int, error) {
	f.partitionsFor = topics
	return f.partitions, f.err
}

func TestKafkaGroupLister_PartitionCountsUsesTheClient(t *testing.T) {
	c := &fakeStrictClient{partitions: map[string]int{"orders": 3}}
	got, err := NewKafkaGroupLister(c).PartitionCounts(context.Background(), []string{"orders"})
	if err != nil || !reflect.DeepEqual(got, map[string]int{"orders": 3}) {
		t.Fatalf("PartitionCounts = %v, %v; want orders=3", got, err)
	}
	if !reflect.DeepEqual(c.partitionsFor, []string{"orders"}) {
		t.Errorf("client asked about %v, want [orders]", c.partitionsFor)
	}
	boom := errors.New("metadata failed")
	if _, err := NewKafkaGroupLister(&fakeStrictClient{err: boom}).PartitionCounts(context.Background(), []string{"orders"}); !errors.Is(err, boom) {
		t.Fatalf("PartitionCounts error = %v, want the client's error", err)
	}
}
