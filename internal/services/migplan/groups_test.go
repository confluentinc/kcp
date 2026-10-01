package migplan

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
)

type fakeStrictClient struct {
	groups []types.ConsumerGroupListing
	err    error
}

func (f *fakeStrictClient) ListGroupsAllBrokers() ([]types.ConsumerGroupListing, error) {
	return f.groups, f.err
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

func TestGroupFacts(t *testing.T) {
	f := groupFacts(
		[]types.ConsumerGroupListing{{GroupID: "a"}, {GroupID: "b"}},
		[]types.ConsumerGroupListing{{GroupID: "b", State: "Stable"}, {GroupID: "c", State: "Empty"}},
	)
	if !reflect.DeepEqual(f.SourceGroups, []string{"a", "b"}) {
		t.Errorf("SourceGroups = %v", f.SourceGroups)
	}
	if !reflect.DeepEqual(f.TargetStates, map[string]string{"b": "Stable", "c": "Empty"}) {
		t.Errorf("TargetStates = %v", f.TargetStates)
	}
}
