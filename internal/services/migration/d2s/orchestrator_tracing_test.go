package d2s

import (
	"context"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracing_D2SExecute_StepSpansUnderTheRunSpan(t *testing.T) {
	rec := tracingtest.Install(t)
	run := newTestRun((&patchRecorder{}).service(), cleanWorld().deps())
	require.NoError(t, run.execute(context.Background(), convertResult()))

	spans := tracingtest.Spans(rec)
	runSpan := tracingtest.Find(t, spans, "migration.run")
	strategy, _ := tracingtest.Attr(runSpan, "kcp.strategy")
	assert.Equal(t, "d2s", strategy.AsString())
	assert.Equal(t, []string{
		"step.initialize", "step.fence", "step.verify_fence", "step.sync_offsets", "step.switch",
	}, tracingtest.StepNames(tracingtest.Children(spans, runSpan)))
	tracingtest.AssertNoValues(t, spans, "orders-app", "orders")
}

func TestTracing_D2SExecute_RollbackSpanAfterAFailureWhileFenced(t *testing.T) {
	rec := tracingtest.Install(t)
	t.Setenv(killpoint.FailEnvVar, EventSyncOffsets)
	run := newTestRun((&patchRecorder{}).service(), cleanWorld().deps())
	require.Error(t, run.execute(context.Background(), convertResult()))

	spans := tracingtest.Spans(rec)
	rollback := tracingtest.Find(t, spans, "migration.rollback")
	path, _ := tracingtest.Attr(rollback, "kcp.rollback.path")
	assert.Equal(t, migration.RollbackPathAbortFence, path.AsString())
	abort := tracingtest.Find(t, spans, "step.abort_fence")
	assert.Equal(t, rollback.SpanContext().SpanID(), abort.Parent().SpanID())
}
