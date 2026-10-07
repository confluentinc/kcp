package groupoffsets

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recorder struct {
	setups   atomic.Int64
	released atomic.Int64
	mu       sync.Mutex
	handled  []string
}

func (r *recorder) setup(do func(item string) error) func() (worker, error) {
	return func() (worker, error) {
		r.setups.Add(1)
		return worker{
			do: func(item string) error {
				r.mu.Lock()
				r.handled = append(r.handled, item)
				r.mu.Unlock()
				if do != nil {
					return do(item)
				}
				return nil
			},
			release: func() { r.released.Add(1) },
		}, nil
	}
}

func names(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("g%03d", i)
	}
	return out
}

func TestForEach_HandlesEveryItemOnceAndReleasesEveryWorker(t *testing.T) {
	r := &recorder{}
	items := names(20)

	require.NoError(t, forEach(context.Background(), items, 4, r.setup(nil)))

	sort.Strings(r.handled)
	assert.Equal(t, items, r.handled)
	assert.EqualValues(t, 4, r.setups.Load())
	assert.EqualValues(t, 4, r.released.Load(), "every worker's client must be released")
}

func TestForEach_NeverStartsMoreWorkersThanItems(t *testing.T) {
	r := &recorder{}
	require.NoError(t, forEach(context.Background(), names(2), 8, r.setup(nil)))
	assert.EqualValues(t, 2, r.setups.Load(), "a worker is a client connection; do not open idle ones")
}

func TestForEach_NoItemsStartsNothing(t *testing.T) {
	r := &recorder{}
	require.NoError(t, forEach(context.Background(), nil, 4, r.setup(nil)))
	assert.Zero(t, r.setups.Load())
}

func TestForEach_AZeroOrNegativeWorkerCountStillRuns(t *testing.T) {
	r := &recorder{}
	require.NoError(t, forEach(context.Background(), names(3), 0, r.setup(nil)))
	assert.Len(t, r.handled, 3)
}

func TestForEach_StopsHandingOutItemsAfterAFailure(t *testing.T) {
	r := &recorder{}
	err := forEach(context.Background(), names(200), 1, r.setup(func(string) error { return errors.New("denied") }))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "g000")
	assert.Less(t, len(r.handled), 10, "a failed item fails the run; the other ~200 must not be attempted")
}

func TestForEach_ReportsTheLowestNamedFailure(t *testing.T) {
	// Hold all three items in flight so they fail together; the reported item must not depend on which
	// worker happened to finish first.
	for i := 0; i < 20; i++ {
		var barrier sync.WaitGroup
		barrier.Add(3)
		r := &recorder{}
		err := forEach(context.Background(), []string{"g3", "g1", "g2"}, 3, r.setup(func(item string) error {
			barrier.Done()
			barrier.Wait()
			return fmt.Errorf("denied for %s", item)
		}))

		require.Error(t, err)
		require.Contains(t, err.Error(), "g1: denied for g1", "run %d: want the lowest-named failure", i)
		require.Contains(t, err.Error(), "and 2 more failed", "run %d", i)
	}
}

func TestForEach_AFailureKeepsItsCause(t *testing.T) {
	boom := errors.New("boom")
	err := forEach(context.Background(), names(3), 1, (&recorder{}).setup(func(string) error { return boom }))
	require.ErrorIs(t, err, boom)
}

func TestForEach_ASetupFailureStopsTheRunAndReleasesStartedWorkers(t *testing.T) {
	boom := errors.New("cannot connect")
	var calls, released atomic.Int64
	setup := func() (worker, error) {
		if calls.Add(1) == 2 {
			return worker{}, boom
		}
		return worker{do: func(string) error { return nil }, release: func() { released.Add(1) }}, nil
	}

	err := forEach(context.Background(), names(10), 3, setup)

	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "starting worker")
	assert.EqualValues(t, 1, released.Load(), "the worker that did start must be released")
	assert.EqualValues(t, 2, calls.Load(), "no further workers are started once one fails")
}

func TestForEach_ACancelledContextStopsTheRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &recorder{}

	err := forEach(ctx, names(100), 1, r.setup(func(string) error { cancel(); return nil }))

	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, len(r.handled), 5)
}
