package kafka

import (
	"errors"
	"testing"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/mocks"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestKafkaService_ScanKafkaResources(t *testing.T) {
	tests := []struct {
		name          string
		mockClient    *mocks.MockKafkaAdmin
		clusterType   kafkatypes.ClusterType
		wantErr       bool
		wantErrMsg    string
		wantClusterID bool
		wantTopics    bool
		wantAcls      bool
		wantAclsNil   bool
	}{
		{
			name: "describeKafkaCluster returns error",
			mockClient: &mocks.MockKafkaAdmin{
				GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
					return nil, errors.New("cluster connection failed")
				},
			},
			clusterType: kafkatypes.ClusterTypeProvisioned,
			wantErr:     true,
			wantErrMsg:  "❌ Failed to describe kafka cluster: cluster connection failed",
		},
		{
			name: "scanClusterTopics returns error",
			mockClient: &mocks.MockKafkaAdmin{
				GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
					return &client.ClusterKafkaMetadata{
						ClusterID: "test-cluster-123",
					}, nil
				},
				ListTopicsWithConfigsFunc: func() ([]types.TopicDetails, error) {
					return nil, errors.New("failed to connect to brokers")
				},
			},
			clusterType: kafkatypes.ClusterTypeProvisioned,
			wantErr:     true,
			wantErrMsg:  "❌ Failed to list topics with configs: failed to connect to brokers",
		},
		{
			name: "serverless cluster skips ACL scan successfully",
			mockClient: &mocks.MockKafkaAdmin{
				GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
					return &client.ClusterKafkaMetadata{
						ClusterID: "serverless-cluster-456",
					}, nil
				},
				ListTopicsWithConfigsFunc: func() ([]types.TopicDetails, error) {
					return []types.TopicDetails{
						{
							Name:              "serverless-topic",
							Partitions:        1,
							ReplicationFactor: 1,
							Configurations:    map[string]*string{},
						},
					}, nil
				},
				// Note: No ListAclsFunc needed since ACL scan should be skipped
			},
			clusterType:   kafkatypes.ClusterTypeServerless,
			wantErr:       false,
			wantClusterID: true,
			wantTopics:    true,
			wantAclsNil:   true,
		},
		{
			name: "scanKafkaAcls returns error for provisioned cluster",
			mockClient: &mocks.MockKafkaAdmin{
				GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
					return &client.ClusterKafkaMetadata{
						ClusterID: "provisioned-cluster-789",
					}, nil
				},
				ListTopicsWithConfigsFunc: func() ([]types.TopicDetails, error) {
					return []types.TopicDetails{
						{
							Name:              "provisioned-topic",
							Partitions:        3,
							ReplicationFactor: 2,
							Configurations:    map[string]*string{},
						},
					}, nil
				},
				ListAclsFunc: func() ([]types.Acls, error) {
					return nil, errors.New("ACL authorization failed")
				},
			},
			clusterType: kafkatypes.ClusterTypeProvisioned,
			wantErr:     true,
			wantErrMsg:  "❌ Failed to list acls: ACL authorization failed",
		},
		{
			name: "successful full scan for provisioned cluster",
			mockClient: &mocks.MockKafkaAdmin{
				GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
					return &client.ClusterKafkaMetadata{
						ClusterID: "success-cluster-999",
					}, nil
				},
				ListTopicsWithConfigsFunc: func() ([]types.TopicDetails, error) {
					retentionMs := "604800000"
					return []types.TopicDetails{
						{
							Name:              "orders",
							Partitions:        6,
							ReplicationFactor: 3,
							Configurations: map[string]*string{
								"retention.ms": &retentionMs,
							},
						},
						{
							Name:              "users",
							Partitions:        3,
							ReplicationFactor: 2,
							Configurations:    map[string]*string{},
						},
					}, nil
				},
				ListAclsFunc: func() ([]types.Acls, error) {
					return []types.Acls{
						{
							ResourceType:        "Topic",
							ResourceName:        "orders",
							ResourcePatternType: "Literal",
							Principal:           "User:orders-service",
							Host:                "*",
							Operation:           "Write",
							PermissionType:      "Allow",
						},
					}, nil
				},
			},
			clusterType:   kafkatypes.ClusterTypeProvisioned,
			wantErr:       false,
			wantClusterID: true,
			wantTopics:    true,
			wantAcls:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ks := &KafkaService{
				client:     tt.mockClient,
				authType:   types.AuthTypeIAM,
				clusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test/abc-123",
			}

			result, err := ks.ScanKafkaResources(tt.clusterType)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErrMsg, err.Error())
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)

				// Verify cluster ID is populated
				if tt.wantClusterID {
					assert.NotEmpty(t, result.ClusterID)
				}

				// Verify topics are populated if expected
				if tt.wantTopics {
					assert.NotNil(t, result.Topics)
					assert.NotEmpty(t, result.Topics.Details)
				}

				// Verify ACLs based on expectations
				if tt.wantAclsNil {
					assert.Nil(t, result.Acls)
				} else if tt.wantAcls {
					assert.NotNil(t, result.Acls)
					assert.NotEmpty(t, result.Acls)
				}
			}
		})
	}
}

func TestKafkaService_scanClusterTopics(t *testing.T) {
	tests := []struct {
		name       string
		mockClient *mocks.MockKafkaAdmin
		wantErr    bool
		wantErrMsg string
		wantTopics []types.TopicDetails
	}{
		{
			name: "ListTopicsWithConfigs returns error",
			mockClient: &mocks.MockKafkaAdmin{
				ListTopicsWithConfigsFunc: func() ([]types.TopicDetails, error) {
					return nil, errors.New("network timeout")
				},
			},
			wantErr:    true,
			wantErrMsg: "❌ Failed to list topics with configs: network timeout",
			wantTopics: nil,
		},
		{
			name: "successful topic scan and processing",
			mockClient: &mocks.MockKafkaAdmin{
				ListTopicsWithConfigsFunc: func() ([]types.TopicDetails, error) {
					retentionMs := "86400000"
					cleanupPolicy := "delete"
					return []types.TopicDetails{
						{
							Name:              "test-topic-1",
							Partitions:        3,
							ReplicationFactor: 2,
							Configurations: map[string]*string{
								"retention.ms":   &retentionMs,
								"cleanup.policy": &cleanupPolicy,
							},
						},
						{
							Name:              "test-topic-2",
							Partitions:        6,
							ReplicationFactor: 3,
							Configurations: map[string]*string{
								"retention.ms": &retentionMs,
							},
						},
					}, nil
				},
			},
			wantErr: false,
			wantTopics: func() []types.TopicDetails {
				retentionMs := "86400000"
				cleanupPolicy := "delete"
				return []types.TopicDetails{
					{
						Name:              "test-topic-1",
						Partitions:        3,
						ReplicationFactor: 2,
						Configurations: map[string]*string{
							"retention.ms":   &retentionMs,
							"cleanup.policy": &cleanupPolicy,
						},
					},
					{
						Name:              "test-topic-2",
						Partitions:        6,
						ReplicationFactor: 3,
						Configurations: map[string]*string{
							"retention.ms": &retentionMs,
						},
					},
				}
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ks := &KafkaService{
				client:     tt.mockClient,
				authType:   types.AuthTypeIAM,
				clusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test/abc-123",
			}

			result, err := ks.scanClusterTopics()

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErrMsg, err.Error())
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.Len(t, result, len(tt.wantTopics))

				// Since map iteration order is not guaranteed, we need to check each topic individually
				for _, expectedTopic := range tt.wantTopics {
					found := false
					for _, actualTopic := range result {
						if actualTopic.Name == expectedTopic.Name {
							assert.Equal(t, expectedTopic.Partitions, actualTopic.Partitions)
							assert.Equal(t, expectedTopic.ReplicationFactor, actualTopic.ReplicationFactor)
							assert.Equal(t, expectedTopic.Configurations, actualTopic.Configurations)
							found = true
							break
						}
					}
					assert.True(t, found, "Expected topic %s not found in result", expectedTopic.Name)
				}
			}
		})
	}
}

func TestKafkaService_describeKafkaCluster(t *testing.T) {
	tests := []struct {
		name         string
		mockClient   *mocks.MockKafkaAdmin
		wantErr      bool
		wantErrMsg   string
		wantMetadata *client.ClusterKafkaMetadata
	}{
		{
			name: "GetClusterKafkaMetadata returns error",
			mockClient: &mocks.MockKafkaAdmin{
				GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
					return nil, errors.New("cluster unreachable")
				},
			},
			wantErr:      true,
			wantErrMsg:   "❌ Failed to describe kafka cluster: cluster unreachable",
			wantMetadata: nil,
		},
		{
			name: "successful cluster description",
			mockClient: &mocks.MockKafkaAdmin{
				GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
					return &client.ClusterKafkaMetadata{
						ClusterID: "test-cluster-456",
					}, nil
				},
			},
			wantErr: false,
			wantMetadata: &client.ClusterKafkaMetadata{
				ClusterID: "test-cluster-456",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ks := &KafkaService{
				client:     tt.mockClient,
				authType:   types.AuthTypeIAM,
				clusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test/abc-123",
			}

			result, err := ks.describeKafkaCluster()

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErrMsg, err.Error())
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantMetadata, result)
			}
		})
	}
}

func TestKafkaService_scanKafkaAcls(t *testing.T) {
	tests := []struct {
		name       string
		mockClient *mocks.MockKafkaAdmin
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "ListAcls returns error",
			mockClient: &mocks.MockKafkaAdmin{
				ListAclsFunc: func() ([]types.Acls, error) {
					return nil, errors.New("connection failed")
				},
			},
			wantErr:    true,
			wantErrMsg: "❌ Failed to list acls: connection failed",
		},
		{
			name: "successful ACL scan pass-through",
			mockClient: &mocks.MockKafkaAdmin{
				ListAclsFunc: func() ([]types.Acls, error) {
					return []types.Acls{
						{
							ResourceType:        "Topic",
							ResourceName:        "test-topic",
							ResourcePatternType: "Literal",
							Principal:           "User:test-user",
							Host:                "*",
							Operation:           "Read",
							PermissionType:      "Allow",
						},
						{
							ResourceType:        "Topic",
							ResourceName:        "test-topic",
							ResourcePatternType: "Literal",
							Principal:           "User:another-user",
							Host:                "192.168.1.1",
							Operation:           "Write",
							PermissionType:      "Deny",
						},
					}, nil
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ks := &KafkaService{
				client:     tt.mockClient,
				authType:   types.AuthTypeIAM,
				clusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test/abc-123",
			}

			result, err := ks.scanKafkaAcls()

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErrMsg, err.Error())
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)

				// Verify ACL pass-through: should have 2 ACLs
				assert.Len(t, result, 2)

				// Check first ACL
				assert.Equal(t, "Topic", result[0].ResourceType)
				assert.Equal(t, "test-topic", result[0].ResourceName)
				assert.Equal(t, "Literal", result[0].ResourcePatternType)
				assert.Equal(t, "User:test-user", result[0].Principal)
				assert.Equal(t, "*", result[0].Host)
				assert.Equal(t, "Read", result[0].Operation)
				assert.Equal(t, "Allow", result[0].PermissionType)

				// Check second ACL
				assert.Equal(t, "Topic", result[1].ResourceType)
				assert.Equal(t, "test-topic", result[1].ResourceName)
				assert.Equal(t, "User:another-user", result[1].Principal)
				assert.Equal(t, "192.168.1.1", result[1].Host)
				assert.Equal(t, "Write", result[1].Operation)
				assert.Equal(t, "Deny", result[1].PermissionType)
			}
		})
	}
}
