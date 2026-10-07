package groupoffsets

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type commitFunc func(group string, offsets Offsets) error

func (f commitFunc) CommitGroupOffsets(g string, o Offsets) error { return f(g, o) }

// commitLog is a CommitterFactory that records every commit and how many committers it built and released.
type commitLog struct {
	mu       sync.Mutex
	commits  map[string]Offsets
	calls    []string
	errs     map[string]error
	built    atomic.Int64
	released atomic.Int64
}

func newCommitLog() *commitLog {
	return &commitLog{commits: map[string]Offsets{}, errs: map[string]error{}}
}

func (l *commitLog) factory() CommitterFactory {
	return func() (GroupCommitter, func(), error) {
		l.built.Add(1)
		return commitFunc(func(group string, offsets Offsets) error {
			l.mu.Lock()
			l.calls = append(l.calls, group)
			l.commits[group] = offsets
			l.mu.Unlock()
			return l.errs[group]
		}), func() { l.released.Add(1) }, nil
	}
}

func planOf(groups ...string) *Plan {
	p := &Plan{}
	for _, g := range groups {
		p.Commits = append(p.Commits, Commit{Group: g, Offsets: Offsets{"orders": {0: 1}}})
	}
	return p
}

func TestApply_CommitsEachGroupOnceWithItsOffsets(t *testing.T) {
	log := newCommitLog()
	plan := &Plan{Commits: []Commit{
		{Group: "a", Offsets: Offsets{"orders": {0: 5, 1: 6}}},
		{Group: "b", Offsets: Offsets{"payments": {0: 2}}},
	}}

	require.NoError(t, Apply(context.Background(), log.factory(), plan, 2))

	sort.Strings(log.calls)
	assert.Equal(t, []string{"a", "b"}, log.calls, "one commit per group, never one per partition")
	assert.Equal(t, Offsets{"orders": {0: 5, 1: 6}}, log.commits["a"])
	assert.Equal(t, Offsets{"payments": {0: 2}}, log.commits["b"])
}

func TestApply_AFailedGroupFailsTheRunNamingItAndKeepingTheCause(t *testing.T) {
	boom := errors.New("UNKNOWN_MEMBER_ID")
	log := newCommitLog()
	log.errs["g2"] = boom

	err := Apply(context.Background(), log.factory(), planOf("g1", "g2", "g3"), 1)

	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "g2")
	assert.Contains(t, err.Error(), "syncing committed offsets")
}

func TestApply_StopsAfterTheFirstFailure(t *testing.T) {
	log := newCommitLog()
	plan := planOf(names(100)...)
	log.errs["g000"] = errors.New("denied")

	require.Error(t, Apply(context.Background(), log.factory(), plan, 1))

	assert.Less(t, len(log.calls), 10, "a failure fails the run: do not go on writing the other groups")
}

func TestApply_AnEmptyOrNilPlanBuildsNoCommitters(t *testing.T) {
	log := newCommitLog()
	require.NoError(t, Apply(context.Background(), log.factory(), nil, 4))
	require.NoError(t, Apply(context.Background(), log.factory(), &Plan{}, 4))
	assert.Zero(t, log.built.Load())
}

func TestApply_EachWorkerGetsItsOwnCommitterAndReleasesIt(t *testing.T) {
	log := newCommitLog()
	require.NoError(t, Apply(context.Background(), log.factory(), planOf(names(6)...), 3))
	assert.EqualValues(t, 3, log.built.Load())
	assert.EqualValues(t, 3, log.released.Load())
}

func TestApply_ACancelledContextStopsTheRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	log := newCommitLog()

	err := Apply(ctx, log.factory(), planOf(names(10)...), 2)

	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, log.calls)
}

func TestRefusedPlanWritesNothing(t *testing.T) {
	// The composition the d2s FSM relies on: BuildPlan refuses, so there is no plan, so Apply writes nothing,
	// not even for the group whose offsets were fine.
	snap := Snapshot{"fine": {"orders": {0: 1}}, "bad": {"orders": {0: 99}}}
	plan, err := BuildPlan(snap, map[string]map[int32]int64{"orders": {0: 10}})
	require.ErrorIs(t, err, ErrOutOfRange)

	log := newCommitLog()
	require.NoError(t, Apply(context.Background(), log.factory(), plan, 2))

	assert.Empty(t, log.calls)
	assert.Zero(t, log.built.Load())
}
