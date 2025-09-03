package clusters

import (
	"errors"
	"testing"

	"github.com/IBM/sarama"
	kafkaTypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/mocks"
	kafkaservice "github.com/confluentinc/kcp/internal/services/kafka"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClustersScanner() *ClustersScanner {
	return NewClustersScanner(&ClustersScannerOpts{
		DiscoverDir:     "testdata/discover",
		CredentialsFile: "testdata/credentials.yaml",
	})
}

func TestClustersScanner_ScanKafkaResources(t *testing.T) {
	tests := []struct {
		name                string
		mockClusterMetadata *client.ClusterKafkaMetadata
		mockTopics          map[string]sarama.TopicDetail
		mockAcls            []sarama.ResourceAcls
		mockError           error
		wantError           string
		expectedClusterID   string
		expectedTopicCount  int
		expectedAclCount    int
	}{
		{
			name: "successful direct kafka scan",
			mockClusterMetadata: &client.ClusterKafkaMetadata{
				Brokers:      make([]*sarama.Broker, 3),
				ControllerID: 1,
				ClusterID:    "test-cluster-123",
			},
			mockTopics: map[string]sarama.TopicDetail{
				"topic1": {},
				"topic2": {},
			},
			mockAcls: []sarama.ResourceAcls{
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
			},
			expectedClusterID:  "test-cluster-123",
			expectedTopicCount: 2,
			expectedAclCount:   1,
		},
		{
			name:      "handles admin client creation error",
			mockError: errors.New("failed to create admin client"),
			wantError: "❌ Failed to setup admin client: failed to create admin client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAdmin := &mocks.MockKafkaAdmin{
				GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
					return tt.mockClusterMetadata, nil
				},
				ListTopicsFunc: func() (map[string]sarama.TopicDetail, error) {
					return tt.mockTopics, nil
				},
				ListAclsFunc: func() ([]sarama.ResourceAcls, error) {
					return tt.mockAcls, nil
				},
				CloseFunc: func() error { return nil },
			}

			adminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkaTypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error) {
				if tt.mockError != nil {
					return nil, tt.mockError
				}
				return mockAdmin, nil
			}

			clusterInfo := types.ClusterInformation{
				Cluster: kafkaTypes.Cluster{
					ClusterType: kafkaTypes.ClusterTypeProvisioned,
					Provisioned: &kafkaTypes.Provisioned{
						CurrentBrokerSoftwareInfo: &kafkaTypes.BrokerSoftwareInfo{
							KafkaVersion: stringPtr("4.0.x.kraft"),
						},
					},
				},
			}

			clusterScanner := newTestClustersScanner()
			kafkaService := kafkaservice.NewKafkaService(kafkaservice.KafkaServiceOpts{
				KafkaAdminFactory: adminFactory,
				AuthType:          types.AuthTypeIAM,
				ClusterArn:        "arn:aws:kafka:eu-west-1:123456789012:cluster/test-cluster",
			})

			err := clusterScanner.scanKafkaResources(&clusterInfo, kafkaService, "broker1:9092,broker2:9092")

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedClusterID, clusterInfo.ClusterID)
			assert.Len(t, clusterInfo.Topics, tt.expectedTopicCount)
			assert.Len(t, clusterInfo.Acls, tt.expectedAclCount)
		})
	}
}

func TestClustersScanner_GetKafkaVersion(t *testing.T) {
	tests := []struct {
		name        string
		clusterInfo types.ClusterInformation
		expected    string
	}{
		{
			name: "Provisioned cluster with version",
			clusterInfo: types.ClusterInformation{
				Cluster: kafkaTypes.Cluster{
					ClusterType: kafkaTypes.ClusterTypeProvisioned,
					Provisioned: &kafkaTypes.Provisioned{
						CurrentBrokerSoftwareInfo: &kafkaTypes.BrokerSoftwareInfo{
							KafkaVersion: stringPtr("2.8.1"),
						},
					},
				},
			},
			expected: "2.8.1",
		},
		{
			name: "Serverless cluster",
			clusterInfo: types.ClusterInformation{
				Cluster: kafkaTypes.Cluster{
					ClusterType: kafkaTypes.ClusterTypeServerless,
				},
			},
			expected: "4.0.0",
		},
		{
			name: "Unknown cluster type",
			clusterInfo: types.ClusterInformation{
				Cluster: kafkaTypes.Cluster{
					ClusterType: "unknown",
				},
			},
			expected: "4.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kafkaService := &kafkaservice.KafkaService{}
			result := kafkaService.GetKafkaVersion(&tt.clusterInfo)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}
