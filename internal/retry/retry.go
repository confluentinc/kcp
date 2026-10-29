// Package retry calls an operation again when it fails on a connection-level error, waiting longer each time,
// until it succeeds, fails some other way, runs out of budget, or its context ends. kcp uses it on the calls a
// connection storm breaks: a fence drains a gateway's connections, thousands of clients reconnect at once, and
// for a while the clusters reset connections, kcp's among them.
package retry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// The schedule every Do uses: wait FirstWait after the first failure and double the wait after each later one,
// up to MaxWait. Do gives up rather than start a wait that would end more than Budget after the first failure;
// the attempts' own time counts against the budget. There is no jitter: one kcp process makes these calls.
const (
	FirstWait = 500 * time.Millisecond
	MaxWait   = 10 * time.Second
	Budget    = 2 * time.Minute
)

// Schedule is a backoff schedule. Do uses FirstWait, MaxWait and Budget unless a test replaces it.
type Schedule struct {
	FirstWait time.Duration
	MaxWait   time.Duration
	Budget    time.Duration
}

var (
	schedule = Schedule{FirstWait: FirstWait, MaxWait: MaxWait, Budget: Budget}
	// now and wait are vars so this package's tests can run the schedule on a fake clock.
	now  = time.Now
	wait = func(ctx context.Context, d time.Duration) error { return tracing.Wait(ctx, "retry_backoff", d) }
)

// SetScheduleForTest replaces the schedule every Do uses and returns the function that restores it. Only
// tests call it, through retrytest.Fast.
func SetScheduleForTest(s Schedule) (restore func()) {
	old := schedule
	schedule = s
	return func() { schedule = old }
}

// GaveUpError is Do's error once the budget runs out. Retryable never retries it, so retries nested inside
// each other stop together.
type GaveUpError struct {
	Operation string
	Attempts  int
	Elapsed   time.Duration
	Err       error
}

func (e *GaveUpError) Error() string {
	return fmt.Sprintf("%s: gave up after %d attempts over %s: %v", e.Operation, e.Attempts, e.Elapsed.Round(time.Millisecond), e.Err)
}

func (e *GaveUpError) Unwrap() error { return e.Err }

type options struct {
	beforeRetry func()
	retryable   func(error) bool
	attrs       []attribute.KeyValue
}

// Option configures one Do call.
type Option func(*options)

// WithBeforeRetry runs f before each retry, after the wait. Kafka callers close the broker connection the
// failed attempt used, so the retry opens a fresh one.
func WithBeforeRetry(f func()) Option { return func(o *options) { o.beforeRetry = f } }

// WithRetryable replaces Retryable as the test of which errors are retried.
func WithRetryable(f func(error) bool) Option { return func(o *options) { o.retryable = f } }

// WithAttributes adds attrs to every retry.attempt event. They must not name topics, groups or pods.
func WithAttributes(attrs ...attribute.KeyValue) Option {
	return func(o *options) { o.attrs = append(o.attrs, attrs...) }
}

// withoutRetriesKey marks a context under which Do calls fn once.
type withoutRetriesKey struct{}

// WithoutRetries returns a context under which Do calls fn once and returns its error unchanged, recording
// nothing: for callers that show their own errors and must not be held up or have warnings written over their
// output, such as a full-screen view.
func WithoutRetries(ctx context.Context) context.Context {
	return context.WithValue(ctx, withoutRetriesKey{}, true)
}

// recordAttempts sets kcp.retry.attempts on the span current in ctx once a call has needed more than one
// attempt. Every way Do returns after a retry has started calls it, so the count is there however the call ends.
func recordAttempts(ctx context.Context, attempt int) {
	if attempt > 1 {
		tracing.SetAttributes(ctx, attribute.Int("kcp.retry.attempts", attempt))
	}
}

// Do calls fn until it returns nil or an error the classifier (Retryable by default) rejects, the budget runs
// out, or ctx ends. A rejected error is returned unchanged, even once ctx has ended: it is the answer, and no
// retry was cut short. When ctx ends during an attempt that failed with an error the classifier accepts, or
// during the wait after one, Do returns ctx's error joined with fn's last error. Running out of budget returns
// a *GaveUpError wrapping the last error.
//
// Each retry adds a retry.attempt event to the span current in ctx, waits inside a wait.retry_backoff span, and
// logs a warning. A call that needed retries sets kcp.retry.attempts on that span however it ends, including
// when ctx ends during a retry, and kcp.retry.gave_up when it ran out of budget. A call that succeeds first time records nothing. Under WithoutRetries Do calls fn once
// and returns its error unchanged.
func Do(ctx context.Context, operation string, fn func(ctx context.Context) error, opts ...Option) error {
	if once, _ := ctx.Value(withoutRetriesKey{}).(bool); once {
		return fn(ctx)
	}
	o := options{retryable: Retryable}
	for _, opt := range opts {
		opt(&o)
	}
	s := schedule
	var firstFailure time.Time
	next := s.FirstWait
	for attempt := 1; ; attempt++ {
		err := fn(ctx)
		if err == nil {
			recordAttempts(ctx, attempt)
			return nil
		}
		if !o.retryable(err) {
			recordAttempts(ctx, attempt)
			return err
		}
		if ctx.Err() != nil {
			recordAttempts(ctx, attempt)
			return errors.Join(ctx.Err(), err)
		}
		if attempt == 1 {
			firstFailure = now()
		}
		elapsed := now().Sub(firstFailure)
		if elapsed+next > s.Budget {
			tracing.SetAttributes(ctx, attribute.Int("kcp.retry.attempts", attempt), attribute.Bool("kcp.retry.gave_up", true))
			slog.Warn("❌ giving up after connection errors", "operation", operation, "attempts", attempt,
				"elapsed", elapsed.Round(time.Millisecond), "error", err)
			return &GaveUpError{Operation: operation, Attempts: attempt, Elapsed: elapsed, Err: err}
		}
		attrs := append([]attribute.KeyValue{
			attribute.String("kcp.retry.operation", operation),
			attribute.Int("kcp.retry.attempt", attempt),
			attribute.Int64("kcp.retry.next_wait_ms", next.Milliseconds()),
			attribute.String("kcp.retry.error", err.Error()),
		}, o.attrs...)
		trace.SpanFromContext(ctx).AddEvent("retry.attempt", trace.WithAttributes(attrs...))
		slog.Warn("⚠️ connection error, retrying", "operation", operation, "attempt", attempt, "wait", next, "error", err)
		if werr := wait(ctx, next); werr != nil {
			recordAttempts(ctx, attempt)
			return errors.Join(werr, err)
		}
		if o.beforeRetry != nil {
			o.beforeRetry()
		}
		next = min(next*2, s.MaxWait)
	}
}
