package d2s

import (
	"context"
	"testing"

	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracing_D2S_VerifyAndSyncInnerSpans(t *testing.T) {
	rec := tracingtest.Install(t)
	run := newTestRun((&patchRecorder{}).service(), cleanWorld().deps())
	require.NoError(t, run.execute(context.Background(), convertResult()))

	spans := tracingtest.Spans(rec)
	verify := tracingtest.Find(t, spans, "step.verify_fence")
	assert.Subset(t, tracingtest.Names(tracingtest.Children(spans, verify)),
		[]string{"groupoffsets.snapshot", "groupoffsets.detect_rogue_commits"})
	sync := tracingtest.Find(t, spans, "step.sync_offsets")
	assert.Equal(t, []string{"groupoffsets.destination_hwm", "groupoffsets.apply"},
		tracingtest.Names(tracingtest.Children(spans, sync)))
	tracingtest.AssertNoValues(t, spans, "orders-app", "orders")
}

func TestTracing_D2S_ZeroWindowIsMarkedSkipped(t *testing.T) {
	rec := tracingtest.Install(t)
	run := newTestRun((&patchRecorder{}).service(), cleanWorld().deps())
	require.NoError(t, run.orch.Execute(context.Background(), convertResult(), Policy{Workers: 2}))

	spans := tracingtest.Spans(rec)
	verify := tracingtest.Find(t, spans, "step.verify_fence")
	reason, _ := tracingtest.Attr(verify, "kcp.step.skip_reason")
	assert.Equal(t, "detection_disabled", reason.AsString())
	children := tracingtest.Names(tracingtest.Children(spans, verify))
	assert.Contains(t, children, "groupoffsets.snapshot")
	assert.NotContains(t, children, "groupoffsets.detect_rogue_commits")
	assert.NotContains(t, tracingtest.Names(spans), "wait.rogue_commit_window")
}
