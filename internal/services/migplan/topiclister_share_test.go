package migplan

import (
	"context"
	"strings"
	"testing"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/testsupport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceTopicListerSharesClient(t *testing.T) {
	_, c := testsupport.MockSaramaClient(t)

	lister, closer, err := sourceTopicLister(nil, c)
	require.NoError(t, err)
	require.NotNil(t, lister)
	require.NotNil(t, closer)

	require.NoError(t, closer.Close())
	assert.False(t, c.Closed(), "sharing a client must not let the lister close it")
}

func TestTargetTopicListerSharesClient(t *testing.T) {
	_, c := testsupport.MockSaramaClient(t)

	lister, closer, err := targetTopicLister(nil, c)
	require.NoError(t, err)
	require.NotNil(t, lister)
	require.NotNil(t, closer)

	require.NoError(t, closer.Close())
	assert.False(t, c.Closed(), "sharing a client must not let the lister close it")
}

// Proves the data path, not just construction: a topic lister backed by a
// from-client admin must return the cluster's real topics.
func TestSharedClientTopicListerListsRealTopics(t *testing.T) {
	broker := sarama.NewMockBroker(t, 1)
	defer broker.Close()

	metadata := sarama.NewMockMetadataResponse(t).
		SetBroker(broker.Addr(), broker.BrokerID()).
		SetController(broker.BrokerID()).
		SetLeader("orders", 0, broker.BrokerID()).
		SetLeader("payments", 0, broker.BrokerID())
	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"ApiVersionsRequest":     sarama.NewMockApiVersionsResponse(t),
		"MetadataRequest":        metadata,
		"DescribeConfigsRequest": sarama.NewMockDescribeConfigsResponse(t),
	})

	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_0_0
	c, err := sarama.NewClient([]string{broker.Addr()}, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	lister, closer, err := sourceTopicLister(nil, c)
	require.NoError(t, err)
	defer func() { _ = closer.Close() }()

	topics, err := lister.ListTopics(context.Background())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"orders", "payments"}, topics,
		"a shared-client-backed topic lister must return the cluster's real topics")
}

// With no shared client, resolution falls back to dialing from the manifest —
// proven here by the manifest-validation error surfacing (no network needed).
func TestTopicListerFallsBackToManifestDial(t *testing.T) {
	_, _, err := sourceTopicLister(gm(nil), nil)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "spec.source.credentials"),
		"nil shared client must delegate to buildSourceTopicLister, got: %v", err)

	_, _, err = targetTopicLister(gm(nil), nil)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "spec.target.kafka"),
		"nil shared client must delegate to buildTargetTopicLister, got: %v", err)
}
