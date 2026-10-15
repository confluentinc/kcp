package migration

import (
	"context"
	"testing"

	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracing_PassThroughStepsAreMarkedSkipped(t *testing.T) {
	rec := tracingtest.Install(t)
	orch, _ := newHappyPathOrchestrator(t, nil) // no offset-sync pause, nothing to restore
	require.NoError(t, orch.Execute(context.Background(), 0, testAuth, uninitializedReconcileResult(nil)))

	spans := tracingtest.Spans(rec)
	for step, reason := range map[string]string{
		"step.pause_offset_sync":   "offset_sync_pause_not_requested",
		"step.restore_offset_sync": "nothing_to_restore",
	} {
		got, _ := tracingtest.Attr(tracingtest.Find(t, spans, step), "kcp.step.skip_reason")
		assert.Equal(t, reason, got.AsString(), step)
	}
	_, fenceSkipped := tracingtest.Attr(tracingtest.Find(t, spans, "step.fence"), "kcp.step.skipped")
	assert.False(t, fenceSkipped, "a fence that ran is not marked skipped")
}

func TestTracing_PromoteRecordsBatchesAndPolls(t *testing.T) {
	rec := tracingtest.Install(t)
	orch, _ := newHappyPathOrchestrator(t, []string{"topic-a", "topic-b"})
	orch.actions.SetPromoteBatchSize(1)
	require.NoError(t, orch.Execute(context.Background(), 0, testAuth, uninitializedReconcileResult(nil)))

	spans := tracingtest.Spans(rec)
	promote := tracingtest.Find(t, spans, "step.promote")
	batches, _ := tracingtest.Attr(promote, "kcp.batches")
	assert.Equal(t, int64(2), batches.AsInt64(), "batch size 1 over two topics → two batches")
	assert.Empty(t, promote.Events(), "each batch is a clusterlink.promote_mirror_topics span, not an event")
	polls, _ := tracingtest.Attr(promote, "kcp.polls")
	assert.GreaterOrEqual(t, polls.AsInt64(), int64(2))
	assert.NotEmpty(t, tracingtest.FindAll(spans, "wait.promote_poll"))
	lagPolls, _ := tracingtest.Attr(tracingtest.Find(t, spans, "step.wait_for_lags"), "kcp.polls")
	assert.GreaterOrEqual(t, lagPolls.AsInt64(), int64(1))
	tracingtest.AssertNoValues(t, spans, "topic-a", "topic-b")
}
