package tail

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exercise the production seam against sarama's own MockBroker, so
// the FetchRequest is really encoded, really parsed by sarama's broker-side
// decoder, and the response really decoded back. That is what the hand-rolled
// fake structurally cannot prove: a version ladder that omits a field the wire
// format requires fails here and nowhere else.

// fetchAPIKeys advertises a broker's supported API versions with the Fetch
// ceiling under test.
func fetchAPIKeys(fetchMax int16) []sarama.ApiVersionsResponseKey {
	return []sarama.ApiVersionsResponseKey{
		{ApiKey: 0, MinVersion: 0, MaxVersion: 8}, // Produce
		{ApiKey: fetchAPIKey, MinVersion: 0, MaxVersion: fetchMax},
		{ApiKey: 2, MinVersion: 0, MaxVersion: 5},  // ListOffsets
		{ApiKey: 3, MinVersion: 0, MaxVersion: 9},  // Metadata
		{ApiKey: 18, MinVersion: 0, MaxVersion: 3}, // ApiVersions
	}
}

// newTestClient builds a real sarama client pointed at the given seed broker.
func newTestClient(t *testing.T, addr string) sarama.Client {
	t.Helper()
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_6_0_0
	cfg.Metadata.AllowAutoTopicCreation = false
	cfg.Metadata.Retry.Max = 1
	cfg.Net.ReadTimeout = 2 * time.Second
	cfg.Net.WriteTimeout = 2 * time.Second

	client, err := sarama.NewClient([]string{addr}, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// servedResponse builds the fetch response the mock hands back: three
// transactional records from one producer, plus its commit marker.
func servedResponse(topic string, version int16) *sarama.FetchResponse {
	r := &sarama.FetchResponse{Version: version}
	r.AddError(topic, 0, sarama.ErrNoError)
	r.SetLastStableOffset(topic, 0, 4)
	r.GetBlock(topic, 0).HighWaterMarkOffset = 4
	r.AddRecordBatch(topic, 0, sarama.ByteEncoder("k0"), sarama.ByteEncoder("v0"), 0, 7777, true)
	r.AddRecordBatch(topic, 0, sarama.ByteEncoder("k1"), sarama.ByteEncoder("v1"), 1, 7777, true)
	r.AddRecordBatch(topic, 0, sarama.ByteEncoder("k2"), sarama.ByteEncoder("v2"), 2, 7777, true)
	r.AddControlRecord(topic, 0, 3, 7777, sarama.ControlRecordCommit)
	return r
}

// emptyServedResponse is what the mock returns once the log is drained.
func emptyServedResponse(topic string, version int16) *sarama.FetchResponse {
	r := &sarama.FetchResponse{Version: version}
	r.AddError(topic, 0, sarama.ErrNoError)
	r.SetLastStableOffset(topic, 0, 4)
	r.GetBlock(topic, 0).HighWaterMarkOffset = 4
	return r
}

// lastFetchRequest returns the most recent FetchRequest the mock decoded.
func lastFetchRequest(t *testing.T, b *sarama.MockBroker) *sarama.FetchRequest {
	t.Helper()
	var found *sarama.FetchRequest
	for _, rr := range b.History() {
		if req, ok := rr.Request.(*sarama.FetchRequest); ok {
			found = req
		}
	}
	require.NotNil(t, found, "the mock broker never decoded a FetchRequest")
	return found
}

func TestAFetchRequestRoundTripsAgainstAMockBrokerAtTheNegotiatedVersion(t *testing.T) {
	const topic = "txn-state"
	const version = int16(11)

	broker := sarama.NewMockBroker(t, 1)
	defer broker.Close()
	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t).SetApiKeys(fetchAPIKeys(version)),
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(broker.Addr(), broker.BrokerID()).
			SetController(broker.BrokerID()).
			SetLeader(topic, 0, broker.BrokerID()),
		"OffsetRequest": sarama.NewMockOffsetResponse(t).
			SetOffset(topic, 0, sarama.OffsetOldest, 0).
			SetOffset(topic, 0, sarama.OffsetNewest, 4),
		"FetchRequest": sarama.NewMockSequence(
			servedResponse(topic, version),
			emptyServedResponse(topic, version),
		),
	})

	seam := NewSaramaClient(newTestClient(t, broker.Addr()), DefaultFetchVersionCeiling)

	tl := New(seam, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)
	got := collect(t, out, 4, cancel)

	// The records that come back must be the ones the mock served, having
	// travelled through real encode and decode in both directions.
	require.Len(t, got, 4)
	for i := 0; i < 3; i++ {
		require.Len(t, got[i].Records, 1)
		assert.Equal(t, int64(i), got[i].Records[0].Offset)
		assert.Equal(t, "k"+string(rune('0'+i)), string(got[i].Records[0].Key))
		assert.Equal(t, "v"+string(rune('0'+i)), string(got[i].Records[0].Value))
		assert.Equal(t, int64(7777), got[i].ProducerID, "the producer id must survive the wire")
		assert.True(t, got[i].IsTransactional)
		assert.False(t, got[i].Control)
	}
	assert.True(t, got[3].Control, "the commit marker must arrive flagged as a control batch")

	// And the request the broker actually decoded must carry the negotiated
	// version with its version-gated fields.
	req := lastFetchRequest(t, broker)
	assert.Equal(t, version, req.Version)
	assert.Equal(t, sarama.ReadCommitted, req.Isolation)
	assert.Equal(t, int32(0), req.SessionID)
	assert.Equal(t, int32(-1), req.SessionEpoch)
	assert.Positive(t, req.MaxBytes)
	assert.Positive(t, req.MaxWaitTime)

	// Four, not three: a control batch carries a record of its own, and it was
	// read from the log like any other.
	assert.Equal(t, int64(4), tl.Stats().RecordsRead)
	assert.Equal(t, int64(0), tl.Stats().Lag, "the reader is caught up to the last stable offset")
}

func TestTheRequestVersionClampsToTheBrokersAdvertisedFetchCeiling(t *testing.T) {
	const topic = "txn-state"
	// An older broker: it tops out at Fetch v6, well below the local ceiling.
	const version = int16(6)

	broker := sarama.NewMockBroker(t, 1)
	defer broker.Close()

	served := &sarama.FetchResponse{Version: version}
	served.AddError(topic, 0, sarama.ErrNoError)
	served.SetLastStableOffset(topic, 0, 2)
	served.GetBlock(topic, 0).HighWaterMarkOffset = 2
	served.AddRecordBatch(topic, 0, nil, sarama.ByteEncoder("old-broker"), 0, 31337, true)

	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t).SetApiKeys(fetchAPIKeys(version)),
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(broker.Addr(), broker.BrokerID()).
			SetController(broker.BrokerID()).
			SetLeader(topic, 0, broker.BrokerID()),
		"OffsetRequest": sarama.NewMockOffsetResponse(t).
			SetOffset(topic, 0, sarama.OffsetOldest, 0).
			SetOffset(topic, 0, sarama.OffsetNewest, 2),
		"FetchRequest": sarama.NewMockSequence(served, emptyServedResponse(topic, version)),
	})

	seam := NewSaramaClient(newTestClient(t, broker.Addr()), DefaultFetchVersionCeiling)

	tl := New(seam, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)
	got := collect(t, out, 1, cancel)

	assert.Equal(t, "old-broker", string(got[0].Records[0].Value))
	assert.Equal(t, int64(31337), got[0].ProducerID)

	req := lastFetchRequest(t, broker)
	assert.Equal(t, version, req.Version, "the request must clamp to what the broker advertises")
	assert.Equal(t, sarama.ReadCommitted, req.Isolation, "the isolation level is still carried at v6")
	assert.Equal(t, int32(0), req.SessionEpoch, "the session fields are not on the wire below v7")
}

func TestABrokerAdvertisingBelowVersion4FailsFastWithAnActionableError(t *testing.T) {
	const topic = "txn-state"

	broker := sarama.NewMockBroker(t, 1)
	defer broker.Close()
	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		// Fetch v3 predates the isolation level, the last stable offset and
		// the aborted-transaction list, so read-committed reading is simply
		// not expressible against this broker.
		"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t).SetApiKeys(fetchAPIKeys(3)),
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(broker.Addr(), broker.BrokerID()).
			SetController(broker.BrokerID()).
			SetLeader(topic, 0, broker.BrokerID()),
		"OffsetRequest": sarama.NewMockOffsetResponse(t).
			SetOffset(topic, 0, sarama.OffsetOldest, 0).
			SetOffset(topic, 0, sarama.OffsetNewest, 0),
	})

	seam := NewSaramaClient(newTestClient(t, broker.Addr()), DefaultFetchVersionCeiling)

	tl := New(seam, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.Error(t, err, "an unsupportable broker must fail at startup, not retry forever")
	assert.ErrorIs(t, err, ErrFetchVersionUnsupported)
	assert.Contains(t, err.Error(), "v4", "the error must name the version the reader needs")
	assert.NotContains(t, err.Error(), topic, "no topic name may reach an error, which is logged to kcp.log")
}

// spySeam wraps the production seam to record what each fetch actually asked
// for. The request still goes over the wire; only the ask is observed, because
// a FetchRequest's per-partition blocks are not readable from outside sarama.
type spySeam struct {
	Client
	mu      sync.Mutex
	specs   []FetchSpec
	leaders []Leader
}

func (s *spySeam) Fetch(l Leader, spec FetchSpec) (*sarama.FetchResponse, error) {
	s.mu.Lock()
	s.specs = append(s.specs, spec)
	s.leaders = append(s.leaders, l)
	s.mu.Unlock()
	return s.Client.Fetch(l, spec)
}

// fetchesTo returns the specs issued against a given broker id.
func (s *spySeam) fetchesTo(id int32) []FetchSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []FetchSpec
	for i, l := range s.leaders {
		if l.ID == id {
			out = append(out, s.specs[i])
		}
	}
	return out
}

func TestMovingTheLeaderResumesAgainstTheNewBrokerWithNoGapOrDuplication(t *testing.T) {
	const topic = "txn-state"
	const version = int16(11)

	oldLeader := sarama.NewMockBroker(t, 1)
	defer oldLeader.Close()
	newLeader := sarama.NewMockBroker(t, 2)
	defer newLeader.Close()

	// Before the move: broker 1 leads and serves offsets 0..2, then starts
	// rejecting because leadership has gone elsewhere.
	before := &sarama.FetchResponse{Version: version}
	before.AddError(topic, 0, sarama.ErrNoError)
	before.SetLastStableOffset(topic, 0, 5)
	before.GetBlock(topic, 0).HighWaterMarkOffset = 5
	before.AddRecordBatch(topic, 0, nil, sarama.ByteEncoder("r0"), 0, 900, true)
	before.AddRecordBatch(topic, 0, nil, sarama.ByteEncoder("r1"), 1, 900, true)
	before.AddRecordBatch(topic, 0, nil, sarama.ByteEncoder("r2"), 2, 900, true)

	moved := &sarama.FetchResponse{Version: version}
	moved.AddError(topic, 0, sarama.ErrNotLeaderForPartition)

	// After the move: broker 2 serves the rest.
	after := &sarama.FetchResponse{Version: version}
	after.AddError(topic, 0, sarama.ErrNoError)
	after.SetLastStableOffset(topic, 0, 5)
	after.GetBlock(topic, 0).HighWaterMarkOffset = 5
	after.AddRecordBatch(topic, 0, nil, sarama.ByteEncoder("r3"), 3, 900, true)
	after.AddRecordBatch(topic, 0, nil, sarama.ByteEncoder("r4"), 4, 900, true)

	// The first metadata answer names broker 1; every later one names broker 2,
	// which is what a leader election looks like to a client.
	metadataBefore := sarama.NewMockMetadataResponse(t).
		SetBroker(oldLeader.Addr(), oldLeader.BrokerID()).
		SetController(oldLeader.BrokerID()).
		SetLeader(topic, 0, oldLeader.BrokerID())
	metadataAfter := sarama.NewMockMetadataResponse(t).
		SetBroker(oldLeader.Addr(), oldLeader.BrokerID()).
		SetBroker(newLeader.Addr(), newLeader.BrokerID()).
		SetController(oldLeader.BrokerID()).
		SetLeader(topic, 0, newLeader.BrokerID())

	oldLeader.SetHandlerByMap(map[string]sarama.MockResponse{
		"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t).SetApiKeys(fetchAPIKeys(version)),
		"MetadataRequest":    sarama.NewMockSequence(metadataBefore, metadataAfter),
		"OffsetRequest": sarama.NewMockOffsetResponse(t).
			SetOffset(topic, 0, sarama.OffsetOldest, 0).
			SetOffset(topic, 0, sarama.OffsetNewest, 5),
		"FetchRequest": sarama.NewMockSequence(before, moved),
	})
	newLeader.SetHandlerByMap(map[string]sarama.MockResponse{
		"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t).SetApiKeys(fetchAPIKeys(version)),
		"MetadataRequest":    metadataAfter,
		"OffsetRequest": sarama.NewMockOffsetResponse(t).
			SetOffset(topic, 0, sarama.OffsetOldest, 0).
			SetOffset(topic, 0, sarama.OffsetNewest, 5),
		"FetchRequest": sarama.NewMockSequence(after, emptyServedResponse(topic, version)),
	})

	seam := &spySeam{Client: NewSaramaClient(newTestClient(t, oldLeader.Addr()), DefaultFetchVersionCeiling)}

	tl := New(seam, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)
	got := collect(t, out, 5, cancel)

	var offsets []int64
	var values []string
	var leaders []int32
	for _, b := range got {
		for _, rec := range b.Records {
			offsets = append(offsets, rec.Offset)
			values = append(values, string(rec.Value))
		}
		leaders = append(leaders, b.Leader)
	}
	assert.Equal(t, []int64{0, 1, 2, 3, 4}, offsets, "the move must leave no gap and no duplicate")
	assert.Equal(t, []string{"r0", "r1", "r2", "r3", "r4"}, values)
	assert.Equal(t, []int32{1, 1, 1, 2, 2}, leaders, "the last two batches came from the new leader")

	toNew := seam.fetchesTo(newLeader.BrokerID())
	require.NotEmpty(t, toNew, "the reader must actually fetch from the new leader")
	assert.Equal(t, int64(3), toNew[0].Offset, "the retry resumes from the last offset consumed, not from scratch")

	assert.Equal(t, int64(1), tl.Stats().LeadershipErrors)
	assert.Equal(t, int64(5), tl.Stats().RecordsRead)
}
