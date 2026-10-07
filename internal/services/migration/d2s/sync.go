package d2s

import (
	"context"
	"errors"
	"fmt"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/services/groupoffsets"
	"github.com/confluentinc/kcp/internal/services/migration"
)

// errNoHandoff is returned by SyncOffsets when this run's verify_fence handed over no snapshot.
var errNoHandoff = errors.New("sync_offsets has no committed-offset snapshot from this run's verify_fence; it never reads the source itself, so re-run the conversion")

// LiveMembersError is a destination consumer group the broker refused kcp's admin commit for because it has
// live members (UNKNOWN_MEMBER_ID). Retrying cannot help: its members must stop first.
type LiveMembersError struct {
	Group string
	Err   error
}

func (e *LiveMembersError) Error() string {
	return fmt.Sprintf("consumer group %s has live members on the destination; stop them, then rerun", e.Group)
}

func (e *LiveMembersError) Unwrap() error { return e.Err }

// liveMembersCommitter turns the broker's UNKNOWN_MEMBER_ID into a LiveMembersError naming the group.
type liveMembersCommitter struct {
	groupoffsets.GroupCommitter
}

func (c liveMembersCommitter) CommitGroupOffsets(group string, offsets groupoffsets.Offsets) error {
	err := c.GroupCommitter.CommitGroupOffsets(group, offsets)
	if errors.Is(err, sarama.ErrUnknownMemberId) {
		return &LiveMembersError{Group: group, Err: err}
	}
	return err
}

func liveMembersAware(newCommitter groupoffsets.CommitterFactory) groupoffsets.CommitterFactory {
	return func() (groupoffsets.GroupCommitter, func(), error) {
		c, release, err := newCommitter()
		if err != nil {
			return nil, nil, err
		}
		return liveMembersCommitter{c}, release, nil
	}
}

// SyncOffsets runs the sync_offsets transition on the snapshot this run's verify_fence handed over (snapshot
// 2, restricted to the in-scope groups); it never reads the source. In order: a fresh destination topic
// listing, refusing any snapshot topic missing from it, then one high-water-mark sweep whose client never
// auto-creates a topic (groupoffsets.DestinationHighWaterMarks); the out-of-range guard, which refuses the
// whole sync on any offset past its partition's high-water mark or any partition the destination lacks
// (groupoffsets.BuildPlan); then one admin commit per group on p.Workers workers (groupoffsets.Apply). A
// refusal writes nothing; any failure triggers abort_fence, which is safe because offsets written before the
// switch are inert.
func (a *D2SActions) SyncOffsets(ctx context.Context, config *migration.MigrationConfig, p Policy) error {
	snap := a.handoff
	if snap == nil {
		return errNoHandoff
	}
	hwm, err := groupoffsets.DestinationHighWaterMarks(ctx, a.deps.DestinationTopics, a.deps.HighWaterMarks, snap)
	if err != nil {
		return err
	}
	plan, err := groupoffsets.BuildPlan(snap, hwm)
	if err != nil {
		return err
	}
	if len(plan.Commits) == 0 {
		a.reporter.Success("No in-scope consumer group has committed offsets — nothing to sync")
		return nil
	}
	a.reporter.Detail("Writing %d committed offset(s) for %d consumer group(s) to the destination...", plan.Partitions(), len(plan.Commits))
	if err := groupoffsets.Apply(ctx, liveMembersAware(a.deps.Committers), plan, p.Workers); err != nil {
		return err
	}
	a.reporter.Success("Synced %d committed offset(s) for %d consumer group(s) to the destination", plan.Partitions(), len(plan.Commits))
	return nil
}
