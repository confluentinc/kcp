package groupoffsets

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/confluentinc/kcp/internal/client"
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

// GroupOffsetFetcher reads one group's committed offsets (offsets >= 0 only).
type GroupOffsetFetcher interface {
	CommittedOffsets(group string) (Offsets, error)
}

// FetcherFactory returns a fetcher and the function that releases it (which may be nil). Each pool worker
// calls it once, so each worker owns its own client.
type FetcherFactory func() (GroupOffsetFetcher, func(), error)

// TakeSnapshot reads the committed offsets of every group in groups, on up to workers workers. Any failure
// fails the whole snapshot: a group silently skipped is a consumer left without its offset.
func TakeSnapshot(ctx context.Context, newFetcher FetcherFactory, groups []string, workers int) (Snapshot, error) {
	snap := Snapshot{}
	var mu sync.Mutex
	err := forEach(ctx, groups, workers, func() (worker, error) {
		f, release, err := newFetcher()
		if err != nil {
			return worker{}, err
		}
		return worker{
			do: func(group string) error {
				offsets, err := f.CommittedOffsets(group)
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
		return nil, fmt.Errorf("reading committed offsets: %w", err)
	}
	return snap, nil
}
