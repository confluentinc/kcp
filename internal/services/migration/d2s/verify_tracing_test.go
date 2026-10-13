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
		[]string{"groupoffsets.list_groups", "groupoffsets.snapshot", "groupoffsets.detect_rogue_commits"})
	sync := tracingtest.Find(t, spans, "step.sync_offsets")
	assert.Equal(t, []string{"groupoffsets.destination_hwm", "groupoffsets.build_plan", "groupoffsets.apply"},
		tracingtest.Names(tracingtest.Children(spans, sync)))
	tracingtest.AssertNoValues(t, spans, "orders-app", "orders")
}
