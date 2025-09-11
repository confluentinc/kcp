package metrics

// import (
// 	"context"
// 	"testing"
// 	"time"

// 	"github.com/aws/aws-sdk-go-v2/aws"
// 	"github.com/aws/aws-sdk-go-v2/service/kafka"
// 	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
// 	"github.com/confluentinc/kcp/internal/types"
// 	"github.com/stretchr/testify/assert"
// 	"github.com/stretchr/testify/mock"
// )

// // Mock implementations
// type MockMSKService struct {
// 	mock.Mock
// }

// func (m *MockMSKService) DescribeCluster(ctx context.Context, clusterArn *string) (*kafkatypes.Cluster, error) {
// 	args := m.Called(ctx, clusterArn)
// 	if args.Get(0) == nil {
// 		return nil, args.Error(1)
// 	}
// 	return args.Get(0).(*kafkatypes.Cluster), args.Error(1)
// }

// func (m *MockMSKService) IsFetchFromFollowerEnabled(ctx context.Context, cluster kafkatypes.Cluster) (*bool, error) {
// 	args := m.Called(ctx, cluster)
// 	if args.Get(0) == nil {
// 		return nil, args.Error(1)
// 	}
// 	return args.Get(0).(*bool), args.Error(1)
// }

// func (m *MockMSKService) GetBootstrapBrokers(ctx context.Context, clusterArn *string) (*kafka.GetBootstrapBrokersOutput, error) {
// 	args := m.Called(ctx, clusterArn)
// 	if args.Get(0) == nil {
// 		return nil, args.Error(1)
// 	}
// 	return args.Get(0).(*kafka.GetBootstrapBrokersOutput), args.Error(1)
// }

// func (m *MockMSKService) ParseBrokerAddresses(brokers kafka.GetBootstrapBrokersOutput, authType types.AuthType) ([]string, error) {
// 	args := m.Called(brokers, authType)
// 	if args.Get(0) == nil {
// 		return nil, args.Error(1)
// 	}
// 	return args.Get(0).([]string), args.Error(1)
// }

// type MockMetricService struct {
// 	mock.Mock
// }

// func (m *MockMetricService) GetAverageMetric(clusterName string, metricName string, node *int) (float64, error) {
// 	args := m.Called(clusterName, metricName, node)
// 	return args.Get(0).(float64), args.Error(1)
// }

// func (m *MockMetricService) GetPeakMetric(clusterName string, metricName string, node *int) (float64, error) {
// 	args := m.Called(clusterName, metricName, node)
// 	return args.Get(0).(float64), args.Error(1)
// }

// func (m *MockMetricService) GetServerlessAverageMetric(clusterName string, metricName string) (float64, error) {
// 	args := m.Called(clusterName, metricName)
// 	return args.Get(0).(float64), args.Error(1)
// }

// func (m *MockMetricService) GetServerlessPeakMetric(clusterName string, metricName string) (float64, error) {
// 	args := m.Called(clusterName, metricName)
// 	return args.Get(0).(float64), args.Error(1)
// }

// func (m *MockMetricService) GetAverageBytesInPerSec(clusterName string, numNodes int, topic string) ([]float64, error) {
// 	args := m.Called(clusterName, numNodes, topic)
// 	if args.Get(0) == nil {
// 		return nil, args.Error(1)
// 	}
// 	return args.Get(0).([]float64), args.Error(1)
// }

// func (m *MockMetricService) GetGlobalMetric(clusterName string, metricName string) (float64, error) {
// 	args := m.Called(clusterName, metricName)
// 	return args.Get(0).(float64), args.Error(1)
// }

// // Helper functions
// func createTestProvisionedCluster() kafkatypes.Cluster {
// 	clusterName := "test-cluster"
// 	kafkaVersion := "3.5.1"
// 	instanceType := "kafka.m5.large"
// 	numberOfBrokerNodes := int32(3)
// 	volumeSize := int32(100)

// 	return kafkatypes.Cluster{
// 		ClusterArn:  aws.String("arn:aws:kafka:us-west-2:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012-1"),
// 		ClusterName: &clusterName,
// 		ClusterType: kafkatypes.ClusterTypeProvisioned,
// 		Provisioned: &kafkatypes.Provisioned{
// 			CurrentBrokerSoftwareInfo: &kafkatypes.BrokerSoftwareInfo{
// 				KafkaVersion: &kafkaVersion,
// 			},
// 			BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
// 				InstanceType: &instanceType,
// 				StorageInfo: &kafkatypes.StorageInfo{
// 					EbsStorageInfo: &kafkatypes.EBSStorageInfo{
// 						VolumeSize: &volumeSize,
// 					},
// 				},
// 			},
// 			NumberOfBrokerNodes: &numberOfBrokerNodes,
// 			EnhancedMonitoring:  kafkatypes.EnhancedMonitoringDefault,
// 			StorageMode:         kafkatypes.StorageModeLocal,
// 			ClientAuthentication: &kafkatypes.ClientAuthentication{
// 				Sasl: &kafkatypes.Sasl{
// 					Iam: &kafkatypes.Iam{
// 						Enabled: aws.Bool(true),
// 					},
// 				},
// 			},
// 		},
// 	}
// }

// func createTestServerlessCluster() kafkatypes.Cluster {
// 	clusterName := "test-serverless-cluster"

// 	return kafkatypes.Cluster{
// 		ClusterArn:  aws.String("arn:aws:kafka:us-west-2:123456789012:cluster/test-serverless-cluster/12345678-1234-1234-1234-123456789012-1"),
// 		ClusterName: &clusterName,
// 		ClusterType: kafkatypes.ClusterTypeServerless,
// 		Serverless: &kafkatypes.Serverless{
// 			ClientAuthentication: &kafkatypes.ServerlessClientAuthentication{
// 				Sasl: &kafkatypes.ServerlessSasl{
// 					Iam: &kafkatypes.Iam{
// 						Enabled: aws.Bool(true),
// 					},
// 				},
// 			},
// 		},
// 	}
// }

// func createTestCollector(mskService MSKService, metricService MetricService) *ClusterMetricsCollector {
// 	return NewClusterMetrics(mskService, metricService, ClusterMetricsOpts{
// 		Region:     "us-west-2",
// 		StartDate:  time.Now().AddDate(0, 0, -7),
// 		EndDate:    time.Now(),
// 		ClusterArn: "arn:aws:kafka:us-west-2:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012-1",
// 		AuthType:   types.AuthTypeIAM,
// 		SkipKafka:  false,
// 	})
// }

// // Tests
// func TestNewClusterMetrics(t *testing.T) {
// 	mskService := &MockMSKService{}
// 	metricService := &MockMetricService{}

// 	opts := ClusterMetricsOpts{
// 		Region:     "us-west-2",
// 		StartDate:  time.Now().AddDate(0, 0, -7),
// 		EndDate:    time.Now(),
// 		ClusterArn: "test-arn",
// 		AuthType:   types.AuthTypeIAM,
// 		SkipKafka:  false,
// 	}

// 	collector := NewClusterMetrics(mskService, metricService, opts)

// 	assert.NotNil(t, collector)
// 	assert.Equal(t, "us-west-2", collector.region)
// 	assert.Equal(t, "test-arn", collector.clusterArn)
// }

// func TestClusterMetricsCollector_ProcessCluster_Provisioned(t *testing.T) {
// 	mskService := &MockMSKService{}
// 	metricService := &MockMetricService{}

// 	cluster := createTestProvisionedCluster()
// 	mskService.On("DescribeCluster", mock.Anything, mock.Anything).Return(&cluster, nil)
// 	mskService.On("IsFetchFromFollowerEnabled", mock.Anything, cluster).Return(aws.Bool(false), nil)

// 	// Mock metric service calls for provisioned cluster
// 	for i := 1; i <= 3; i++ {
// 		nodeID := i
// 		metricService.On("GetAverageMetric", "test-cluster", "BytesInPerSec", &nodeID).Return(100.0, nil)
// 		metricService.On("GetAverageMetric", "test-cluster", "BytesOutPerSec", &nodeID).Return(50.0, nil)
// 		metricService.On("GetAverageMetric", "test-cluster", "MessagesInPerSec", &nodeID).Return(1000.0, nil)
// 		metricService.On("GetAverageMetric", "test-cluster", "KafkaDataLogsDiskUsed", &nodeID).Return(75.0, nil)
// 		metricService.On("GetAverageMetric", "test-cluster", "RemoteLogSizeBytes", &nodeID).Return(0.0, nil)

// 		metricService.On("GetPeakMetric", "test-cluster", "BytesInPerSec", &nodeID).Return(200.0, nil)
// 		metricService.On("GetPeakMetric", "test-cluster", "BytesOutPerSec", &nodeID).Return(100.0, nil)
// 		metricService.On("GetPeakMetric", "test-cluster", "MessagesInPerSec", &nodeID).Return(2000.0, nil)
// 		metricService.On("GetPeakMetric", "test-cluster", "KafkaDataLogsDiskUsed", &nodeID).Return(85.0, nil)
// 		metricService.On("GetPeakMetric", "test-cluster", "RemoteLogSizeBytes", &nodeID).Return(0.0, nil)
// 		metricService.On("GetPeakMetric", "test-cluster", "ClientConnectionCount", &nodeID).Return(50.0, nil)
// 		metricService.On("GetPeakMetric", "test-cluster", "PartitionCount", &nodeID).Return(100.0, nil)
// 		metricService.On("GetPeakMetric", "test-cluster", "LeaderCount", &nodeID).Return(50.0, nil)
// 		metricService.On("GetPeakMetric", "test-cluster", "ReplicationBytesOutPerSec", &nodeID).Return(25.0, nil)
// 		metricService.On("GetPeakMetric", "test-cluster", "ReplicationBytesInPerSec", &nodeID).Return(25.0, nil)
// 	}

// 	// Mock global metrics
// 	metricService.On("GetGlobalMetric", "test-cluster", "GlobalPartitionCount").Return(300.0, nil)
// 	metricService.On("GetGlobalMetric", "test-cluster", "GlobalTopicCount").Return(10.0, nil)

// 	collector := createTestCollector(mskService, metricService)

// 	result, err := collector.ProcessCluster()

// 	assert.NoError(t, err)
// 	assert.NotNil(t, result)
// 	assert.Equal(t, "test-cluster", result.ClusterName)
// 	assert.Equal(t, "PROVISIONED", result.ClusterType)
// 	assert.Len(t, result.NodesMetrics, 3)
// 	assert.NotNil(t, result.ClusterMetricsSummary.InstanceType)
// 	assert.Equal(t, "kafka.m5.large", *result.ClusterMetricsSummary.InstanceType)

// 	mskService.AssertExpectations(t)
// 	metricService.AssertExpectations(t)
// }

// func TestClusterMetricsCollector_ProcessCluster_Serverless(t *testing.T) {
// 	mskService := &MockMSKService{}
// 	metricService := &MockMetricService{}

// 	cluster := createTestServerlessCluster()
// 	mskService.On("DescribeCluster", mock.Anything, mock.Anything).Return(&cluster, nil)
// 	mskService.On("IsFetchFromFollowerEnabled", mock.Anything, cluster).Return(aws.Bool(false), nil)

// 	// Mock metric service calls for serverless
// 	metricService.On("GetServerlessAverageMetric", "test-serverless-cluster", "BytesInPerSec").Return(100.0, nil)
// 	metricService.On("GetServerlessAverageMetric", "test-serverless-cluster", "BytesOutPerSec").Return(50.0, nil)
// 	metricService.On("GetServerlessAverageMetric", "test-serverless-cluster", "MessagesInPerSec").Return(1000.0, nil)
// 	metricService.On("GetServerlessAverageMetric", "test-serverless-cluster", "KafkaDataLogsDiskUsed").Return(75.0, nil)
// 	metricService.On("GetServerlessAverageMetric", "test-serverless-cluster", "RemoteLogSizeBytes").Return(0.0, nil)

// 	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "BytesInPerSec").Return(200.0, nil)
// 	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "BytesOutPerSec").Return(100.0, nil)
// 	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "MessagesInPerSec").Return(2000.0, nil)
// 	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "KafkaDataLogsDiskUsed").Return(85.0, nil)
// 	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "RemoteLogSizeBytes").Return(0.0, nil)
// 	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "ClientConnectionCount").Return(50.0, nil)
// 	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "PartitionCount").Return(100.0, nil)
// 	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "LeaderCount").Return(50.0, nil)
// 	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "ReplicationBytesOutPerSec").Return(25.0, nil)
// 	metricService.On("GetServerlessPeakMetric", "test-serverless-cluster", "ReplicationBytesInPerSec").Return(25.0, nil)

// 	// Mock global metrics
// 	metricService.On("GetGlobalMetric", "test-serverless-cluster", "GlobalPartitionCount").Return(100.0, nil)
// 	metricService.On("GetGlobalMetric", "test-serverless-cluster", "GlobalTopicCount").Return(10.0, nil)

// 	collector := createTestCollector(mskService, metricService)

// 	result, err := collector.ProcessCluster()

// 	assert.NoError(t, err)
// 	assert.NotNil(t, result)
// 	assert.Equal(t, "test-serverless-cluster", result.ClusterName)
// 	assert.Equal(t, "SERVERLESS", result.ClusterType)
// 	assert.Len(t, result.NodesMetrics, 1)

// 	mskService.AssertExpectations(t)
// 	metricService.AssertExpectations(t)
// }

// func TestClusterMetricsCollector_ProcessCluster_Error(t *testing.T) {
// 	mskService := &MockMSKService{}
// 	metricService := &MockMetricService{}

// 	mskService.On("DescribeCluster", mock.Anything, mock.Anything).Return(nil, assert.AnError)

// 	collector := createTestCollector(mskService, metricService)

// 	result, err := collector.ProcessCluster()

// 	assert.Error(t, err)
// 	assert.Nil(t, result)
// 	assert.Contains(t, err.Error(), "Failed to get clusters")
// 	mskService.AssertExpectations(t)
// }

// func TestClusterMetricsCollector_CalculateReplicationFactor(t *testing.T) {
// 	collector := &ClusterMetricsCollector{}

// 	// Test with valid data
// 	nodesMetrics := []types.NodeMetrics{
// 		{PartitionCountMax: 100},
// 		{PartitionCountMax: 100},
// 		{PartitionCountMax: 100},
// 	}
// 	globalPartitionCountMax := 100.0

// 	result := collector.calculateReplicationFactor(nodesMetrics, globalPartitionCountMax)

// 	assert.NotNil(t, result)
// 	assert.Equal(t, 3.0, *result)

// 	// Test with zero global partition count
// 	result = collector.calculateReplicationFactor(nodesMetrics, 0)
// 	assert.NotNil(t, result)
// 	assert.Equal(t, 0.0, *result)
// }

// func TestClusterMetricsCollector_CalculateClusterMetricsSummary(t *testing.T) {
// 	collector := &ClusterMetricsCollector{}

// 	// Test with valid data
// 	nodesMetrics := []types.NodeMetrics{
// 		{
// 			BytesInPerSecAvg:         100.0,
// 			BytesOutPerSecAvg:        50.0,
// 			BytesInPerSecMax:         200.0,
// 			BytesOutPerSecMax:        100.0,
// 			VolumeSizeGB:             aws.Int(100),
// 			KafkaDataLogsDiskUsedAvg: 75.0,
// 			RemoteLogSizeBytesAvg:    0.0,
// 		},
// 		{
// 			BytesInPerSecAvg:         100.0,
// 			BytesOutPerSecAvg:        50.0,
// 			BytesInPerSecMax:         200.0,
// 			BytesOutPerSecMax:        100.0,
// 			VolumeSizeGB:             aws.Int(100),
// 			KafkaDataLogsDiskUsedAvg: 75.0,
// 			RemoteLogSizeBytesAvg:    0.0,
// 		},
// 	}

// 	result := collector.calculateClusterMetricsSummary(nodesMetrics)

// 	assert.NotNil(t, result)
// 	assert.NotNil(t, result.AvgIngressThroughputMegabytesPerSecond)
// 	assert.NotNil(t, result.PeakIngressThroughputMegabytesPerSecond)
// 	assert.NotNil(t, result.AvgEgressThroughputMegabytesPerSecond)
// 	assert.NotNil(t, result.PeakEgressThroughputMegabytesPerSecond)
// 	assert.NotNil(t, result.RetentionDays)
// 	assert.NotNil(t, result.LocalRetentionInPrimaryStorageHours)

// 	// Test with empty metrics
// 	result = collector.calculateClusterMetricsSummary([]types.NodeMetrics{})
// 	assert.Equal(t, types.ClusterMetricsSummary{}, result)
// }
