package kafka

import (
	"errors"
	"testing"

	"github.com/IBM/sarama"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockMSKService is a mock implementation of MSKService
type MockMSKService struct {
	mock.Mock
}

func (m *MockMSKService) ParseBrokerAddresses(brokers kafka.GetBootstrapBrokersOutput, authType types.AuthType) ([]string, error) {
	args := m.Called(brokers, authType)
	return args.Get(0).([]string), args.Error(1)
}

// MockKafkaAdmin is a mock implementation of KafkaAdmin
type MockKafkaAdmin struct {
	mock.Mock
}

func (m *MockKafkaAdmin) ListTopics() (map[string]sarama.TopicDetail, error) {
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
	mockMSK := &MockMSKService{}
	mockFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error) {
		return &MockKafkaAdmin{}, nil
	}

	opts := KafkaServiceOpts{
		MSKService:        mockMSK,
		KafkaAdminFactory: mockFactory,
		AuthType:          types.AuthTypeIAM,
		ClusterArn:        "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster",
	}

	service := NewKafkaService(opts)

	assert.NotNil(t, service)
	assert.Equal(t, mockMSK, service.mskService)
	assert.Equal(t, types.AuthTypeIAM, service.authType)
	assert.Equal(t, "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster", service.clusterArn)
}

func TestKafkaService_ScanKafkaResources_Provisioned(t *testing.T) {
	mockMSK := &MockMSKService{}
	mockAdmin := &MockKafkaAdmin{}

	mockFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error) {
		return mockAdmin, nil
	}

	service := NewKafkaService(KafkaServiceOpts{
		MSKService:        mockMSK,
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
		BootstrapBrokers: kafka.GetBootstrapBrokersOutput{},
	}

	// Set up mock expectations
	mockMSK.On("ParseBrokerAddresses", mock.Anything, types.AuthTypeIAM).Return([]string{"broker1:9092", "broker2:9092"}, nil)

	mockAdmin.On("GetClusterKafkaMetadata").Return(&client.ClusterKafkaMetadata{
		ClusterID: "test-cluster-id",
	}, nil)

	mockAdmin.On("ListTopics").Return(map[string]sarama.TopicDetail{
		"topic1": {},
		"topic2": {},
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
	assert.Len(t, clusterInfo.Topics, 2)
	assert.Contains(t, clusterInfo.Topics, "topic1")
	assert.Contains(t, clusterInfo.Topics, "topic2")
	assert.Len(t, clusterInfo.Acls, 1)
	assert.Equal(t, "Topic", clusterInfo.Acls[0].ResourceType)
	assert.Equal(t, "topic1", clusterInfo.Acls[0].ResourceName)

	// Verify all mocks were called
	mockMSK.AssertExpectations(t)
	mockAdmin.AssertExpectations(t)
}

func TestKafkaService_ScanKafkaResources_Serverless(t *testing.T) {
	mockMSK := &MockMSKService{}
	mockAdmin := &MockKafkaAdmin{}

	mockFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error) {
		return mockAdmin, nil
	}

	service := NewKafkaService(KafkaServiceOpts{
		MSKService:        mockMSK,
		KafkaAdminFactory: mockFactory,
		AuthType:          types.AuthTypeIAM,
		ClusterArn:        "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster",
	})

	// Create test cluster info for serverless
	clusterInfo := &types.ClusterInformation{
		Cluster: kafkatypes.Cluster{
			ClusterType: kafkatypes.ClusterTypeServerless,
		},
		BootstrapBrokers: kafka.GetBootstrapBrokersOutput{},
	}

	// Set up mock expectations
	mockMSK.On("ParseBrokerAddresses", mock.Anything, types.AuthTypeIAM).Return([]string{"broker1:9092"}, nil)

	mockAdmin.On("GetClusterKafkaMetadata").Return(&client.ClusterKafkaMetadata{
		ClusterID: "test-serverless-cluster-id",
	}, nil)

	mockAdmin.On("ListTopics").Return(map[string]sarama.TopicDetail{
		"serverless-topic": {},
	}, nil)

	mockAdmin.On("Close").Return(nil)

	// Execute the test
	err := service.ScanKafkaResources(clusterInfo)

	// Verify results
	require.NoError(t, err)
	assert.Equal(t, "test-serverless-cluster-id", clusterInfo.ClusterID)
	assert.Len(t, clusterInfo.Topics, 1)
	assert.Contains(t, clusterInfo.Topics, "serverless-topic")
	assert.Empty(t, clusterInfo.Acls) // ACLs should be empty for serverless

	// Verify mocks were called (note: ListAcls should NOT be called for serverless)
	mockMSK.AssertExpectations(t)
	mockAdmin.AssertExpectations(t)
}

func TestKafkaService_ScanKafkaResources_Error(t *testing.T) {
	mockMSK := &MockMSKService{}

	service := NewKafkaService(KafkaServiceOpts{
		MSKService: mockMSK,
		AuthType:   types.AuthTypeIAM,
		ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster",
	})

	clusterInfo := &types.ClusterInformation{
		BootstrapBrokers: kafka.GetBootstrapBrokersOutput{},
	}

	// Set up mock to return error
	mockMSK.On("ParseBrokerAddresses", mock.Anything, types.AuthTypeIAM).Return([]string{}, errors.New("parse error"))

	// Execute the test
	err := service.ScanKafkaResources(clusterInfo)

	// Verify error is returned
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse error")

	mockMSK.AssertExpectations(t)
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
			result := service.getKafkaVersion(tt.clusterInfo)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}
