package discovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/grouping"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fixture builders -------------------------------------------------------
//
// Keys are hand-assembled bytes against Kafka's OffsetCommitKey.json and
// GroupMetadataKey.json schemas rather than produced by an encoder from the
// package under test, so a decoder that agrees only with itself still fails.

// be16, be32, kstr and concat are the byte builders this suite shares with the
// __transaction_state reader's (txnstate_test.go), same package.

// commitKey builds an OffsetCommitKey v1 — the key an EOS app's
// sendOffsetsToTransaction writes for one consumed topic-partition.
func commitKey(group, topic string, partition int32) []byte {
	return concat(be16(1), kstr(group), kstr(topic), be32(partition))
}

// groupMetadataKey builds a GroupMetadataKey v2 — consumer-group state, which
// shares the __consumer_offsets topic with offset commits but names no topic.
func groupMetadataKey(group string) []byte {
	return concat(be16(2), kstr(group))
}

// commitBatch builds the batch a transactional offset commit arrives in: the
// header carries the producer id that ties it to its transaction.
func commitBatch(producerID int64, keys ...[]byte) tail.Batch {
	b := tail.Batch{
		Topic:           DefaultConsumerOffsetsTopic,
		ProducerID:      producerID,
		IsTransactional: true,
	}
	for i, k := range keys {
		b.Records = append(b.Records, tail.Record{
			Offset: int64(i),
			Key:    k,
			Value:  []byte{0, 1}, // a non-empty value: a live commit, not a tombstone
		})
	}
	return b
}

// flush runs the tail's final flush into a buffered channel and returns
// everything it emitted.
func flush(t *testing.T, tl *ConsumerOffsetsTail) []Observation {
	t.Helper()
	out := make(chan Observation, 256)
	tl.FinalFlush(out)
	close(out)
	var got []Observation
	for obs := range out {
		got = append(got, obs)
	}
	return got
}

// --- tests ------------------------------------------------------------------

func TestATransactionalCommitWhoseProducerIDIsInTheCatalogResolvesToAnObservation(t *testing.T) {
	// The exact join this whole unit exists for: __transaction_state recorded that
	// producer 4242 is running transaction "payments-txn-0", and __consumer_offsets
	// recorded that producer 4242 committed an offset for "orders.in". Neither
	// record alone says the transaction consumes that topic; joining them on the
	// producer id does, with no assumption about how the names relate.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)

	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})
	tl.HandleBatch(commitBatch(4242, commitKey("payments-group", "orders.in", 0)))

	got := flush(t, tl)

	require.Len(t, got, 1)
	assert.Equal(t, "payments-txn-0", got[0].TxnID)
	assert.Equal(t, int64(4242), got[0].ProducerID)
	assert.Equal(t, []string{"orders.in"}, got[0].Topics)
	assert.True(t, got[0].ReadProcessWrite, "a recovered consumed input makes the transaction read-process-write")
	assert.Equal(t, SourceConsumerOffsets, got[0].Source)
	assert.False(t, got[0].ObservedAt.IsZero())
}

func TestANonTransactionalBatchIsIgnored(t *testing.T) {
	// The overwhelming majority of __consumer_offsets traffic is ordinary,
	// non-transactional offset commits. A batch header carries a producer id for
	// plain idempotent producers too, so correlating one would attribute an
	// unrelated consumer's topics to whichever transaction happens to share that
	// id. Only a commit written INSIDE a transaction says anything about one.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	b := commitBatch(4242, commitKey("payments-group", "orders.in", 0))
	b.IsTransactional = false
	tl.HandleBatch(b)

	assert.Empty(t, flush(t, tl), "a non-transactional commit must not correlate")
}

func TestAControlBatchIsIgnored(t *testing.T) {
	// A control batch is the transaction marker itself — the COMMIT or ABORT the
	// coordinator writes — not application data. The tail component delivers
	// control batches flagged rather than filtering them, so dropping them is
	// this consumer's job. A marker is transactional and carries a real producer
	// id, so every other filter here waves it through; only the flag stops it.
	// Its record key is not an OffsetCommitKey at all, so treating it as one
	// would decode a marker as a consumed topic.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	b := commitBatch(4242, commitKey("payments-group", "orders.in", 0))
	b.Control = true
	tl.HandleBatch(b)

	assert.Empty(t, flush(t, tl), "a control batch must not correlate")
}

func TestABatchWithANonPositiveProducerIDIsIgnored(t *testing.T) {
	// -1 is Kafka's "no producer id" sentinel and 0 is the zero value of an
	// absent field. Buffering either would key a pending entry on a sentinel, so
	// every unrelated producer lacking an id would pool its consumed topics into
	// one bucket — and the moment any transaction happened to be catalogued
	// under that sentinel, the whole pool would be attributed to it. The
	// catalog's own guard only refuses to WRITE a non-positive id; refusing to
	// READ one is this side's job.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	for _, pid := range []int64{-1, 0} {
		tl.HandleBatch(commitBatch(pid, commitKey("payments-group", "orders.in", 0)))
	}

	assert.Empty(t, flush(t, tl), "a sentinel producer id must not correlate")
	// And it is not merely unresolved — nothing was buffered under the sentinel
	// key at all, so a later catalog entry cannot resurrect it.
	assert.Empty(t, tl.resolveWith(map[int64]string{-1: "some-txn", 0: "other-txn"}, time.Now()),
		"nothing may be buffered under a sentinel producer id")
}

func TestABatchFromAnotherTopicOnTheSharedChannelIsIgnored(t *testing.T) {
	// One tail instance serves both this consumer and the __transaction_state
	// reader, fanning every partition of both topics into a single channel, so
	// each consumer must demultiplex on the batch's topic. The batch here is
	// transactional with a real producer id, so every other filter waves it
	// through — only the topic check stops a __transaction_state record's key
	// from being decoded as an OffsetCommitKey and published as a consumed topic.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	b := commitBatch(4242, commitKey("payments-group", "orders.in", 0))
	b.Topic = "__transaction_state"
	tl.HandleBatch(b)

	assert.Empty(t, flush(t, tl), "a batch from another topic must not correlate")
}

func TestTheDemultiplexedTopicFollowsTheConfiguredTopic(t *testing.T) {
	// The demux compares against a configured name, not a hardcoded one. U11
	// builds the tail's TopicSpec and this consumer from the same setting, so a
	// hardcode here would silently drop every batch on a cluster whose offsets
	// topic is named otherwise — a consumer that reads nothing looks exactly
	// like a cluster with no exactly-once traffic.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{Topic: "__custom_offsets"})

	b := commitBatch(4242, commitKey("payments-group", "orders.in", 0))
	b.Topic = "__custom_offsets"
	tl.HandleBatch(b)

	got := flush(t, tl)
	require.Len(t, got, 1)
	assert.Equal(t, []string{"orders.in"}, got[0].Topics)
}

func TestAGroupMetadataKeyContributesNoConsumedTopic(t *testing.T) {
	// Key version 2 is GroupMetadataKey: consumer-group state sharing the
	// __consumer_offsets topic, naming no topic at all. It decodes successfully,
	// so nothing upstream rejects it — and its empty Topic field would otherwise
	// be buffered as a consumed topic named "", which reaches the YAML as a
	// nameless member of a real transaction's group.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	tl.HandleBatch(commitBatch(4242, groupMetadataKey("payments-group")))

	assert.Empty(t, flush(t, tl), "group metadata names no consumed topic")
}

func TestAnInternalConsumedTopicIsDropped(t *testing.T) {
	// An exactly-once app can commit offsets for an internal topic — a Streams
	// repartition or changelog topic is the routine case. Publishing one as a
	// recovered input would hand the grouping stage an edge through a topic
	// every such app shares, and the union-find would chain unrelated workloads
	// into one group. Grouping filters internal topics itself, but this source
	// also feeds the audit trail and the recovered-topics stat, both of which
	// would otherwise report a topic no operator can migrate.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	tl.HandleBatch(commitBatch(4242,
		commitKey("payments-group", "__consumer_offsets", 0),
		commitKey("payments-group", "app-repartition-store-changelog", 0),
		commitKey("payments-group", "orders.in", 0),
	))

	got := flush(t, tl)

	require.Len(t, got, 1)
	assert.Equal(t, []string{"app-repartition-store-changelog", "orders.in"}, got[0].Topics,
		"only __-prefixed topics are internal; a Streams changelog is a real topic")
}

func TestATombstonedCommitIsNotALiveConsumedInput(t *testing.T) {
	// A tombstone — a valid key with an empty value — is how the coordinator
	// DELETES an offset commit on expiry or an admin DeleteOffsets. Its key
	// still names a topic, so every decode-side check passes it; reading it as
	// a live commit would report an input the application no longer consumes,
	// and the operator would migrate a topic into a group that does not need it.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	b := commitBatch(4242, commitKey("payments-group", "orders.in", 0))
	b.Records[0].Value = nil
	tl.HandleBatch(b)

	assert.Empty(t, flush(t, tl), "a tombstone is a deleted commit, not a live one")
}

func TestAKeyThatFailsToDecodeIsCountedAndDoesNotStopTheTail(t *testing.T) {
	// Record keys are a broker-internal binary format, not part of the stable
	// client protocol, so a broker upgrade can change them under us. A decode
	// failure must neither panic nor abandon the rest of the batch — dropping
	// the remaining records would silently lose recoveries — and it must be
	// counted, because the counter is the only format-drift alarm this source
	// has. A run that decoded nothing otherwise looks like an idle cluster.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	tl.HandleBatch(commitBatch(4242,
		be16(99),  // an unsupported key version
		[]byte{0}, // shorter than the version prefix itself
		commitKey("payments-group", "orders.in", 0),
	))

	got := flush(t, tl)

	require.Len(t, got, 1, "the records after a bad key are still processed")
	assert.Equal(t, []string{"orders.in"}, got[0].Topics)
	assert.Equal(t, int64(2), tl.Stats().KeyDecodeErrors)
}

func TestAnUnresolvedProducerStaysBufferedAndThenEmitsExactlyOnce(t *testing.T) {
	// The two readers race by design: this one starts at LATEST so it sees live
	// commits, while the __transaction_state reader starts at EARLIEST and takes
	// the whole window to catch up. A commit therefore routinely arrives before
	// its transaction is catalogued, so an unresolved sighting must be kept
	// rather than dropped. Once it resolves it must leave the buffer, or every
	// later pass re-emits it — inflating the sample count and writing a
	// duplicate audit line for a coupling that happened once.
	cat := NewTxnCatalog()
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})
	tl.HandleBatch(commitBatch(4242, commitKey("payments-group", "orders.in", 0)))

	assert.Empty(t, flush(t, tl), "nothing resolves while the catalog is empty")

	cat.Observe("payments-txn-0", 4242)
	got := flush(t, tl)
	require.Len(t, got, 1, "the buffered sighting resolves once the catalog catches up")
	assert.Equal(t, "payments-txn-0", got[0].TxnID)

	assert.Empty(t, flush(t, tl), "a resolved sighting must not be re-emitted")
}

func TestManyCommitsFromOneProducerMergeIntoASortedDeduplicatedTopicSet(t *testing.T) {
	// One producer commits an offset per consumed topic-partition, on every
	// transaction, for the whole window — so the same topic arrives hundreds of
	// times across many batches. They must union into one set rather than a
	// list with repeats, and the order must be stable: the YAML and the audit
	// trail are diffed between runs, and Go randomises map iteration, so an
	// unsorted set would churn the artifacts over identical observations.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	// Arriving out of order, across separate batches, with repeats.
	tl.HandleBatch(commitBatch(4242,
		commitKey("payments-group", "zeta.in", 0),
		commitKey("payments-group", "orders.in", 0),
	))
	tl.HandleBatch(commitBatch(4242,
		commitKey("payments-group", "orders.in", 1), // same topic, another partition
		commitKey("payments-group", "alpha.in", 0),
		commitKey("payments-group", "zeta.in", 3),
	))

	got := flush(t, tl)

	require.Len(t, got, 1, "one producer yields one observation, not one per commit")
	assert.Equal(t, []string{"alpha.in", "orders.in", "zeta.in"}, got[0].Topics)
}

// streamsNamingCorrelates is the Kafka Streams naming convention the
// consumer-group enrichment phase relies on: a Streams app sets group.id to its
// application.id and derives transactional.id as "<application.id>-<taskId>"
// (EOS v1) or "<application.id>-<processId>" (EOS v2). It is reproduced here so
// the test below can assert what that heuristic CANNOT do, rather than merely
// claiming it.
func streamsNamingCorrelates(groupID, txnID string) bool {
	return txnID == groupID || strings.HasPrefix(txnID, groupID+"-")
}

func TestCorrelationSucceedsWhereTheStreamsNamingHeuristicFails(t *testing.T) {
	// This is the case the whole raw-fetch decision was made for. A plain
	// consumer+producer exactly-once app — not Kafka Streams — picks its
	// group.id and its transactional.id independently, so no naming rule can
	// relate them. The enrichment phase is blind to it; the producer id in the
	// batch header is not.
	const groupID = "svc-ingest-consumer"
	const txnID = "billing-writer-7"

	require.False(t, streamsNamingCorrelates(groupID, txnID),
		"fixture must be one the naming heuristic cannot correlate")
	require.False(t, streamsNamingCorrelates(txnID, groupID),
		"nor in the other direction")

	cat := NewTxnCatalog()
	cat.Observe(txnID, 990001)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	tl.HandleBatch(commitBatch(990001, commitKey(groupID, "raw.events", 0)))

	got := flush(t, tl)

	require.Len(t, got, 1, "the producer-id join must recover what naming cannot")
	assert.Equal(t, txnID, got[0].TxnID)
	assert.Equal(t, []string{"raw.events"}, got[0].Topics)
}

func TestARecoveredInputFoldsIntoTheProducedGroupWithItsProducerIDIntact(t *testing.T) {
	// End of the pipeline this source feeds. A transaction's own footprint names
	// only what it PRODUCED, so "billing.out" alone looks like a topic that can
	// migrate on its own — when moving it without "raw.events" breaks
	// exactly-once at cutover. The recovered input has to reach the same group.
	//
	// The producer id is the load-bearing detail. The __transaction_state
	// reader's first sighting of a transaction is routinely an Empty record with
	// no usable producer id, and the naming-based enrichment phase never has
	// one, so this source is often the ONLY observation carrying a real id. The
	// accumulator refuses to let a zero overwrite a positive one; this asserts
	// that this source supplies the positive one in the first place.
	const txnID = "billing-writer-7"
	const pid int64 = 990001

	cat := NewTxnCatalog()
	cat.Observe(txnID, pid)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})
	tl.HandleBatch(commitBatch(pid, commitKey("svc-ingest-consumer", "raw.events", 0)))

	acc := NewAccumulator()
	acc.Add(Observation{
		TxnID:            txnID,
		ProducerID:       0, // an early Empty state record carries no real id yet
		Topics:           []string{"billing.out", "__consumer_offsets"},
		ReadProcessWrite: true,
		Source:           SourceTxnStateLog,
		ObservedAt:       time.Now(),
	})
	for _, obs := range flush(t, tl) {
		acc.Add(obs)
	}
	acc.Add(Observation{
		TxnID:      txnID,
		ProducerID: 0, // the naming-based phase never has one
		Topics:     []string{"raw.events"},
		Source:     SourceConsumerGroups,
		ObservedAt: time.Now(),
	})

	snap := acc.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, pid, snap[0].ProducerID,
		"the only real producer id came from this source and must survive the zero-id guard")
	assert.Equal(t, []string{"__consumer_offsets", "billing.out", "raw.events"}, snap[0].Topics)
	assert.True(t, snap[0].ReadProcessWrite)

	res := grouping.Build([]grouping.Transaction{{
		ID:               snap[0].TxnID,
		Topics:           snap[0].Topics,
		ReadProcessWrite: snap[0].ReadProcessWrite,
	}}, grouping.Options{})

	require.Len(t, res.Groups, 1, "the recovered input must not be a separate group")
	assert.Equal(t, []string{"billing.out", "raw.events"}, res.Groups[0].Topics)
	assert.True(t, res.Groups[0].ReadProcessWrite)
}

func TestASustainedStreamOfUnresolvableCommitsDoesNotGrowThePendingBufferWithoutBound(t *testing.T) {
	// On a large cluster most commits come from producers whose transactions
	// this run never observes — they were compacted away before the window, or
	// belong to an app that never commits again. Those sightings never resolve,
	// so an unbounded buffer grows for the whole run: a multi-hour observation
	// on a busy cluster is exactly the situation a slow memory leak is worst in,
	// because the operator loses the run rather than a request.
	//
	// The oldest go first: a sighting that has waited longest is the least
	// likely to ever resolve, and dropping the newest would discard the commits
	// whose transactions the __transaction_state reader is about to reach.
	const maxPending = 4
	cat := NewTxnCatalog()
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{MaxPendingProducers: maxPending})

	for pid := int64(1); pid <= 10; pid++ {
		tl.HandleBatch(commitBatch(pid, commitKey("g", fmt.Sprintf("topic-%02d", pid), 0)))
	}

	st := tl.Stats()
	assert.Equal(t, maxPending, st.PendingProducers, "the buffer is capped")
	assert.Equal(t, int64(6), st.PendingEvicted, "and what it dropped is counted, not silent")

	// Prove it kept the NEWEST four rather than an arbitrary four: a catalog
	// that resolves all ten may only yield the survivors.
	all := map[int64]string{}
	for pid := int64(1); pid <= 10; pid++ {
		all[pid] = fmt.Sprintf("txn-%02d", pid)
	}
	var survived []string
	for _, obs := range tl.resolveWith(all, time.Now()) {
		survived = append(survived, obs.Topics[0])
	}
	sort.Strings(survived)
	assert.Equal(t, []string{"topic-07", "topic-08", "topic-09", "topic-10"}, survived)
}

func TestAnUnresolvedSightingAgesOutAfterAMultipleOfTheInterval(t *testing.T) {
	// The entry cap alone only engages once the buffer is full, which on a
	// moderately busy cluster may be never — so a run can still carry thousands
	// of sightings from the first minute for hours, none of which will ever
	// resolve. Ageing them out keeps the buffer proportional to what is
	// plausibly still in flight rather than to the length of the run.
	//
	// The TTL is a multiple of the resolve interval, not an absolute duration,
	// because the interval is what sets how many chances a sighting gets.
	const interval = time.Second
	start := time.Now()
	now := start

	cat := NewTxnCatalog()
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{
		Interval:            interval,
		PendingTTLIntervals: 3,
		Now:                 func() time.Time { return now },
	})

	tl.HandleBatch(commitBatch(1, commitKey("g", "old.in", 0)))
	now = start.Add(2 * interval)
	tl.HandleBatch(commitBatch(2, commitKey("g", "new.in", 0)))

	// A pass three and a half intervals after the first sighting: it is past its
	// three-interval TTL, the second — two intervals younger — is not.
	now = start.Add(3*interval + interval/2)
	out := make(chan Observation, 8)
	tl.resolveAndFlush(context.Background(), out)

	st := tl.Stats()
	assert.Equal(t, 1, st.PendingProducers)
	assert.Equal(t, int64(1), st.PendingEvicted, "an aged-out sighting is counted, not silently lost")

	// The survivor is the younger one, not an arbitrary one.
	got := tl.resolveWith(map[int64]string{1: "txn-1", 2: "txn-2"}, now)
	require.Len(t, got, 1)
	assert.Equal(t, "txn-2", got[0].TxnID)
	assert.Equal(t, []string{"new.in"}, got[0].Topics)
}

func TestAResolvePassResolvesBeforeItExpires(t *testing.T) {
	// Ordering inside one pass is load-bearing. A sighting that has just reached
	// its TTL may be exactly the one the __transaction_state reader has finally
	// caught up to — expiring first would discard a recovery on the very pass
	// that could have made it, and the loss would be invisible except as an
	// eviction count.
	const interval = time.Second
	start := time.Now()
	now := start

	cat := NewTxnCatalog()
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{
		Interval:            interval,
		PendingTTLIntervals: 2,
		Now:                 func() time.Time { return now },
	})
	tl.HandleBatch(commitBatch(7, commitKey("g", "late.in", 0)))

	// The catalog catches up only now, well past the sighting's TTL.
	cat.Observe("late-txn", 7)
	now = start.Add(10 * interval)

	out := make(chan Observation, 8)
	tl.resolveAndFlush(context.Background(), out)
	close(out)

	var got []Observation
	for obs := range out {
		got = append(got, obs)
	}
	require.Len(t, got, 1, "a resolvable sighting must not be expired out from under the join")
	assert.Equal(t, "late-txn", got[0].TxnID)
	assert.Zero(t, tl.Stats().PendingEvicted)
}

func TestRunFinalFlushesOnAFreshContextAfterTheWindowIsCancelled(t *testing.T) {
	// Most resolutions land in the final flush. This reader starts at LATEST so
	// it sees commits immediately, while the __transaction_state reader starts
	// at EARLIEST and is still catching up — so a transaction routinely becomes
	// known only as the window closes. By then the observation context is
	// already cancelled, so flushing on it would resolve every buffered sighting
	// (removing it from the buffer) and then send none of them. The whole point
	// of the phase would be lost at the last step, silently.
	cat := NewTxnCatalog()
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{
		Interval: time.Hour, // long enough that no interval pass fires
	})

	ctx, cancel := context.WithCancel(context.Background())
	in := make(chan tail.Batch)
	out := make(chan Observation, 8)

	done := make(chan error, 1)
	go func() { done <- tl.Run(ctx, in, out) }()

	in <- commitBatch(4242, commitKey("payments-group", "orders.in", 0))

	// The window ends. Only now does the state reader reach this transaction.
	cat.Observe("payments-txn-0", 4242)
	cancel()
	close(in)

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its input closed")
	}

	close(out)
	var got []Observation
	for obs := range out {
		got = append(got, obs)
	}
	require.Len(t, got, 1, "the final flush must still deliver on a cancelled context")
	assert.Equal(t, "payments-txn-0", got[0].TxnID)
	assert.Equal(t, []string{"orders.in"}, got[0].Topics)
}

func TestAnIntervalPassAbandonedMidSendDoesNotLoseWhatItResolved(t *testing.T) {
	// An interval pass races the end of the window: the context can be cancelled
	// between resolving a sighting and sending its observation. A pass that
	// dropped the entry when it resolved it would then have removed the sighting
	// from the buffer AND failed to deliver it — the recovery is gone, with no
	// counter moving and the final flush finding nothing left to do. Nothing
	// downstream can tell that apart from a transaction that had no inputs.
	//
	// So a sighting leaves the buffer only once its observation has actually
	// been handed over.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})
	tl.HandleBatch(commitBatch(4242, commitKey("payments-group", "orders.in", 0)))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tl.resolveAndFlush(ctx, make(chan Observation)) // nobody reads; the send is abandoned

	require.Equal(t, 1, tl.Stats().PendingProducers, "an undelivered sighting stays buffered")

	got := flush(t, tl)
	require.Len(t, got, 1, "and the final flush still delivers it")
	assert.Equal(t, "payments-txn-0", got[0].TxnID)
	assert.Equal(t, []string{"orders.in"}, got[0].Topics)
}

func TestTheStatsReportWhatThisPhaseItselfRecovered(t *testing.T) {
	// The report credits each recovered input to the phase that actually found
	// it — producer-id correlation or the naming heuristic — so this source has
	// to say which topics IT recovered. Inferring it downstream is not possible:
	// both phases emit observations under the same transactional id, and the
	// accumulator unions them, so by the time the report sees a footprint the
	// provenance of an individual topic is gone.
	//
	// Only DELIVERED recoveries count. A sighting resolved but not handed over
	// has recovered nothing yet, and counting it would credit this phase for an
	// input that never reached the accumulator.
	cat := NewTxnCatalog()
	cat.Observe("txn-a", 11)
	cat.Observe("txn-b", 22)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	tl.HandleBatch(commitBatch(11,
		commitKey("group-one", "zeta.in", 0),
		commitKey("group-one", "alpha.in", 0),
	))
	tl.HandleBatch(commitBatch(22, commitKey("group-two", "alpha.in", 0)))
	// A sighting whose transaction is never catalogued recovers nothing.
	tl.HandleBatch(commitBatch(33, commitKey("group-three", "ghost.in", 0)))

	require.Len(t, flush(t, tl), 2)

	st := tl.Stats()
	assert.Equal(t, []string{"alpha.in", "zeta.in"}, st.RecoveredTopics,
		"deduplicated across producers and sorted; the unresolved one is absent")
	assert.Equal(t, 2, st.GroupsLinked)
	assert.Equal(t, 2, st.Correlations)
}

func TestTheStatsSeparateRecordsSeenFromTransactionalCommits(t *testing.T) {
	// The keep-up signal needs both numbers, and their RATIO is the diagnostic.
	// The overwhelming majority of __consumer_offsets traffic is ordinary
	// commits and group metadata, so a healthy run on an exactly-once cluster
	// reads a great many records and correlates a small fraction of them. A run
	// reporting zero records read means the tail never got going; a run reading
	// plenty but correlating none means the cluster has no exactly-once traffic
	// — and without both counts those two look identical in the summary.
	cat := NewTxnCatalog()
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	// Two ordinary commits: seen, not correlatable.
	plain := commitBatch(4242, commitKey("plain-group", "orders.in", 0), commitKey("plain-group", "orders.in", 1))
	plain.IsTransactional = false
	tl.HandleBatch(plain)

	// One transactional commit carrying two consumed topic-partitions.
	tl.HandleBatch(commitBatch(4242,
		commitKey("eos-group", "orders.in", 0),
		commitKey("eos-group", "payments.in", 0),
	))

	// A batch on the other topic is not this source's traffic at all.
	other := commitBatch(4242, commitKey("eos-group", "orders.in", 0))
	other.Topic = DefaultTxnStateTopic
	tl.HandleBatch(other)

	st := tl.Stats()
	assert.Equal(t, int64(4), st.RecordsSeen, "every record on OUR topic, transactional or not")
	assert.Equal(t, int64(2), st.TxnRecords, "only the transactional commits that yielded a consumed topic")
}

func TestWhenTheOffsetsTopicIsUnreadableTheProbeShortCircuitsTheTail(t *testing.T) {
	// R13. Reading __consumer_offsets is an optional grant: managed clusters
	// hide the topic and an operator's credentials may simply lack the ACL.
	// That must degrade to the naming heuristic alone, not fail the run — the
	// __transaction_state footprints are still worth having.
	//
	// The flag matters as much as the short-circuit. A phase that never ran and
	// a phase that ran and found nothing both report zero recovered inputs, and
	// the report must not credit the first as evidence the cluster has no
	// read-process-write applications.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)

	var probes int
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{
		Probe: func(context.Context) error {
			probes++
			return errors.New("topic authorization failed")
		},
	})

	require.False(t, tl.Available(context.Background()))

	in := make(chan tail.Batch, 1)
	in <- commitBatch(4242, commitKey("payments-group", "orders.in", 0))
	close(in)
	out := make(chan Observation, 4)
	require.NoError(t, tl.Run(context.Background(), in, out))
	close(out)

	var got []Observation
	for obs := range out {
		got = append(got, obs)
	}
	assert.Empty(t, got, "an unreadable topic correlates nothing")

	st := tl.Stats()
	assert.True(t, st.Unavailable, "the report must not read this as a cluster with no EOS inputs")
	assert.Contains(t, st.UnavailableReason, "topic authorization failed",
		"and the reason has to reach the operator, or the warning is unactionable")
	assert.Zero(t, st.RecordsSeen, "no batch is decoded after a failed probe")
	assert.Equal(t, 1, probes, "probed once for the run, not once per caller")
}

func TestWithNoProbeConfiguredTheTailRunsNormally(t *testing.T) {
	// The probe is injected by the command wiring; the zero value must stay
	// usable, and "no probe" must not read as "unavailable" — that would silently
	// disable the phase for every caller that did not supply one.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	require.True(t, tl.Available(context.Background()))

	tl.HandleBatch(commitBatch(4242, commitKey("payments-group", "orders.in", 0)))

	require.Len(t, flush(t, tl), 1)
	assert.False(t, tl.Stats().Unavailable)
}
