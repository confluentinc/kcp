package migplan

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/types"
)

func TestGatherConvertFacts_GathersEverythingButTheSourceGroupsAndTheirCommits(t *testing.T) {
	src := &fakeGroupLister{groups: []types.ConsumerGroupListing{{GroupID: "orders-app"}}}
	tgt := &fakeGroupLister{groups: []types.ConsumerGroupListing{{GroupID: "dest-only", State: "Stable"}}}

	f, err := GatherConvertFacts(context.Background(), convertEngine(src, tgt).convertProviders())
	if err != nil {
		t.Fatal(err)
	}

	if f.Gateway == nil || f.Gateway.Route == nil || f.Gateway.Route.Name != "migration-route" {
		t.Errorf("Gateway = %+v, want the loaded route", f.Gateway)
	}
	if !reflect.DeepEqual(f.SourceTopics, []string{"orders"}) || !reflect.DeepEqual(f.TargetTopics, []string{"orders"}) {
		t.Errorf("topics = %v / %v, want [orders] on both", f.SourceTopics, f.TargetTopics)
	}
	if f.Link == nil || len(f.Link.LinkMirrors) != 1 {
		t.Errorf("Link = %+v, want the one link mirror", f.Link)
	}
	if !f.SourceCanDescribeGroups || !f.TargetCanDescribeGroups || !f.SourceCanDescribeTopics || !f.TargetCanDescribeTopics || !f.TargetCanCommitOffsets {
		t.Errorf("probes = %+v, want all five allowed", f)
	}
	if !reflect.DeepEqual(f.TargetGroupListing, tgt.groups) {
		t.Errorf("TargetGroupListing = %v, want the destination listing", f.TargetGroupListing)
	}
	want := reconcile.PartitionCounts{Source: map[string]int{"orders": 1}, Target: map[string]int{"orders": 1}}
	if !reflect.DeepEqual(f.Partitions, want) {
		t.Errorf("Partitions = %+v, want %+v", f.Partitions, want)
	}
	if src.calls != 0 {
		t.Errorf("source ListGroups called %d times; the gather leaves the source listing to its caller", src.calls)
	}
	if src.trackedN != 0 || tgt.trackedN != 0 {
		t.Errorf("CommittedTopics called %d/%d times; the gather never fetches committed offsets", src.trackedN, tgt.trackedN)
	}
	if src.commitN != 0 || tgt.commitN != 1 {
		t.Errorf("CanCommitAnyOffsets source/destination = %d/%d, want 0/1", src.commitN, tgt.commitN)
	}
}

func TestGatherConvertFacts_ADeniedProbeIsAFactNotAnError(t *testing.T) {
	tgt := &fakeGroupLister{commitDenied: true}
	f, err := GatherConvertFacts(context.Background(), convertEngine(&fakeGroupLister{}, tgt).convertProviders())
	if err != nil {
		t.Fatalf("GatherConvertFacts error = %v, want the denial as a fact", err)
	}
	if f.TargetCanCommitOffsets {
		t.Error("TargetCanCommitOffsets = true, want false")
	}
	if tgt.partitionsN != 1 {
		t.Errorf("partition counts read %d times, want 1: a commit denial is not a DESCRIBE denial", tgt.partitionsN)
	}
}

func TestGatherConvertFacts_SkipsPartitionCountsWhenADescribeProbeDenied(t *testing.T) {
	src := &fakeGroupLister{topicsDenied: true, partitionsErr: errors.New("TOPIC_AUTHORIZATION_FAILED")}
	tgt := &fakeGroupLister{}
	f, err := GatherConvertFacts(context.Background(), convertEngine(src, tgt).convertProviders())
	if err != nil {
		t.Fatalf("GatherConvertFacts error = %v, want the denial as a fact", err)
	}
	if src.partitionsN != 0 || tgt.partitionsN != 0 {
		t.Errorf("PartitionCounts called %d/%d times after a denied probe, want 0/0", src.partitionsN, tgt.partitionsN)
	}
	if f.SourceCanDescribeTopics || f.Partitions.Source != nil || f.Partitions.Target != nil {
		t.Errorf("facts = %+v, want the denial and no partition counts", f)
	}
	if tgt.commitN != 1 {
		t.Errorf("the offset-write probe ran %d times, want 1: every credential line is reported together", tgt.commitN)
	}
}

func TestGatherConvertFacts_ReadFailuresAreErrors(t *testing.T) {
	boom := errors.New("boom")
	ok := func() ConvertProviders {
		return convertEngine(&fakeGroupLister{}, &fakeGroupLister{}).convertProviders()
	}
	for name, p := range map[string]func() ConvertProviders{
		"gateway":            func() ConvertProviders { p := ok(); p.Gateway = &fakeGateway{err: boom}; return p },
		"source topics":      func() ConvertProviders { p := ok(); p.Source = &fakeLister{err: boom}; return p },
		"target topics":      func() ConvertProviders { p := ok(); p.Target = &fakeLister{err: boom}; return p },
		"link":               func() ConvertProviders { p := ok(); p.Link = &fakeLink{err: boom}; return p },
		"group probe":        func() ConvertProviders { p := ok(); p.SourceGroups = &fakeGroupLister{accessErr: boom}; return p },
		"topic probe":        func() ConvertProviders { p := ok(); p.TargetGroups = &fakeGroupLister{topicsErr: boom}; return p },
		"offset-write probe": func() ConvertProviders { p := ok(); p.TargetGroups = &fakeGroupLister{commitErr: boom}; return p },
		"destination groups": func() ConvertProviders { p := ok(); p.TargetGroups = &fakeGroupLister{err: boom}; return p },
		"partition counts":   func() ConvertProviders { p := ok(); p.TargetGroups = &fakeGroupLister{partitionsErr: boom}; return p },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := GatherConvertFacts(context.Background(), p()); !errors.Is(err, boom) {
				t.Fatalf("GatherConvertFacts error = %v, want the read failure as an error", err)
			}
		})
	}
}

func TestGatherConvertFacts_NeedsBothGroupListers(t *testing.T) {
	p := convertEngine(&fakeGroupLister{}, &fakeGroupLister{}).convertProviders()
	p.TargetGroups = nil
	if _, err := GatherConvertFacts(context.Background(), p); err == nil || !strings.Contains(err.Error(), "consumer-group listers") {
		t.Fatalf("GatherConvertFacts error = %v, want the missing-lister error", err)
	}
}

func TestConvertFacts_GroupFacts(t *testing.T) {
	f := &ConvertFacts{
		SourceCanDescribeGroups: true, TargetCanDescribeGroups: true,
		SourceCanDescribeTopics: true, TargetCanDescribeTopics: true,
		TargetCanCommitOffsets: false,
		TargetGroupListing:     []types.ConsumerGroupListing{{GroupID: "g", State: "Empty"}},
	}
	tracked := map[string][]string{"a": {"orders"}}

	gf := f.GroupFacts(tracked)

	if gf.SourceListingIncomplete != "" || gf.TargetListingIncomplete != "" || gf.SourceTopicsIncomplete != "" || gf.TargetTopicsIncomplete != "" {
		t.Errorf("visibility gaps = %+v, want none", gf)
	}
	if gf.TargetCommitDenied != commitGap(false) {
		t.Errorf("TargetCommitDenied = %q, want commitGap(false)", gf.TargetCommitDenied)
	}
	if !reflect.DeepEqual(gf.TrackedTopics, tracked) || !reflect.DeepEqual(gf.TargetStates, map[string]string{"g": "Empty"}) {
		t.Errorf("GroupFacts = %+v, want the tracked topics and the destination states", gf)
	}
}
