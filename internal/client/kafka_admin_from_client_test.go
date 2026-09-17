package client

import (
	"testing"

	"github.com/IBM/sarama"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/testsupport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// apiVersionsRequests counts how many broker connection handshakes
// (ApiVersionsRequest) the mock has served — one per fresh connection opened.
func apiVersionsRequests(b *sarama.MockBroker) int {
	n := 0
	for _, rr := range b.History() {
		if _, ok := rr.Request.(*sarama.ApiVersionsRequest); ok {
			n++
		}
	}
	return n
}

func TestNewKafkaAdminFromClient(t *testing.T) {
	t.Run("wraps an already-dialed client as a KafkaAdmin", func(t *testing.T) {
		_, c := testsupport.MockSaramaClient(t)

		admin, err := NewKafkaAdminFromClient(c)
		require.NoError(t, err)
		require.NotNil(t, admin)
	})

	t.Run("Close is a no-op and leaves the shared client open", func(t *testing.T) {
		_, c := testsupport.MockSaramaClient(t)

		admin, err := NewKafkaAdminFromClient(c)
		require.NoError(t, err)

		require.NoError(t, admin.Close())
		assert.False(t, c.Closed(), "admin.Close() must NOT close the client it was built from")
		require.NoError(t, c.RefreshMetadata(), "the shared client is still usable after admin.Close()")
	})

	// The no-op behaviour above is confined to the from-client path; a natively
	// dialed admin must still own and close its client.
	t.Run("does not affect NewKafkaAdmin's owning Close", func(t *testing.T) {
		broker := sarama.NewMockBroker(t, 1)
		defer broker.Close()
		metadata := sarama.NewMockMetadataResponse(t).
			SetBroker(broker.Addr(), broker.BrokerID()).
			SetController(broker.BrokerID())
		broker.SetHandlerByMap(map[string]sarama.MockResponse{
			"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t),
			"MetadataRequest":    metadata,
		})

		admin, err := NewKafkaAdmin([]string{broker.Addr()}, kafkatypes.ClientBrokerTls, "", "2.8.0", WithUnauthenticatedPlaintextAuth())
		require.NoError(t, err)

		kac, ok := admin.(*KafkaAdminClient)
		require.True(t, ok)
		require.Nil(t, kac.client, "a natively dialed admin holds no shared client")

		require.NoError(t, admin.Close())
		_, err = kac.admin.Controller()
		assert.ErrorIs(t, err, sarama.ErrClosedClient, "NewKafkaAdmin's Close() must actually close its own client")
	})
}

// A from-client admin must read the cluster id over the shared client's
// existing connection, not dial a fresh one each call. The mock has no
// ClusterID setter, so clusterIDFromClient itself errors; what's under test
// is that a second call opens no new broker connection.
func TestFromClientAdminReusesConnectionForClusterID(t *testing.T) {
	broker, c := testsupport.MockSaramaClient(t)

	admin, err := NewKafkaAdminFromClient(c)
	require.NoError(t, err)
	kac, ok := admin.(*KafkaAdminClient)
	require.True(t, ok)

	// First read warms the connection.
	_, _ = kac.clusterIDFromClient()

	before := apiVersionsRequests(broker)
	_, _ = kac.clusterIDFromClient() // must reuse the warm connection
	after := apiVersionsRequests(broker)

	assert.Equal(t, before, after,
		"a from-client admin must not open a new broker connection on each cluster-id read")
}
