package tracing

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// writeOneTrace exports a parent and a child span through a fresh provider
// appending to path, and returns the trace ID.
func writeOneTrace(t *testing.T, path string) string {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	exp, err := otlptrace.New(context.Background(), &fileClient{f: f})
	require.NoError(t, err)
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	ctx, parent := tp.Tracer("test").Start(context.Background(), "parent")
	_, child := tp.Tracer("test").Start(ctx, "child")
	child.End()
	parent.End()
	require.NoError(t, tp.Shutdown(context.Background()))
	return parent.SpanContext().TraceID().String()
}

// readLines decodes every line of path as an OTLP/JSON export request.
func readLines(t *testing.T, path string) []ptrace.Traces {
	t.Helper()
	r, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	var out []ptrace.Traces
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		req := ptraceotlp.NewExportRequest()
		require.NoError(t, req.UnmarshalJSON(sc.Bytes()), "every line must be OTLP/JSON")
		out = append(out, req.Traces())
	}
	require.NoError(t, sc.Err())
	return out
}

func firstSpan(tr ptrace.Traces) ptrace.Span {
	return tr.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
}

func TestFileClient_WritesOTLPJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	traceID := writeOneTrace(t, path)

	lines := readLines(t, path)
	require.Len(t, lines, 2, "the syncer exports each span as it ends: child, then parent")

	child, parent := firstSpan(lines[0]), firstSpan(lines[1])
	assert.Equal(t, "child", child.Name())
	assert.Equal(t, "parent", parent.Name())
	assert.Equal(t, traceID, parent.TraceID().String(), "IDs are hex, as OTLP/JSON requires")
	assert.Equal(t, parent.SpanID(), child.ParentSpanID())
}

func TestFileClient_Appends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	first := writeOneTrace(t, path)
	second := writeOneTrace(t, path)

	lines := readLines(t, path)
	require.Len(t, lines, 4, "a second run appends rather than overwriting")
	assert.NotEqual(t, first, second)
	assert.Equal(t, second, firstSpan(lines[3]).TraceID().String())
}

func TestFileClient_HexIDsInRawJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	traceID := writeOneTrace(t, path)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"traceId":"`+traceID+`"`, "plain protojson would write base64 here")
	assert.Contains(t, string(raw), `"kind":1`, "enums are integers in OTLP/JSON")
}
