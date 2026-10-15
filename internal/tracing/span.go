package tracing

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// scope is the instrumentation scope of spans this package opens itself (the
// root span and wait.* spans).
const scope = "kcp/tracing"

// Start opens a span named name, a child of the span in ctx. It asks the
// global provider for the tracer on every call: tests install a fresh provider
// per test, and a tracer cached earlier would stay bound to the old one.
func Start(ctx context.Context, scope, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(scope).Start(ctx, name, trace.WithAttributes(attrs...))
}

// End closes span, recording err (an exception event and an error status)
// when it is non-nil.
func End(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// SetAttributes annotates the span current in ctx.
func SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	trace.SpanFromContext(ctx).SetAttributes(attrs...)
}

// MarkSkipped flags the span current in ctx — a step span — as a pass-through
// that did no work, and why.
func MarkSkipped(ctx context.Context, reason string) {
	SetAttributes(ctx, attribute.Bool("kcp.step.skipped", true), attribute.String("kcp.step.skip_reason", reason))
}

// Wait sleeps for d inside a wait.<reason> span, so a trace separates time kcp
// spends waiting on purpose from time spent waiting on a cluster. It returns
// ctx.Err() if ctx ends first.
func Wait(ctx context.Context, reason string, d time.Duration) error {
	ctx, span := Start(ctx, scope, "wait."+reason, attribute.Int64("kcp.wait.configured_ms", d.Milliseconds()))
	timer := time.NewTimer(d)
	defer timer.Stop()
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case <-timer.C:
	}
	End(span, err)
	return err
}
