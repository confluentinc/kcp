package metrics

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestBuildProvisionedMetadata(t *testing.T) {
	timeWindow := types.CloudWatchTimeWindow{
		StartTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		Period:    86400,
	}

	t.Run("fully populated cluster returns correct metadata", func(t *testing.T) {
		cluster := kafkatypes.Cluster{
			ClusterName: aws.String("test-cluster"),
			ClusterType: kafkatypes.ClusterTypeProvisioned,
			ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123:cluster/test/abc"),
			Provisioned: &kafkatypes.Provisioned{
				NumberOfBrokerNodes: aws.Int32(3),
				EnhancedMonitoring:  kafkatypes.EnhancedMonitoringDefault,
				StorageMode:         kafkatypes.StorageModeLocal,
				CurrentBrokerSoftwareInfo: &kafkatypes.BrokerSoftwareInfo{
					KafkaVersion: aws.String("3.5.1"),
				},
				BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
					InstanceType:        aws.String("kafka.m5.large"),
					BrokerAZDistribution: kafkatypes.BrokerAZDistributionDefault,
				},
			},
		}
		meta := buildProvisionedMetadata(cluster, timeWindow, false)
		assert.Equal(t, 3, meta.NumberOfBrokerNodes)
		assert.Equal(t, "3.5.1", meta.KafkaVersion)
		assert.Equal(t, "kafka.m5.large", meta.InstanceType)
		assert.Equal(t, string(kafkatypes.EnhancedMonitoringDefault), meta.EnhancedMonitoring)
		assert.Equal(t, types.BrokerTypeStandard, meta.BrokerType)
	})

	t.Run("nil BrokerNodeGroupInfo defaults gracefully", func(t *testing.T) {
		cluster := kafkatypes.Cluster{
			ClusterName: aws.String("test-cluster"),
			ClusterType: kafkatypes.ClusterTypeProvisioned,
			Provisioned: &kafkatypes.Provisioned{
				NumberOfBrokerNodes: aws.Int32(3),
				EnhancedMonitoring:  kafkatypes.EnhancedMonitoringDefault,
				BrokerNodeGroupInfo: nil,
				CurrentBrokerSoftwareInfo: &kafkatypes.BrokerSoftwareInfo{
					KafkaVersion: aws.String("3.5.1"),
				},
			},
		}
		meta := buildProvisionedMetadata(cluster, timeWindow, false)
		assert.Equal(t, 3, meta.NumberOfBrokerNodes)
		assert.Equal(t, "3.5.1", meta.KafkaVersion)
		assert.Empty(t, meta.InstanceType)
		assert.Empty(t, meta.BrokerAzDistribution)
	})

	t.Run("nil CurrentBrokerSoftwareInfo defaults gracefully", func(t *testing.T) {
		cluster := kafkatypes.Cluster{
			ClusterName: aws.String("test-cluster"),
			ClusterType: kafkatypes.ClusterTypeProvisioned,
			Provisioned: &kafkatypes.Provisioned{
				NumberOfBrokerNodes:       aws.Int32(3),
				CurrentBrokerSoftwareInfo: nil,
				BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
					InstanceType: aws.String("kafka.m5.large"),
				},
			},
		}
		meta := buildProvisionedMetadata(cluster, timeWindow, false)
		assert.Empty(t, meta.KafkaVersion)
		assert.Equal(t, 3, meta.NumberOfBrokerNodes)
	})

	t.Run("nil NumberOfBrokerNodes defaults to 0", func(t *testing.T) {
		cluster := kafkatypes.Cluster{
			ClusterName: aws.String("test-cluster"),
			ClusterType: kafkatypes.ClusterTypeProvisioned,
			Provisioned: &kafkatypes.Provisioned{
				NumberOfBrokerNodes: nil,
				CurrentBrokerSoftwareInfo: &kafkatypes.BrokerSoftwareInfo{
					KafkaVersion: aws.String("3.5.1"),
				},
				BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
					InstanceType: aws.String("kafka.m5.large"),
				},
			},
		}
		meta := buildProvisionedMetadata(cluster, timeWindow, false)
		assert.Equal(t, 0, meta.NumberOfBrokerNodes)
	})

	t.Run("express broker type detected from instance type prefix", func(t *testing.T) {
		cluster := kafkatypes.Cluster{
			ClusterName: aws.String("test-cluster"),
			ClusterType: kafkatypes.ClusterTypeProvisioned,
			Provisioned: &kafkatypes.Provisioned{
				NumberOfBrokerNodes: aws.Int32(3),
				CurrentBrokerSoftwareInfo: &kafkatypes.BrokerSoftwareInfo{
					KafkaVersion: aws.String("3.5.1"),
				},
				BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
					InstanceType: aws.String("express.m5.4xlarge"),
				},
			},
		}
		meta := buildProvisionedMetadata(cluster, timeWindow, false)
		assert.Equal(t, types.BrokerTypeExpress, meta.BrokerType)
	})
}
