package groupoffsets

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ErrRogueCommits is returned by DetectRogueCommits when committed offsets did not stay put between two
// snapshots taken after the fence: a client committing to the source directly (bypassing the fenced gateway),
// commits leaking past a fence not yet enforced on every gateway pod, or an admin resetting or deleting
// offsets.
var ErrRogueCommits = errors.New("committed offsets changed after the fence")

// Change is one (group, topic, partition) whose committed offset is new, different, or gone between two
// snapshots.
type Change struct {
	Group     string
	Topic     string
	Partition int32
	Before    int64 // zero when New
	After     int64 // zero when Removed
	New       bool
	Removed   bool
}

// Changes lists every (group, topic, partition) that is new in after, has a different value, or is missing
// from after, sorted by group, topic, partition. Any difference counts, not only growth: an admin resetting a
// group to a lower offset changes what would be copied just as a commit does. A removal counts too — a group
// deleted or a partition's offset deleted or expired during the window — so that "no changes" means exactly
// that the source offsets stayed put, and syncing the second snapshot cannot silently drop a group.
func Changes(before, after Snapshot) []Change {
	var out []Change
	for g, topics := range after {
		for t, parts := range topics {
			for p, a := range parts {
				b, ok := before[g][t][p]
				switch {
				case !ok:
					out = append(out, Change{Group: g, Topic: t, Partition: p, After: a, New: true})
				case a != b:
					out = append(out, Change{Group: g, Topic: t, Partition: p, Before: b, After: a})
				}
			}
		}
	}
	for g, topics := range before {
		for t, parts := range topics {
			for p, b := range parts {
				if _, ok := after[g][t][p]; !ok {
					out = append(out, Change{Group: g, Topic: t, Partition: p, Before: b, Removed: true})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		if out[i].Topic != out[j].Topic {
			return out[i].Topic < out[j].Topic
		}
		return out[i].Partition < out[j].Partition
	})
	return out
}

// RogueCommitsError carries the changes DetectRogueCommits found.
type RogueCommitsError struct {
	Changes []Change
}

func (e *RogueCommitsError) Error() string {
	const show = 5
	var parts []string
	for i, c := range e.Changes {
		if i == show {
			break
		}
		switch {
		case c.New:
			parts = append(parts, fmt.Sprintf("group %s topic %s partition %d: new commit at %d", c.Group, c.Topic, c.Partition, c.After))
		case c.Removed:
			parts = append(parts, fmt.Sprintf("group %s topic %s partition %d: removed (was %d)", c.Group, c.Topic, c.Partition, c.Before))
		default:
			parts = append(parts, fmt.Sprintf("group %s topic %s partition %d: %d -> %d", c.Group, c.Topic, c.Partition, c.Before, c.After))
		}
	}
	msg := fmt.Sprintf("%s: %d partition(s) changed, e.g. %s; the source offsets did not stay put while the gateway was fenced — "+
		"a client committing to the source directly, bypassing the gateway, or an admin resetting or deleting offsets; "+
		"stop it, then re-run",
		ErrRogueCommits, len(e.Changes), strings.Join(parts, "; "))
	if len(e.Changes) > show {
		msg += fmt.Sprintf(" (and %d more)", len(e.Changes)-show)
	}
	return msg
}

// Is makes errors.Is(err, ErrRogueCommits) true for a RogueCommitsError.
func (e *RogueCommitsError) Is(target error) bool { return target == ErrRogueCommits }

// WaitFunc waits for d or until ctx ends. Injected so tests do not sleep.
type WaitFunc func(ctx context.Context, d time.Duration) error

// Sleep is the real WaitFunc.
func Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// GroupLister lists every consumer group on the source, from a fresh strict listing.
type GroupLister func(ctx context.Context) ([]string, error)

// DetectRogueCommits waits window, then lists the source groups afresh and takes the second snapshot itself
// on up to workers workers, and compares it with first. A fresh listing is the point: a snapshot over first's
// groups could never see a group that started committing during the window.
//
// If nothing changed it returns the second snapshot, which is the source data sync copies. What that proves is
// that no offset changed during the window; a commit after the second snapshot is not seen (it is missing on
// the destination, so its consumer re-reads those records after the switch). If anything changed it returns a
// *RogueCommitsError. A failure to wait, list or fetch is returned as is: not being able to look is not
// evidence of a commit.
//
// The caller has already applied the conversion's group rule to first, so every group in it is in scope;
// there is no filter here.
func DetectRogueCommits(ctx context.Context, first Snapshot, listGroups GroupLister, newFetcher FetcherFactory, workers int, window time.Duration, wait WaitFunc) (Snapshot, error) {
	if err := wait(ctx, window); err != nil {
		return nil, err
	}
	groups, err := listGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing source consumer groups for the second snapshot: %w", err)
	}
	second, err := TakeSnapshot(ctx, newFetcher, groups, workers)
	if err != nil {
		return nil, err
	}
	if changes := Changes(first, second); len(changes) > 0 {
		return nil, &RogueCommitsError{Changes: changes}
	}
	return second, nil
}
