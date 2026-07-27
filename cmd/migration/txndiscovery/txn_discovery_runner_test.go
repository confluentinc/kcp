package txndiscovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDescriber stands in for the cluster admin's DescribeTopics.
type fakeDescriber struct {
	md      []*sarama.TopicMetadata
	err     error
	askedes [][]string
}

func (f *fakeDescriber) DescribeTopics(topics []string) ([]*sarama.TopicMetadata, error) {
	f.askedes = append(f.askedes, topics)
	return f.md, f.err
}

func metadataFor(name string, err sarama.KError, partitions int) []*sarama.TopicMetadata {
	md := &sarama.TopicMetadata{Name: name, Err: err, IsInternal: true}
	for i := range partitions {
		md.Partitions = append(md.Partitions, &sarama.PartitionMetadata{ID: int32(i)})
	}
	return []*sarama.TopicMetadata{md}
}

// R12: a readable transaction-state topic is the whole precondition of the run,
// so it passes preflight.
func TestPreflight_ReadableTopic_Passes(t *testing.T) {
	d := &fakeDescriber{md: metadataFor("__transaction_state", sarama.ErrNoError, 50)}

	require.NoError(t, probeTxnStateTopic(d, "__transaction_state"))
	assert.Equal(t, [][]string{{"__transaction_state"}}, d.askedes)
}

// R12, abuse case: U1 turned auto-creation off, so a mistyped --txn-state-topic
// now reaches preflight as an unknown topic rather than being created. Reporting
// it as an ACL problem sends the operator to their Kafka administrator for a
// typo they can fix themselves.
func TestPreflight_UnknownTopic_IsNotReportedAsAuthorization(t *testing.T) {
	d := &fakeDescriber{md: metadataFor("__transaciton_state", sarama.ErrUnknownTopicOrPartition, 0)}

	err := probeTxnStateTopic(d, "__transaciton_state")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
	assert.Contains(t, err.Error(), "--txn-state-topic")
	assert.NotContains(t, strings.ToLower(err.Error()), "acl")
	assert.NotContains(t, strings.ToLower(err.Error()), "credential")
}

// R12: an authorization failure names the two things that can fix it.
func TestPreflight_AuthorizationFailure_NamesCredentialsAndACLs(t *testing.T) {
	for _, kerr := range []sarama.KError{
		sarama.ErrTopicAuthorizationFailed,
		sarama.ErrClusterAuthorizationFailed,
		sarama.ErrSASLAuthenticationFailed,
	} {
		t.Run(kerr.Error(), func(t *testing.T) {
			d := &fakeDescriber{md: metadataFor("__transaction_state", kerr, 0)}

			err := probeTxnStateTopic(d, "__transaction_state")
			require.Error(t, err)
			assert.Contains(t, strings.ToLower(err.Error()), "credential")
			assert.Contains(t, strings.ToLower(err.Error()), "acl")
			assert.NotContains(t, err.Error(), "does not exist")
		})
	}
}

// R12: a broker that cannot be reached at all is the same fail-fast, and its
// cause is still credentials or connectivity rather than a missing topic.
func TestPreflight_UnreachableBroker_FailsNamingCredentialsAndACLs(t *testing.T) {
	d := &fakeDescriber{err: errors.New("dial tcp 10.0.0.1:9092: connect: connection refused")}

	err := probeTxnStateTopic(d, "__transaction_state")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "credential")
	assert.Contains(t, strings.ToLower(err.Error()), "acl")
	assert.Contains(t, err.Error(), "connection refused", "the underlying cause has to survive")
}

// Data sensitivity: every command error is slog.Error'd by main, so it lands in
// kcp.log — the file operators attach to support tickets. The topic name is
// operator-supplied and must not travel with it.
func TestPreflight_Errors_DoNotCarryTheTopicName(t *testing.T) {
	const distinctive = "acme.payments.settlements.txnstate"

	for _, d := range []*fakeDescriber{
		{md: metadataFor(distinctive, sarama.ErrUnknownTopicOrPartition, 0)},
		{md: metadataFor(distinctive, sarama.ErrTopicAuthorizationFailed, 0)},
		{err: errors.New("connection refused")},
		{md: nil},
	} {
		err := probeTxnStateTopic(d, distinctive)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), distinctive)
	}
}

// R12: a response that names no topic at all is a failure, not a pass. Treating
// an empty metadata list as success would let the run start against a cluster
// that never confirmed the topic.
func TestPreflight_EmptyMetadata_Fails(t *testing.T) {
	d := &fakeDescriber{md: nil}

	require.Error(t, probeTxnStateTopic(d, "__transaction_state"))
}

// R10: one tail.Tail serves both readers over ONE channel carrying both topics.
// A shared channel delivers each batch to exactly one receiver, so handing the
// same channel to both readers silently splits the stream between them —
// records vanish with no error anywhere. Every batch must be COPIED to every
// destination.
func TestFanOut_CopiesEveryBatchToEveryDestination(t *testing.T) {
	src := make(chan tail.Batch, 2)
	src <- tail.Batch{Topic: "__transaction_state", Partition: 3, Records: []tail.Record{{Offset: 7}}}
	src <- tail.Batch{Topic: "__consumer_offsets", Partition: 11, ProducerID: 4242}
	close(src)

	a, b := make(chan tail.Batch, 4), make(chan tail.Batch, 4)
	fanOut(context.Background(), src, []chan tail.Batch{a, b})

	got := func(ch chan tail.Batch) []tail.Batch {
		var out []tail.Batch
		for batch := range ch {
			out = append(out, batch)
		}
		return out
	}
	first, second := got(a), got(b)

	require.Len(t, first, 2, "every destination sees every batch")
	assert.Equal(t, first, second, "both destinations see the same batches, in the same order")
	assert.Equal(t, "__transaction_state", first[0].Topic)
	assert.Equal(t, "__consumer_offsets", first[1].Topic)
}

// The fan-out closes its destinations when the source ends: that close is what
// makes the __transaction_state reader return and, crucially, what triggers the
// consumer-offsets tail's final flush.
func TestFanOut_ClosesDestinationsWhenTheSourceEnds(t *testing.T) {
	src := make(chan tail.Batch)
	close(src)

	a := make(chan tail.Batch, 1)
	fanOut(context.Background(), src, []chan tail.Batch{a})

	_, open := <-a
	assert.False(t, open, "the destination must be closed, not merely empty")
}

// A reader that has stopped receiving must not wedge the fan-out: the
// destinations still close, so the other readers still finish and flush.
func TestFanOut_UnreadDestination_DoesNotWedgeShutdown(t *testing.T) {
	src := make(chan tail.Batch, 1)
	src <- tail.Batch{Topic: "__transaction_state"}

	ctx, cancel := context.WithCancel(context.Background())
	blocked := make(chan tail.Batch) // unbuffered and never read
	done := make(chan struct{})
	go func() {
		defer close(done)
		fanOut(ctx, src, []chan tail.Batch{blocked})
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("fanOut blocked on an unread destination after cancellation")
	}
	_, open := <-blocked
	assert.False(t, open, "the destination is closed even on the cancellation path")
}
