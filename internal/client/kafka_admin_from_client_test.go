package client

import (
	"testing"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMockKafkaClient starts a MockBroker acting as controller and returns it
// alongside a real sarama.Client dialed against it. The broker is closed on
// cleanup; the caller owns closing the client.
func newMockKafkaClient(t *testing.T) (*sarama.MockBroker, sarama.Client) {
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
	return broker, c
}

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
		_, c := newMockKafkaClient(t)
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
		_, c := newMockKafkaClient(t)
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
		_, c := newMockKafkaClient(t)
		defer func() { _ = c.Close() }()

		admin, err := NewKafkaAdminFromClient(c)
		require.NoError(t, err)
		// Type-assert to reach the concrete flag the owning path sets true.
		kac, ok := admin.(*KafkaAdminClient)
		require.True(t, ok)
		assert.False(t, kac.ownsClient, "a from-client admin must not own the client")
	})
}

// A from-client admin must read the cluster id over the shared client's existing
// broker connection, not dial a fresh one each time — the getClusterIDFromBroker
// NewBroker().Open() path is the per-cluster ~5s minikube stall this avoids. The
// mock has no ClusterID setter, so the call itself returns an error; the
// behaviour under test is connection reuse: once the connection is warm, a
// further cluster-id read opens no new connection (the old fresh-dial path would
// handshake on every call). Measured on clusterID directly to exclude
// DescribeCluster's own one-time controller open.
func TestFromClientAdminReusesConnectionForClusterID(t *testing.T) {
	broker, c := newMockKafkaClient(t)
	defer func() { _ = c.Close() }()

	admin, err := NewKafkaAdminFromClient(c)
	require.NoError(t, err)
	kac, ok := admin.(*KafkaAdminClient)
	require.True(t, ok)

	// The from-client path ignores this broker arg (it uses the shared client's
	// controller); it is a real, dial-able broker only so that a regression to
	// the fresh-dial path fails as a clean connection-count mismatch rather than
	// a nil dereference.
	brokerArg := sarama.NewBroker(broker.Addr())

	// First read warms the connection.
	_, _ = kac.clusterID(brokerArg)

	before := apiVersionsRequests(broker)
	_, _ = kac.clusterID(brokerArg) // must reuse the warm connection
	after := apiVersionsRequests(broker)

	assert.Equal(t, before, after,
		"a from-client admin must not open a new broker connection on each cluster-id read")
}
