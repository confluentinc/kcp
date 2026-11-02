package client

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// coordinatedFetcher is a fakeOffsetFetcher that also finds each group's coordinator, as the real admin
// wrapper does: group -> coordinator address.
type coordinatedFetcher struct {
	fakeOffsetFetcher
	coordinators map[string]string
	lookupErr    error
}

func (f coordinatedFetcher) Coordinator(group string) (*sarama.Broker, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	addr, ok := f.coordinators[group]
	if !ok {
		return nil, fmt.Errorf("no coordinator for %s", group)
	}
	return sarama.NewBroker(addr), nil
}

func intAttr(t *testing.T, s sdktrace.ReadOnlySpan, key string) int64 {
	t.Helper()
	v, ok := tracingtest.Attr(s, key)
	require.True(t, ok, "%s carries %s", s.Name(), key)
	return v.AsInt64()
}

func TestCommittedOffsetsOf_TimesItsLookupsAndFetchesOnTheWorker(t *testing.T) {
	rec := tracingtest.Install(t)
	groups := []string{"g1", "g2", "g3", "g4", "g5", "g6"}
	f := coordinatedFetcher{
		fakeOffsetFetcher: fakeOffsetFetcher{resp: map[string]*sarama.OffsetFetchResponse{}},
		coordinators:      map[string]string{},
	}
	for i, g := range groups {
		f.resp[g] = offsetsResponse(map[string]map[int32]int64{"orders": {0: 5}})
		f.coordinators[g] = fmt.Sprintf("broker-%d:9092", i%2)
	}
	ctx, w := StartGroupWorker(context.Background(), 2)

	for _, g := range groups {
		_, err := committedOffsetsOf(ctx, f, g, nil)
		require.NoError(t, err)
		w.GroupDone()
	}
	w.End(nil)

	spans := tracingtest.Spans(rec)
	s := tracingtest.Find(t, spans, "kafka.group_worker")
	assert.Equal(t, int64(2), intAttr(t, s, "kcp.worker"))
	assert.Equal(t, int64(6), intAttr(t, s, "kcp.groups"))
	assert.Equal(t, int64(6), intAttr(t, s, "kcp.coordinator_lookup.count"), "one coordinator lookup per group")
	assert.Equal(t, int64(6), intAttr(t, s, "kcp.request.count"), "one OffsetFetch per group")
	assert.Equal(t, int64(2), intAttr(t, s, "kcp.coordinators"))
	api, _ := tracingtest.Attr(s, "kcp.request.api")
	assert.Equal(t, "OffsetFetch", api.AsString())
	for _, key := range []string{"kcp.coordinator_lookup.total_ms", "kcp.coordinator_lookup.max_ms", "kcp.request.total_ms", "kcp.request.max_ms"} {
		_, ok := tracingtest.Attr(s, key)
		assert.True(t, ok, "worker span carries %s", key)
	}
	tracingtest.AssertNoValues(t, spans, append(groups, "orders", "broker-0:9092", "broker-1:9092")...)
}

func TestCommittedOffsetsOf_AFailedFetchStillCountsItsAttemptOnTheWorker(t *testing.T) {
	rec := tracingtest.Install(t)
	f := fakeOffsetFetcher{errs: map[string]error{"g1": errors.New("boom")}}
	ctx, w := StartGroupWorker(context.Background(), 0)

	_, err := committedOffsetsOf(ctx, f, "g1", nil)
	require.Error(t, err)
	w.End(err)

	s := tracingtest.Find(t, tracingtest.Spans(rec), "kafka.group_worker")
	assert.Equal(t, codes.Error, s.Status().Code)
	assert.Equal(t, int64(0), intAttr(t, s, "kcp.groups"), "a failed group is not counted as handled")
	assert.Equal(t, int64(1), intAttr(t, s, "kcp.request.count"), "the failed attempt's time is still counted")
}

func TestCommittedOffsetsOf_AFailedCoordinatorLookupIsAnErrorNamingTheGroup(t *testing.T) {
	f := coordinatedFetcher{lookupErr: errors.New("no route to broker")}

	_, err := committedOffsetsOf(context.Background(), f, "orders-app", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "finding the coordinator of consumer group orders-app")
}

func TestCommittedOffsetsOf_WithoutAWorkerInTheContextRecordsNothing(t *testing.T) {
	rec := tracingtest.Install(t)
	f := coordinatedFetcher{
		fakeOffsetFetcher: fakeOffsetFetcher{resp: map[string]*sarama.OffsetFetchResponse{"g1": offsetsResponse(nil)}},
		coordinators:      map[string]string{"g1": "broker-0:9092"},
	}

	_, err := committedOffsetsOf(context.Background(), f, "g1", nil)

	require.NoError(t, err)
	assert.Empty(t, tracingtest.Spans(rec), "a group call opens no span of its own")
}

func TestCommitGroupOffsets_TimesItsLookupAndCommitOnTheWorker(t *testing.T) {
	rec := tracingtest.Install(t)
	c, _ := newClientAndMockBroker(t, 10, commitHandlers(t, sarama.NewMockOffsetCommitResponse(t)))
	ctx, w := StartGroupWorker(context.Background(), 4)

	err := c.CommitGroupOffsets(ctx, "g1", map[string]map[int32]CommittedOffset{"orders": {0: at(10)}})
	w.GroupDone()
	w.End(nil)

	require.NoError(t, err)
	s := tracingtest.Find(t, tracingtest.Spans(rec), "kafka.group_worker")
	assert.Equal(t, int64(4), intAttr(t, s, "kcp.worker"))
	assert.Equal(t, int64(1), intAttr(t, s, "kcp.groups"))
	assert.Equal(t, int64(1), intAttr(t, s, "kcp.coordinator_lookup.count"))
	assert.Equal(t, int64(1), intAttr(t, s, "kcp.request.count"))
	assert.Equal(t, int64(1), intAttr(t, s, "kcp.coordinators"))
	api, _ := tracingtest.Attr(s, "kcp.request.api")
	assert.Equal(t, "OffsetCommit", api.AsString())
}

func TestGroupWorker_EndRecordsTheError(t *testing.T) {
	rec := tracingtest.Install(t)
	_, w := StartGroupWorker(context.Background(), 0)

	w.End(errors.New("boom"))

	s := tracingtest.Find(t, tracingtest.Spans(rec), "kafka.group_worker")
	assert.Equal(t, codes.Error, s.Status().Code)
	assert.Equal(t, int64(0), intAttr(t, s, "kcp.request.count"))
	_, hasAPI := tracingtest.Attr(s, "kcp.request.api")
	assert.False(t, hasAPI, "a worker that sent no request names no request")
}
