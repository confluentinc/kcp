package tracing

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type recorder struct {
	mu   sync.Mutex
	recs []sdklog.Record
}

func (r *recorder) Export(_ context.Context, recs []sdklog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range recs {
		r.recs = append(r.recs, rec.Clone())
	}
	return nil
}
func (r *recorder) Shutdown(context.Context) error   { return nil }
func (r *recorder) ForceFlush(context.Context) error { return nil }

func TestLogHandlerCarriesRootTraceIDAndKeepsDebug(t *testing.T) {
	rec := &recorder{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(rec)))
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(tracetest.NewInMemoryExporter())))
	_, root := tp.Tracer("t").Start(context.Background(), "kcp")

	prev := active
	active = &session{logs: lp, root: root}
	t.Cleanup(func() { active = prev })

	logger := slog.New(LogHandler())
	logger.Debug("debug line")
	logger.Info("info line", "k", "v")

	// kcp.log keeps Debug and above; the OTLP logs carry the same lines.
	if len(rec.recs) != 2 {
		t.Fatalf("got %d records, want 2 (Debug and Info)", len(rec.recs))
	}
	for _, r := range rec.recs {
		if got, want := r.TraceID(), root.SpanContext().TraceID(); got != want {
			t.Errorf("%q: trace id = %s, want the root span's %s", r.Body().AsString(), got, want)
		}
	}
}

func TestLogHandlerNilWhenOTLPOff(t *testing.T) {
	prev := active
	active = &session{}
	t.Cleanup(func() { active = prev })
	if LogHandler() != nil {
		t.Error("want nil without a log provider")
	}
}
