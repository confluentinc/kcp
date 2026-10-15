package tbm

import (
	"context"
	"testing"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
)

func TestTracing_TBMExecute_StepSpansUnderTheRunSpan(t *testing.T) {
	rec := tracingtest.Install(t)
	orch, _ := newTestOrchestrator(t)
	require.NoError(t, orch.Execute(context.Background(), realisticReconcileResult(), 10, 0, clusterlink.BasicAuth{}))

	spans := tracingtest.Spans(rec)
	run := tracingtest.Find(t, spans, "migration.run")
	strategy, _ := tracingtest.Attr(run, "kcp.strategy")
	assert.Equal(t, "tbm", strategy.AsString())
	assert.Equal(t, []string{
		"step.initialize", "step.wait_for_lags", "step.fence", "step.verify_fence", "step.promote", "step.switch",
	}, tracingtest.StepNames(tracingtest.Children(spans, run)))
	tracingtest.AssertNoValues(t, spans, "t1.order")
}

func TestTracing_TBMExecute_RollbackSpanAfterAFailureWhileFenced(t *testing.T) {
	rec := tracingtest.Install(t)
	t.Setenv(killpoint.FailEnvVar, EventVerifyFence)
	orch, _ := newTestOrchestrator(t)
	require.Error(t, orch.Execute(context.Background(), realisticReconcileResult(), 10, 0, clusterlink.BasicAuth{}))

	spans := tracingtest.Spans(rec)
	rollback := tracingtest.Find(t, spans, "migration.rollback")
	path, _ := tracingtest.Attr(rollback, "kcp.rollback.path")
	assert.Equal(t, migration.RollbackPathAbortFence, path.AsString())
	abort := tracingtest.Find(t, spans, "step.abort_fence")
	assert.Equal(t, rollback.SpanContext().SpanID(), abort.Parent().SpanID())
	assert.Equal(t, codes.Error, tracingtest.Find(t, spans, "step.verify_fence").Status().Code)
}
