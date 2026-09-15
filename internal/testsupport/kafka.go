package testsupport

import (
	"testing"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/require"
)

// MockSaramaClient starts a MockBroker acting as controller and returns it
// alongside a real sarama.Client dialed against it, so a shared-client code
// path can be exercised without a live cluster. Both the client and the
// broker are closed on cleanup. Extracted from three near-identical copies
// across the migration/topic-lister/admin test suites (execute-tbm,
// migplan, the from-client Kafka admin).
func MockSaramaClient(t *testing.T) (*sarama.MockBroker, sarama.Client) {
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
	return broker, c
}
