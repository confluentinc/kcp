package tracing

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
)

// newLogProvider exports kcp's log records over OTLP/HTTP, with the same
// resource as the spans, so a log line carries the same harness.* attributes
// (and so the same invocation id) as the trace it belongs to. The exporter
// reads OTEL_EXPORTER_OTLP_LOGS_ENDPOINT, else OTEL_EXPORTER_OTLP_ENDPOINT.
func newLogProvider(ctx context.Context, res *resource.Resource) (*sdklog.LoggerProvider, error) {
	exp, err := otlploghttp.New(ctx)
	if err != nil {
		return nil, err
	}
	return sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
	), nil
}

// LogHandler returns the slog handler that sends records over OTLP, or nil
// when OTLP export is off. Add it to the fan-out beside the file and console
// handlers. It takes every level kcp.log takes, Debug and above.
func LogHandler() slog.Handler {
	if active == nil || active.logs == nil {
		return nil
	}
	return &otelHandler{Handler: otelslog.NewHandler(scope, otelslog.WithLoggerProvider(active.logs))}
}

// otelHandler gives each record the command's root span: kcp logs through
// slog.Info and friends, which carry no context, so a record would have no
// trace id. It gets the root span's, which ties every line of an invocation
// to its trace.
type otelHandler struct {
	slog.Handler
}

func (h *otelHandler) Handle(ctx context.Context, r slog.Record) error {
	if !trace.SpanContextFromContext(ctx).IsValid() && active != nil && active.root != nil {
		ctx = trace.ContextWithSpanContext(ctx, active.root.SpanContext())
	}
	return h.Handler.Handle(ctx, r)
}

func (h *otelHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return &otelHandler{Handler: h.Handler.WithAttrs(a)}
}

func (h *otelHandler) WithGroup(n string) slog.Handler {
	return &otelHandler{Handler: h.Handler.WithGroup(n)}
}
