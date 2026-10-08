package d2s

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"

	"github.com/confluentinc/kcp/internal/services/groupoffsets"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/types"
)

func testPolicy() Policy {
	return Policy{DetectUnroutedCommitsDuration: 10 * time.Second, Workers: 2}
}

func quietReporter() *reporter { return &reporter{out: io.Discard, err: io.Discard} }

// convertInput is spec.route of a conversion manifest for testGatewayYAML's route.
func convertInput() reconcile.ReconcileInput {
	return reconcile.ReconcileInput{Route: "migration-route", TargetDomain: "target", ConvertTo: "static"}
}

// fencedRoute is testGatewayYAML's route as GatherConvertFacts reads it after the fence: dynamic, bound to
// source and target, coordination pinned to source, orders routed to target, kcp's convert fence in
// rules.fencing.
func fencedRoute() *reconcile.GatewayConfig {
	rules := map[string]any{
		"routing": map[string]any{
			"coordination": map[string]any{"group": "source"},
			"default":      "source",
			"conditions":   []any{map[string]any{"topics": []any{"orders"}, "streamingDomain": "target"}},
		},
		"fencing": []any{map[string]any{"topicPatterns": []any{".*"}, "blocked": true}},
	}
	return &reconcile.GatewayConfig{Route: &reconcile.RouteConfig{
		Name:         "migration-route",
		Mode:         "dynamic",
		BoundDomains: []string{"source", "target"},
		Rules:        rules,
		Raw:          map[string]any{"name": "migration-route", "rules": rules},
	}}
}

// cleanFacts is a post-fence world with nothing wrong: one promoted link topic, orders, unprefixed, on both
// clusters with 3 partitions, routed to the destination; every probe allowed; no destination group.
func cleanFacts() *migplan.ConvertFacts {
	return &migplan.ConvertFacts{
		Gateway:      fencedRoute(),
		SourceTopics: []string{"orders"},
		TargetTopics: []string{"orders"},
		Link: &migplan.LinkStatus{
			LinkMirrors: []reconcile.LinkMirror{{SourceTopic: "orders", MirrorTopic: "orders", State: reconcile.MirrorStopped, Status: "STOPPED"}},
		},
		SourceCanDescribeGroups: true,
		TargetCanDescribeGroups: true,
		SourceCanDescribeTopics: true,
		TargetCanDescribeTopics: true,
		TargetCanCommitOffsets:  true,
		Partitions:              reconcile.PartitionCounts{Source: map[string]int{"orders": 3}, Target: map[string]int{"orders": 3}},
	}
}

// fakeSource is the source cluster's consumer groups. The i-th listing returns listings[i] (the last one once
// they run out). A fetch made after the n-th listing returns rounds[n-1] (the last round once they run out):
// verify_fence lists before each snapshot, so rounds[0] is snapshot 1 and rounds[1] snapshot 2.
type fakeSource struct {
	mu       sync.Mutex
	listings [][]string
	rounds   []groupoffsets.Snapshot
	listErr  error
	fetchErr error
	lists    int
	fetches  int
	builds   int
}

func (s *fakeSource) listGroups(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	i := s.lists
	if i >= len(s.listings) {
		i = len(s.listings) - 1
	}
	s.lists++
	return append([]string(nil), s.listings[i]...), nil
}

func (s *fakeSource) fetchers() groupoffsets.FetcherFactory {
	return func() (groupoffsets.GroupOffsetFetcher, func(), error) {
		s.mu.Lock()
		s.builds++
		s.mu.Unlock()
		return s, nil, nil
	}
}

func (s *fakeSource) CommittedOffsets(group string) (groupoffsets.Offsets, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fetchErr != nil {
		return nil, s.fetchErr
	}
	s.fetches++
	r := s.lists
	if r > len(s.rounds) {
		r = len(s.rounds)
	}
	if r < 1 {
		r = 1
	}
	return copyOffsets(s.rounds[r-1][group]), nil
}

func copyOffsets(o groupoffsets.Offsets) groupoffsets.Offsets {
	if o == nil {
		return nil
	}
	out := groupoffsets.Offsets{}
	for t, parts := range o {
		out[t] = map[int32]groupoffsets.CommittedOffset{}
		for p, c := range parts {
			out[t][p] = c
		}
	}
	return out
}

// fakeDestination is the destination cluster: its group listing, its topic listing, the high-water-mark
// sweep and the committers. It records every commit.
type fakeDestination struct {
	mu         sync.Mutex
	groups     []types.ConsumerGroupListing
	groupsErr  error
	groupLists int
	topics     []string
	topicsErr  error
	topicLists int
	hwm        map[string]map[int32]int64
	sweepErr   error
	sweeps     int
	commitErr  map[string]error
	commits    map[string]groupoffsets.Offsets
	builds     int
}

func (d *fakeDestination) listGroups(context.Context) ([]types.ConsumerGroupListing, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.groupLists++
	return d.groups, d.groupsErr
}

func (d *fakeDestination) listTopics(context.Context) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.topicLists++
	return d.topics, d.topicsErr
}

// GetMany is the high-water-mark sweep (offset.Provider).
func (d *fakeDestination) GetMany(_ context.Context, topics []string) (map[string]map[int32]int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sweeps++
	if d.sweepErr != nil {
		return nil, d.sweepErr
	}
	out := map[string]map[int32]int64{}
	for _, t := range topics {
		if parts, ok := d.hwm[t]; ok {
			out[t] = parts
		}
	}
	return out, nil
}

func (d *fakeDestination) committers() groupoffsets.CommitterFactory {
	return func() (groupoffsets.GroupCommitter, func(), error) {
		d.mu.Lock()
		d.builds++
		d.mu.Unlock()
		return d, nil, nil
	}
}

func (d *fakeDestination) CommitGroupOffsets(group string, offsets groupoffsets.Offsets) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.commitErr[group]; err != nil {
		return err
	}
	if d.commits == nil {
		d.commits = map[string]groupoffsets.Offsets{}
	}
	d.commits[group] = offsets
	return nil
}

// world is everything a conversion reads and writes besides the gateway. A test edits its fields before the
// run; deps() reads them at call time.
type world struct {
	src       *fakeSource
	dst       *fakeDestination
	facts     *migplan.ConvertFacts
	gatherErr error
	// gather, when set, replaces facts and gatherErr.
	gather   func(ctx context.Context) (*migplan.ConvertFacts, error)
	gathers  int
	waits    []time.Duration
	rendered bytes.Buffer
}

// cleanWorld is a conversion with nothing wrong: one in-scope group, orders-app, committed on orders
// partitions 0 and 1 (the same in both snapshots), and a destination that has orders with room for both.
func cleanWorld() *world {
	return &world{
		src: &fakeSource{
			listings: [][]string{{"orders-app"}},
			rounds: []groupoffsets.Snapshot{{
				"orders-app": {"orders": {0: {Offset: 5}, 1: {Offset: 7, Metadata: "stream-time=1"}}},
			}},
		},
		dst: &fakeDestination{
			topics: []string{"orders"},
			hwm:    map[string]map[int32]int64{"orders": {0: 10, 1: 10, 2: 10}},
		},
		facts: cleanFacts(),
	}
}

func (w *world) deps() Dependencies {
	return Dependencies{
		Input: convertInput(),
		Gather: func(ctx context.Context) (*migplan.ConvertFacts, error) {
			w.gathers++
			if w.gather != nil {
				return w.gather(ctx)
			}
			return w.facts, w.gatherErr
		},
		SourceGroups:      w.src.listGroups,
		DestinationGroups: w.dst.listGroups,
		Fetchers:          w.src.fetchers(),
		Wait: func(_ context.Context, d time.Duration) error {
			w.waits = append(w.waits, d)
			return nil
		},
		DestinationTopics: w.dst.listTopics,
		HighWaterMarks:    w.dst,
		Committers:        w.dst.committers(),
		Out:               &w.rendered,
	}
}
