package executetbm

import (
	"testing"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestOffsetClient(t *testing.T) {
	t.Run("returns the underlying client for a real offset service", func(t *testing.T) {
		c := mockSaramaClient(t)
		p := offset.NewOffsetService(c)
		assert.True(t, offsetClient(p) == c, "offsetClient must surface the service's own client for reuse")
	})

	t.Run("returns nil for a provider that exposes no client", func(t *testing.T) {
		// A stub provider (as the command's own tests use) surfaces no client, so
		// reconcile falls back to dialing its own topic-lister connections.
		assert.Nil(t, offsetClient(zeroLagOffsetProvider{}))
	})
}
