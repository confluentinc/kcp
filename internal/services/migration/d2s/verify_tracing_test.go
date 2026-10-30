package d2s

import (
	"context"
	"errors"
	"testing"

	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
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

func TestTracing_D2S_VerifyFenceSpansEachPart(t *testing.T) {
	rec := tracingtest.Install(t)
	run := newTestRun((&patchRecorder{}).service(), cleanWorld().deps())
	require.NoError(t, run.execute(context.Background(), convertResult()))

	spans := tracingtest.Spans(rec)
	verify := tracingtest.Find(t, spans, "step.verify_fence")
	children := tracingtest.Children(spans, verify)
	assert.Equal(t, []string{
		"d2s.gather_facts",
		"groupoffsets.list_groups", "groupoffsets.snapshot",
		"d2s.check_link_scope",
		"groupoffsets.detect_rogue_commits",
		"groupoffsets.list_groups", "d2s.check_split_brain",
	}, tracingtest.Names(children))

	var clusters []string
	for _, s := range children {
		if s.Name() == "groupoffsets.list_groups" {
			c, _ := tracingtest.Attr(s, "kcp.cluster")
			clusters = append(clusters, c.AsString())
		}
	}
	assert.Equal(t, []string{"source", "destination"}, clusters)

	scope := tracingtest.Find(t, spans, "d2s.check_link_scope")
	inScope, ok := tracingtest.Attr(scope, "kcp.groups.in_scope")
	require.True(t, ok)
	assert.Equal(t, int64(1), inScope.AsInt64(), "cleanWorld seeds one group, orders-app")
	tracingtest.AssertNoValues(t, spans, "orders-app", "orders")
}

func TestTracing_D2S_ZeroWindowListsTheSourceOnce(t *testing.T) {
	rec := tracingtest.Install(t)
	run := newTestRun((&patchRecorder{}).service(), cleanWorld().deps())
	require.NoError(t, run.orch.Execute(context.Background(), convertResult(), Policy{Workers: 2}))

	var sources int
	for _, s := range tracingtest.FindAll(tracingtest.Spans(rec), "groupoffsets.list_groups") {
		if c, _ := tracingtest.Attr(s, "kcp.cluster"); c.AsString() == "source" {
			sources++
		}
	}
	assert.Equal(t, 1, sources)
}

func TestTracing_D2S_SplitBrainRefusalEndsItsSpanWithError(t *testing.T) {
	rec := tracingtest.Install(t)
	w := cleanWorld()
	deps := w.deps()
	deps.DestinationGroups = func(context.Context) ([]types.ConsumerGroupListing, error) {
		return []types.ConsumerGroupListing{{GroupID: "orders-app", State: "Stable"}}, nil
	}
	run := newTestRun((&patchRecorder{}).service(), deps)

	err := run.execute(context.Background(), convertResult())
	require.True(t, errors.Is(err, ErrVerifyRefused), "a split-brain refusal is still ErrVerifyRefused: %v", err)

	s := tracingtest.Find(t, tracingtest.Spans(rec), "d2s.check_split_brain")
	assert.Equal(t, codes.Error, s.Status().Code)
}
