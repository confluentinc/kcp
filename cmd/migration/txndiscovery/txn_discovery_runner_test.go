package txndiscovery

import (
	"errors"
	"strings"
	"testing"

	"github.com/IBM/sarama"
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
