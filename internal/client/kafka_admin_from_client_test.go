package client

import (
	"testing"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMockKafkaClient starts a MockBroker acting as controller and returns a real
// sarama.Client dialed against it. The broker is closed on cleanup; the caller
// owns closing the client.
func newMockKafkaClient(t *testing.T) sarama.Client {
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
	t.Cleanup(func() { broker.Close() })
	return c
}

func TestNewKafkaAdminFromClient(t *testing.T) {
	t.Run("wraps an already-dialed client as a KafkaAdmin", func(t *testing.T) {
		c := newMockKafkaClient(t)
		defer func() { _ = c.Close() }()

		admin, err := NewKafkaAdminFromClient(c)
		require.NoError(t, err)
		require.NotNil(t, admin)
	})

	// The whole point of the from-client path: the topic lister must reuse the
	// offset client's connections without ever closing them — the offset side
	// owns the client's lifetime. If Close() closed the shared client, the
	// offset providers would get a dead client for wait_for_lags/promote after
	// reconcile returns.
	t.Run("Close is a no-op and leaves the shared client open", func(t *testing.T) {
		c := newMockKafkaClient(t)
		defer func() { _ = c.Close() }()

		admin, err := NewKafkaAdminFromClient(c)
		require.NoError(t, err)

		require.NoError(t, admin.Close())
		assert.False(t, c.Closed(), "admin.Close() must NOT close the client it was built from")

		// The shared client is still usable after admin.Close().
		require.NoError(t, c.RefreshMetadata())
	})

	// A freshly dialed admin (NewKafkaAdmin) still owns and closes its client —
	// the no-op behaviour is confined to the from-client path.
	t.Run("does not affect NewKafkaAdmin's owning Close", func(t *testing.T) {
		c := newMockKafkaClient(t)
		defer func() { _ = c.Close() }()

		admin, err := NewKafkaAdminFromClient(c)
		require.NoError(t, err)
		// Type-assert to reach the concrete flag the owning path sets true.
		kac, ok := admin.(*KafkaAdminClient)
		require.True(t, ok)
		assert.False(t, kac.ownsClient, "a from-client admin must not own the client")
	})
}
