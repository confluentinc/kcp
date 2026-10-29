package groupoffsets

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/retry"
)

// The real client is the fetcher and the committer, with no adapter.
var (
	_ GroupOffsetFetcher = (*client.ConsumerGroupClient)(nil)
	_ GroupCommitter     = (*client.ConsumerGroupClient)(nil)
)

// at is a committed offset with no metadata.
func at(o int64) CommittedOffset { return CommittedOffset{Offset: o} }

type fakeFetcher struct {
	offsets map[string]Offsets
	errs    map[string]error
}

func (f *fakeFetcher) CommittedOffsets(_ context.Context, group string) (Offsets, error) {
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
		"a":    {"orders": {0: at(5), 1: at(6)}},
		"b":    {"orders": {0: at(1)}, "payments": {0: at(9)}},
		"idle": {},
	}}
	factory, _, _ := factoryFor(f)

	snap, err := TakeSnapshot(context.Background(), factory, []string{"a", "b", "idle"}, 2)

	require.NoError(t, err)
	assert.Equal(t, Snapshot{
		"a": {"orders": {0: at(5), 1: at(6)}},
		"b": {"orders": {0: at(1)}, "payments": {0: at(9)}},
	}, snap, "a group with no commits has nothing to sync")
}

func TestSnapshot_GroupsAndTopicsAreSorted(t *testing.T) {
	snap := Snapshot{
		"zeta":  {"orders": {0: at(1)}},
		"alpha": {"payments": {0: at(1)}, "orders": {0: at(2)}},
	}
	assert.Equal(t, []string{"alpha", "zeta"}, snap.Groups())
	assert.Equal(t, []string{"orders", "payments"}, snap.Topics(), "the union across groups, each once")
	assert.Empty(t, Snapshot{}.Topics())
}

func TestTakeSnapshot_AFailedGroupFailsTheWholeSnapshot(t *testing.T) {
	boom := errors.New("not authorized")
	f := &fakeFetcher{
		offsets: map[string]Offsets{"ok": {"orders": {0: at(1)}}},
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
		f.offsets[g] = Offsets{"orders": {0: at(1)}}
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
	f := &fakeFetcher{offsets: map[string]Offsets{"a": {"orders": {0: at(1)}}}}
	snap, err := TakeSnapshot(context.Background(), func() (GroupOffsetFetcher, func(), error) { return f, nil, nil }, []string{"a"}, 1)
	require.NoError(t, err)
	assert.Len(t, snap, 1)
}

func TestTakeSnapshot_AFactoryFailureIsAnError(t *testing.T) {
	boom := errors.New("cannot connect")
	_, err := TakeSnapshot(context.Background(), func() (GroupOffsetFetcher, func(), error) { return nil, nil, boom }, []string{"a"}, 1)
	require.ErrorIs(t, err, boom)
}

// stormFetcher's retrying group retries a connection reset for the whole retry budget; its denied group fails
// for good once the retrying group's fetch has started.
type stormFetcher struct {
	retrying, denied string
	started          chan struct{}
	once             sync.Once
}

func (f *stormFetcher) CommittedOffsets(ctx context.Context, group string) (Offsets, error) {
	switch group {
	case f.retrying:
		return nil, retry.Do(ctx, "kafka.committed_offsets", func(context.Context) error {
			f.once.Do(func() { close(f.started) })
			return connReset
		})
	case f.denied:
		<-f.started
		return nil, fmt.Errorf("denied for %s", group)
	}
	return nil, nil
}

func TestTakeSnapshot_APermanentFailureStopsTheOtherWorkersRetrying(t *testing.T) {
	// The real retry schedule: without the stop, g1's read retries for the whole 2-minute budget.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := &stormFetcher{retrying: "g1", denied: "g2", started: make(chan struct{})}
	factory := func() (GroupOffsetFetcher, func(), error) { return f, nil, nil }

	start := time.Now()
	_, err := TakeSnapshot(ctx, factory, []string{"g1", "g2"}, 2)

	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second, "g1's retries must stop once g2 has failed for good")
	assert.Contains(t, err.Error(), "denied for g2", "the error must be g2's, not g1's stopped retries")
	assert.NotErrorIs(t, err, context.Canceled)
}
