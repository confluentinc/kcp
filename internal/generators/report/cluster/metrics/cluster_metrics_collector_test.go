package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/aws/aws-sdk-go-v2/aws"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock implementations
type MockMSKService struct {
	mock.Mock
}

func (m *MockMSKService) DescribeCluster(ctx context.Context, clusterArn *string) (*kafkatypes.Cluster, error) {
	args := m.Called(ctx, clusterArn)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*kafkatypes.Cluster), args.Error(1)
}

func (m *MockMSKService) IsFetchFromFollowerEnabled(ctx context.Context, cluster kafkatypes.Cluster) (*bool, error) {
	args := m.Called(ctx, cluster)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*bool), args.Error(1)
}

func (m *MockMSKService) GetBootstrapBrokers(ctx context.Context, clusterArn *string, authType types.AuthType) ([]string, error) {
	args := m.Called(ctx, clusterArn, authType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

type MockMetricService struct {
	mock.Mock
}

func (m *MockMetricService) GetAverageMetric(clusterName string, metricName string, node *int) (float64, error) {
	args := m.Called(clusterName, metricName, node)
	return args.Get(0).(float64), args.Error(1)
}

func (m *MockMetricService) GetPeakMetric(clusterName string, metricName string, node *int) (float64, error) {
	args := m.Called(clusterName, metricName, node)
	return args.Get(0).(float64), args.Error(1)
}

func (m *MockMetricService) GetServerlessAverageMetric(clusterName string, metricName string) (float64, error) {
	args := m.Called(clusterName, metricName)
	return args.Get(0).(float64), args.Error(1)
}

func (m *MockMetricService) GetServerlessPeakMetric(clusterName string, metricName string) (float64, error) {
	args := m.Called(clusterName, metricName)
	return args.Get(0).(float64), args.Error(1)
}

func (m *MockMetricService) GetAverageBytesInPerSec(clusterName string, numNodes int, topic string) ([]float64, error) {
	args := m.Called(clusterName, numNodes, topic)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]float64), args.Error(1)
}

type MockKafkaAdmin struct {
	mock.Mock
}

func (m *MockKafkaAdmin) ListTopics() (map[string]sarama.TopicDetail, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]sarama.TopicDetail), args.Error(1)
}

func (m *MockKafkaAdmin) GetClusterKafkaMetadata() (*client.ClusterKafkaMetadata, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*client.ClusterKafkaMetadata), args.Error(1)
}

func (m *MockKafkaAdmin) DescribeConfig() ([]sarama.ConfigEntry, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]sarama.ConfigEntry), args.Error(1)
}

func (m *MockKafkaAdmin) ListAcls() ([]sarama.ResourceAcls, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]sarama.ResourceAcls), args.Error(1)
}

func (m *MockKafkaAdmin) Close() error {
	args := m.Called()
	return args.Error(0)
}

// Helper functions
func createTestProvisionedCluster() kafkatypes.Cluster {
	clusterName := "test-cluster"
	kafkaVersion := "3.5.1"
	instanceType := "kafka.m5.large"
	numberOfBrokerNodes := int32(3)
	volumeSize := int32(100)

	return kafkatypes.Cluster{
		ClusterName: &clusterName,
		ClusterType: kafkatypes.ClusterTypeProvisioned,
		Provisioned: &kafkatypes.Provisioned{
			CurrentBrokerSoftwareInfo: &kafkatypes.BrokerSoftwareInfo{
				KafkaVersion: &kafkaVersion,
			},
			BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
				InstanceType: &instanceType,
				StorageInfo: &kafkatypes.StorageInfo{
					EbsStorageInfo: &kafkatypes.EBSStorageInfo{
						VolumeSize: &volumeSize,
					},
				},
			},
			NumberOfBrokerNodes: &numberOfBrokerNodes,
			EnhancedMonitoring:  kafkatypes.EnhancedMonitoringDefault,
			StorageMode:         kafkatypes.StorageModeLocal,
			ClientAuthentication: &kafkatypes.ClientAuthentication{
				Sasl: &kafkatypes.Sasl{
					Iam: &kafkatypes.Iam{
						Enabled: aws.Bool(true),
					},
				},
			},
		},
	}
}

func createTestServerlessCluster() kafkatypes.Cluster {
	clusterName := "test-serverless-cluster"

	return kafkatypes.Cluster{
		ClusterName: &clusterName,
		ClusterType: kafkatypes.ClusterTypeServerless,
		Serverless: &kafkatypes.Serverless{
			ClientAuthentication: &kafkatypes.ServerlessClientAuthentication{
				Sasl: &kafkatypes.ServerlessSasl{
					Iam: &kafkatypes.Iam{
						Enabled: aws.Bool(true),
					},
				},
			},
		},
	}
}

func createTestCollector(mskService MSKService, metricService MetricService, kafkaAdminFactory KafkaAdminFactory) *ClusterMetricsCollector {
	return NewClusterMetrics(mskService, metricService, kafkaAdminFactory, ClusterMetricsOpts{
		Region:     "us-west-2",
		StartDate:  time.Now().AddDate(0, 0, -7),
		EndDate:    time.Now(),
		ClusterArn: "arn:aws:kafka:us-west-2:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012-1",
		AuthType:   types.AuthTypeIAM,
		SkipKafka:  false,
	})
}

// Tests
func TestNewClusterMetrics(t *testing.T) {
	mskService := &MockMSKService{}
	metricService := &MockMetricService{}
	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		return &MockKafkaAdmin{}, nil
	}

	opts := ClusterMetricsOpts{
		Region:     "us-west-2",
		StartDate:  time.Now().AddDate(0, 0, -7),
		EndDate:    time.Now(),
		ClusterArn: "test-arn",
		AuthType:   types.AuthTypeIAM,
		SkipKafka:  false,
	}

	collector := NewClusterMetrics(mskService, metricService, kafkaAdminFactory, opts)

	assert.NotNil(t, collector)
	assert.Equal(t, "us-west-2", collector.region)
	assert.Equal(t, "test-arn", collector.clusterArn)
	assert.Equal(t, types.AuthTypeIAM, collector.authType)
	assert.False(t, collector.skipKafka)
}

func TestClusterMetricsCollector_Run_Success(t *testing.T) {
	mskService := &MockMSKService{}
	metricService := &MockMetricService{}
	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		return &MockKafkaAdmin{}, nil
	}

	cluster := createTestProvisionedCluster()
	mskService.On("DescribeCluster", mock.Anything, mock.Anything).Return(&cluster, nil)
	mskService.On("IsFetchFromFollowerEnabled", mock.Anything, cluster).Return(aws.Bool(false), nil)
	mskService.On("GetBootstrapBrokers", mock.Anything, mock.Anything, mock.Anything).Return([]string{"broker1:9092"}, nil)

	// Mock metric service calls for provisioned cluster
	for i := 1; i <= 3; i++ {
		nodeID := i
		metricService.On("GetAverageMetric", "test-cluster", "BytesInPerSec", &nodeID).Return(100.0, nil)
		metricService.On("GetAverageMetric", "test-cluster", "BytesOutPerSec", &nodeID).Return(50.0, nil)
		metricService.On("GetAverageMetric", "test-cluster", "MessagesInPerSec", &nodeID).Return(1000.0, nil)
		metricService.On("GetAverageMetric", "test-cluster", "KafkaDataLogsDiskUsed", &nodeID).Return(75.0, nil)
		metricService.On("GetAverageMetric", "test-cluster", "RemoteLogSizeBytes", &nodeID).Return(0.0, nil)

		metricService.On("GetPeakMetric", "test-cluster", "BytesInPerSec", &nodeID).Return(200.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "BytesOutPerSec", &nodeID).Return(100.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "MessagesInPerSec", &nodeID).Return(2000.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "KafkaDataLogsDiskUsed", &nodeID).Return(85.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "RemoteLogSizeBytes", &nodeID).Return(0.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "ClientConnectionCount", &nodeID).Return(50.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "PartitionCount", &nodeID).Return(100.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "GlobalTopicCount", &nodeID).Return(10.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "LeaderCount", &nodeID).Return(50.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "ReplicationBytesOutPerSec", &nodeID).Return(25.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "ReplicationBytesInPerSec", &nodeID).Return(25.0, nil)
	}

	collector := createTestCollector(mskService, metricService, kafkaAdminFactory)

	// Mock Kafka admin for replication factor calculation
	mockAdmin := &MockKafkaAdmin{}
	mockAdmin.On("DescribeConfig").Return([]sarama.ConfigEntry{
		{Name: "default.replication.factor", Value: "3"},
	}, nil)
	mockAdmin.On("ListTopics").Return(map[string]sarama.TopicDetail{
		"test-topic": {ReplicationFactor: 3},
	}, nil)
	mockAdmin.On("Close").Return(nil)

	kafkaAdminFactory = func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		return mockAdmin, nil
	}
	collector.kafkaAdminFactory = kafkaAdminFactory

	metricService.On("GetAverageBytesInPerSec", "test-cluster", 3, "test-topic").Return([]float64{100.0, 100.0, 100.0}, nil)

	err := collector.Run()

	assert.NoError(t, err)
	mskService.AssertExpectations(t)
	metricService.AssertExpectations(t)
	mockAdmin.AssertExpectations(t)

	// Clean up generated files
	os.Remove("test-cluster-metrics.json")
	os.Remove("test-cluster-metrics.md")
}

func TestClusterMetricsCollector_Run_DescribeClusterError(t *testing.T) {
	mskService := &MockMSKService{}
	metricService := &MockMetricService{}
	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		return &MockKafkaAdmin{}, nil
	}

	mskService.On("DescribeCluster", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("cluster not found"))

	collector := createTestCollector(mskService, metricService, kafkaAdminFactory)

	err := collector.Run()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Failed to get clusters")
	mskService.AssertExpectations(t)
}

func TestClusterMetricsCollector_processCluster_Provisioned(t *testing.T) {
	mskService := &MockMSKService{}
	metricService := &MockMetricService{}
	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		return &MockKafkaAdmin{}, nil
	}

	cluster := createTestProvisionedCluster()
	mskService.On("IsFetchFromFollowerEnabled", mock.Anything, cluster).Return(aws.Bool(false), nil)
	mskService.On("GetBootstrapBrokers", mock.Anything, mock.Anything, mock.Anything).Return([]string{"broker1:9092"}, nil)

	// Mock metric service calls
	for i := 1; i <= 3; i++ {
		nodeID := i
		metricService.On("GetAverageMetric", "test-cluster", "BytesInPerSec", &nodeID).Return(100.0, nil)
		metricService.On("GetAverageMetric", "test-cluster", "BytesOutPerSec", &nodeID).Return(50.0, nil)
		metricService.On("GetAverageMetric", "test-cluster", "MessagesInPerSec", &nodeID).Return(1000.0, nil)
		metricService.On("GetAverageMetric", "test-cluster", "KafkaDataLogsDiskUsed", &nodeID).Return(75.0, nil)
		metricService.On("GetAverageMetric", "test-cluster", "RemoteLogSizeBytes", &nodeID).Return(0.0, nil)

		metricService.On("GetPeakMetric", "test-cluster", "BytesInPerSec", &nodeID).Return(200.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "BytesOutPerSec", &nodeID).Return(100.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "MessagesInPerSec", &nodeID).Return(2000.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "KafkaDataLogsDiskUsed", &nodeID).Return(85.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "RemoteLogSizeBytes", &nodeID).Return(0.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "ClientConnectionCount", &nodeID).Return(50.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "PartitionCount", &nodeID).Return(100.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "GlobalTopicCount", &nodeID).Return(10.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "LeaderCount", &nodeID).Return(50.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "ReplicationBytesOutPerSec", &nodeID).Return(25.0, nil)
		metricService.On("GetPeakMetric", "test-cluster", "ReplicationBytesInPerSec", &nodeID).Return(25.0, nil)
	}

	collector := createTestCollector(mskService, metricService, kafkaAdminFactory)

	// Mock Kafka admin for replication factor calculation
	mockAdmin := &MockKafkaAdmin{}
	mockAdmin.On("DescribeConfig").Return([]sarama.ConfigEntry{
		{Name: "default.replication.factor", Value: "3"},
	}, nil)
	mockAdmin.On("ListTopics").Return(map[string]sarama.TopicDetail{
		"test-topic": {ReplicationFactor: 3},
	}, nil)
	mockAdmin.On("Close").Return(nil)

	kafkaAdminFactory = func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		return mockAdmin, nil
	}
	collector.kafkaAdminFactory = kafkaAdminFactory

	metricService.On("GetAverageBytesInPerSec", "test-cluster", 3, "test-topic").Return([]float64{100.0, 100.0, 100.0}, nil)

	result, err := collector.processCluster(cluster)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "test-cluster", result.ClusterName)
	assert.Equal(t, "PROVISIONED", result.ClusterType)
	assert.Len(t, result.NodesMetrics, 3)
	assert.NotNil(t, result.ClusterMetricsSummary.InstanceType)
	assert.Equal(t, "kafka.m5.large", *result.ClusterMetricsSummary.InstanceType)

	mskService.AssertExpectations(t)
	metricService.AssertExpectations(t)
	mockAdmin.AssertExpectations(t)
}

func TestClusterMetricsCollector_processCluster_Serverless(t *testing.T) {
	mskService := &MockMSKService{}
	metricService := &MockMetricService{}
	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		return &MockKafkaAdmin{}, nil
	}

	cluster := createTestServerlessCluster()
	mskService.On("IsFetchFromFollowerEnabled", mock.Anything, cluster).Return(aws.Bool(false), nil)

	// Mock metric service calls for serverless
	metricService.On("GetServerlessAverageMetric", "test-serverless-cluster", "BytesInPerSec").Return(100.0, nil)
	metricService.On("GetServerlessAverageMetric", "test-serverless-cluster", "BytesOutPerSec").Return(50.0, nil)
	metricService.On("GetServerlessAverageMetric", "test-serverless-cluster", "MessagesInPerSec").Return(1000.0, nil)
	metricService.On("GetServerlessAverageMetric", "test-serverless-cluster", "KafkaDataLogsDiskUsed").Return(75.0, nil)
	metricService.On("GetServerlessAverageMetric", "test-serverless-cluster", "RemoteLogSizeBytes").Return(0.0, nil)

	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "BytesInPerSec").Return(200.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "BytesOutPerSec").Return(100.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "MessagesInPerSec").Return(2000.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "KafkaDataLogsDiskUsed").Return(85.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "RemoteLogSizeBytes").Return(0.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "ClientConnectionCount").Return(50.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "PartitionCount").Return(100.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "GlobalTopicCount").Return(10.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "LeaderCount").Return(50.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "ReplicationBytesOutPerSec").Return(25.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "ReplicationBytesInPerSec").Return(25.0, nil)

	collector := createTestCollector(mskService, metricService, kafkaAdminFactory)

	result, err := collector.processCluster(cluster)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "test-serverless-cluster", result.ClusterName)
	assert.Equal(t, "SERVERLESS", result.ClusterType)
	assert.Len(t, result.NodesMetrics, 1)
	assert.Nil(t, result.ClusterMetricsSummary.InstanceType)

	mskService.AssertExpectations(t)
	metricService.AssertExpectations(t)
}

func TestClusterMetricsCollector_calculateRetention(t *testing.T) {
	collector := createTestCollector(&MockMSKService{}, &MockMetricService{}, nil)

	nodesMetrics := []types.NodeMetrics{
		{
			BytesInPerSecAvg:         100.0,
			KafkaDataLogsDiskUsedAvg: 75.0,
			RemoteLogSizeBytesAvg:    0.0,
			VolumeSizeGB:             aws.Int(100),
		},
		{
			BytesInPerSecAvg:         200.0,
			KafkaDataLogsDiskUsedAvg: 50.0,
			RemoteLogSizeBytesAvg:    1000.0,
			VolumeSizeGB:             aws.Int(200),
		},
	}

	retentionDays, localRetentionHours := collector.calculateRetention(nodesMetrics)

	// Expected calculations:
	// totalBytesInPerDay = (100 + 200) * 60 * 60 * 24 = 25,920,000 bytes/day
	// totalLocalStorageUsed = (75/100 * 100 + 50/100 * 200) * 1024^3 = 75 + 100 = 175 GB = 187,904,819,200 bytes
	// totalRemoteStorageUsed = 0 + 1000 = 1000 bytes
	// retention_days = (187,904,819,200 + 1000) / 25,920,000 ≈ 7,247.56 days
	// local_retention_hours = 187,904,819,200 / 25,920,000 ≈ 7,247.56 days = 173,941.44 hours

	assert.Greater(t, retentionDays, 7000.0)
	assert.Greater(t, localRetentionHours, 7000.0) // This should be days, not hours
}

func TestClusterMetricsCollector_calculateClusterMetricsSummary(t *testing.T) {
	collector := createTestCollector(&MockMSKService{}, &MockMetricService{}, nil)

	nodesMetrics := []types.NodeMetrics{
		{
			BytesInPerSecAvg:  100.0,
			BytesOutPerSecAvg: 50.0,
			BytesInPerSecMax:  200.0,
			BytesOutPerSecMax: 100.0,
			PartitionCountMax: 100,
		},
		{
			BytesInPerSecAvg:  200.0,
			BytesOutPerSecAvg: 100.0,
			BytesInPerSecMax:  400.0,
			BytesOutPerSecMax: 200.0,
			PartitionCountMax: 150,
		},
	}

	summary := collector.calculateClusterMetricsSummary(nodesMetrics)

	// Expected calculations:
	// avgIngressThroughputMegabytesPerSecond = (100 + 200) / 1024 / 1024 ≈ 0.000286 MB/s
	// peakIngressThroughputMegabytesPerSecond = (200 + 400) / 1024 / 1024 ≈ 0.000572 MB/s
	// avgEgressThroughputMegabytesPerSecond = (50 + 100) / 1024 / 1024 ≈ 0.000143 MB/s
	// peakEgressThroughputMegabytesPerSecond = (100 + 200) / 1024 / 1024 ≈ 0.000286 MB/s
	// partitions = 100 + 150 = 250

	assert.NotNil(t, summary.AvgIngressThroughputMegabytesPerSecond)
	assert.NotNil(t, summary.PeakIngressThroughputMegabytesPerSecond)
	assert.NotNil(t, summary.AvgEgressThroughputMegabytesPerSecond)
	assert.NotNil(t, summary.PeakEgressThroughputMegabytesPerSecond)
	assert.NotNil(t, summary.Partitions)
	assert.Equal(t, 250.0, *summary.Partitions)
}

func TestClusterMetricsCollector_calculateClusterMetricsSummary_EmptyNodes(t *testing.T) {
	collector := createTestCollector(&MockMSKService{}, &MockMetricService{}, nil)

	summary := collector.calculateClusterMetricsSummary([]types.NodeMetrics{})

	assert.Equal(t, types.ClusterMetricsSummary{}, summary)
}

func TestClusterMetricsCollector_calculateReplicationFactor_SkipKafka(t *testing.T) {
	mskService := &MockMSKService{}
	metricService := &MockMetricService{}
	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		return &MockKafkaAdmin{}, nil
	}

	collector := createTestCollector(mskService, metricService, kafkaAdminFactory)
	collector.skipKafka = true

	cluster := createTestProvisionedCluster()
	result, err := collector.calculateReplicationFactor(cluster)

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestClusterMetricsCollector_calculateReplicationFactor_GetBootstrapBrokersError(t *testing.T) {
	mskService := &MockMSKService{}
	metricService := &MockMetricService{}
	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		return &MockKafkaAdmin{}, nil
	}

	collector := createTestCollector(mskService, metricService, kafkaAdminFactory)

	cluster := createTestProvisionedCluster()
	mskService.On("GetBootstrapBrokers", mock.Anything, mock.Anything, mock.Anything).Return(nil, fmt.Errorf("failed to get brokers"))

	result, err := collector.calculateReplicationFactor(cluster)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "Failed to get bootstrap brokers")
	mskService.AssertExpectations(t)
}

func TestClusterMetricsCollector_calculateReplicationFactor_KafkaAdminFactoryError(t *testing.T) {
	mskService := &MockMSKService{}
	metricService := &MockMetricService{}
	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		return nil, fmt.Errorf("failed to create admin client")
	}

	collector := createTestCollector(mskService, metricService, kafkaAdminFactory)

	cluster := createTestProvisionedCluster()
	mskService.On("GetBootstrapBrokers", mock.Anything, mock.Anything, mock.Anything).Return([]string{"broker1:9092"}, nil)

	result, err := collector.calculateReplicationFactor(cluster)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "Failed to setup admin client")
	mskService.AssertExpectations(t)
}

func TestClusterMetricsCollector_calculateReplicationFactor_Success(t *testing.T) {
	mskService := &MockMSKService{}
	metricService := &MockMetricService{}

	collector := createTestCollector(mskService, metricService, nil)

	cluster := createTestProvisionedCluster()
	mskService.On("GetBootstrapBrokers", mock.Anything, mock.Anything, mock.Anything).Return([]string{"broker1:9092"}, nil)

	// Mock Kafka admin
	mockAdmin := &MockKafkaAdmin{}
	mockAdmin.On("DescribeConfig").Return([]sarama.ConfigEntry{
		{Name: "default.replication.factor", Value: "3"},
	}, nil)
	mockAdmin.On("ListTopics").Return(map[string]sarama.TopicDetail{
		"topic1": {ReplicationFactor: 3},
		"topic2": {ReplicationFactor: 2},
	}, nil)
	mockAdmin.On("Close").Return(nil)

	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		return mockAdmin, nil
	}
	collector.kafkaAdminFactory = kafkaAdminFactory

	metricService.On("GetAverageBytesInPerSec", "test-cluster", 3, "topic1").Return([]float64{100.0, 100.0, 100.0}, nil)
	metricService.On("GetAverageBytesInPerSec", "test-cluster", 3, "topic2").Return([]float64{50.0, 50.0, 50.0}, nil)

	result, err := collector.calculateReplicationFactor(cluster)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Expected: (300*3 + 150*2) / (300 + 150) = (900 + 300) / 450 = 1200 / 450 = 2.67
	assert.InDelta(t, 2.67, *result, 0.01)

	mskService.AssertExpectations(t)
	metricService.AssertExpectations(t)
	mockAdmin.AssertExpectations(t)
}

func TestClusterMetricsCollector_processProvisionedNode(t *testing.T) {
	mskService := &MockMSKService{}
	metricService := &MockMetricService{}
	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		return &MockKafkaAdmin{}, nil
	}

	collector := createTestCollector(mskService, metricService, kafkaAdminFactory)

	nodeID := 1
	metricService.On("GetAverageMetric", "test-cluster", "BytesInPerSec", &nodeID).Return(100.0, nil)
	metricService.On("GetAverageMetric", "test-cluster", "BytesOutPerSec", &nodeID).Return(50.0, nil)
	metricService.On("GetAverageMetric", "test-cluster", "MessagesInPerSec", &nodeID).Return(1000.0, nil)
	metricService.On("GetAverageMetric", "test-cluster", "KafkaDataLogsDiskUsed", &nodeID).Return(75.0, nil)
	metricService.On("GetAverageMetric", "test-cluster", "RemoteLogSizeBytes", &nodeID).Return(0.0, nil)

	metricService.On("GetPeakMetric", "test-cluster", "BytesInPerSec", &nodeID).Return(200.0, nil)
	metricService.On("GetPeakMetric", "test-cluster", "BytesOutPerSec", &nodeID).Return(100.0, nil)
	metricService.On("GetPeakMetric", "test-cluster", "MessagesInPerSec", &nodeID).Return(2000.0, nil)
	metricService.On("GetPeakMetric", "test-cluster", "KafkaDataLogsDiskUsed", &nodeID).Return(85.0, nil)
	metricService.On("GetPeakMetric", "test-cluster", "RemoteLogSizeBytes", &nodeID).Return(0.0, nil)
	metricService.On("GetPeakMetric", "test-cluster", "ClientConnectionCount", &nodeID).Return(50.0, nil)
	metricService.On("GetPeakMetric", "test-cluster", "PartitionCount", &nodeID).Return(100.0, nil)
	metricService.On("GetPeakMetric", "test-cluster", "GlobalTopicCount", &nodeID).Return(10.0, nil)
	metricService.On("GetPeakMetric", "test-cluster", "LeaderCount", &nodeID).Return(50.0, nil)
	metricService.On("GetPeakMetric", "test-cluster", "ReplicationBytesOutPerSec", &nodeID).Return(25.0, nil)
	metricService.On("GetPeakMetric", "test-cluster", "ReplicationBytesInPerSec", &nodeID).Return(25.0, nil)

	result, err := collector.processProvisionedNode("test-cluster", 1, "kafka.m5.large")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 1, result.NodeID)
	assert.Equal(t, "kafka.m5.large", *result.InstanceType)
	assert.Equal(t, 100.0, result.BytesInPerSecAvg)
	assert.Equal(t, 50.0, result.BytesOutPerSecAvg)
	assert.Equal(t, 200.0, result.BytesInPerSecMax)
	assert.Equal(t, 100.0, result.BytesOutPerSecMax)

	metricService.AssertExpectations(t)
}

func TestClusterMetricsCollector_processProvisionedNode_MetricError(t *testing.T) {
	mskService := &MockMSKService{}
	metricService := &MockMetricService{}
	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		return &MockKafkaAdmin{}, nil
	}

	collector := createTestCollector(mskService, metricService, kafkaAdminFactory)

	nodeID := 1
	// Set up all the metric calls that will be made, with the first one returning an error
	metricService.On("GetAverageMetric", "test-cluster", "BytesInPerSec", &nodeID).Return(0.0, fmt.Errorf("metric error"))
	metricService.On("GetAverageMetric", "test-cluster", "BytesOutPerSec", &nodeID).Return(50.0, nil)
	metricService.On("GetAverageMetric", "test-cluster", "MessagesInPerSec", &nodeID).Return(1000.0, nil)
	metricService.On("GetAverageMetric", "test-cluster", "KafkaDataLogsDiskUsed", &nodeID).Return(75.0, nil)
	metricService.On("GetAverageMetric", "test-cluster", "RemoteLogSizeBytes", &nodeID).Return(0.0, nil)

	result, err := collector.processProvisionedNode("test-cluster", 1, "kafka.m5.large")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to get metric BytesInPerSec")

	metricService.AssertExpectations(t)
}

func TestClusterMetricsCollector_processServerlessNode(t *testing.T) {
	mskService := &MockMSKService{}
	metricService := &MockMetricService{}
	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		return &MockKafkaAdmin{}, nil
	}

	collector := createTestCollector(mskService, metricService, kafkaAdminFactory)

	metricService.On("GetServerlessAverageMetric", "test-cluster", "BytesInPerSec").Return(100.0, nil)
	metricService.On("GetServerlessAverageMetric", "test-cluster", "BytesOutPerSec").Return(50.0, nil)
	metricService.On("GetServerlessAverageMetric", "test-cluster", "MessagesInPerSec").Return(1000.0, nil)
	metricService.On("GetServerlessAverageMetric", "test-cluster", "KafkaDataLogsDiskUsed").Return(75.0, nil)
	metricService.On("GetServerlessAverageMetric", "test-cluster", "RemoteLogSizeBytes").Return(0.0, nil)

	metricService.On("GetServerlessPeakMetric", "test-cluster", "BytesInPerSec").Return(200.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-cluster", "BytesOutPerSec").Return(100.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-cluster", "MessagesInPerSec").Return(2000.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-cluster", "KafkaDataLogsDiskUsed").Return(85.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-cluster", "RemoteLogSizeBytes").Return(0.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-cluster", "ClientConnectionCount").Return(50.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-cluster", "PartitionCount").Return(100.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-cluster", "GlobalTopicCount").Return(10.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-cluster", "LeaderCount").Return(50.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-cluster", "ReplicationBytesOutPerSec").Return(25.0, nil)
	metricService.On("GetServerlessPeakMetric", "test-cluster", "ReplicationBytesInPerSec").Return(25.0, nil)

	result, err := collector.processServerlessNode("test-cluster")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 1, result.NodeID)
	assert.Nil(t, result.InstanceType)
	assert.Equal(t, 100.0, result.BytesInPerSecAvg)
	assert.Equal(t, 50.0, result.BytesOutPerSecAvg)
	assert.Equal(t, 200.0, result.BytesInPerSecMax)
	assert.Equal(t, 100.0, result.BytesOutPerSecMax)

	metricService.AssertExpectations(t)
}

func TestClusterMetricsCollector_writeOutput(t *testing.T) {
	mskService := &MockMSKService{}
	metricService := &MockMetricService{}
	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		return &MockKafkaAdmin{}, nil
	}

	collector := createTestCollector(mskService, metricService, kafkaAdminFactory)

	avgIngress := 0.0003
	peakIngress := 0.0006
	avgEgress := 0.0001
	peakEgress := 0.0003
	partitions := 300.0
	retentionDays := 9320.6756
	replicationFactor := 3.0
	followerFetching := false
	tieredStorage := false
	instanceType := "kafka.m5.large"

	metrics := types.ClusterMetrics{
		ClusterName: "test-cluster",
		ClusterType: "PROVISIONED",
		NodesMetrics: []types.NodeMetrics{
			{
				NodeID:       1,
				InstanceType: aws.String("kafka.m5.large"),
			},
		},
		ClusterMetricsSummary: types.ClusterMetricsSummary{
			AvgIngressThroughputMegabytesPerSecond:  &avgIngress,
			PeakIngressThroughputMegabytesPerSecond: &peakIngress,
			AvgEgressThroughputMegabytesPerSecond:   &avgEgress,
			PeakEgressThroughputMegabytesPerSecond:  &peakEgress,
			Partitions:                              &partitions,
			RetentionDays:                           &retentionDays,
			ReplicationFactor:                       &replicationFactor,
			FollowerFetching:                        &followerFetching,
			TieredStorage:                           &tieredStorage,
			InstanceType:                            &instanceType,
		},
	}

	err := collector.writeOutput(metrics)

	assert.NoError(t, err)

	// Verify JSON file was created
	jsonData, err := os.ReadFile("test-cluster-metrics.json")
	assert.NoError(t, err)

	var result types.ClusterMetrics
	err = json.Unmarshal(jsonData, &result)
	assert.NoError(t, err)
	assert.Equal(t, "test-cluster", result.ClusterName)
	assert.Equal(t, "PROVISIONED", result.ClusterType)

	// Verify Markdown file was created
	_, err = os.Stat("test-cluster-metrics.md")
	assert.NoError(t, err)

	// Clean up
	os.Remove("test-cluster-metrics.json")
	os.Remove("test-cluster-metrics.md")
}

func TestStructToMap(t *testing.T) {
	type TestStruct struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	testStruct := TestStruct{
		Name:  "test",
		Value: 42,
	}

	result, err := structToMap(testStruct)

	assert.NoError(t, err)
	assert.Equal(t, "test", result["name"])
	assert.Equal(t, float64(42), result["value"]) // JSON unmarshals numbers as float64
}

func TestStructToMap_NilInput(t *testing.T) {
	result, err := structToMap(nil)

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestStructToMap_InvalidJSON(t *testing.T) {
	// Create a struct that can't be marshaled to JSON
	type InvalidStruct struct {
		Channel chan int `json:"channel"` // Channels can't be marshaled to JSON
	}

	invalidStruct := InvalidStruct{
		Channel: make(chan int),
	}

	result, err := structToMap(invalidStruct)

	assert.Error(t, err)
	assert.Nil(t, result)
}
