package groupoffsets

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
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
	assert.Equal(t, []string{"wait.rogue_commit_window", "groupoffsets.list_groups", "groupoffsets.snapshot"},
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
	var opens []sdktrace.ReadOnlySpan
	for _, s := range tracingtest.Children(spans, snap) {
		if s.Name() == "groupoffsets.open_client" {
			opens = append(opens, s)
		}
	}
	require.Len(t, opens, 2)
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

func TestTracing_ListGroups_RecordsClusterAndCount(t *testing.T) {
	rec := tracingtest.Install(t)
	lister := func(context.Context) ([]string, error) { return []string{"a", "b", "c"}, nil }

	got, err := ListGroups(context.Background(), "source", lister)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, got)

	s := tracingtest.Find(t, tracingtest.Spans(rec), "groupoffsets.list_groups")
	cluster, _ := tracingtest.Attr(s, "kcp.cluster")
	assert.Equal(t, "source", cluster.AsString())
	n, _ := tracingtest.Attr(s, "kcp.groups")
	assert.Equal(t, int64(3), n.AsInt64())
	tracingtest.AssertNoValues(t, tracingtest.Spans(rec), "a", "b", "c")
}

func TestTracing_ListGroups_FailureEndsSpanWithErrorAndReturnsItUnchanged(t *testing.T) {
	rec := tracingtest.Install(t)
	boom := errors.New("broker b2 failed")
	lister := func(context.Context) ([]string, error) { return nil, boom }

	_, err := ListGroups(context.Background(), "destination", lister)
	require.Same(t, boom, err)

	s := tracingtest.Find(t, tracingtest.Spans(rec), "groupoffsets.list_groups")
	assert.Equal(t, codes.Error, s.Status().Code)
	_, hasCount := tracingtest.Attr(s, "kcp.groups")
	assert.False(t, hasCount, "a failed listing records no count")
}

// spanNameIn is the name of the span current in ctx, or "" when it is not a recorded span.
func spanNameIn(ctx context.Context) string {
	if s, ok := trace.SpanFromContext(ctx).(sdktrace.ReadOnlySpan); ok {
		return s.Name()
	}
	return ""
}

// groupWorkers is the kafka.group_worker spans under parent, with the groups each finished summed.
func groupWorkers(t *testing.T, spans []sdktrace.ReadOnlySpan, parent sdktrace.ReadOnlySpan) (workers []int64, groups int64) {
	t.Helper()
	for _, s := range tracingtest.Children(spans, parent) {
		if s.Name() != "kafka.group_worker" {
			continue
		}
		w, ok := tracingtest.Attr(s, "kcp.worker")
		require.True(t, ok, "kafka.group_worker carries kcp.worker")
		g, ok := tracingtest.Attr(s, "kcp.groups")
		require.True(t, ok, "kafka.group_worker carries kcp.groups")
		workers = append(workers, w.AsInt64())
		groups += g.AsInt64()
	}
	return workers, groups
}

func TestTracing_TakeSnapshot_SpansEachWorkerAndHandsItTheGroups(t *testing.T) {
	rec := tracingtest.Install(t)
	offs := Offsets{"orders": {0: at(5)}}
	var mu sync.Mutex
	var seen []string
	factory := func() (GroupOffsetFetcher, func(), error) {
		return fetchFunc(func(ctx context.Context, group string) (Offsets, error) {
			mu.Lock()
			seen = append(seen, spanNameIn(ctx))
			mu.Unlock()
			return offs, nil
		}), nil, nil
	}

	_, err := TakeSnapshot(context.Background(), factory, []string{"a", "b", "c"}, 2)
	require.NoError(t, err)

	spans := tracingtest.Spans(rec)
	workers, groups := groupWorkers(t, spans, tracingtest.Find(t, spans, "groupoffsets.snapshot"))
	assert.ElementsMatch(t, []int64{0, 1}, workers, "one kafka.group_worker per worker, under the snapshot")
	assert.Equal(t, int64(3), groups, "every group is counted once, on the worker that fetched it")
	assert.Equal(t, []string{"kafka.group_worker", "kafka.group_worker", "kafka.group_worker"}, seen,
		"each group's fetch runs in its worker's span, so the client times its round trips there")
	tracingtest.AssertNoValues(t, spans, "a", "b", "c", "orders")
}

func TestTracing_Apply_SpansEachWorker(t *testing.T) {
	rec := tracingtest.Install(t)

	require.NoError(t, Apply(context.Background(), newCommitLog().factory(), planOf("a", "b", "c"), 2))

	spans := tracingtest.Spans(rec)
	workers, groups := groupWorkers(t, spans, tracingtest.Find(t, spans, "groupoffsets.apply"))
	assert.ElementsMatch(t, []int64{0, 1}, workers)
	assert.Equal(t, int64(3), groups)
}

func TestTracing_TakeSnapshot_AFailedGroupEndsItsWorkerSpanWithError(t *testing.T) {
	rec := tracingtest.Install(t)
	factory, _, _ := factoryFor(&fakeFetcher{errs: map[string]error{"a": errors.New("boom")}})

	_, err := TakeSnapshot(context.Background(), factory, []string{"a"}, 1)
	require.Error(t, err)

	w := tracingtest.Find(t, tracingtest.Spans(rec), "kafka.group_worker")
	assert.Equal(t, codes.Error, w.Status().Code)
	g, _ := tracingtest.Attr(w, "kcp.groups")
	assert.Equal(t, int64(0), g.AsInt64(), "a failed group is not counted as finished")
}

type fetchFunc func(ctx context.Context, group string) (Offsets, error)

func (f fetchFunc) CommittedOffsets(ctx context.Context, group string) (Offsets, error) {
	return f(ctx, group)
}
