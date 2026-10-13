package tbm

import (
	"context"
	"testing"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracing_TBM_DetectionDisabledIsMarkedSkipped(t *testing.T) {
	rec := tracingtest.Install(t)
	orch, _ := newTestOrchestrator(t)
	require.NoError(t, orch.Execute(context.Background(), realisticReconcileResult(), 10, 0, clusterlink.BasicAuth{}))

	spans := tracingtest.Spans(rec)
	reason, _ := tracingtest.Attr(tracingtest.Find(t, spans, "step.verify_fence"), "kcp.step.skip_reason")
	assert.Equal(t, "detection_disabled", reason.AsString())
	batches, _ := tracingtest.Attr(tracingtest.Find(t, spans, "step.promote"), "kcp.batches")
	assert.GreaterOrEqual(t, batches.AsInt64(), int64(1))
}
