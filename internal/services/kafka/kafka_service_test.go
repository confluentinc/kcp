package kafka

import (
	"testing"

	"github.com/IBM/sarama"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockKafkaAdmin is a mock implementation of KafkaAdmin
type MockKafkaAdmin struct {
	mock.Mock
}

func (m *MockKafkaAdmin) ListTopicsWithConfigs() (map[string]sarama.TopicDetail, error) {
	args := m.Called()
	return args.Get(0).(map[string]sarama.TopicDetail), args.Error(1)
}

func (m *MockKafkaAdmin) GetClusterKafkaMetadata() (*client.ClusterKafkaMetadata, error) {
	args := m.Called()
	return args.Get(0).(*client.ClusterKafkaMetadata), args.Error(1)
}

func (m *MockKafkaAdmin) DescribeConfig() ([]sarama.ConfigEntry, error) {
	args := m.Called()
	return args.Get(0).([]sarama.ConfigEntry), args.Error(1)
}

func (m *MockKafkaAdmin) ListAcls() ([]sarama.ResourceAcls, error) {
	args := m.Called()
	return args.Get(0).([]sarama.ResourceAcls), args.Error(1)
}

func (m *MockKafkaAdmin) Close() error {
	args := m.Called()
	return args.Error(0)
}

func TestNewKafkaService(t *testing.T) {
	mockFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error) {
		return &MockKafkaAdmin{}, nil
	}

	opts := KafkaServiceOpts{
		KafkaAdminFactory: mockFactory,
		AuthType:          types.AuthTypeIAM,
		ClusterArn:        "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster",
	}

	service := NewKafkaService(opts)

	assert.NotNil(t, service)
	assert.Equal(t, types.AuthTypeIAM, service.authType)
	assert.Equal(t, "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster", service.clusterArn)
}

func TestKafkaService_ScanKafkaResources_Provisioned(t *testing.T) {
	mockAdmin := &MockKafkaAdmin{}

	mockFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error) {
		return mockAdmin, nil
	}

	service := NewKafkaService(KafkaServiceOpts{
		KafkaAdminFactory: mockFactory,
		AuthType:          types.AuthTypeIAM,
		ClusterArn:        "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster",
	})

	// Create test cluster info
	clusterInfo := &types.ClusterInformation{
		Cluster: kafkatypes.Cluster{
			ClusterType: kafkatypes.ClusterTypeProvisioned,
			Provisioned: &kafkatypes.Provisioned{
				CurrentBrokerSoftwareInfo: &kafkatypes.BrokerSoftwareInfo{
					KafkaVersion: stringPtr("2.8.1"),
				},
			},
		},
		BootstrapBrokers: kafka.GetBootstrapBrokersOutput{
			BootstrapBrokerStringPublicSaslIam: stringPtr("broker1:9092,broker2:9092"),
		},
	}

	mockAdmin.On("GetClusterKafkaMetadata").Return(&client.ClusterKafkaMetadata{
		ClusterID: "test-cluster-id",
	}, nil)

	mockAdmin.On("ListTopicsWithConfigs").Return(map[string]sarama.TopicDetail{
		"topic1": {
			NumPartitions:     3,
			ReplicationFactor: 3,
			ConfigEntries: map[string]*string{
				"cleanup.policy":      aws.String("compact"),
				"local.retention.ms":  aws.String("11111111"),
				"retention.ms":        aws.String("111111111"),
				"min.insync.replicas": aws.String("1"),
			},
		},
		"topic2": {
			NumPartitions:     1,
			ReplicationFactor: 1,
			ConfigEntries: map[string]*string{
				"cleanup.policy":      aws.String("delete"),
				"local.retention.ms":  aws.String("22222222"),
				"retention.ms":        aws.String("222222222"),
				"min.insync.replicas": aws.String("2"),
			},
		},
	}, nil)

	mockAdmin.On("ListAcls").Return([]sarama.ResourceAcls{
		{
			Resource: sarama.Resource{
				ResourceType:        sarama.AclResourceTopic,
				ResourceName:        "topic1",
				ResourcePatternType: sarama.AclPatternLiteral,
			},
			Acls: []*sarama.Acl{
				{
					Principal:      "User:test-user",
					Host:           "*",
					Operation:      sarama.AclOperationRead,
					PermissionType: sarama.AclPermissionAllow,
				},
			},
		},
	}, nil)

	mockAdmin.On("Close").Return(nil)

	// Execute the test
	err := service.ScanKafkaResources(clusterInfo)

	// Verify results
	require.NoError(t, err)
	assert.Equal(t, "test-cluster-id", clusterInfo.ClusterID)
	assert.Len(t, clusterInfo.Topics.Details, 2)

	// Verify topic1 with ConfigEntries
	topic1Found := false
	topic2Found := false
	for _, topic := range clusterInfo.Topics.Details {
		switch topic.Name {
		case "topic1":
			topic1Found = true
			assert.Equal(t, 3, topic.Partitions)
			assert.Equal(t, 3, topic.ReplicationFactor)
			assert.Equal(t, aws.String("compact"), topic.Configurations["cleanup.policy"])
			assert.Equal(t, aws.String("11111111"), topic.Configurations["local.retention.ms"])
			assert.Equal(t, aws.String("111111111"), topic.Configurations["retention.ms"])
			assert.Equal(t, aws.String("1"), topic.Configurations["min.insync.replicas"])
		case "topic2":
			topic2Found = true
			assert.Equal(t, 1, topic.Partitions)
			assert.Equal(t, 1, topic.ReplicationFactor)
			assert.Equal(t, aws.String("delete"), topic.Configurations["cleanup.policy"])
			assert.Equal(t, aws.String("22222222"), topic.Configurations["local.retention.ms"])
			assert.Equal(t, aws.String("222222222"), topic.Configurations["retention.ms"])
			assert.Equal(t, aws.String("2"), topic.Configurations["min.insync.replicas"])
		}
	}
	assert.True(t, topic1Found, "topic1 should be found")
	assert.True(t, topic2Found, "topic2 should be found")

	assert.Len(t, clusterInfo.Acls, 1)
	assert.Equal(t, "Topic", clusterInfo.Acls[0].ResourceType)
	assert.Equal(t, "topic1", clusterInfo.Acls[0].ResourceName)

	// Verify all mocks were called
	mockAdmin.AssertExpectations(t)
}

func TestKafkaService_ScanKafkaResources_Serverless(t *testing.T) {
	mockAdmin := &MockKafkaAdmin{}

	mockFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error) {
		return mockAdmin, nil
	}

	service := NewKafkaService(KafkaServiceOpts{
		KafkaAdminFactory: mockFactory,
		AuthType:          types.AuthTypeIAM,
		ClusterArn:        "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster",
	})

	// Create test cluster info for serverless
	clusterInfo := &types.ClusterInformation{
		Cluster: kafkatypes.Cluster{
			ClusterType: kafkatypes.ClusterTypeServerless,
		},
		BootstrapBrokers: kafka.GetBootstrapBrokersOutput{
			BootstrapBrokerStringPublicSaslIam: stringPtr("broker1:9092"),
		},
	}

	mockAdmin.On("GetClusterKafkaMetadata").Return(&client.ClusterKafkaMetadata{
		ClusterID: "test-serverless-cluster-id",
	}, nil)

	// Mock ListTopicsWithConfigs for serverless topic (returns empty configs)
	mockAdmin.On("ListTopicsWithConfigs").Return(map[string]sarama.TopicDetail{
		"serverless-topic": {
			NumPartitions:     1,
			ReplicationFactor: 1,
			ConfigEntries:     map[string]*string{}, // Empty config entries for serverless
		},
	}, nil)

	mockAdmin.On("Close").Return(nil)

	// Execute the test
	err := service.ScanKafkaResources(clusterInfo)

	// Verify results
	require.NoError(t, err)
	assert.Equal(t, "test-serverless-cluster-id", clusterInfo.ClusterID)
	assert.Len(t, clusterInfo.Topics.Details, 1)
	assert.Equal(t, "serverless-topic", clusterInfo.Topics.Details[0].Name)
	assert.Equal(t, 1, clusterInfo.Topics.Details[0].Partitions)
	assert.Equal(t, 1, clusterInfo.Topics.Details[0].ReplicationFactor)
	// Verify that configurations map is empty since DescribeTopicConfigs returned empty configs
	// and ConfigEntries is nil
	assert.Empty(t, clusterInfo.Topics.Details[0].Configurations)
	assert.Empty(t, clusterInfo.Acls) // ACLs should be empty for serverless

	// Verify mocks were called (note: ListAcls should NOT be called for serverless)
	mockAdmin.AssertExpectations(t)
}

func TestKafkaService_ScanKafkaResources_Error(t *testing.T) {
	service := NewKafkaService(KafkaServiceOpts{
		AuthType:   types.AuthTypeIAM,
		ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster",
	})

	clusterInfo := &types.ClusterInformation{
		BootstrapBrokers: kafka.GetBootstrapBrokersOutput{},
	}

	// Execute the test - should fail because no broker addresses are available
	err := service.ScanKafkaResources(clusterInfo)

	// Verify error is returned
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No SASL/IAM brokers found in the cluster")
}

func TestKafkaService_getKafkaVersion(t *testing.T) {
	service := &KafkaService{}

	tests := []struct {
		name        string
		clusterInfo *types.ClusterInformation
		expected    string
	}{
		{
			name: "Provisioned cluster with version",
			clusterInfo: &types.ClusterInformation{
				Cluster: kafkatypes.Cluster{
					ClusterType: kafkatypes.ClusterTypeProvisioned,
					Provisioned: &kafkatypes.Provisioned{
						CurrentBrokerSoftwareInfo: &kafkatypes.BrokerSoftwareInfo{
							KafkaVersion: stringPtr("2.8.1"),
						},
					},
				},
			},
			expected: "2.8.1", // Assuming utils.ConvertKafkaVersion returns the same value
		},
		{
			name: "Serverless cluster",
			clusterInfo: &types.ClusterInformation{
				Cluster: kafkatypes.Cluster{
					ClusterType: kafkatypes.ClusterTypeServerless,
				},
			},
			expected: "4.0.0",
		},
		{
			name: "Unknown cluster type",
			clusterInfo: &types.ClusterInformation{
				Cluster: kafkatypes.Cluster{
					ClusterType: "unknown",
				},
			},
			expected: "4.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.GetKafkaVersion(tt.clusterInfo)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}
