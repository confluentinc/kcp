package retry

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/tracing"
	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock runs Do's schedule without sleeping: each wait advances the clock and is recorded.
type fakeClock struct {
	t     time.Time
	waits []time.Duration
}

func useFakeClock(t *testing.T) *fakeClock {
	t.Helper()
	c := &fakeClock{t: time.Unix(0, 0)}
	oldNow, oldWait := now, wait
	now = func() time.Time { return c.t }
	wait = func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.waits = append(c.waits, d)
		c.t = c.t.Add(d)
		return nil
	}
	t.Cleanup(func() { now, wait = oldNow, oldWait })
	return c
}

var reset = &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}

func TestDo_WaitsDoubleUpToTheCapThenGivesUpAtTheBudget(t *testing.T) {
	c := useFakeClock(t)
	calls := 0

	err := Do(context.Background(), "kafka.list_groups", func(context.Context) error { calls++; return reset })

	var gave *GaveUpError
	require.ErrorAs(t, err, &gave)
	s := time.Second
	assert.Equal(t, []time.Duration{
		500 * time.Millisecond, s, 2 * s, 4 * s, 8 * s,
		10 * s, 10 * s, 10 * s, 10 * s, 10 * s, 10 * s, 10 * s, 10 * s, 10 * s, 10 * s,
	}, c.waits, "waits double from 500 ms and stop growing at 10 s; the next wait would end past 2 minutes")
	assert.Equal(t, 16, calls)
	assert.Equal(t, 16, gave.Attempts)
	assert.Equal(t, 115500*time.Millisecond, gave.Elapsed)
	assert.ErrorIs(t, err, syscall.ECONNRESET, "the cause is kept")
	assert.Contains(t, err.Error(), "kafka.list_groups: gave up after 16 attempts over 1m55.5s")
}

func TestDo_TheAttemptsOwnTimeCountsAgainstTheBudget(t *testing.T) {
	c := useFakeClock(t)
	calls := 0

	err := Do(context.Background(), "op", func(context.Context) error {
		calls++
		c.t = c.t.Add(30 * time.Second)
		return reset
	})

	var gave *GaveUpError
	require.ErrorAs(t, err, &gave)
	assert.Equal(t, 5, calls)
	assert.Equal(t, []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second}, c.waits)
}

func TestDo_ReturnsOnceTheCallSucceeds(t *testing.T) {
	c := useFakeClock(t)
	calls := 0

	err := Do(context.Background(), "op", func(context.Context) error {
		calls++
		if calls < 3 {
			return reset
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 3, calls)
	assert.Equal(t, []time.Duration{500 * time.Millisecond, time.Second}, c.waits)
}

func TestDo_ANonRetryableErrorReturnsUnchangedAtOnce(t *testing.T) {
	c := useFakeClock(t)
	boom := errors.New("authorization failed")
	calls := 0

	err := Do(context.Background(), "op", func(context.Context) error { calls++; return boom })

	assert.Same(t, boom, err)
	assert.Equal(t, 1, calls)
	assert.Empty(t, c.waits)
}

func TestDo_ANonRetryableErrorAfterRetriesIsReturnedAsItIs(t *testing.T) {
	useFakeClock(t)
	boom := errors.New("authorization failed")
	calls := 0

	err := Do(context.Background(), "op", func(context.Context) error {
		calls++
		if calls < 3 {
			return reset
		}
		return boom
	})

	assert.Same(t, boom, err)
	assert.Equal(t, 3, calls)
}

func TestDo_StopsAtOnceWhenTheContextEnds(t *testing.T) {
	c := useFakeClock(t)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0

	err := Do(ctx, "op", func(context.Context) error {
		calls++
		if calls == 2 {
			cancel()
		}
		return reset
	})

	require.ErrorIs(t, err, context.Canceled)
	assert.ErrorIs(t, err, syscall.ECONNRESET, "the last failure is kept alongside the cancellation")
	assert.Equal(t, 2, calls)
	assert.Len(t, c.waits, 1)
}

func TestDo_ACallersDeadlineIsNotRetried(t *testing.T) {
	useFakeClock(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	calls := 0

	err := Do(ctx, "op", func(ctx context.Context) error { calls++; return ctx.Err() })

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, calls)
}

func TestDo_RunsBeforeRetryBeforeEachRetryAndNeverAfterTheLastAttempt(t *testing.T) {
	useFakeClock(t)
	calls, hooks := 0, 0

	_ = Do(context.Background(), "op", func(context.Context) error {
		calls++
		assert.Equal(t, calls-1, hooks, "the hook runs once before every attempt after the first")
		return reset
	}, WithBeforeRetry(func() { hooks++ }))

	assert.Equal(t, 16, calls)
	assert.Equal(t, 15, hooks)
}

func TestDo_WithRetryableReplacesTheClassifier(t *testing.T) {
	useFakeClock(t)
	notReady := errors.New("coordinator loading")
	calls := 0

	err := Do(context.Background(), "op", func(context.Context) error {
		calls++
		if calls < 2 {
			return notReady
		}
		return nil
	}, WithRetryable(func(err error) bool { return errors.Is(err, notReady) }))

	require.NoError(t, err)
	assert.Equal(t, 2, calls)
}

func TestDo_UnderWithoutRetriesCallsOnceAndReturnsTheErrorUnchanged(t *testing.T) {
	rec := tracingtest.Install(t)
	c := useFakeClock(t)
	ctx, span := tracing.Start(WithoutRetries(context.Background()), "test", "step.lag_check")
	calls := 0

	err := Do(ctx, "rest.get", func(context.Context) error { calls++; return reset })
	span.End()

	assert.Same(t, reset, err, "the error is returned as fn returned it")
	assert.Equal(t, 1, calls)
	assert.Empty(t, c.waits, "no wait")
	step := tracingtest.Find(t, tracingtest.Spans(rec), "step.lag_check")
	assert.Empty(t, step.Events(), "no retry.attempt event")
	assert.Empty(t, step.Attributes(), "no kcp.retry attribute")
}
