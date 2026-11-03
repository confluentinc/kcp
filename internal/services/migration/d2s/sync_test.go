package d2s

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/services/groupoffsets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syncWith runs SyncOffsets on w as if this run's verify_fence had handed snap over.
func syncWith(w *world, snap groupoffsets.Snapshot, p Policy) error {
	a := NewD2SActions(&mockGatewayService{}, w.deps())
	a.reporter = quietReporter()
	a.handoff = snap
	return a.SyncOffsets(context.Background(), testConfig(), p)
}

// handedOver is cleanWorld's snapshot: orders-app at orders[0]=5, orders[1]=7 with its metadata.
func handedOver() groupoffsets.Snapshot { return cleanWorld().src.rounds[0] }

func TestSyncOffsets_WritesTheHandedOverSnapshot(t *testing.T) {
	w := cleanWorld()

	require.NoError(t, syncWith(w, handedOver(), testPolicy()))

	assert.Equal(t, map[string]groupoffsets.Offsets(handedOver()), w.dst.commits, "every offset and its metadata, unchanged")
	assert.Equal(t, 1, w.dst.topicLists, "one fresh destination topic listing before the sweep")
	assert.Equal(t, 1, w.dst.sweeps, "one high-water-mark sweep")
	assert.Zero(t, w.src.lists+w.src.fetches, "sync never reads the source")
}

func TestSyncOffsets_UsesOneCommitterPerWorker(t *testing.T) {
	w := cleanWorld()
	snap := groupoffsets.Snapshot{
		"a": {"orders": {0: {Offset: 1}}},
		"b": {"orders": {0: {Offset: 2}}},
		"c": {"orders": {0: {Offset: 3}}},
	}

	require.NoError(t, syncWith(w, snap, Policy{DetectUnroutedCommitsDuration: time.Second, Workers: 2}))

	assert.Equal(t, 2, w.dst.builds, "one committer, so one client, per worker")
	assert.Len(t, w.dst.commits, 3)
}

// Review Focus 1.
func TestSyncOffsets_EmptySnapshotWritesNothing(t *testing.T) {
	w := cleanWorld()

	require.NoError(t, syncWith(w, groupoffsets.Snapshot{}, testPolicy()))

	assert.Empty(t, w.dst.commits)
	assert.Zero(t, w.dst.topicLists+w.dst.sweeps+w.dst.builds, "nothing to sync touches nothing")
}

func TestSyncOffsets_RefusalsWriteNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*fakeDestination)
		is   error
		want string
	}{
		{"a committed topic was deleted on the destination", func(d *fakeDestination) { d.topics = nil }, groupoffsets.ErrMissingTopics, "orders"},
		{"an offset is past the destination's high-water mark", func(d *fakeDestination) { d.hwm["orders"][0] = 4 }, groupoffsets.ErrOutOfRange, "committed 5, high-water mark 4"},
		{"a committed partition is missing on the destination", func(d *fakeDestination) { delete(d.hwm["orders"], 1) }, groupoffsets.ErrOutOfRange, "no such partition"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := cleanWorld()
			tc.edit(w.dst)

			err := syncWith(w, handedOver(), testPolicy())

			require.ErrorIs(t, err, tc.is)
			assert.Contains(t, err.Error(), tc.want)
			assert.Zero(t, w.dst.builds, "no committer is even built")
			assert.Empty(t, w.dst.commits)
			if errors.Is(tc.is, groupoffsets.ErrMissingTopics) {
				assert.Zero(t, w.dst.sweeps, "a missing topic is never swept, so never auto-created")
			}
		})
	}
}

func TestSyncOffsets_LiveMembersOnTheDestinationAreNamed(t *testing.T) {
	w := cleanWorld()
	w.dst.commitErr = map[string]error{
		"orders-app": fmt.Errorf("committing offset for consumer group orders-app, topic orders partition 0: %w", sarama.ErrUnknownMemberId),
	}

	err := syncWith(w, handedOver(), testPolicy())

	var live *LiveMembersError
	require.ErrorAs(t, err, &live)
	assert.Equal(t, "orders-app", live.Group)
	assert.Contains(t, err.Error(), "consumer group orders-app has live members on the destination; stop them, then rerun")
	assert.ErrorIs(t, err, sarama.ErrUnknownMemberId, "the broker's answer is kept for errors.Is")
}

func TestSyncOffsets_OtherCommitFailuresPassThrough(t *testing.T) {
	w := cleanWorld()
	w.dst.commitErr = map[string]error{"orders-app": fmt.Errorf("x: %w", sarama.ErrGroupAuthorizationFailed)}

	err := syncWith(w, handedOver(), testPolicy())

	require.ErrorIs(t, err, sarama.ErrGroupAuthorizationFailed)
	var live *LiveMembersError
	assert.False(t, errors.As(err, &live))
}

func TestSyncOffsets_ReadFailuresAreErrors(t *testing.T) {
	boom := errors.New("boom")
	for name, edit := range map[string]func(*fakeDestination){
		"listing destination topics": func(d *fakeDestination) { d.topicsErr = boom },
		"sweeping high-water marks":  func(d *fakeDestination) { d.sweepErr = boom },
	} {
		t.Run(name, func(t *testing.T) {
			w := cleanWorld()
			edit(w.dst)

			err := syncWith(w, handedOver(), testPolicy())

			require.ErrorIs(t, err, boom)
			assert.Zero(t, w.dst.builds)
		})
	}
}
