package groupoffsets

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeFetcher struct {
	offsets map[string]Offsets
	errs    map[string]error
}

func (f *fakeFetcher) CommittedOffsets(group string) (Offsets, error) {
	if err := f.errs[group]; err != nil {
		return nil, err
	}
	return f.offsets[group], nil
}

// factoryFor returns a FetcherFactory over f plus counters for how often it built and released.
func factoryFor(f *fakeFetcher) (FetcherFactory, *atomic.Int64, *atomic.Int64) {
	var built, released atomic.Int64
	return func() (GroupOffsetFetcher, func(), error) {
		built.Add(1)
		return f, func() { released.Add(1) }, nil
	}, &built, &released
}

func TestTakeSnapshot_CollectsEveryGroupAndOmitsGroupsWithNoCommits(t *testing.T) {
	f := &fakeFetcher{offsets: map[string]Offsets{
		"a":    {"orders": {0: 5, 1: 6}},
		"b":    {"orders": {0: 1}, "payments": {0: 9}},
		"idle": {},
	}}
	factory, _, _ := factoryFor(f)

	snap, err := TakeSnapshot(context.Background(), factory, []string{"a", "b", "idle"}, 2)

	require.NoError(t, err)
	assert.Equal(t, Snapshot{
		"a": {"orders": {0: 5, 1: 6}},
		"b": {"orders": {0: 1}, "payments": {0: 9}},
	}, snap, "a group with no commits has nothing to sync")
}

func TestSnapshot_GroupsAndTopicsAreSorted(t *testing.T) {
	snap := Snapshot{
		"zeta":  {"orders": {0: 1}},
		"alpha": {"payments": {0: 1}, "orders": {0: 2}},
	}
	assert.Equal(t, []string{"alpha", "zeta"}, snap.Groups())
	assert.Equal(t, []string{"orders", "payments"}, snap.Topics(), "the union across groups, each once")
	assert.Empty(t, Snapshot{}.Topics())
}

func TestTakeSnapshot_AFailedGroupFailsTheWholeSnapshot(t *testing.T) {
	boom := errors.New("not authorized")
	f := &fakeFetcher{
		offsets: map[string]Offsets{"ok": {"orders": {0: 1}}},
		errs:    map[string]error{"bad": boom},
	}
	factory, _, _ := factoryFor(f)

	snap, err := TakeSnapshot(context.Background(), factory, []string{"ok", "bad"}, 1)

	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "bad")
	assert.Nil(t, snap, "a partial snapshot would leave a group's offsets uncopied")
}

func TestTakeSnapshot_EachWorkerGetsItsOwnFetcherAndReleasesIt(t *testing.T) {
	f := &fakeFetcher{offsets: map[string]Offsets{}}
	var groups []string
	for _, g := range names(6) {
		f.offsets[g] = Offsets{"orders": {0: 1}}
		groups = append(groups, g)
	}
	factory, built, released := factoryFor(f)

	_, err := TakeSnapshot(context.Background(), factory, groups, 3)

	require.NoError(t, err)
	assert.EqualValues(t, 3, built.Load())
	assert.EqualValues(t, 3, released.Load())
}

func TestTakeSnapshot_NoGroupsBuildsNoFetchers(t *testing.T) {
	factory, built, _ := factoryFor(&fakeFetcher{})
	snap, err := TakeSnapshot(context.Background(), factory, nil, 4)
	require.NoError(t, err)
	assert.Empty(t, snap)
	assert.Zero(t, built.Load())
}

func TestTakeSnapshot_ANilReleaseIsAllowed(t *testing.T) {
	f := &fakeFetcher{offsets: map[string]Offsets{"a": {"orders": {0: 1}}}}
	snap, err := TakeSnapshot(context.Background(), func() (GroupOffsetFetcher, func(), error) { return f, nil, nil }, []string{"a"}, 1)
	require.NoError(t, err)
	assert.Len(t, snap, 1)
}

func TestTakeSnapshot_AFactoryFailureIsAnError(t *testing.T) {
	boom := errors.New("cannot connect")
	_, err := TakeSnapshot(context.Background(), func() (GroupOffsetFetcher, func(), error) { return nil, nil, boom }, []string{"a"}, 1)
	require.ErrorIs(t, err, boom)
}
