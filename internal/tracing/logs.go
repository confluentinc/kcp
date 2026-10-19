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
// handlers.
func LogHandler() slog.Handler {
	if active == nil || active.logs == nil {
		return nil
	}
	return &otelHandler{
		Handler: otelslog.NewHandler(scope, otelslog.WithLoggerProvider(active.logs)),
		min:     slog.LevelInfo,
	}
}

// otelHandler wraps the OTel bridge with two things it lacks:
//   - an Info floor: the file keeps Debug, the log store does not need it;
//   - the command's root span: kcp logs through slog.Info and friends, which
//     carry no context, so a record would have no trace id. It gets the root
//     span's, which ties every line of an invocation to its trace.
type otelHandler struct {
	slog.Handler
	min slog.Level
}

func (h *otelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.Handler.Enabled(ctx, l)
}

func (h *otelHandler) Handle(ctx context.Context, r slog.Record) error {
	if !trace.SpanContextFromContext(ctx).IsValid() && active != nil && active.root != nil {
		ctx = trace.ContextWithSpanContext(ctx, active.root.SpanContext())
	}
	return h.Handler.Handle(ctx, r)
}

func (h *otelHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return &otelHandler{Handler: h.Handler.WithAttrs(a), min: h.min}
}

func (h *otelHandler) WithGroup(n string) slog.Handler {
	return &otelHandler{Handler: h.Handler.WithGroup(n), min: h.min}
}
