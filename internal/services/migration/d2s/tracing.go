package d2s

import (
	"context"

	"github.com/confluentinc/kcp/internal/tracing"
)

// tracerName is the instrumentation scope of this package's spans.
const tracerName = "kcp/services/migration/d2s"

// inSpan runs fn inside a span named name, a child of the span in ctx, and ends the span with fn's error (a
// refusal included, so a refused check shows as the span that refused).
func inSpan(ctx context.Context, name string, fn func(ctx context.Context) error) (err error) {
	ctx, span := tracing.Start(ctx, tracerName, name)
	defer func() { tracing.End(span, err) }()
	return fn(ctx)
}
