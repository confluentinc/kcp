// Package tracingtest installs an in-memory span recorder as the global tracer
// provider for one test, and helps assert on what was recorded.
package tracingtest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"
)

// Install makes a fresh recorder the global provider until the test ends. Tests
// using it must not call t.Parallel().
func Install(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(noop.NewTracerProvider())
		_ = tp.Shutdown(context.Background())
	})
	return rec
}

// Spans returns the ended spans in start order.
func Spans(rec *tracetest.SpanRecorder) []sdktrace.ReadOnlySpan {
	spans := rec.Ended()
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].StartTime().Before(spans[j].StartTime()) })
	return spans
}

// Find returns the first span named name, failing the test if there is none.
func Find(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	t.Fatalf("no span named %q among %v", name, Names(spans))
	return nil
}

// FindAll returns every span named name, in start order.
func FindAll(spans []sdktrace.ReadOnlySpan, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range spans {
		if s.Name() == name {
			out = append(out, s)
		}
	}
	return out
}

// Children returns parent's direct children, in start order.
func Children(spans []sdktrace.ReadOnlySpan, parent sdktrace.ReadOnlySpan) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range spans {
		if s.Parent().SpanID() == parent.SpanContext().SpanID() {
			out = append(out, s)
		}
	}
	return out
}

// Names returns each span's name.
func Names(spans []sdktrace.ReadOnlySpan) []string {
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.Name())
	}
	return out
}

// StepNames returns the names of the step.* spans, in start order.
func StepNames(spans []sdktrace.ReadOnlySpan) []string {
	var out []string
	for _, s := range spans {
		if strings.HasPrefix(s.Name(), "step.") {
			out = append(out, s.Name())
		}
	}
	return out
}

// Attr returns the span's attribute key.
func Attr(s sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

// ForbiddenValues lists every span attribute whose value, or any element of a
// slice value, equals one of forbidden, as "span.key=value".
func ForbiddenValues(spans []sdktrace.ReadOnlySpan, forbidden ...string) []string {
	bad := map[string]bool{}
	for _, f := range forbidden {
		bad[f] = true
	}
	var out []string
	for _, s := range spans {
		for _, kv := range s.Attributes() {
			vals := []string{kv.Value.String()}
			if kv.Value.Type() == attribute.STRINGSLICE {
				vals = kv.Value.AsStringSlice()
			}
			for _, v := range vals {
				if bad[v] {
					out = append(out, fmt.Sprintf("%s.%s=%s", s.Name(), kv.Key, v))
				}
			}
		}
	}
	return out
}

// AssertNoValues fails the test if any span attribute carries one of forbidden
// — the guard that keeps topic and group names out of traces.
func AssertNoValues(t *testing.T, spans []sdktrace.ReadOnlySpan, forbidden ...string) {
	t.Helper()
	if found := ForbiddenValues(spans, forbidden...); len(found) > 0 {
		t.Errorf("span attributes must not carry names, found: %v", found)
	}
}
