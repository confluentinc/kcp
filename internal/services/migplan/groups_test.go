package migplan

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/groupoffsets"
	"github.com/confluentinc/kcp/internal/types"
)

type fakeStrictClient struct {
	groups        []types.ConsumerGroupListing
	err           error
	canDescribe   bool
	canTopics     bool
	partitions    map[string]int
	partitionsFor []string
	canCommit     bool
}

func (f *fakeStrictClient) CanDescribeAnyGroup(context.Context) (bool, error) {
	return f.canDescribe, f.err
}

func (f *fakeStrictClient) CanDescribeAnyTopic(context.Context) (bool, error) {
	return f.canTopics, f.err
}

func (f *fakeStrictClient) ListGroupsAllBrokers(context.Context) ([]types.ConsumerGroupListing, error) {
	return f.groups, f.err
}

func TestKafkaGroupLister_UsesTheStrictListing(t *testing.T) {
	want := []types.ConsumerGroupListing{{GroupID: "g1", State: "Stable"}}
	got, err := NewKafkaGroupLister(&fakeStrictClient{groups: want}, nil, 0).ListGroups(context.Background())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ListGroups = %v, %v; want %v", got, err, want)
	}
	boom := errors.New("broker b2 failed")
	if _, err := NewKafkaGroupLister(&fakeStrictClient{err: boom}, nil, 0).ListGroups(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("ListGroups error = %v, want the strict listing's error", err)
	}
}

// offsetsFetcher is one worker's client: each group's committed offsets, or the error.
type offsetsFetcher struct {
	offsets map[string]groupoffsets.Offsets
	err     error
}

func (f offsetsFetcher) CommittedOffsets(_ context.Context, group string) (groupoffsets.Offsets, error) {
	return f.offsets[group], f.err
}

func TestKafkaGroupLister_CommittedTopicsReadsOnOneClientPerWorker(t *testing.T) {
	offsets := map[string]groupoffsets.Offsets{}
	groups := []string{"g1", "g2", "g3", "g4", "g5", "g6"}
	for _, g := range groups {
		offsets[g] = groupoffsets.Offsets{"orders": {0: {Offset: 1}}}
	}
	var built, released atomic.Int64
	fetchers := func() (groupoffsets.GroupOffsetFetcher, func(), error) {
		built.Add(1)
		return offsetsFetcher{offsets: offsets}, func() { released.Add(1) }, nil
	}

	got, err := NewKafkaGroupLister(&fakeStrictClient{}, fetchers, 3).CommittedTopics(context.Background(), groups)

	if err != nil || len(got) != 6 || !reflect.DeepEqual(got["g1"], []string{"orders"}) {
		t.Fatalf("CommittedTopics = %v, %v; want every group tracking orders", got, err)
	}
	if built.Load() != 3 || released.Load() != 3 {
		t.Fatalf("built %d clients and released %d, want one per worker (3): workers sharing a client wait on each other's connections", built.Load(), released.Load())
	}
	boom := errors.New("coordinator not available")
	failing := func() (groupoffsets.GroupOffsetFetcher, func(), error) { return offsetsFetcher{err: boom}, nil, nil }
	if _, err := NewKafkaGroupLister(&fakeStrictClient{}, failing, 3).CommittedTopics(context.Background(), groups); !errors.Is(err, boom) {
		t.Fatalf("CommittedTopics error = %v, want the fetch's error", err)
	}
}

func TestOffsetSyncWorkers_FollowsTheConversionsConcurrency(t *testing.T) {
	conversion := func(n int) *manifest.GatewayMigration {
		return &manifest.GatewayMigration{Spec: manifest.GatewaySpec{DynamicToStaticConversion: &manifest.DynamicToStaticConversion{
			Policies: manifest.DynamicToStaticPolicies{OffsetSyncConcurrency: n}}}}
	}
	if got := offsetSyncWorkers(conversion(16)); got != 16 {
		t.Fatalf("offsetSyncWorkers = %d, want the manifest's 16", got)
	}
	if got := offsetSyncWorkers(conversion(0)); got != manifest.DefaultOffsetSyncConcurrency {
		t.Fatalf("offsetSyncWorkers = %d, want the default %d when unset", got, manifest.DefaultOffsetSyncConcurrency)
	}
	if got := offsetSyncWorkers(&manifest.GatewayMigration{}); got != manifest.DefaultOffsetSyncConcurrency {
		t.Fatalf("offsetSyncWorkers = %d, want the default %d without a conversion section", got, manifest.DefaultOffsetSyncConcurrency)
	}
}

func TestKafkaGroupLister_CanDescribeAnyGroupUsesTheClient(t *testing.T) {
	got, err := NewKafkaGroupLister(&fakeStrictClient{canDescribe: true}, nil, 0).CanDescribeAnyGroup(context.Background())
	if err != nil || !got {
		t.Fatalf("CanDescribeAnyGroup = %v, %v; want true", got, err)
	}
	boom := errors.New("describe failed")
	if _, err := NewKafkaGroupLister(&fakeStrictClient{err: boom}, nil, 0).CanDescribeAnyGroup(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("CanDescribeAnyGroup error = %v, want the client's error", err)
	}
}

func TestKafkaGroupLister_CanDescribeAnyTopicUsesTheClient(t *testing.T) {
	got, err := NewKafkaGroupLister(&fakeStrictClient{canTopics: true}, nil, 0).CanDescribeAnyTopic(context.Background())
	if err != nil || !got {
		t.Fatalf("CanDescribeAnyTopic = %v, %v; want true", got, err)
	}
	boom := errors.New("probe failed")
	if _, err := NewKafkaGroupLister(&fakeStrictClient{err: boom}, nil, 0).CanDescribeAnyTopic(context.Background()); !errors.Is(err, boom) {
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
		[]types.ConsumerGroupListing{{GroupID: "b", Type: "classic", State: "Stable"}, {GroupID: "c", Type: "share", State: "Empty"}},
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
	if !reflect.DeepEqual(f.TargetStates, map[string]string{"b": "Stable", "c": "Empty"}) {
		t.Errorf("TargetStates = %v", f.TargetStates)
	}
	if !reflect.DeepEqual(f.TargetTypes, map[string]string{"b": "classic", "c": "share"}) {
		t.Errorf("TargetTypes = %v", f.TargetTypes)
	}
}

func (f *fakeStrictClient) PartitionCounts(_ context.Context, topics []string) (map[string]int, error) {
	f.partitionsFor = topics
	return f.partitions, f.err
}

func TestKafkaGroupLister_PartitionCountsUsesTheClient(t *testing.T) {
	c := &fakeStrictClient{partitions: map[string]int{"orders": 3}}
	got, err := NewKafkaGroupLister(c, nil, 0).PartitionCounts(context.Background(), []string{"orders"})
	if err != nil || !reflect.DeepEqual(got, map[string]int{"orders": 3}) {
		t.Fatalf("PartitionCounts = %v, %v; want orders=3", got, err)
	}
	if !reflect.DeepEqual(c.partitionsFor, []string{"orders"}) {
		t.Errorf("client asked about %v, want [orders]", c.partitionsFor)
	}
	boom := errors.New("metadata failed")
	if _, err := NewKafkaGroupLister(&fakeStrictClient{err: boom}, nil, 0).PartitionCounts(context.Background(), []string{"orders"}); !errors.Is(err, boom) {
		t.Fatalf("PartitionCounts error = %v, want the client's error", err)
	}
}

func (f *fakeStrictClient) CanCommitAnyOffsets(context.Context) (bool, error) {
	return f.canCommit, f.err
}

func TestKafkaGroupLister_CanCommitAnyOffsetsUsesTheClient(t *testing.T) {
	got, err := NewKafkaGroupLister(&fakeStrictClient{canCommit: true}, nil, 0).CanCommitAnyOffsets(context.Background())
	if err != nil || !got {
		t.Fatalf("CanCommitAnyOffsets = %v, %v; want true", got, err)
	}
	boom := errors.New("probe failed")
	if _, err := NewKafkaGroupLister(&fakeStrictClient{err: boom}, nil, 0).CanCommitAnyOffsets(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("CanCommitAnyOffsets error = %v, want the client's error", err)
	}
}

func TestCommitGap(t *testing.T) {
	if got := commitGap(true); got != "" {
		t.Errorf("allowed: gap = %q, want none", got)
	}
	denied := commitGap(false)
	for _, want := range []string{"destination credential", "READ on all consumer groups and all topics"} {
		if !strings.Contains(denied, want) {
			t.Errorf("denied: gap = %q, want it to contain %q", denied, want)
		}
	}
	// The check runs on the destination only, which is never MSK.
	if strings.Contains(denied, "MSK") {
		t.Errorf("denied: gap = %q must not give MSK IAM advice for the destination", denied)
	}
	if strings.Contains(denied, "DESCRIBE") {
		t.Errorf("denied: gap = %q must ask for READ, not DESCRIBE", denied)
	}
}

func TestGroupFacts_CarriesTheCommitGap(t *testing.T) {
	f := groupFacts(nil, nil, listingGaps{targetCommit: "tc"})
	if f.TargetCommitDenied != "tc" {
		t.Errorf("TargetCommitDenied = %q, want the gap carried", f.TargetCommitDenied)
	}
}
