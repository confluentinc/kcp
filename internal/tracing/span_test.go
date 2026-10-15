package tracing_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/tracing"
	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

func TestStartEnd_RecordsErrorStatus(t *testing.T) {
	rec := tracingtest.Install(t)
	ctx, parent := tracing.Start(context.Background(), "test", "parent", attribute.Int("kcp.topics", 3))
	_, child := tracing.Start(ctx, "test", "child")
	tracing.End(child, errors.New("boom"))
	tracing.End(parent, nil)

	spans := tracingtest.Spans(rec)
	p := tracingtest.Find(t, spans, "parent")
	c := tracingtest.Find(t, spans, "child")
	assert.Equal(t, p.SpanContext().SpanID(), c.Parent().SpanID())
	assert.Equal(t, codes.Error, c.Status().Code)
	assert.Equal(t, "boom", c.Status().Description)
	require.Len(t, c.Events(), 1, "RecordError adds an exception event")
	assert.Equal(t, codes.Unset, p.Status().Code)
	v, ok := tracingtest.Attr(p, "kcp.topics")
	require.True(t, ok)
	assert.Equal(t, int64(3), v.AsInt64())
}

func TestMarkSkippedAndSetAttributes_AnnotateTheCurrentSpan(t *testing.T) {
	rec := tracingtest.Install(t)
	ctx, s := tracing.Start(context.Background(), "test", "step.fence")
	tracing.MarkSkipped(ctx, "no_fence_in_plan")
	tracing.SetAttributes(ctx, attribute.Int("kcp.polls", 4))
	tracing.End(s, nil)

	span := tracingtest.Find(t, tracingtest.Spans(rec), "step.fence")
	skipped, _ := tracingtest.Attr(span, "kcp.step.skipped")
	reason, _ := tracingtest.Attr(span, "kcp.step.skip_reason")
	polls, _ := tracingtest.Attr(span, "kcp.polls")
	assert.True(t, skipped.AsBool())
	assert.Equal(t, "no_fence_in_plan", reason.AsString())
	assert.Equal(t, int64(4), polls.AsInt64())
}

func TestWait_SpansTheWaitAndHonoursCancellation(t *testing.T) {
	rec := tracingtest.Install(t)
	require.NoError(t, tracing.Wait(context.Background(), "promote_poll", time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, tracing.Wait(ctx, "promote_poll", time.Hour), context.Canceled)

	waits := tracingtest.FindAll(tracingtest.Spans(rec), "wait.promote_poll")
	require.Len(t, waits, 2)
	ms, _ := tracingtest.Attr(waits[0], "kcp.wait.configured_ms")
	assert.Equal(t, int64(1), ms.AsInt64())
	assert.Equal(t, codes.Error, waits[1].Status().Code, "a cancelled wait is recorded as an error")
}

func TestForbiddenValues_FlagsAForbiddenAttributeValue(t *testing.T) {
	rec := tracingtest.Install(t)
	_, s := tracing.Start(context.Background(), "test", "x", attribute.String("kcp.note", "orders"))
	tracing.End(s, nil)
	assert.Equal(t, []string{"x.kcp.note=orders"}, tracingtest.ForbiddenValues(tracingtest.Spans(rec), "orders"))
}
