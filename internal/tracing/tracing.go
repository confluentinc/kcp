package tracing

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// shutdownTimeout bounds the final flush, so an unreachable collector can't
// hold kcp's exit.
const shutdownTimeout = 5 * time.Second

type session struct {
	provider *sdktrace.TracerProvider
	root     trace.Span
}

// active is the process's tracing session; nil when tracing is off.
var active *session

func openAppend(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}

// Setup reads the tracing configuration and, when tracing is on, installs the
// global tracer provider. It never fails the command: every problem is logged
// as a warning and kcp carries on, with or without tracing.
func Setup(ctx context.Context) {
	c := resolveConfig(os.Getenv, openAppend)
	for _, w := range c.warnings {
		slog.Warn("⚠️ " + w)
	}
	if !c.enabled {
		return
	}
	tp, err := newProvider(ctx, c)
	if err != nil {
		slog.Warn("⚠️ tracing could not start; continuing without it", "error", err)
		if c.file != nil {
			_ = c.file.Close()
		}
		return
	}
	otel.SetTracerProvider(tp)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.Warn("⚠️ trace export failed", "error", err)
	}))
	active = &session{provider: tp}
	slog.Info("✅ tracing enabled", "file", c.filePath, "otlp", c.otlp)
}

func newProvider(ctx context.Context, c config) (*sdktrace.TracerProvider, error) {
	res, err := resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceName("kcp"),
			semconv.ServiceVersion(build_info.Version),
			attribute.String("kcp.build.commit", build_info.Commit),
		),
		resource.WithFromEnv(), // OTEL_RESOURCE_ATTRIBUTES, OTEL_SERVICE_NAME
	)
	switch {
	case errors.Is(err, resource.ErrPartialResource):
		// A malformed entry (e.g. run:x for run=x) is dropped; say so, or the run
		// goes unlabelled with no hint why.
		slog.Warn("⚠️ some OTEL_RESOURCE_ATTRIBUTES entries were ignored", "error", err)
	case err != nil:
		return nil, err
	}
	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		// Explicit, so an OTEL_TRACES_SAMPLER in the environment can't drop spans.
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	}
	if c.file != nil {
		exp, err := otlptrace.New(ctx, &fileClient{f: c.file})
		if err != nil {
			return nil, err
		}
		opts = append(opts, sdktrace.WithSyncer(exp))
	}
	if c.otlp {
		exp, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, err
		}
		opts = append(opts, sdktrace.WithBatcher(exp))
	}
	return sdktrace.NewTracerProvider(opts...), nil
}

// StartRoot opens the command's root span, named after the command path
// ("kcp migration execute"), and returns ctx carrying it. It returns ctx
// unchanged when tracing is off.
func StartRoot(ctx context.Context, name string) context.Context {
	if active == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, active.root = otel.Tracer(scope).Start(ctx, name)
	return ctx
}

// Shutdown ends the root span — as an error when the command failed — and
// flushes every exporter. It runs from main on every exit path that returns,
// so a failed run's trace is written too.
func Shutdown(cmdErr error) {
	if active == nil {
		return
	}
	s := active
	active = nil
	if s.root != nil {
		End(s.root, cmdErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := s.provider.Shutdown(ctx); err != nil {
		slog.Warn("⚠️ failed to flush traces", "error", err)
	}
}
