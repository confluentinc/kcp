package groupoffsets

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
)

func TestTracing_DetectRogueCommits_SpansWaitAndSnapshot(t *testing.T) {
	rec := tracingtest.Install(t)
	offs := Offsets{"orders": {0: at(5)}}
	factory, _, _ := factoryFor(&fakeFetcher{offsets: map[string]Offsets{"orders-app": offs}})
	first := Snapshot{"orders-app": offs}
	lister := func(context.Context) ([]string, error) { return []string{"orders-app"}, nil }

	_, err := DetectRogueCommits(context.Background(), first, lister, factory, 2, time.Millisecond, Sleep)
	require.NoError(t, err)

	spans := tracingtest.Spans(rec)
	detect := tracingtest.Find(t, spans, "groupoffsets.detect_rogue_commits")
	assert.Equal(t, []string{"wait.rogue_commit_window", "groupoffsets.snapshot"},
		tracingtest.Names(tracingtest.Children(spans, detect)))
	snap := tracingtest.Find(t, spans, "groupoffsets.snapshot")
	n, _ := tracingtest.Attr(snap, "kcp.groups.with_commits")
	assert.Equal(t, int64(1), n.AsInt64())
	tracingtest.AssertNoValues(t, spans, "orders-app", "orders")
}

func TestTracing_TakeSnapshot_OpensOneClientSpanPerWorker(t *testing.T) {
	rec := tracingtest.Install(t)
	offs := Offsets{"orders": {0: at(5)}}
	factory, _, _ := factoryFor(&fakeFetcher{offsets: map[string]Offsets{"a": offs, "b": offs, "c": offs}})

	_, err := TakeSnapshot(context.Background(), factory, []string{"a", "b", "c"}, 2)
	require.NoError(t, err)

	spans := tracingtest.Spans(rec)
	snap := tracingtest.Find(t, spans, "groupoffsets.snapshot")
	opens := tracingtest.Children(spans, snap)
	assert.Equal(t, []string{"groupoffsets.open_client", "groupoffsets.open_client"}, tracingtest.Names(opens))
	var workers []int64
	for _, s := range opens {
		v, ok := tracingtest.Attr(s, "kcp.worker")
		require.True(t, ok, "open_client carries kcp.worker")
		workers = append(workers, v.AsInt64())
	}
	assert.ElementsMatch(t, []int64{0, 1}, workers)
}

func TestTracing_TakeSnapshot_NoMoreClientSpansThanGroups(t *testing.T) {
	rec := tracingtest.Install(t)
	factory, _, _ := factoryFor(&fakeFetcher{offsets: map[string]Offsets{"a": {"orders": {0: at(1)}}}})

	_, err := TakeSnapshot(context.Background(), factory, []string{"a"}, 8)
	require.NoError(t, err)

	assert.Len(t, tracingtest.FindAll(tracingtest.Spans(rec), "groupoffsets.open_client"), 1)
}

func TestTracing_TakeSnapshot_FailedClientEndsItsSpanWithError(t *testing.T) {
	rec := tracingtest.Install(t)
	denied := errors.New("SASL authentication failed")
	factory := func() (GroupOffsetFetcher, func(), error) { return nil, nil, denied }

	_, err := TakeSnapshot(context.Background(), factory, []string{"a", "b"}, 2)
	require.ErrorIs(t, err, denied)

	opens := tracingtest.FindAll(tracingtest.Spans(rec), "groupoffsets.open_client")
	require.Len(t, opens, 1, "the pool stops building workers after the first setup failure")
	assert.Equal(t, codes.Error, opens[0].Status().Code)
}
