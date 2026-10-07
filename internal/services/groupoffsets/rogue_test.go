package groupoffsets

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChanges_ReportsNewDifferentAndRemovedValuesSorted(t *testing.T) {
	before := Snapshot{"a": {"orders": {0: 5, 1: 6}}, "b": {"orders": {0: 1}}, "gone": {"orders": {0: 4}}}
	after := Snapshot{
		"c": {"orders": {0: 2}},                     // a new group
		"a": {"orders": {0: 5, 1: 9}},               // partition 1 moved
		"b": {"orders": {0: 1}, "payments": {0: 3}}, // a new topic
		// "gone" is missing: its offsets were deleted or expired
	}

	assert.Equal(t, []Change{
		{Group: "a", Topic: "orders", Partition: 1, Before: 6, After: 9},
		{Group: "b", Topic: "payments", Partition: 0, After: 3, New: true},
		{Group: "c", Topic: "orders", Partition: 0, After: 2, New: true},
		{Group: "gone", Topic: "orders", Partition: 0, Before: 4, Removed: true},
	}, Changes(before, after))
}

func TestChanges_AnyDifferenceCountsNotOnlyGrowth(t *testing.T) {
	// An admin resetting a group to a lower offset changes what would be copied, exactly as a commit does.
	got := Changes(Snapshot{"a": {"orders": {0: 10}}}, Snapshot{"a": {"orders": {0: 3}}})
	assert.Equal(t, []Change{{Group: "a", Topic: "orders", Partition: 0, Before: 10, After: 3}}, got)
}

func TestChanges_AGroupMissingFromTheSecondSnapshotIsAChange(t *testing.T) {
	// DeleteGroups during the fence, or an idle group's offsets expiring: either way the source offsets did
	// not stay put, and syncing the second snapshot would silently drop the group.
	got := Changes(Snapshot{"a": {"orders": {0: 10, 1: 11}}, "b": {"orders": {0: 1}}}, Snapshot{"b": {"orders": {0: 1}}})
	assert.Equal(t, []Change{
		{Group: "a", Topic: "orders", Partition: 0, Before: 10, Removed: true},
		{Group: "a", Topic: "orders", Partition: 1, Before: 11, Removed: true},
	}, got)
}

func TestChanges_APartitionMissingFromTheSecondSnapshotIsAChange(t *testing.T) {
	// OffsetDelete on one partition: the group is still there, one position is not.
	got := Changes(Snapshot{"a": {"orders": {0: 10, 1: 11}}}, Snapshot{"a": {"orders": {0: 10}}})
	assert.Equal(t, []Change{{Group: "a", Topic: "orders", Partition: 1, Before: 11, Removed: true}}, got)
}

func TestChanges_IdenticalSnapshotsHaveNone(t *testing.T) {
	s := Snapshot{"a": {"orders": {0: 5}}}
	assert.Empty(t, Changes(s, Snapshot{"a": {"orders": {0: 5}}}))
	assert.Empty(t, Changes(nil, nil))
}

// waitRecorder is a WaitFunc that records the windows asked for without sleeping.
type waitRecorder struct {
	windows []time.Duration
	err     error
}

func (w *waitRecorder) wait(_ context.Context, d time.Duration) error {
	w.windows = append(w.windows, d)
	return w.err
}

// listerOf returns a GroupLister answering groups, and a counter of its calls.
func listerOf(groups ...string) (GroupLister, *int) {
	calls := 0
	return func(context.Context) ([]string, error) { calls++; return groups, nil }, &calls
}

func TestDetectRogueCommits_ReturnsTheSecondSnapshotWhenNothingMoved(t *testing.T) {
	first := Snapshot{"a": {"orders": {0: 5}}}
	factory, _, _ := factoryFor(&fakeFetcher{offsets: map[string]Offsets{"a": {"orders": {0: 5}}}})
	lister, listed := listerOf("a")
	w := &waitRecorder{}

	got, err := DetectRogueCommits(context.Background(), first, lister, factory, 2, 30*time.Second, w.wait)

	require.NoError(t, err)
	assert.Equal(t, Snapshot{"a": {"orders": {0: 5}}}, got, "the second snapshot is what sync copies, so it is returned")
	assert.Equal(t, []time.Duration{30 * time.Second}, w.windows, "waits the whole window before looking again")
	assert.Equal(t, 1, *listed, "the second snapshot lists the groups afresh")
}

func TestDetectRogueCommits_AGroupThatStartedCommittingDuringTheWindowIsAChange(t *testing.T) {
	// Snapshot 1 knew only "a"; "late" appeared during the window. Only a fresh listing can see it.
	first := Snapshot{"a": {"orders": {0: 5}}}
	factory, _, _ := factoryFor(&fakeFetcher{offsets: map[string]Offsets{
		"a":    {"orders": {0: 5}},
		"late": {"orders": {0: 1}},
	}})
	lister, _ := listerOf("a", "late")

	_, err := DetectRogueCommits(context.Background(), first, lister, factory, 1, time.Second, (&waitRecorder{}).wait)

	require.ErrorIs(t, err, ErrRogueCommits)
	var rce *RogueCommitsError
	require.True(t, errors.As(err, &rce))
	assert.Equal(t, []Change{{Group: "late", Topic: "orders", Partition: 0, After: 1, New: true}}, rce.Changes)
}

func TestDetectRogueCommits_AGroupThatDisappearedIsAChange(t *testing.T) {
	first := Snapshot{"a": {"orders": {0: 5}}, "b": {"orders": {0: 2}}}
	factory, _, _ := factoryFor(&fakeFetcher{offsets: map[string]Offsets{"a": {"orders": {0: 5}}}})
	lister, _ := listerOf("a") // b was deleted during the window

	_, err := DetectRogueCommits(context.Background(), first, lister, factory, 1, time.Second, (&waitRecorder{}).wait)

	require.ErrorIs(t, err, ErrRogueCommits)
	assert.Contains(t, err.Error(), "group b topic orders partition 0: removed (was 2)")
}

func TestDetectRogueCommits_AChangeIsRogueCommits(t *testing.T) {
	first := Snapshot{"a": {"orders": {0: 5}}}
	factory, _, _ := factoryFor(&fakeFetcher{offsets: map[string]Offsets{"a": {"orders": {0: 7}}}})
	lister, _ := listerOf("a")

	got, err := DetectRogueCommits(context.Background(), first, lister, factory, 1, time.Second, (&waitRecorder{}).wait)

	require.ErrorIs(t, err, ErrRogueCommits)
	assert.Nil(t, got)
	var rce *RogueCommitsError
	require.True(t, errors.As(err, &rce))
	assert.Equal(t, []Change{{Group: "a", Topic: "orders", Partition: 0, Before: 5, After: 7}}, rce.Changes)
}

func TestRogueCommitsError_NamesTheFirstChangesAndCapsTheRest(t *testing.T) {
	var changes []Change
	for i := 0; i < 12; i++ {
		changes = append(changes, Change{Group: fmt.Sprintf("g%02d", i), Topic: "orders", Partition: 0, Before: 1, After: 2})
	}
	msg := (&RogueCommitsError{Changes: changes}).Error()

	assert.Contains(t, msg, "12 partition(s)")
	assert.Contains(t, msg, "g00")
	assert.Contains(t, msg, "and 7 more")
	assert.NotContains(t, msg, "g11", "only the first few are listed")
	assert.Contains(t, msg, "did not stay put", "say what it means")
}

func TestDetectRogueCommits_ACancelledWaitSkipsTheSecondLook(t *testing.T) {
	w := &waitRecorder{err: context.Canceled}
	lister, listed := listerOf("a")
	factory, built, _ := factoryFor(&fakeFetcher{})

	_, err := DetectRogueCommits(context.Background(), Snapshot{}, lister, factory, 1, time.Second, w.wait)

	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, *listed)
	assert.Zero(t, built.Load())
}

func TestDetectRogueCommits_AListingFailureIsAnError(t *testing.T) {
	boom := errors.New("broker b2 failed")
	factory, _, _ := factoryFor(&fakeFetcher{})
	_, err := DetectRogueCommits(context.Background(), Snapshot{},
		func(context.Context) ([]string, error) { return nil, boom }, factory, 1, time.Second, (&waitRecorder{}).wait)
	require.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, ErrRogueCommits, "a failure to look is not evidence of a commit")
}

func TestDetectRogueCommits_AFetchFailureIsAnError(t *testing.T) {
	boom := errors.New("cannot read offsets")
	factory, _, _ := factoryFor(&fakeFetcher{errs: map[string]error{"a": boom}})
	lister, _ := listerOf("a")
	_, err := DetectRogueCommits(context.Background(), Snapshot{"a": {"orders": {0: 1}}}, lister, factory, 1, time.Second, (&waitRecorder{}).wait)
	require.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, ErrRogueCommits)
}

func TestSleep(t *testing.T) {
	require.NoError(t, Sleep(context.Background(), time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	require.ErrorIs(t, Sleep(ctx, time.Hour), context.Canceled)
	assert.Less(t, time.Since(start), time.Second, "a cancelled wait returns at once")
}
