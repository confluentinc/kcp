package retry_test

import (
	"context"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/confluentinc/kcp/internal/retry"
	"github.com/confluentinc/kcp/internal/retry/retrytest"
	"github.com/confluentinc/kcp/internal/tracing"
	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var reset = &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}

func retryEvents(span sdktrace.ReadOnlySpan) []sdktrace.Event {
	var out []sdktrace.Event
	for _, e := range span.Events() {
		if e.Name == "retry.attempt" {
			out = append(out, e)
		}
	}
	return out
}

func TestDo_RecordsEachRetryOnTheCurrentSpan(t *testing.T) {
	rec := tracingtest.Install(t)
	retrytest.Fast(t)
	ctx, span := tracing.Start(context.Background(), "test", "step.verify_fence")
	calls := 0

	err := retry.Do(ctx, "kafka.list_groups", func(context.Context) error {
		calls++
		if calls < 3 {
			return reset
		}
		return nil
	}, retry.WithAttributes(attribute.String("kcp.cluster", "source")))
	span.End()

	require.NoError(t, err)
	spans := tracingtest.Spans(rec)
	step := tracingtest.Find(t, spans, "step.verify_fence")
	events := retryEvents(step)
	require.Len(t, events, 2)
	assert.Contains(t, events[0].Attributes, attribute.String("kcp.retry.operation", "kafka.list_groups"))
	assert.Contains(t, events[0].Attributes, attribute.Int("kcp.retry.attempt", 1))
	assert.Contains(t, events[1].Attributes, attribute.Int("kcp.retry.attempt", 2))
	assert.Contains(t, events[0].Attributes, attribute.String("kcp.cluster", "source"))
	assert.Contains(t, step.Attributes(), attribute.Int("kcp.retry.attempts", 3))
	assert.Len(t, tracingtest.FindAll(spans, "wait.retry_backoff"), 2, "each wait is its own span")
}

func TestDo_RecordsNothingWhenTheFirstAttemptSucceeds(t *testing.T) {
	rec := tracingtest.Install(t)
	retrytest.Fast(t)
	ctx, span := tracing.Start(context.Background(), "test", "step.promote")

	require.NoError(t, retry.Do(ctx, "rest.promote", func(context.Context) error { return nil }))
	span.End()

	step := tracingtest.Find(t, tracingtest.Spans(rec), "step.promote")
	assert.Empty(t, retryEvents(step))
	for _, kv := range step.Attributes() {
		assert.NotEqual(t, attribute.Key("kcp.retry.attempts"), kv.Key)
	}
}

func TestDo_MarksTheSpanWhenItGivesUp(t *testing.T) {
	rec := tracingtest.Install(t)
	retrytest.Fast(t)
	ctx, span := tracing.Start(context.Background(), "test", "step.verify_fence")

	err := retry.Do(ctx, "kafka.list_offsets", func(context.Context) error { return reset })
	span.End()

	var gave *retry.GaveUpError
	require.ErrorAs(t, err, &gave)
	step := tracingtest.Find(t, tracingtest.Spans(rec), "step.verify_fence")
	assert.Contains(t, step.Attributes(), attribute.Bool("kcp.retry.gave_up", true))
	assert.Contains(t, step.Attributes(), attribute.Int("kcp.retry.attempts", gave.Attempts))
}

func TestDo_RecordsTheAttemptCountWhenTheContextEndsAfterAFailedRetry(t *testing.T) {
	rec := tracingtest.Install(t)
	retrytest.Fast(t)
	ctx, span := tracing.Start(context.Background(), "test", "step.verify_fence")
	ctx, cancel := context.WithCancel(ctx)
	calls := 0

	err := retry.Do(ctx, "kafka.list_groups", func(context.Context) error {
		calls++
		if calls == 2 {
			cancel()
		}
		return reset
	})
	span.End()

	require.ErrorIs(t, err, context.Canceled)
	step := tracingtest.Find(t, tracingtest.Spans(rec), "step.verify_fence")
	assert.Contains(t, step.Attributes(), attribute.Int("kcp.retry.attempts", 2))
	assert.NotContains(t, step.Attributes(), attribute.Bool("kcp.retry.gave_up", true))
}
