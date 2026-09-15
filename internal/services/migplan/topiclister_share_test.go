package migplan

import (
	"strings"
	"testing"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSaramaClient dials a real sarama.Client against an in-process MockBroker,
// so the shared-client reuse path can be exercised without a live cluster.
func mockSaramaClient(t *testing.T) sarama.Client {
	t.Helper()
	broker := sarama.NewMockBroker(t, 1)
	metadata := sarama.NewMockMetadataResponse(t).
		SetBroker(broker.Addr(), broker.BrokerID()).
		SetController(broker.BrokerID())
	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t),
		"MetadataRequest":    metadata,
	})
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_0_0
	c, err := sarama.NewClient([]string{broker.Addr()}, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = c.Close()
		broker.Close()
	})
	return c
}

func TestSourceTopicListerSharesClient(t *testing.T) {
	c := mockSaramaClient(t)

	lister, closer, err := sourceTopicLister(nil, c)
	require.NoError(t, err)
	require.NotNil(t, lister)
	require.NotNil(t, closer)

	// Reconcile defers closer.Close(); for a shared client that must be a no-op
	// so the offset side (which owns the client) keeps a live connection for
	// wait_for_lags/promote after reconcile returns.
	require.NoError(t, closer.Close())
	assert.False(t, c.Closed(), "sharing a client must not let the lister close it")
}

func TestTargetTopicListerSharesClient(t *testing.T) {
	c := mockSaramaClient(t)

	lister, closer, err := targetTopicLister(nil, c)
	require.NoError(t, err)
	require.NotNil(t, lister)
	require.NotNil(t, closer)

	require.NoError(t, closer.Close())
	assert.False(t, c.Closed(), "sharing a client must not let the lister close it")
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
