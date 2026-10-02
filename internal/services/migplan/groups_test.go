package migplan

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

type fakeStrictClient struct {
	groups  []types.ConsumerGroupListing
	err     error
	tracked map[string][]string
	access  client.DescribeAccess
}

func (f *fakeStrictClient) ClusterDescribeAccess() (client.DescribeAccess, error) {
	return f.access, f.err
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

func TestKafkaGroupLister_ClusterDescribeAccessUsesTheClient(t *testing.T) {
	got, err := NewKafkaGroupLister(&fakeStrictClient{access: client.DescribeDenied}).ClusterDescribeAccess(context.Background())
	if err != nil || got != client.DescribeDenied {
		t.Fatalf("ClusterDescribeAccess = %v, %v; want denied", got, err)
	}
	boom := errors.New("metadata failed")
	if _, err := NewKafkaGroupLister(&fakeStrictClient{err: boom}).ClusterDescribeAccess(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("ClusterDescribeAccess error = %v, want the client's error", err)
	}
}

func TestListingGap(t *testing.T) {
	for _, side := range []string{"source", "destination"} {
		t.Run(side, func(t *testing.T) {
			if got := listingGap(side, client.DescribeGranted); got != "" {
				t.Errorf("granted: gap = %q, want none", got)
			}
			denied := listingGap(side, client.DescribeDenied)
			if !strings.Contains(denied, "the "+side+" credential lacks DESCRIBE on the cluster") {
				t.Errorf("denied: gap = %q, want it to name the %s and the missing cluster DESCRIBE", denied, side)
			}
			unknown := listingGap(side, client.DescribeUnknown)
			if !strings.Contains(unknown, "the "+side+" credential") || !strings.Contains(unknown, "could not") {
				t.Errorf("unknown: gap = %q, want a refusal naming the %s and saying the check could not be made", unknown, side)
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
		"a source gap",
		"a destination gap",
	)
	if f.SourceListingIncomplete != "a source gap" || f.TargetListingIncomplete != "a destination gap" {
		t.Errorf("listing gaps = %q / %q, want both carried", f.SourceListingIncomplete, f.TargetListingIncomplete)
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
