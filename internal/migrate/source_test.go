package migrate

import (
	"context"
	"testing"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/mocks"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

// withMockAdmin swaps buildSourceAdmin for one returning the given mock, and
// restores it on cleanup.
func withMockAdmin(t *testing.T, m *mocks.MockKafkaAdmin) {
	t.Helper()
	old := buildSourceAdmin
	buildSourceAdmin = func(types.OSKClusterAuth) (client.KafkaAdmin, error) { return m, nil }
	t.Cleanup(func() { buildSourceAdmin = old })
}

func strPtr(s string) *string { return &s }

func TestOSKSourceReader_ListTopics_SortsNames(t *testing.T) {
	withMockAdmin(t, &mocks.MockKafkaAdmin{
		ListTopicsWithConfigsFunc: func() (map[string]sarama.TopicDetail, error) {
			return map[string]sarama.TopicDetail{
				"zeta":  {},
				"alpha": {},
				"mike":  {},
			}, nil
		},
		CloseFunc: func() error { return nil },
	})

	r := NewOSKSourceReader(types.OSKClusterAuth{})
	got, err := r.ListTopics(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "mike", "zeta"}, got)
}

func TestOSKSourceReader_DescribeTopics_FiltersAndMaps(t *testing.T) {
	withMockAdmin(t, &mocks.MockKafkaAdmin{
		ListTopicsWithNonDefaultConfigsFunc: func() (map[string]sarama.TopicDetail, error) {
			return map[string]sarama.TopicDetail{
				"orders": {
					NumPartitions:     6,
					ReplicationFactor: 3,
					ConfigEntries: map[string]*string{
						"retention.ms":   strPtr("604800000"),
						"cleanup.policy": strPtr("compact"),
						"nilval":         nil, // must be dropped
					},
				},
				"events": {NumPartitions: 1, ReplicationFactor: 1},
				// not requested → must be excluded
				"internal": {NumPartitions: 50, ReplicationFactor: 3},
			}, nil
		},
		CloseFunc: func() error { return nil },
	})

	r := NewOSKSourceReader(types.OSKClusterAuth{})
	got, err := r.DescribeTopics(context.Background(), []string{"orders", "events", "absent"})
	require.NoError(t, err)

	// sorted by name; "absent" is silently skipped; "internal" excluded
	require.Len(t, got, 2)
	require.Equal(t, "events", got[0].Name)
	require.Equal(t, "orders", got[1].Name)

	orders := got[1]
	require.Equal(t, 6, orders.Partitions)
	require.Equal(t, 3, orders.ReplicationFactor)
	require.Equal(t, map[string]string{
		"retention.ms":   "604800000",
		"cleanup.policy": "compact",
	}, orders.Configs)
}
