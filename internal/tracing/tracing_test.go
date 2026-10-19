package tracing

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"
)

func resetGlobal(t *testing.T) {
	t.Cleanup(func() {
		active = nil
		otel.SetTracerProvider(noop.NewTracerProvider())
	})
}

// spansByName decodes the trace file at path, keyed by span name.
func spansByName(t *testing.T, path string) map[string]ptrace.Span {
	t.Helper()
	r, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	out := map[string]ptrace.Span{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		req := ptraceotlp.NewExportRequest()
		require.NoError(t, req.UnmarshalJSON(sc.Bytes()))
		rss := req.Traces().ResourceSpans()
		for i := 0; i < rss.Len(); i++ {
			sss := rss.At(i).ScopeSpans()
			for j := 0; j < sss.Len(); j++ {
				ss := sss.At(j).Spans()
				for k := 0; k < ss.Len(); k++ {
					out[ss.At(k).Name()] = ss.At(k)
				}
			}
		}
	}
	return out
}

func TestSetup_Off_InstallsNothing(t *testing.T) {
	resetGlobal(t)
	t.Setenv(EnvEnabled, "")
	before := otel.GetTracerProvider()
	Setup(context.Background())
	assert.Nil(t, active)
	assert.Equal(t, before, otel.GetTracerProvider(), "off leaves the global provider in place")
	ctx := context.Background()
	assert.Equal(t, ctx, StartRoot(ctx, "kcp x"), "StartRoot is a no-op when off")
	Shutdown(nil) // must not panic
}

func TestSetupShutdown_WritesARootSpanWithTheCommandError(t *testing.T) {
	resetGlobal(t)
	path := filepath.Join(t.TempDir(), "t.jsonl")
	t.Setenv(EnvEnabled, "true")
	t.Setenv(EnvTraceFile, path)
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "run=perf-1")

	Setup(context.Background())
	require.NotNil(t, active)
	ctx := StartRoot(context.TODO(), "kcp migration execute")
	_, child := Start(ctx, "test", "migration.run")
	End(child, nil)
	Shutdown(errors.New("failed to execute migration"))

	spans := spansByName(t, path)
	root, ok := spans["kcp migration execute"]
	require.True(t, ok, "root span written at shutdown")
	assert.Equal(t, ptrace.StatusCodeError, root.Status().Code())
	assert.Equal(t, root.SpanID(), spans["migration.run"].ParentSpanID())
	assert.Nil(t, active, "Shutdown clears the session")
}

func TestSetup_WarnsWhenResourceAttributesAreIgnored(t *testing.T) {
	resetGlobal(t)
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	t.Setenv(EnvEnabled, "true")
	t.Setenv(EnvTraceFile, filepath.Join(t.TempDir(), "t.jsonl"))
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "run:perf-1") // ':' for '=': the SDK drops the entry

	Setup(context.Background())
	t.Cleanup(func() { Shutdown(nil) })

	require.NotNil(t, active, "a malformed entry doesn't stop tracing")
	assert.Contains(t, logs.String(), "OTEL_RESOURCE_ATTRIBUTES entries were ignored")
}

func TestSetup_OTLP_InstallsALogHandler(t *testing.T) {
	resetGlobal(t)
	t.Setenv(EnvEnabled, "true")
	t.Setenv(EnvTraceFile, filepath.Join(t.TempDir(), "t.jsonl"))
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1") // nothing listens; nothing is sent before Shutdown

	Setup(context.Background())
	t.Cleanup(func() { Shutdown(nil) })

	require.NotNil(t, active)
	assert.NotNil(t, LogHandler(), "OTLP spans bring OTLP logs")
}

func TestSetup_FileOnly_NoLogHandler(t *testing.T) {
	resetGlobal(t)
	t.Setenv(EnvEnabled, "true")
	t.Setenv(EnvTraceFile, filepath.Join(t.TempDir(), "t.jsonl"))
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	Setup(context.Background())
	t.Cleanup(func() { Shutdown(nil) })

	require.NotNil(t, active)
	assert.Nil(t, LogHandler(), "the trace file gets no logs")
}
