package execute

import (
	"bytes"
	"context"
	"testing"

	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// commandWithRootSpan is a command whose context carries a root span, as the
// root hook leaves it in a real run.
func commandWithRootSpan() (*cobra.Command, trace.Span) {
	ctx, root := otel.Tracer("test").Start(context.Background(), "kcp migration execute")
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetContext(ctx)
	return cmd, root
}

// requireUnderRoot asserts migration.build_services and migration.run are
// direct children of root, in the same trace, and returns migration.run.
func requireUnderRoot(t *testing.T, spans []sdktrace.ReadOnlySpan, root trace.Span) sdktrace.ReadOnlySpan {
	t.Helper()
	run := tracingtest.Find(t, spans, "migration.run")
	for _, s := range []sdktrace.ReadOnlySpan{tracingtest.Find(t, spans, "migration.build_services"), run} {
		assert.Equal(t, root.SpanContext().TraceID(), s.SpanContext().TraceID(), "%s is in the command's trace", s.Name())
		assert.Equal(t, root.SpanContext().SpanID(), s.Parent().SpanID(), "%s is a child of the command span", s.Name())
	}
	return run
}

// requireStarted asserts run has a migration.started child carrying run's
// strategy and migration id, so a run can be listed while it is still going.
func requireStarted(t *testing.T, spans []sdktrace.ReadOnlySpan, run sdktrace.ReadOnlySpan) {
	t.Helper()
	started := tracingtest.Find(t, spans, "migration.started")
	assert.Equal(t, run.SpanContext().SpanID(), started.Parent().SpanID(), "migration.started is a child of migration.run")
	for _, key := range []string{"kcp.strategy", "kcp.migration.id"} {
		want, _ := tracingtest.Attr(run, key)
		got, ok := tracingtest.Attr(started, key)
		require.True(t, ok, "migration.started carries %s", key)
		assert.Equal(t, want, got, key)
	}
}

func TestExecute_StaticMode_TracesUnderTheCommandSpan(t *testing.T) {
	rec := tracingtest.Install(t)
	f := newFixture(t, nil)
	g := loadGateway(t, f.manifestPath)
	config := buildFreshMigrationConfig(g, g.Metadata.Name)
	cmd, root := commandWithRootSpan()

	require.NoError(t, runStaticBranch(cmd, g, &config, staticResult([]string{"t1.order"}), stubDeps(nil, nil)))
	root.End()

	spans := tracingtest.Spans(rec)
	run := requireUnderRoot(t, spans, root)
	requireStarted(t, spans, run)
	assert.Len(t, tracingtest.StepNames(tracingtest.Children(spans, run)), 8)
	tracingtest.AssertNoValues(t, spans, "t1.order")
}

func TestExecute_DynamicMode_TracesUnderTheCommandSpan(t *testing.T) {
	rec := tracingtest.Install(t)
	f := dynamicFixture(t)
	config := dynamicConfig(t, f, nil)
	g := loadGateway(t, f.manifestPath)
	cmd, root := commandWithRootSpan()

	require.NoError(t, runDynamicBranch(cmd, g, config, dynamicResult(config), stubDeps(nil, nil)))
	root.End()

	spans := tracingtest.Spans(rec)
	run := requireUnderRoot(t, spans, root)
	requireStarted(t, spans, run)
	strategy, _ := tracingtest.Attr(run, "kcp.strategy")
	assert.Equal(t, "tbm", strategy.AsString())
}

func TestExecute_ConvertMode_TracesUnderTheCommandSpan(t *testing.T) {
	rec := tracingtest.Install(t)
	f := convertFixture(t)
	g := loadGateway(t, f.manifestPath)
	config := buildFreshMigrationConfig(g, "msk-prod-to-cc-batch-1")
	w := newConvertWorld("orders-app")
	cmd, root := commandWithRootSpan()

	require.NoError(t, runConvertBranch(cmd, g, &config, convertPlanResult(), convertDeps(&patchRecordingGateway{}, w)))
	root.End()

	spans := tracingtest.Spans(rec)
	run := requireUnderRoot(t, spans, root)
	requireStarted(t, spans, run)
	strategy, _ := tracingtest.Attr(run, "kcp.strategy")
	assert.Equal(t, "d2s", strategy.AsString())
	tracingtest.AssertNoValues(t, spans, "orders-app", "orders")
}
