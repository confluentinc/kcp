package groupoffsets

import (
	"context"
	"fmt"
)

// GroupCommitter writes one group's offsets to the destination. *client.ConsumerGroupClient satisfies it.
type GroupCommitter interface {
	CommitGroupOffsets(group string, offsets Offsets) error
}

// CommitterFactory returns a committer and the function that releases it (which may be nil). Each pool worker
// calls it once, so each worker owns its own client.
type CommitterFactory func() (GroupCommitter, func(), error)

// Apply writes the plan: one OffsetCommit per group, on up to workers workers. Commits are idempotent, so a
// partial earlier attempt, or a rollback and redo, simply repeats them; there is deliberately no destination
// read to skip equal ones, since a read costs the same round trip as the write it would save. Any failure
// fails the run and stops further writes. A nil or empty plan writes nothing and builds no clients.
func Apply(ctx context.Context, newCommitter CommitterFactory, plan *Plan, workers int) error {
	if plan == nil || len(plan.Commits) == 0 {
		return nil
	}
	byGroup := make(map[string]Offsets, len(plan.Commits))
	groups := make([]string, 0, len(plan.Commits))
	for _, c := range plan.Commits {
		byGroup[c.Group] = c.Offsets
		groups = append(groups, c.Group)
	}
	err := forEach(ctx, groups, workers, func() (worker, error) {
		c, release, err := newCommitter()
		if err != nil {
			return worker{}, err
		}
		return worker{
			do:      func(group string) error { return c.CommitGroupOffsets(group, byGroup[group]) },
			release: release,
		}, nil
	})
	if err != nil {
		return fmt.Errorf("syncing committed offsets: %w", err)
	}
	return nil
}
