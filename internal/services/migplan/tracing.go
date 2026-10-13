package migplan

import (
	"context"

	"github.com/confluentinc/kcp/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
)

// tracerName is the instrumentation scope of this package's spans.
const tracerName = "kcp/services/migplan"

// traced runs fn inside a span named name.
func traced[T any](ctx context.Context, name string, fn func(context.Context) (T, error), attrs ...attribute.KeyValue) (T, error) {
	ctx, span := tracing.Start(ctx, tracerName, name, attrs...)
	v, err := fn(ctx)
	tracing.End(span, err)
	return v, err
}

func clusterAttr(c string) attribute.KeyValue { return attribute.String("kcp.cluster", c) }
