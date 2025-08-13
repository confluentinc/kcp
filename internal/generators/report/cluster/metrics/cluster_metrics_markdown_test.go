package metrics

import (
	"testing"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestAddClusterMetricsSummary_WithNilValues_NoPanics(t *testing.T) {
	// Create a collector instance
	collector := &ClusterMetricsCollector{}

	// Create a cluster with nil FollowerFetching field
	cluster := types.ClusterMetrics{
		ClusterName: "test-cluster",
		ClusterType: "provisioned",
		ClusterMetricsSummary: types.ClusterMetricsSummary{
			AvgIngressThroughputMegabytesPerSecond:  nil,
			PeakIngressThroughputMegabytesPerSecond: nil,
			AvgEgressThroughputMegabytesPerSecond:   nil,
			PeakEgressThroughputMegabytesPerSecond:  nil,
			RetentionDays:                           nil,
			Partitions:                              nil,
			ReplicationFactor:                       nil,
			FollowerFetching:                        nil,
			TieredStorage:                           nil,
			LocalRetentionInPrimaryStorageHours:     nil,
			InstanceType:                            nil,
		},
	}

	// Create a markdown instance
	md := markdown.New()

	assert.NotPanics(t, func() {
		collector.addClusterMetricsSummary(md, cluster)
	}, "Did not expect panic when ClusterMetricsSummary members are nil")
}
