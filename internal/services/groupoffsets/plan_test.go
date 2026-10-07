package groupoffsets

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSweep is an offset.Provider that records the topics it was asked for.
type fakeSweep struct {
	hwm   map[string]map[int32]int64
	err   error
	asked [][]string
}

func (f *fakeSweep) GetMany(_ context.Context, topics []string) (map[string]map[int32]int64, error) {
	f.asked = append(f.asked, topics)
	return f.hwm, f.err
}

func topicsLister(topics ...string) TopicLister {
	return func(context.Context) ([]string, error) { return topics, nil }
}

func TestDestinationHighWaterMarks_SweepsTheSnapshotsTopics(t *testing.T) {
	snap := Snapshot{"a": {"orders": {0: 1}}, "b": {"payments": {0: 2}, "orders": {1: 3}}}
	sweep := &fakeSweep{hwm: map[string]map[int32]int64{"orders": {0: 9, 1: 9}, "payments": {0: 9}}}

	hwm, err := DestinationHighWaterMarks(context.Background(), topicsLister("orders", "payments", "other"), sweep, snap)

	require.NoError(t, err)
	assert.Equal(t, sweep.hwm, hwm)
	assert.Equal(t, [][]string{{"orders", "payments"}}, sweep.asked, "one sweep, over exactly the committed topics")
}

func TestDestinationHighWaterMarks_ATopicMissingOnTheDestinationRefusesBeforeTheSweep(t *testing.T) {
	snap := Snapshot{"a": {"orders": {0: 1}, "zgone": {0: 1}}, "b": {"agone": {0: 2}}}
	sweep := &fakeSweep{}

	hwm, err := DestinationHighWaterMarks(context.Background(), topicsLister("orders"), sweep, snap)

	require.ErrorIs(t, err, ErrMissingTopics)
	assert.Nil(t, hwm)
	var mte *MissingTopicsError
	require.True(t, errors.As(err, &mte))
	assert.Equal(t, []string{"agone", "zgone"}, mte.Topics, "every missing topic, sorted")
	assert.Contains(t, err.Error(), "deleted on the destination during the run")
	assert.Empty(t, sweep.asked, "never sweep a missing topic: a sweep client allowed to auto-create could create it")
}

func TestDestinationHighWaterMarks_FailuresAreErrorsNotRefusals(t *testing.T) {
	boom := errors.New("broker down")
	snap := Snapshot{"a": {"orders": {0: 1}}}

	_, err := DestinationHighWaterMarks(context.Background(),
		func(context.Context) ([]string, error) { return nil, boom }, &fakeSweep{}, snap)
	require.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, ErrMissingTopics)

	_, err = DestinationHighWaterMarks(context.Background(), topicsLister("orders"), &fakeSweep{err: boom}, snap)
	require.ErrorIs(t, err, boom)
}

func TestDestinationHighWaterMarks_AnEmptySnapshotSweepsNothing(t *testing.T) {
	sweep := &fakeSweep{}
	hwm, err := DestinationHighWaterMarks(context.Background(), topicsLister(), sweep, Snapshot{})
	require.NoError(t, err)
	assert.Empty(t, hwm)
	assert.Empty(t, sweep.asked)
}

func TestBuildPlan_InRangeOffsetsBecomeOneCommitPerGroup(t *testing.T) {
	snap := Snapshot{
		"b": {"orders": {0: 1}, "payments": {0: 2}},
		"a": {"orders": {0: 5, 1: 6}},
	}
	hwm := map[string]map[int32]int64{"orders": {0: 10, 1: 10}, "payments": {0: 2}}

	plan, err := BuildPlan(snap, hwm)

	require.NoError(t, err)
	assert.Equal(t, []Commit{
		{Group: "a", Offsets: Offsets{"orders": {0: 5, 1: 6}}},
		{Group: "b", Offsets: Offsets{"orders": {0: 1}, "payments": {0: 2}}},
	}, plan.Commits, "sorted by group; payments' offset equals its high-water mark and is valid")
	assert.Equal(t, 4, plan.Partitions())
}

func TestBuildPlan_AnOffsetEqualToTheHighWaterMarkIsValidOnePastIsNot(t *testing.T) {
	hwm := map[string]map[int32]int64{"orders": {0: 5}}

	// A caught-up consumer commits exactly the high-water mark.
	plan, err := BuildPlan(Snapshot{"g": {"orders": {0: 5}}}, hwm)
	require.NoError(t, err)
	assert.Len(t, plan.Commits, 1)

	// One past the end would be OFFSET_OUT_OF_RANGE on the consumer's next fetch.
	plan, err = BuildPlan(Snapshot{"g": {"orders": {0: 6}}}, hwm)
	require.ErrorIs(t, err, ErrOutOfRange)
	assert.Nil(t, plan)
	var oor *OutOfRangeError
	require.True(t, errors.As(err, &oor))
	assert.Equal(t, []Violation{{Group: "g", Topic: "orders", Partition: 0, Offset: 6, HighWaterMark: 5}}, oor.Violations)
}

func TestBuildPlan_ACommittedPartitionWithNoDestinationPartitionRefuses(t *testing.T) {
	// Partition 3 was committed on the source but the destination topic only has 0 and 1; and the
	// high-water-mark sweep returned nothing at all for "payments". Neither is ever skipped.
	snap := Snapshot{"g": {"orders": {0: 1, 3: 4}, "payments": {0: 1}}}
	hwm := map[string]map[int32]int64{"orders": {0: 10, 1: 10}}

	plan, err := BuildPlan(snap, hwm)

	require.ErrorIs(t, err, ErrOutOfRange)
	assert.Nil(t, plan, "a position with nowhere to go must never be silently dropped")
	var oor *OutOfRangeError
	require.True(t, errors.As(err, &oor))
	assert.Equal(t, []Violation{
		{Group: "g", Topic: "orders", Partition: 3, Offset: 4, NoPartition: true},
		{Group: "g", Topic: "payments", Partition: 0, Offset: 1, NoPartition: true},
	}, oor.Violations)
	assert.Contains(t, err.Error(), "the destination has no such partition")
}

func TestBuildPlan_EveryViolationIsReportedSortedAndNothingIsPlanned(t *testing.T) {
	snap := Snapshot{
		"b":  {"orders": {0: 99}},
		"a":  {"orders": {1: 50, 0: 60}, "payments": {0: 1}},
		"ok": {"payments": {0: 1}},
	}
	hwm := map[string]map[int32]int64{"orders": {0: 10, 1: 10}, "payments": {0: 5}}

	plan, err := BuildPlan(snap, hwm)

	require.Error(t, err)
	assert.Nil(t, plan, "a refusal leaves the destination untouched: even the group that was fine is not planned")
	var oor *OutOfRangeError
	require.True(t, errors.As(err, &oor))
	assert.Equal(t, []Violation{
		{Group: "a", Topic: "orders", Partition: 0, Offset: 60, HighWaterMark: 10},
		{Group: "a", Topic: "orders", Partition: 1, Offset: 50, HighWaterMark: 10},
		{Group: "b", Topic: "orders", Partition: 0, Offset: 99, HighWaterMark: 10},
	}, oor.Violations)
	assert.Contains(t, err.Error(), "3 committed offset(s)")
	assert.Contains(t, err.Error(), "group a topic orders partition 0: committed 60, high-water mark 10")
}

func TestBuildPlan_AnEmptySnapshotIsAnEmptyPlan(t *testing.T) {
	plan, err := BuildPlan(Snapshot{}, nil)
	require.NoError(t, err)
	assert.Empty(t, plan.Commits)
	assert.Zero(t, plan.Partitions())
}
