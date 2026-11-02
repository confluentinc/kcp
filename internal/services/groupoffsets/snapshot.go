package groupoffsets

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
)

// CommittedOffset is one partition's committed offset and the metadata stored with it.
type CommittedOffset = client.CommittedOffset

// Offsets is topic -> partition -> committed offset (with its metadata). It is a type alias so that
// client.ConsumerGroupClient, whose CommittedOffsets returns the plain map type, satisfies GroupOffsetFetcher
// and GroupCommitter without a conversion.
type Offsets = map[string]map[int32]CommittedOffset

// Snapshot is every group's committed offsets: group -> topic -> partition -> committed offset. A group with
// no committed offsets is not in it.
type Snapshot map[string]Offsets

// Groups returns the groups in the snapshot, sorted.
func (s Snapshot) Groups() []string {
	out := make([]string, 0, len(s))
	for g := range s {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// Topics returns the union of the topics every group has committed on, sorted. Under the conversion's group
// rule these are all cluster-link topics.
func (s Snapshot) Topics() []string {
	seen := map[string]struct{}{}
	for _, topics := range s {
		for t := range topics {
			seen[t] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// CommittedTopics is, per group in the snapshot, the sorted topics it has committed on: the input of the
// conversion's group rule.
func (s Snapshot) CommittedTopics() map[string][]string {
	out := make(map[string][]string, len(s))
	for group, topics := range s {
		names := make([]string, 0, len(topics))
		for t := range topics {
			names = append(names, t)
		}
		sort.Strings(names)
		out[group] = names
	}
	return out
}

// GroupOffsetFetcher reads one group's committed offsets (offsets >= 0 only).
type GroupOffsetFetcher interface {
	CommittedOffsets(ctx context.Context, group string) (Offsets, error)
}

// FetcherFactory returns a fetcher and the function that releases it (which may be nil). Each pool worker
// calls it once, so each worker owns its own client.
type FetcherFactory func() (GroupOffsetFetcher, func(), error)

// TakeSnapshot reads the committed offsets of every group in groups, on up to workers workers. Any failure
// fails the whole snapshot: a group silently skipped is a consumer left without its offset.
func TakeSnapshot(ctx context.Context, newFetcher FetcherFactory, groups []string, workers int) (_ Snapshot, err error) {
	ctx, span := tracing.Start(ctx, tracerName, "groupoffsets.snapshot",
		attribute.Int("kcp.groups", len(groups)), attribute.Int("kcp.workers", workers))
	defer func() { tracing.End(span, err) }()
	snap, err := readOffsets(ctx, newFetcher, groups, workers)
	if err != nil {
		return nil, fmt.Errorf("reading committed offsets: %w", err)
	}
	span.SetAttributes(attribute.Int("kcp.groups.with_commits", len(snap)))
	return snap, nil
}

// CommittedTopics returns, for each group in groups that has committed an offset, the sorted topics it has
// committed on, read on up to workers workers as a snapshot is, each with its own client. It starts no
// groupoffsets.snapshot span: reconcile reads the topics under its own span, and a conversion's snapshots are
// the two its fence check takes. Any failure fails the read: a group skipped would leave its topics unchecked.
func CommittedTopics(ctx context.Context, newFetcher FetcherFactory, groups []string, workers int) (map[string][]string, error) {
	snap, err := readOffsets(ctx, newFetcher, groups, workers)
	if err != nil {
		return nil, err
	}
	return snap.CommittedTopics(), nil
}

// readOffsets reads the committed offsets of every group in groups on up to workers workers, each with the
// fetcher newFetcher builds for it. A group with no committed offsets is left out.
func readOffsets(ctx context.Context, newFetcher FetcherFactory, groups []string, workers int) (Snapshot, error) {
	snap := Snapshot{}
	var mu sync.Mutex
	err := forEach(ctx, groups, workers, func() (worker, error) {
		f, release, err := newFetcher()
		if err != nil {
			return worker{}, err
		}
		return worker{
			do: func(ctx context.Context, group string) error {
				offsets, err := f.CommittedOffsets(ctx, group)
				if err != nil {
					return err
				}
				if len(offsets) == 0 {
					return nil
				}
				mu.Lock()
				snap[group] = offsets
				mu.Unlock()
				return nil
			},
			release: release,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}
