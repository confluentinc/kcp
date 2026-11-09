package gateway

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/confluentinc/kcp/internal/tracing"
)

// phaseSpan is a span a poll loop opens the first time it enters a phase and
// closes when the phase ends or the wait does. It times the part of a wait that
// a plainer check would not have made, so a trace shows what the stricter wait
// costs on its own. The zero value is ready to use; enter and leave are
// idempotent, so a loop can call enter on every poll and defer leave once.
type phaseSpan struct {
	span trace.Span
}

// enter opens the span, a child of the span in ctx, unless it is already open.
func (p *phaseSpan) enter(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	if p.span != nil {
		return
	}
	_, p.span = tracing.Start(ctx, tracerName, name, attrs...)
}

// leave closes the span, recording err when it is non-nil. It does nothing when
// the span was never opened or is already closed.
func (p *phaseSpan) leave(err error) {
	if p.span == nil {
		return
	}
	tracing.End(p.span, err)
	p.span = nil
}
