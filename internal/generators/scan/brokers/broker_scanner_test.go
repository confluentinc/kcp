package brokers

import (
	"errors"
	"testing"

	"github.com/IBM/sarama"
	kafkaTypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/mocks"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestBrokerScanner(kafkaAdminFactory KafkaAdminFactory, clusterInfo types.ClusterInformation, authType types.AuthType) *BrokerScanner {
	return NewBrokerScanner(kafkaAdminFactory, clusterInfo, &BrokerScannerOpts{
		AuthType: authType,
	})
}

func TestBrokerScanner_DescribeKafkaCluster(t *testing.T) {
	tests := []struct {
		name                 string
		mockClusterMetadata  *client.ClusterKafkaMetadata
		mockError            error
		wantError            string
		expectedBrokerCount  int
		expectedControllerID int32
		expectedClusterID    string
	}{
		{
			name: "successful cluster description with complete metadata",
			mockClusterMetadata: &client.ClusterKafkaMetadata{
				Brokers:      make([]*sarama.Broker, 3), // 3 brokers
				ControllerID: 1,
				ClusterID:    "test-cluster-123",
			},
			expectedBrokerCount:  3,
			expectedControllerID: 1,
			expectedClusterID:    "test-cluster-123",
		},
		{
			name: "successful cluster description with empty cluster ID",
			mockClusterMetadata: &client.ClusterKafkaMetadata{
				Brokers:      make([]*sarama.Broker, 1), // 1 broker
				ControllerID: 2,
				ClusterID:    "", // Empty cluster ID
			},
			expectedBrokerCount:  1,
			expectedControllerID: 2,
			expectedClusterID:    "",
		},
		{
			name: "successful cluster description with no brokers",
			mockClusterMetadata: &client.ClusterKafkaMetadata{
				Brokers:      []*sarama.Broker{},
				ControllerID: 0,
				ClusterID:    "empty-cluster",
			},
			expectedBrokerCount:  0,
			expectedControllerID: 0,
			expectedClusterID:    "empty-cluster",
		},
		{
			name:      "handles DescribeCluster API error",
			mockError: errors.New("kafka admin connection failed"),
			wantError: "❌ Failed to describe kafka cluster: kafka admin connection failed",
		},
		{
			name:      "handles timeout error from admin client",
			mockError: errors.New("context deadline exceeded"),
			wantError: "❌ Failed to describe kafka cluster: context deadline exceeded",
		},
		{
			name:      "handles authentication error",
			mockError: errors.New("SASL authentication failed"),
			wantError: "❌ Failed to describe kafka cluster: SASL authentication failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAdmin := &mocks.MockKafkaAdmin{
				GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
					return tt.mockClusterMetadata, tt.mockError
				},
			}

			brokerScanner := newTestBrokerScanner(nil, types.ClusterInformation{}, types.AuthTypeIAM)
			result, err := brokerScanner.describeKafkaCluster(mockAdmin)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)

			assert.Equal(t, tt.expectedBrokerCount, len(result.Brokers), "Broker count should match")
			assert.Equal(t, tt.expectedControllerID, result.ControllerID, "Controller ID should match")
			assert.Equal(t, tt.expectedClusterID, result.ClusterID, "Cluster ID should match")

			assert.NotNil(t, result.Brokers, "Brokers slice should not be nil")
		})
	}
}

func TestBrokerScanner_DescribeKafkaCluster_Integration(t *testing.T) {
	tests := []struct {
		name                string
		mockClusterMetadata *client.ClusterKafkaMetadata
		mockDescribeError   error
		wantClusterID       string
		wantError           string
	}{
		{
			name: "integration test - cluster metadata is properly stored in ClusterInformation",
			mockClusterMetadata: &client.ClusterKafkaMetadata{
				Brokers:      make([]*sarama.Broker, 2), // 2 brokers
				ControllerID: 1,
				ClusterID:    "integration-test-cluster",
			},
			wantClusterID: "integration-test-cluster",
		},
		{
			name: "integration test - empty cluster ID is handled properly",
			mockClusterMetadata: &client.ClusterKafkaMetadata{
				Brokers:      make([]*sarama.Broker, 1), // 1 broker
				ControllerID: 1,
				ClusterID:    "",
			},
			wantClusterID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAdmin := &mocks.MockKafkaAdmin{
				GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
					return tt.mockClusterMetadata, tt.mockDescribeError
				},
				CloseFunc: func() error { return nil },
			}

			kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkaTypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error) {
				return mockAdmin, nil
			}

			brokerScanner := newTestBrokerScanner(kafkaAdminFactory, types.ClusterInformation{}, types.AuthTypeIAM)
			result, err := brokerScanner.describeKafkaCluster(mockAdmin)

			require.NoError(t, err)
			require.NotNil(t, result)

			assert.Equal(t, tt.wantClusterID, result.ClusterID)
		})
	}
}

func TestBrokerScanner_ScanClusterTopics(t *testing.T) {
	tests := []struct {
		name       string
		topics     map[string]sarama.TopicDetail
		mockError  error
		wantTopics []string
		wantError  string
	}{
		{
			name: "returns topics successfully",
			topics: map[string]sarama.TopicDetail{
				"topic1": {},
				"topic2": {},
			},
			wantTopics: []string{"topic1", "topic2"},
		},
		{
			name:       "handles empty topic list",
			topics:     map[string]sarama.TopicDetail{},
			wantTopics: []string{},
		},
		{
			name:      "handles topic listing error",
			mockError: errors.New("kafka error"),
			wantError: "❌ Failed to list topics",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAdmin := &mocks.MockKafkaAdmin{
				ListTopicsFunc: func() (map[string]sarama.TopicDetail, error) {
					return tt.topics, tt.mockError
				},
				CloseFunc: func() error { return nil },
			}

			brokerScanner := newTestBrokerScanner(nil, types.ClusterInformation{}, types.AuthTypeIAM)
			result, err := brokerScanner.scanClusterTopics(mockAdmin)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.ElementsMatch(t, tt.wantTopics, result)
		})
	}
}

func TestBrokerScanner_ListAcls(t *testing.T) {
	tests := []struct {
		name      string
		acls      []sarama.ResourceAcls
		mockError error
		wantAcls  []types.Acls
		wantError string
	}{
		{
			name: "returns single acl successfully",
			acls: []sarama.ResourceAcls{
				{
					Resource: sarama.Resource{
						ResourceType:        sarama.AclResourceAny,
						ResourceName:        "test-resource",
						ResourcePatternType: sarama.AclPatternAny,
					},
					Acls: []*sarama.Acl{
						{
							Principal:      "test-principal",
							Host:           "*",
							Operation:      sarama.AclOperationAny,
							PermissionType: sarama.AclPermissionAny,
						},
					},
				},
			},
			wantAcls: []types.Acls{
				{
					ResourceType:        "Any",
					ResourceName:        "test-resource",
					ResourcePatternType: "Any",
					Principal:           "test-principal",
					Host:                "*",
					Operation:           "Any",
					PermissionType:      "Any",
				},
			},
		},
		{
			name: "returns multiple acls successfully",
			acls: []sarama.ResourceAcls{
				{
					Resource: sarama.Resource{
						ResourceType: sarama.AclResourceAny,
						ResourceName: "test-resource",
						ResourcePatternType: sarama.AclPatternAny,
					},
					Acls: []*sarama.Acl{
						{
							Principal: "test-principal",
							Host: "*",
							Operation: sarama.AclOperationAny,
							PermissionType: sarama.AclPermissionAny,
						},
						{
							Principal: "test-principal",
							Host: "*",
							Operation: sarama.AclOperationAny,
							PermissionType: sarama.AclPermissionAny,
						},
					},
				},
			},
			wantAcls: []types.Acls{
				{
					ResourceType: "Any",
					ResourceName: "test-resource",
					ResourcePatternType: "Any",
					Principal: "test-principal",
					Host: "*",
					Operation: "Any",
					PermissionType: "Any",
				},
				{
					ResourceType: "Any",
					ResourceName: "test-resource",
					ResourcePatternType: "Any",
					Principal: "test-principal",
					Host: "*",
					Operation: "Any",
					PermissionType: "Any",
				},
			},
		},
		{
			name:     "handles empty acls list",
			acls:     []sarama.ResourceAcls{},
			wantAcls: []types.Acls{},
		},
		{
			name:      "handles acls listing error",
			mockError: errors.New("kafka error"),
			wantError: "❌ Failed to list acls",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAdmin := &mocks.MockKafkaAdmin{
				ListAclsFunc: func() ([]sarama.ResourceAcls, error) {
					return tt.acls, tt.mockError
				},
			}

			brokerScanner := newTestBrokerScanner(nil, types.ClusterInformation{}, types.AuthTypeIAM)
			result, err := brokerScanner.scanKafkaAcls(mockAdmin)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.ElementsMatch(t, tt.wantAcls, result)
		})
	}
}
