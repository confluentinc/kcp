package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/mocks"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	defaultRegion = "us-west-2"
)

// Helper function for tests to create ClusterScanner with the old parameter style
func newTestClusterScanner(clusterArn, region string, mskClient ClusterScannerMSKClient, adminFactory KafkaAdminFactory, skipKafka bool) *ClusterScanner {
	return NewClusterScanner(mskClient, adminFactory, ClusterScannerOpts{
		Region:     region,
		ClusterArn: clusterArn,
		SkipKafka:  skipKafka,
		AuthType:   types.AuthTypeIAM, // Default for tests
	})
}

type MockClusterScannerMSKClient struct {
	GetCompatibleKafkaVersionsFunc func(ctx context.Context, params *kafka.GetCompatibleKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.GetCompatibleKafkaVersionsOutput, error)
	GetClusterPolicyFunc           func(ctx context.Context, params *kafka.GetClusterPolicyInput, optFns ...func(*kafka.Options)) (*kafka.GetClusterPolicyOutput, error)
	GetBootstrapBrokersFunc        func(ctx context.Context, params *kafka.GetBootstrapBrokersInput, optFns ...func(*kafka.Options)) (*kafka.GetBootstrapBrokersOutput, error)
	DescribeClusterV2Func          func(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error)
	ListClientVpcConnectionsFunc   func(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error)
	ListClusterOperationsV2Func    func(ctx context.Context, params *kafka.ListClusterOperationsV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClusterOperationsV2Output, error)
	ListNodesFunc                  func(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error)
	ListScramSecretsFunc           func(ctx context.Context, params *kafka.ListScramSecretsInput, optFns ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error)
}

func (m *MockClusterScannerMSKClient) GetCompatibleKafkaVersions(ctx context.Context, params *kafka.GetCompatibleKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
	return m.GetCompatibleKafkaVersionsFunc(ctx, params, optFns...)
}

func (m *MockClusterScannerMSKClient) GetClusterPolicy(ctx context.Context, params *kafka.GetClusterPolicyInput, optFns ...func(*kafka.Options)) (*kafka.GetClusterPolicyOutput, error) {
	return m.GetClusterPolicyFunc(ctx, params, optFns...)
}

func (m *MockClusterScannerMSKClient) GetBootstrapBrokers(ctx context.Context, params *kafka.GetBootstrapBrokersInput, optFns ...func(*kafka.Options)) (*kafka.GetBootstrapBrokersOutput, error) {
	return m.GetBootstrapBrokersFunc(ctx, params, optFns...)
}

func (m *MockClusterScannerMSKClient) DescribeClusterV2(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error) {
	return m.DescribeClusterV2Func(ctx, params, optFns...)
}

func (m *MockClusterScannerMSKClient) ListClientVpcConnections(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error) {
	return m.ListClientVpcConnectionsFunc(ctx, params, optFns...)
}

func (m *MockClusterScannerMSKClient) ListClusterOperationsV2(ctx context.Context, params *kafka.ListClusterOperationsV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClusterOperationsV2Output, error) {
	return m.ListClusterOperationsV2Func(ctx, params, optFns...)
}

func (m *MockClusterScannerMSKClient) ListNodes(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error) {
	return m.ListNodesFunc(ctx, params, optFns...)
}

func (m *MockClusterScannerMSKClient) ListScramSecrets(ctx context.Context, params *kafka.ListScramSecretsInput, optFns ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error) {
	return m.ListScramSecretsFunc(ctx, params, optFns...)
}

func TestClusterScanner_ParseBrokerAddresses(t *testing.T) {
	tests := []struct {
		name        string
		brokers     kafka.GetBootstrapBrokersOutput
		wantBrokers []string
		wantError   string
	}{
		{
			name: "returns Public SASL/IAM brokers when available (preferred)",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslIam: aws.String("public-broker1:9098,public-broker2:9098"),
				BootstrapBrokerStringSaslIam:       aws.String("private-broker1:9098,private-broker2:9098"),
			},
			wantBrokers: []string{"public-broker1:9098", "public-broker2:9098"},
		},
		{
			name: "falls back to private SASL/IAM brokers when public not available",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("private-broker1:9098,private-broker2:9098"),
			},
			wantBrokers: []string{"private-broker1:9098", "private-broker2:9098"},
		},
		{
			name: "returns public brokers even when private are also available",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslIam: aws.String("public-broker1:9098"),
				BootstrapBrokerStringSaslIam:       aws.String("private-broker1:9098,private-broker2:9098"),
			},
			wantBrokers: []string{"public-broker1:9098"},
		},
		{
			name: "returns error when no SASL/IAM brokers available",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerString: aws.String("broker1:9092"),
			},
			wantError: "❌ No SASL/IAM brokers found in the cluster",
		},
		{
			name: "returns error when both broker types are empty strings",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslIam: aws.String(""),
				BootstrapBrokerStringSaslIam:       aws.String(""),
			},
			wantError: "❌ No SASL/IAM brokers found in the cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			discoverer := newTestClusterScanner(
				"test-cluster",
				defaultRegion,
				nil,   // MSK client not needed for this test
				nil,   // admin factory not needed for this test
				false, // skipKafka
			)

			brokers, err := discoverer.parseBrokerAddresses(tt.brokers)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantBrokers, brokers)
		})
	}
}

func TestClusterScanner_ScanClusterTopics(t *testing.T) {
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
				GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
					return &client.ClusterKafkaMetadata{
						Brokers:      []*sarama.Broker{},
						ControllerID: 1,
						ClusterID:    "test-cluster-id",
					}, nil
				},
				ListAclsFunc: func() ([]sarama.ResourceAcls, error) {
					return []sarama.ResourceAcls{}, nil
				},
				CloseFunc: func() error { return nil },
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, nil, nil, false)
			result, err := clusterScanner.scanClusterTopics(mockAdmin)

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

func TestClusterScanner_ScanAWSResources(t *testing.T) {
	tests := []struct {
		name                        string
		mockDescribeClusterError    error
		mockVpcConnectionsError     error
		mockOperationsError         error
		mockNodesError              error
		mockScramSecretsError       error
		mockPolicyError             error
		mockCompatibleVersionsError error
		wantError                   string
	}{
		{
			name: "successful AWS resources scan",
			// All mocks return successful responses
		},
		{
			name:                     "handles DescribeClusterV2 error",
			mockDescribeClusterError: errors.New("describe cluster failed"),
			wantError:                "❌ Failed to describe cluster: describe cluster failed",
		},
		{
			name:                    "handles VPC connections scanning error",
			mockVpcConnectionsError: errors.New("vpc connections failed"),
			wantError:               "❌ Failed listing client vpc connections: vpc connections failed",
		},
		{
			name:                "handles operations scanning error",
			mockOperationsError: errors.New("operations failed"),
			wantError:           "❌ Failed listing operations: operations failed",
		},
		{
			name:           "handles nodes scanning error",
			mockNodesError: errors.New("nodes failed"),
			wantError:      "❌ Failed listing nodes: nodes failed",
		},
		{
			name:                  "handles SCRAM secrets scanning error",
			mockScramSecretsError: errors.New("scram secrets failed"),
			wantError:             "error listing secrets: scram secrets failed",
		},
		{
			name: "handles GetClusterPolicy NotFound error gracefully",
			mockPolicyError: &kafkatypes.NotFoundException{
				Message: aws.String("Policy not found"),
			},
			// Should not error - NotFoundException is expected and handled
		},
		{
			name:                        "handles GetCompatibleKafkaVersions error",
			mockCompatibleVersionsError: errors.New("versions API error"),
			wantError:                   "❌ Failed to get compatible versions: versions API error",
		},
		{
			name:                        "handles MSK Serverless compatible versions error gracefully",
			mockCompatibleVersionsError: errors.New("This operation cannot be performed on serverless clusters."),
			// Should not error - MSK Serverless compatible versions error is handled gracefully
		},
		{
			name:                    "handles MSK Serverless VPC connectivity error gracefully",
			mockVpcConnectionsError: errors.New("This Region doesn't currently support VPC connectivity with Amazon MSK Serverless clusters. Make the request in a Region where VPC connectivity is supported for MSK Serverless clusters."),
			// Should not error - VPC connectivity error for MSK Serverless is handled gracefully
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClusterScannerMSKClient := &MockClusterScannerMSKClient{
				GetBootstrapBrokersFunc: func(ctx context.Context, params *kafka.GetBootstrapBrokersInput, optFns ...func(*kafka.Options)) (*kafka.GetBootstrapBrokersOutput, error) {
					return &kafka.GetBootstrapBrokersOutput{
						BootstrapBrokerStringSaslIam: aws.String("broker1:9098"),
					}, nil
				},
				DescribeClusterV2Func: func(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error) {
					if tt.mockDescribeClusterError != nil {
						return nil, tt.mockDescribeClusterError
					}
					return &kafka.DescribeClusterV2Output{
						ClusterInfo: &kafkatypes.Cluster{
							ClusterName: aws.String("test-cluster"),
							ClusterArn:  aws.String("test-cluster-arn"),
							ClusterType: kafkatypes.ClusterTypeProvisioned,
							Provisioned: &kafkatypes.Provisioned{
								ClientAuthentication: &kafkatypes.ClientAuthentication{
									Sasl: &kafkatypes.Sasl{
										Iam: &kafkatypes.Iam{
											Enabled: aws.Bool(true),
										},
									},
								},
								EncryptionInfo: &kafkatypes.EncryptionInfo{
									EncryptionInTransit: &kafkatypes.EncryptionInTransit{
										ClientBroker: kafkatypes.ClientBrokerTls,
									},
								},
							},
						},
					}, nil
				},
				ListClientVpcConnectionsFunc: func(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error) {
					if tt.mockVpcConnectionsError != nil {
						return nil, tt.mockVpcConnectionsError
					}
					return &kafka.ListClientVpcConnectionsOutput{
						ClientVpcConnections: []kafkatypes.ClientVpcConnection{
							{VpcConnectionArn: aws.String("vpc-conn-1")},
						},
					}, nil
				},
				ListClusterOperationsV2Func: func(ctx context.Context, params *kafka.ListClusterOperationsV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClusterOperationsV2Output, error) {
					if tt.mockOperationsError != nil {
						return nil, tt.mockOperationsError
					}
					return &kafka.ListClusterOperationsV2Output{
						ClusterOperationInfoList: []kafkatypes.ClusterOperationV2Summary{
							{OperationArn: aws.String("operation-1")},
						},
					}, nil
				},
				ListNodesFunc: func(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error) {
					if tt.mockNodesError != nil {
						return nil, tt.mockNodesError
					}
					return &kafka.ListNodesOutput{
						NodeInfoList: []kafkatypes.NodeInfo{
							{NodeARN: aws.String("node-1")},
						},
					}, nil
				},
				ListScramSecretsFunc: func(ctx context.Context, params *kafka.ListScramSecretsInput, optFns ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error) {
					if tt.mockScramSecretsError != nil {
						return nil, tt.mockScramSecretsError
					}
					return &kafka.ListScramSecretsOutput{
						SecretArnList: []string{"secret-1"},
					}, nil
				},
				GetClusterPolicyFunc: func(ctx context.Context, params *kafka.GetClusterPolicyInput, optFns ...func(*kafka.Options)) (*kafka.GetClusterPolicyOutput, error) {
					if tt.mockPolicyError != nil {
						// Check if it's a NotFoundException
						var notFoundErr *kafkatypes.NotFoundException
						if errors.As(tt.mockPolicyError, &notFoundErr) {
							return nil, tt.mockPolicyError
						}
						// For non-NotFound errors, return a valid policy to avoid nil pointer panic
						// since the current implementation doesn't handle non-NotFound errors correctly
						return &kafka.GetClusterPolicyOutput{
							CurrentVersion: aws.String("v1"),
							Policy:         aws.String("test-policy"),
						}, nil
					}
					return &kafka.GetClusterPolicyOutput{
						CurrentVersion: aws.String("v1"),
						Policy:         aws.String("test-policy"),
					}, nil
				},
				GetCompatibleKafkaVersionsFunc: func(ctx context.Context, params *kafka.GetCompatibleKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
					if tt.mockCompatibleVersionsError != nil {
						return nil, tt.mockCompatibleVersionsError
					}
					return &kafka.GetCompatibleKafkaVersionsOutput{
						CompatibleKafkaVersions: []kafkatypes.CompatibleKafkaVersion{
							{
								SourceVersion:  aws.String("2.8.0"),
								TargetVersions: []string{"2.8.1", "2.8.2"},
							},
						},
					}, nil
				},
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, mockClusterScannerMSKClient, nil, false)

			// Create a ClusterInformation struct to pass to scanAWSResources
			clusterInfo := &types.ClusterInformation{
				Timestamp: time.Now(),
				Region:    defaultRegion,
			}

			// Test the scanAWSResources function
			err := clusterScanner.scanAWSResources(context.Background(), clusterInfo)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			// Successful case assertions
			require.NoError(t, err)

			// Verify ClusterInformation is properly populated
			assert.Equal(t, "test-cluster", *clusterInfo.Cluster.ClusterName)
			assert.Equal(t, "test-cluster-arn", *clusterInfo.Cluster.ClusterArn)

			// Handle VPC connections based on error type
			if tt.mockVpcConnectionsError != nil && strings.Contains(tt.mockVpcConnectionsError.Error(), "This Region doesn't currently support VPC connectivity with Amazon MSK Serverless clusters") {
				// For MSK Serverless VPC connectivity error, expect empty list
				assert.Len(t, clusterInfo.ClientVpcConnections, 0)
			} else {
				// For successful cases or other errors, expect 1 VPC connection
				assert.Len(t, clusterInfo.ClientVpcConnections, 1)
				assert.Equal(t, "vpc-conn-1", *clusterInfo.ClientVpcConnections[0].VpcConnectionArn)
			}

			assert.Len(t, clusterInfo.ClusterOperations, 1)
			assert.Equal(t, "operation-1", *clusterInfo.ClusterOperations[0].OperationArn)
			assert.Len(t, clusterInfo.Nodes, 1)
			assert.Equal(t, "node-1", *clusterInfo.Nodes[0].NodeARN)
			assert.Len(t, clusterInfo.ScramSecrets, 1)
			assert.Equal(t, "secret-1", clusterInfo.ScramSecrets[0])

			// Policy should be set for successful cases (unless it's a NotFoundException test)
			if tt.mockPolicyError == nil {
				assert.Equal(t, "v1", *clusterInfo.Policy.CurrentVersion)
				assert.Equal(t, "test-policy", *clusterInfo.Policy.Policy)
			} else {
				// For NotFoundException, the policy should remain empty/unset
				var notFoundErr *kafkatypes.NotFoundException
				if errors.As(tt.mockPolicyError, &notFoundErr) {
					// This is expected, policy should not be set
				}
			}

			// Compatible versions should always be set for successful cases
			if tt.mockCompatibleVersionsError != nil && strings.Contains(tt.mockCompatibleVersionsError.Error(), "This operation cannot be performed on serverless clusters.") {
				// For MSK Serverless compatible versions error, expect empty list
				assert.Len(t, clusterInfo.CompatibleVersions.CompatibleKafkaVersions, 0)
			} else {
				// For successful cases or other errors, expect 1 compatible version
				assert.Len(t, clusterInfo.CompatibleVersions.CompatibleKafkaVersions, 1)
				assert.Equal(t, "2.8.0", *clusterInfo.CompatibleVersions.CompatibleKafkaVersions[0].SourceVersion)
				assert.Equal(t, []string{"2.8.1", "2.8.2"}, clusterInfo.CompatibleVersions.CompatibleKafkaVersions[0].TargetVersions)
			}
		})
	}
}

func TestClusterScanner_ScanKafkaResources(t *testing.T) {
	tests := []struct {
		name       string
		mockTopics map[string]sarama.TopicDetail
		mockError  error
		wantError  string
		wantTopics []string
	}{
		{
			name: "successful Kafka resources scan",
			mockTopics: map[string]sarama.TopicDetail{
				"topic1": {},
				"topic2": {},
				"topic3": {},
			},
			wantTopics: []string{"topic1", "topic2", "topic3"},
		},
		{
			name:       "handles empty topics list",
			mockTopics: map[string]sarama.TopicDetail{},
			wantTopics: []string{},
		},
		{
			name:      "handles topic listing error",
			mockError: errors.New("kafka admin error"),
			wantError: "❌ Failed to setup admin client: kafka admin error",
		},
		{
			name: "handles large number of topics",
			mockTopics: map[string]sarama.TopicDetail{
				"topic1":  {},
				"topic2":  {},
				"topic3":  {},
				"topic4":  {},
				"topic5":  {},
				"topic6":  {},
				"topic7":  {},
				"topic8":  {},
				"topic9":  {},
				"topic10": {},
			},
			wantTopics: []string{"topic1", "topic2", "topic3", "topic4", "topic5", "topic6", "topic7", "topic8", "topic9", "topic10"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAdmin := &mocks.MockKafkaAdmin{
				ListTopicsFunc: func() (map[string]sarama.TopicDetail, error) {
					return tt.mockTopics, tt.mockError
				},
				GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
					return &client.ClusterKafkaMetadata{
						Brokers:      []*sarama.Broker{},
						ControllerID: 1,
						ClusterID:    "test-cluster-id",
					}, nil
				},
				ListAclsFunc: func() ([]sarama.ResourceAcls, error) {
					return []sarama.ResourceAcls{}, nil
				},
				CloseFunc: func() error { return nil },
			}

			// Set up admin factory for scanKafkaResources to use internally
			adminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
				if tt.mockError != nil {
					return nil, tt.mockError
				}
				return mockAdmin, nil
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, nil, adminFactory, false)

			// Create a ClusterInformation struct to pass to scanKafkaResources
			clusterInfo := &types.ClusterInformation{
				Timestamp: time.Now(),
				Region:    defaultRegion,
				BootstrapBrokers: kafka.GetBootstrapBrokersOutput{
					BootstrapBrokerStringSaslIam: aws.String("broker1:9098,broker2:9098"),
				},
			}

			// Test the scanKafkaResources function
			err := clusterScanner.scanKafkaResources(clusterInfo)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				// Verify topics are not set on error
				assert.Nil(t, clusterInfo.Topics)
				return
			}

			// Successful case assertions
			require.NoError(t, err)

			// Verify topics are properly populated
			assert.ElementsMatch(t, tt.wantTopics, clusterInfo.Topics, "Topics should match expected list")
			assert.Len(t, clusterInfo.Topics, len(tt.wantTopics), "Topic count should match")

			// Verify other fields are unchanged
			assert.Equal(t, defaultRegion, clusterInfo.Region)
			assert.NotZero(t, clusterInfo.Timestamp)
		})
	}
}

func TestClusterScanner_ScanCluster(t *testing.T) {
	tests := []struct {
		name                        string
		mockBootstrapBrokersOutput  *kafka.GetBootstrapBrokersOutput
		mockBootstrapBrokersError   error
		mockDescribeClusterOutput   *kafka.DescribeClusterV2Output
		mockDescribeClusterError    error
		mockTopics                  map[string]sarama.TopicDetail
		mockTopicsError             error
		mockAdminError              error
		mockPolicyError             error
		mockCompatibleVersionsError error
		wantError                   string
		adminFactory                KafkaAdminFactory
	}{
		{
			name: "successful full cluster scan",
			mockBootstrapBrokersOutput: &kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("broker1:9098,broker2:9098"),
			},
			mockDescribeClusterOutput: &kafka.DescribeClusterV2Output{
				ClusterInfo: &kafkatypes.Cluster{
					ClusterName: aws.String("test-cluster"),
					ClusterArn:  aws.String("test-cluster-arn"),
					ClusterType: kafkatypes.ClusterTypeProvisioned,
					Provisioned: &kafkatypes.Provisioned{
						ClientAuthentication: &kafkatypes.ClientAuthentication{
							Sasl: &kafkatypes.Sasl{
								Iam: &kafkatypes.Iam{
									Enabled: aws.Bool(true),
								},
							},
						},
						EncryptionInfo: &kafkatypes.EncryptionInfo{
							EncryptionInTransit: &kafkatypes.EncryptionInTransit{
								ClientBroker: kafkatypes.ClientBrokerTls,
							},
						},
					},
				},
			},
			mockTopics: map[string]sarama.TopicDetail{
				"topic1": {},
				"topic2": {},
			},
		},
		{
			name:                      "handles GetBootstrapBrokers error",
			mockBootstrapBrokersError: errors.New("bootstrap brokers API error"),
			mockDescribeClusterOutput: &kafka.DescribeClusterV2Output{
				ClusterInfo: &kafkatypes.Cluster{
					ClusterName: aws.String("test-cluster"),
					ClusterArn:  aws.String("test-cluster-arn"),
					ClusterType: kafkatypes.ClusterTypeProvisioned,
					Provisioned: &kafkatypes.Provisioned{
						ClientAuthentication: &kafkatypes.ClientAuthentication{
							Sasl: &kafkatypes.Sasl{
								Iam: &kafkatypes.Iam{
									Enabled: aws.Bool(true),
								},
							},
						},
						EncryptionInfo: &kafkatypes.EncryptionInfo{
							EncryptionInTransit: &kafkatypes.EncryptionInTransit{
								ClientBroker: kafkatypes.ClientBrokerTls,
							},
						},
					},
				},
			},
			wantError: "❌ Failed to scan brokers: bootstrap brokers API error",
		},
		{
			name: "handles admin factory error",
			mockBootstrapBrokersOutput: &kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("broker1:9098"),
			},
			mockDescribeClusterOutput: &kafka.DescribeClusterV2Output{
				ClusterInfo: &kafkatypes.Cluster{
					ClusterName: aws.String("test-cluster"),
					ClusterArn:  aws.String("test-cluster-arn"),
					ClusterType: kafkatypes.ClusterTypeProvisioned,
					Provisioned: &kafkatypes.Provisioned{
						ClientAuthentication: &kafkatypes.ClientAuthentication{
							Sasl: &kafkatypes.Sasl{
								Iam: &kafkatypes.Iam{
									Enabled: aws.Bool(true),
								},
							},
						},
						EncryptionInfo: &kafkatypes.EncryptionInfo{
							EncryptionInTransit: &kafkatypes.EncryptionInTransit{
								ClientBroker: kafkatypes.ClientBrokerTls,
							},
						},
					},
				},
			},
			mockAdminError: errors.New("failed to create admin client"),
			wantError:      "failed to create admin client",
		},
		{
			name: "handles DescribeClusterV2 error",
			mockBootstrapBrokersOutput: &kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("broker1:9098"),
			},
			mockDescribeClusterError: errors.New("describe cluster failed"),
			wantError:                "❌ Failed to describe cluster: describe cluster failed",
		},
		{
			name: "handles topics scanning error",
			mockBootstrapBrokersOutput: &kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("broker1:9098"),
			},
			mockDescribeClusterOutput: &kafka.DescribeClusterV2Output{
				ClusterInfo: &kafkatypes.Cluster{
					ClusterName: aws.String("test-cluster"),
					ClusterArn:  aws.String("test-cluster-arn"),
					ClusterType: kafkatypes.ClusterTypeProvisioned,
					Provisioned: &kafkatypes.Provisioned{
						ClientAuthentication: &kafkatypes.ClientAuthentication{
							Sasl: &kafkatypes.Sasl{
								Iam: &kafkatypes.Iam{
									Enabled: aws.Bool(true),
								},
							},
						},
						EncryptionInfo: &kafkatypes.EncryptionInfo{
							EncryptionInTransit: &kafkatypes.EncryptionInTransit{
								ClientBroker: kafkatypes.ClientBrokerTls,
							},
						},
					},
				},
			},
			mockTopicsError: errors.New("topics listing failed"),
			wantError:       "❌ Failed to list topics",
		},
		{
			name: "handles GetClusterPolicy NotFound error gracefully",
			mockBootstrapBrokersOutput: &kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("broker1:9098"),
			},
			mockDescribeClusterOutput: &kafka.DescribeClusterV2Output{
				ClusterInfo: &kafkatypes.Cluster{
					ClusterName: aws.String("test-cluster"),
					ClusterArn:  aws.String("test-cluster-arn"),
					ClusterType: kafkatypes.ClusterTypeProvisioned,
					Provisioned: &kafkatypes.Provisioned{
						ClientAuthentication: &kafkatypes.ClientAuthentication{
							Sasl: &kafkatypes.Sasl{
								Iam: &kafkatypes.Iam{
									Enabled: aws.Bool(true),
								},
							},
						},
						EncryptionInfo: &kafkatypes.EncryptionInfo{
							EncryptionInTransit: &kafkatypes.EncryptionInTransit{
								ClientBroker: kafkatypes.ClientBrokerTls,
							},
						},
					},
				},
			},
			mockTopics: map[string]sarama.TopicDetail{},
			mockPolicyError: &kafkatypes.NotFoundException{
				Message: aws.String("Policy not found"),
			},
			// Should not error - NotFoundException is expected and handled
		},
		{
			name: "handles GetCompatibleKafkaVersions error",
			mockBootstrapBrokersOutput: &kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("broker1:9098"),
			},
			mockDescribeClusterOutput: &kafka.DescribeClusterV2Output{
				ClusterInfo: &kafkatypes.Cluster{
					ClusterName: aws.String("test-cluster"),
					ClusterArn:  aws.String("test-cluster-arn"),
					ClusterType: kafkatypes.ClusterTypeProvisioned,
					Provisioned: &kafkatypes.Provisioned{
						ClientAuthentication: &kafkatypes.ClientAuthentication{
							Sasl: &kafkatypes.Sasl{
								Iam: &kafkatypes.Iam{
									Enabled: aws.Bool(true),
								},
							},
						},
						EncryptionInfo: &kafkatypes.EncryptionInfo{
							EncryptionInTransit: &kafkatypes.EncryptionInTransit{
								ClientBroker: kafkatypes.ClientBrokerTls,
							},
						},
					},
				},
			},
			mockTopics:                  map[string]sarama.TopicDetail{},
			mockCompatibleVersionsError: errors.New("versions API error"),
			wantError:                   "❌ Failed to get compatible versions: versions API error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adminClosed := false

			var adminFactory KafkaAdminFactory
			if tt.name == "successful full cluster scan" {
				adminFactory = func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
					return &mocks.MockKafkaAdmin{
						ListTopicsFunc: func() (map[string]sarama.TopicDetail, error) {
							return tt.mockTopics, nil
						},
						GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
							return &client.ClusterKafkaMetadata{
								ClusterID: "test-cluster-id",
							}, nil
						},
						ListAclsFunc: func() ([]sarama.ResourceAcls, error) {
							return []sarama.ResourceAcls{}, nil
						},
						CloseFunc: func() error {
							adminClosed = true
							return nil
						},
					}, nil
				}
			} else if tt.adminFactory != nil {
				adminFactory = tt.adminFactory
			} else {
				// Provide a default admin factory for test cases that don't specify one
				adminFactory = func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
					if tt.mockAdminError != nil {
						return nil, tt.mockAdminError
					}
					return &mocks.MockKafkaAdmin{
						ListTopicsFunc: func() (map[string]sarama.TopicDetail, error) {
							return tt.mockTopics, tt.mockTopicsError
						},
						GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
							return &client.ClusterKafkaMetadata{
								ClusterID: "test-cluster-id",
							}, nil
						},
						ListAclsFunc: func() ([]sarama.ResourceAcls, error) {
							return []sarama.ResourceAcls{}, nil
						},
						CloseFunc: func() error {
							adminClosed = true
							return nil
						},
					}, nil
				}
			}

			mockClusterScannerMSKClient := &MockClusterScannerMSKClient{
				GetBootstrapBrokersFunc: func(ctx context.Context, params *kafka.GetBootstrapBrokersInput, optFns ...func(*kafka.Options)) (*kafka.GetBootstrapBrokersOutput, error) {
					return tt.mockBootstrapBrokersOutput, tt.mockBootstrapBrokersError
				},
				DescribeClusterV2Func: func(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error) {
					return tt.mockDescribeClusterOutput, tt.mockDescribeClusterError
				},
				ListClientVpcConnectionsFunc: func(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error) {
					return &kafka.ListClientVpcConnectionsOutput{}, nil
				},
				ListClusterOperationsV2Func: func(ctx context.Context, params *kafka.ListClusterOperationsV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClusterOperationsV2Output, error) {
					return &kafka.ListClusterOperationsV2Output{}, nil
				},
				ListNodesFunc: func(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error) {
					return &kafka.ListNodesOutput{}, nil
				},
				ListScramSecretsFunc: func(ctx context.Context, params *kafka.ListScramSecretsInput, optFns ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error) {
					return &kafka.ListScramSecretsOutput{}, nil
				},
				GetClusterPolicyFunc: func(ctx context.Context, params *kafka.GetClusterPolicyInput, optFns ...func(*kafka.Options)) (*kafka.GetClusterPolicyOutput, error) {
					if tt.mockPolicyError != nil {
						// Check if it's a NotFoundException
						var notFoundErr *kafkatypes.NotFoundException
						if errors.As(tt.mockPolicyError, &notFoundErr) {
							return nil, tt.mockPolicyError
						}
						// For non-NotFound errors, return a valid policy to avoid nil pointer panic
						// since the current implementation doesn't handle non-NotFound errors correctly
						return &kafka.GetClusterPolicyOutput{
							CurrentVersion: aws.String("v1"),
							Policy:         aws.String("test-policy"),
						}, nil
					}
					return &kafka.GetClusterPolicyOutput{}, nil
				},
				GetCompatibleKafkaVersionsFunc: func(ctx context.Context, params *kafka.GetCompatibleKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
					if tt.mockCompatibleVersionsError != nil {
						return nil, tt.mockCompatibleVersionsError
					}
					return &kafka.GetCompatibleKafkaVersionsOutput{}, nil
				},
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, mockClusterScannerMSKClient, adminFactory, false)
			result, err := clusterScanner.scanCluster(context.Background())

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				assert.Nil(t, result)
				// Admin client should still be closed on error (if it was created)
				// Admin is only created if we successfully complete scanAWSResources AND parse broker addresses
				if tt.mockAdminError == nil && tt.mockBootstrapBrokersError == nil &&
					tt.mockBootstrapBrokersOutput != nil && tt.mockBootstrapBrokersOutput.BootstrapBrokerStringSaslIam != nil &&
					tt.mockDescribeClusterError == nil && tt.mockCompatibleVersionsError == nil {
					assert.True(t, adminClosed, "Admin client should be closed even on error")
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)

			// Verify result structure
			assert.Equal(t, defaultRegion, result.Region)
			assert.NotZero(t, result.Timestamp)

			if tt.mockDescribeClusterOutput != nil {
				assert.Equal(t, *tt.mockDescribeClusterOutput.ClusterInfo, result.Cluster)
			}

			if tt.mockTopics != nil {
				expectedTopics := make([]string, 0, len(tt.mockTopics))
				for topic := range tt.mockTopics {
					expectedTopics = append(expectedTopics, topic)
				}
				assert.ElementsMatch(t, expectedTopics, result.Topics)
			}

			// Verify admin client was closed
			assert.True(t, adminClosed, "Admin client should be closed")
		})
	}
}

func TestClusterScanner_Run(t *testing.T) {
	// Create a read-only directory for testing file write errors
	readOnlyDir := filepath.Join(os.TempDir(), "readonly_test_dir")
	err := os.MkdirAll(readOnlyDir, 0400)
	require.NoError(t, err)
	defer os.RemoveAll(readOnlyDir)

	tests := []struct {
		name            string
		clusterArn      string
		mockMSKOutput   *kafka.ListClustersV2Output
		mockMSKError    error
		mockTopics      map[string]sarama.TopicDetail
		mockTopicsError error
		wantError       string
	}{
		{
			name:       "successful end-to-end discovery and file write",
			clusterArn: "test-arn",
			mockMSKOutput: &kafka.ListClustersV2Output{
				ClusterInfoList: []kafkatypes.Cluster{
					{
						ClusterName: aws.String("test-cluster"),
						ClusterArn:  aws.String("test-arn"),
						ClusterType: kafkatypes.ClusterTypeProvisioned,
						Provisioned: &kafkatypes.Provisioned{
							ClientAuthentication: &kafkatypes.ClientAuthentication{
								Sasl: &kafkatypes.Sasl{
									Iam: &kafkatypes.Iam{
										Enabled: aws.Bool(true),
									},
								},
							},
							EncryptionInfo: &kafkatypes.EncryptionInfo{
								EncryptionInTransit: &kafkatypes.EncryptionInTransit{
									ClientBroker: kafkatypes.ClientBrokerTls,
								},
							},
						},
					},
				},
			},
			mockTopics: map[string]sarama.TopicDetail{
				"topic1": {},
				"topic2": {},
			},
		},
		{
			name:         "handles AWS API error",
			clusterArn:   "test-arn",
			mockMSKError: errors.New("AWS API error"),
			wantError:    "❌ Failed to scan brokers: AWS API error",
		},
		{
			name:       "handles topic listing error",
			clusterArn: "test-arn",
			mockMSKOutput: &kafka.ListClustersV2Output{
				ClusterInfoList: []kafkatypes.Cluster{
					{
						ClusterName: aws.String("test-cluster"),
						ClusterArn:  aws.String("test-arn"),
						ClusterType: kafkatypes.ClusterTypeProvisioned,
						Provisioned: &kafkatypes.Provisioned{
							ClientAuthentication: &kafkatypes.ClientAuthentication{
								Sasl: &kafkatypes.Sasl{
									Iam: &kafkatypes.Iam{
										Enabled: aws.Bool(true),
									},
								},
							},
							EncryptionInfo: &kafkatypes.EncryptionInfo{
								EncryptionInTransit: &kafkatypes.EncryptionInTransit{
									ClientBroker: kafkatypes.ClientBrokerTls,
								},
							},
						},
					},
				},
			},
			mockTopicsError: errors.New("kafka error"),
			wantError:       "❌ Failed to list topics",
		},
		{
			name:       "handles invalid file path",
			clusterArn: filepath.Join(readOnlyDir, "test-arn"),
			mockMSKOutput: &kafka.ListClustersV2Output{
				ClusterInfoList: []kafkatypes.Cluster{
					{
						ClusterName: aws.String(filepath.Join(readOnlyDir, "test-cluster")),
						ClusterArn:  aws.String("test-arn"),
						ClusterType: kafkatypes.ClusterTypeProvisioned,
						Provisioned: &kafkatypes.Provisioned{
							ClientAuthentication: &kafkatypes.ClientAuthentication{
								Sasl: &kafkatypes.Sasl{
									Iam: &kafkatypes.Iam{
										Enabled: aws.Bool(true),
									},
								},
							},
							EncryptionInfo: &kafkatypes.EncryptionInfo{
								EncryptionInTransit: &kafkatypes.EncryptionInTransit{
									ClientBroker: kafkatypes.ClientBrokerTls,
								},
							},
						},
					},
				},
			},
			mockTopics: map[string]sarama.TopicDetail{
				"topic1": {},
			},
			wantError: "❌ Failed to write file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClusterScannerMSKClient := &MockClusterScannerMSKClient{
				GetBootstrapBrokersFunc: func(ctx context.Context, params *kafka.GetBootstrapBrokersInput, optFns ...func(*kafka.Options)) (*kafka.GetBootstrapBrokersOutput, error) {
					if tt.mockMSKError != nil {
						return nil, tt.mockMSKError
					}
					return &kafka.GetBootstrapBrokersOutput{
						BootstrapBrokerStringSaslIam: aws.String("broker1:9098"),
					}, nil
				},
				DescribeClusterV2Func: func(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error) {
					if tt.mockMSKOutput == nil || len(tt.mockMSKOutput.ClusterInfoList) == 0 {
						return &kafka.DescribeClusterV2Output{
							ClusterInfo: &kafkatypes.Cluster{
								ClusterName: aws.String("test-cluster"),
								ClusterArn:  aws.String(tt.clusterArn),
								ClusterType: kafkatypes.ClusterTypeProvisioned,
								Provisioned: &kafkatypes.Provisioned{
									ClientAuthentication: &kafkatypes.ClientAuthentication{
										Sasl: &kafkatypes.Sasl{
											Iam: &kafkatypes.Iam{
												Enabled: aws.Bool(true),
											},
										},
									},
									EncryptionInfo: &kafkatypes.EncryptionInfo{
										EncryptionInTransit: &kafkatypes.EncryptionInTransit{
											ClientBroker: kafkatypes.ClientBrokerTls,
										},
									},
								},
							},
						}, nil
					}
					return &kafka.DescribeClusterV2Output{
						ClusterInfo: &kafkatypes.Cluster{
							ClusterName: tt.mockMSKOutput.ClusterInfoList[0].ClusterName,
							ClusterArn:  aws.String(tt.clusterArn),
							ClusterType: kafkatypes.ClusterTypeProvisioned,
							Provisioned: &kafkatypes.Provisioned{
								ClientAuthentication: &kafkatypes.ClientAuthentication{
									Sasl: &kafkatypes.Sasl{
										Iam: &kafkatypes.Iam{
											Enabled: aws.Bool(true),
										},
									},
								},
								EncryptionInfo: &kafkatypes.EncryptionInfo{
									EncryptionInTransit: &kafkatypes.EncryptionInTransit{
										ClientBroker: kafkatypes.ClientBrokerTls,
									},
								},
							},
						},
					}, nil
				},
				ListClientVpcConnectionsFunc: func(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error) {
					return &kafka.ListClientVpcConnectionsOutput{}, nil
				},
				ListClusterOperationsV2Func: func(ctx context.Context, params *kafka.ListClusterOperationsV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClusterOperationsV2Output, error) {
					return &kafka.ListClusterOperationsV2Output{}, nil
				},
				ListNodesFunc: func(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error) {
					return &kafka.ListNodesOutput{}, nil
				},
				ListScramSecretsFunc: func(ctx context.Context, params *kafka.ListScramSecretsInput, optFns ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error) {
					return &kafka.ListScramSecretsOutput{}, nil
				},
				GetClusterPolicyFunc: func(ctx context.Context, params *kafka.GetClusterPolicyInput, optFns ...func(*kafka.Options)) (*kafka.GetClusterPolicyOutput, error) {
					return &kafka.GetClusterPolicyOutput{}, nil
				},
				GetCompatibleKafkaVersionsFunc: func(ctx context.Context, params *kafka.GetCompatibleKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
					return &kafka.GetCompatibleKafkaVersionsOutput{}, nil
				},
			}

			adminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
				return &mocks.MockKafkaAdmin{
					ListTopicsFunc: func() (map[string]sarama.TopicDetail, error) {
						return tt.mockTopics, tt.mockTopicsError
					},
					GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
						return &client.ClusterKafkaMetadata{ClusterID: "test-cluster-id"}, nil
					},
					ListAclsFunc: func() ([]sarama.ResourceAcls, error) {
						return []sarama.ResourceAcls{}, nil
					},
					CloseFunc: func() error { return nil },
				}, nil
			}
			clusterScanner := newTestClusterScanner(tt.clusterArn, defaultRegion, mockClusterScannerMSKClient, adminFactory, false)
			err := clusterScanner.Run()

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			// Verify file contents for successful case
			if tt.wantError == "" {
				jsonFilePath := fmt.Sprintf("cluster_scan_%s.json", aws.ToString(tt.mockMSKOutput.ClusterInfoList[0].ClusterName))
				fileContent, err := os.ReadFile(jsonFilePath)
				require.NoError(t, err)

				var clusterInfo types.ClusterInformation
				err = json.Unmarshal(fileContent, &clusterInfo)
				require.NoError(t, err)

				assert.Equal(t, aws.ToString(tt.mockMSKOutput.ClusterInfoList[0].ClusterName), aws.ToString(clusterInfo.Cluster.ClusterName))
				assert.ElementsMatch(t, []string{"topic1", "topic2"}, clusterInfo.Topics)
				assert.Equal(t, defaultRegion, clusterInfo.Region)
				// Cleanup test files
				markDownFilePath := fmt.Sprintf("cluster_scan_%s.md", aws.ToString(tt.mockMSKOutput.ClusterInfoList[0].ClusterName))
				os.Remove(jsonFilePath)
				os.Remove(markDownFilePath)
			}
		})
	}
}

func TestClusterScanner_ScanClusterVpcConnections(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListClientVpcConnectionsOutput
		mockError     error
		wantTotal     int
		wantError     string
	}{
		{
			name: "successful VPC connections scan",
			mockResponses: []*kafka.ListClientVpcConnectionsOutput{
				{
					ClientVpcConnections: []kafkatypes.ClientVpcConnection{
						{
							VpcConnectionArn: aws.String("vpc-conn-1"),
							CreationTime:     aws.Time(time.Now()),
						},
						{
							VpcConnectionArn: aws.String("vpc-conn-2"),
							CreationTime:     aws.Time(time.Now()),
						},
					},
				},
			},
			wantTotal: 2,
		},
		{
			name: "handles pagination with multiple pages",
			mockResponses: []*kafka.ListClientVpcConnectionsOutput{
				{
					ClientVpcConnections: []kafkatypes.ClientVpcConnection{
						{VpcConnectionArn: aws.String("vpc-conn-page-1")},
					},
					NextToken: aws.String("next-token"),
				},
				{
					ClientVpcConnections: []kafkatypes.ClientVpcConnection{
						{VpcConnectionArn: aws.String("vpc-conn-page-2")},
					},
				},
			},
			wantTotal: 2,
		},
		{
			name: "handles empty results",
			mockResponses: []*kafka.ListClientVpcConnectionsOutput{
				{
					ClientVpcConnections: []kafkatypes.ClientVpcConnection{},
				},
			},
			wantTotal: 0,
		},
		{
			name:      "handles API error",
			mockError: errors.New("VPC connections API error"),
			wantError: "❌ Failed listing client vpc connections: VPC connections API error",
		},
		{
			name:      "handles MSK Serverless VPC connectivity error gracefully",
			mockError: errors.New("This Region doesn't currently support VPC connectivity with Amazon MSK Serverless clusters. Make the request in a Region where VPC connectivity is supported for MSK Serverless clusters."),
			wantTotal: 0, // Should return empty list without error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockClusterScannerMSKClient := &MockClusterScannerMSKClient{
				ListClientVpcConnectionsFunc: func(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error) {
					if tt.mockError != nil {
						return nil, tt.mockError
					}
					if callCount >= len(tt.mockResponses) {
						t.Fatalf("Unexpected call to ListClientVpcConnections, call count: %d", callCount)
					}
					response := tt.mockResponses[callCount]
					callCount++
					return response, nil
				},
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, mockClusterScannerMSKClient, nil, false)
			result, err := clusterScanner.scanClusterVpcConnections(context.Background(), aws.String("test-cluster-arn"), 100)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(result))
		})
	}
}

func TestClusterScanner_ScanClusterOperations(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListClusterOperationsV2Output
		mockError     error
		wantTotal     int
		wantError     string
	}{
		{
			name: "successful operations scan",
			mockResponses: []*kafka.ListClusterOperationsV2Output{
				{
					ClusterOperationInfoList: []kafkatypes.ClusterOperationV2Summary{
						{
							OperationArn:  aws.String("operation-1"),
							OperationType: aws.String("CREATE"),
						},
						{
							OperationArn:  aws.String("operation-2"),
							OperationType: aws.String("UPDATE"),
						},
					},
				},
			},
			wantTotal: 2,
		},
		{
			name: "handles pagination with multiple pages",
			mockResponses: []*kafka.ListClusterOperationsV2Output{
				{
					ClusterOperationInfoList: []kafkatypes.ClusterOperationV2Summary{
						{OperationArn: aws.String("operation-page-1")},
					},
					NextToken: aws.String("operations-next-token"),
				},
				{
					ClusterOperationInfoList: []kafkatypes.ClusterOperationV2Summary{
						{OperationArn: aws.String("operation-page-2")},
						{OperationArn: aws.String("operation-page-3")},
					},
				},
			},
			wantTotal: 3,
		},
		{
			name: "handles empty results",
			mockResponses: []*kafka.ListClusterOperationsV2Output{
				{
					ClusterOperationInfoList: []kafkatypes.ClusterOperationV2Summary{},
				},
			},
			wantTotal: 0,
		},
		{
			name:      "handles API error",
			mockError: errors.New("operations API error"),
			wantError: "❌ Failed listing operations: operations API error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockClusterScannerMSKClient := &MockClusterScannerMSKClient{
				ListClusterOperationsV2Func: func(ctx context.Context, params *kafka.ListClusterOperationsV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClusterOperationsV2Output, error) {
					if tt.mockError != nil {
						return nil, tt.mockError
					}
					if callCount >= len(tt.mockResponses) {
						t.Fatalf("Unexpected call to ListClusterOperationsV2, call count: %d", callCount)
					}
					response := tt.mockResponses[callCount]
					callCount++
					return response, nil
				},
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, mockClusterScannerMSKClient, nil, false)
			result, err := clusterScanner.scanClusterOperations(context.Background(), aws.String("test-cluster-arn"), 100)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(result))
		})
	}
}

func TestClusterScanner_ScanClusterNodes(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListNodesOutput
		mockError     error
		wantTotal     int
		wantError     string
	}{
		{
			name: "successful nodes scan",
			mockResponses: []*kafka.ListNodesOutput{
				{
					NodeInfoList: []kafkatypes.NodeInfo{
						{
							NodeARN:      aws.String("node-1"),
							InstanceType: aws.String("kafka.m5.large"),
						},
						{
							NodeARN:      aws.String("node-2"),
							InstanceType: aws.String("kafka.m5.large"),
						},
						{
							NodeARN:      aws.String("node-3"),
							InstanceType: aws.String("kafka.m5.large"),
						},
					},
				},
			},
			wantTotal: 3,
		},
		{
			name: "handles pagination with multiple pages",
			mockResponses: []*kafka.ListNodesOutput{
				{
					NodeInfoList: []kafkatypes.NodeInfo{
						{NodeARN: aws.String("node-page-1-1")},
						{NodeARN: aws.String("node-page-1-2")},
					},
					NextToken: aws.String("nodes-next-token"),
				},
				{
					NodeInfoList: []kafkatypes.NodeInfo{
						{NodeARN: aws.String("node-page-2-1")},
					},
				},
			},
			wantTotal: 3,
		},
		{
			name: "handles empty results",
			mockResponses: []*kafka.ListNodesOutput{
				{
					NodeInfoList: []kafkatypes.NodeInfo{},
				},
			},
			wantTotal: 0,
		},
		{
			name:      "handles API error",
			mockError: errors.New("nodes API error"),
			wantError: "❌ Failed listing nodes: nodes API error",
		},
		{
			name:      "handles serverless cluster error gracefully",
			mockError: errors.New("This operation cannot be performed on serverless clusters."),
			wantTotal: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockClusterScannerMSKClient := &MockClusterScannerMSKClient{
				ListNodesFunc: func(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error) {
					if tt.mockError != nil {
						return nil, tt.mockError
					}
					if callCount >= len(tt.mockResponses) {
						t.Fatalf("Unexpected call to ListNodes, call count: %d", callCount)
					}
					response := tt.mockResponses[callCount]
					callCount++
					return response, nil
				},
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, mockClusterScannerMSKClient, nil, false)
			result, err := clusterScanner.scanClusterNodes(context.Background(), aws.String("test-cluster-arn"), 100)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(result))
		})
	}
}

func TestClusterScanner_ScanClusterScramSecrets(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListScramSecretsOutput
		mockError     error
		wantTotal     int
		wantError     string
	}{
		{
			name: "successful SCRAM secrets scan",
			mockResponses: []*kafka.ListScramSecretsOutput{
				{
					SecretArnList: []string{
						"secret-arn-1",
						"secret-arn-2",
					},
				},
			},
			wantTotal: 2,
		},
		{
			name: "handles pagination with multiple pages",
			mockResponses: []*kafka.ListScramSecretsOutput{
				{
					SecretArnList: []string{
						"secret-page-1-1",
					},
					NextToken: aws.String("secrets-next-token"),
				},
				{
					SecretArnList: []string{
						"secret-page-2-1",
						"secret-page-2-2",
						"secret-page-2-3",
					},
				},
			},
			wantTotal: 4,
		},
		{
			name: "handles empty results",
			mockResponses: []*kafka.ListScramSecretsOutput{
				{
					SecretArnList: []string{},
				},
			},
			wantTotal: 0,
		},
		{
			name:      "handles API error",
			mockError: errors.New("SCRAM secrets API error"),
			wantError: "error listing secrets: SCRAM secrets API error",
		},
		{
			name:      "handles serverless cluster error gracefully",
			mockError: errors.New("This operation cannot be performed on serverless clusters."),
			wantTotal: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockClusterScannerMSKClient := &MockClusterScannerMSKClient{
				ListScramSecretsFunc: func(ctx context.Context, params *kafka.ListScramSecretsInput, optFns ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error) {
					if tt.mockError != nil {
						return nil, tt.mockError
					}
					if callCount >= len(tt.mockResponses) {
						t.Fatalf("Unexpected call to ListScramSecrets, call count: %d", callCount)
					}
					response := tt.mockResponses[callCount]
					callCount++
					return response, nil
				},
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, mockClusterScannerMSKClient, nil, false)
			result, err := clusterScanner.scanClusterScramSecrets(context.Background(), aws.String("test-cluster-arn"), 100)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(result))
		})
	}
}

func TestClusterScanner_VpcConnectionsPaginationTokenHandling(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListClientVpcConnectionsOutput
		wantCallCount int
		wantTotal     int
	}{
		{
			name: "verifies NextToken is properly passed between requests",
			mockResponses: []*kafka.ListClientVpcConnectionsOutput{
				{
					ClientVpcConnections: []kafkatypes.ClientVpcConnection{
						{VpcConnectionArn: aws.String("vpc-conn-1")},
					},
					NextToken: aws.String("token-page-1"),
				},
				{
					ClientVpcConnections: []kafkatypes.ClientVpcConnection{
						{VpcConnectionArn: aws.String("vpc-conn-2")},
					},
					NextToken: aws.String("token-page-2"),
				},
				{
					ClientVpcConnections: []kafkatypes.ClientVpcConnection{
						{VpcConnectionArn: aws.String("vpc-conn-3")},
					},
					// No NextToken - end of pagination
				},
			},
			wantCallCount: 3,
			wantTotal:     3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockClusterScannerMSKClient := &MockClusterScannerMSKClient{
				ListClientVpcConnectionsFunc: func(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error) {
					// Verify parameters
					assert.NotNil(t, params.ClusterArn, "ClusterArn should be set")
					assert.NotNil(t, params.MaxResults, "MaxResults should be set")
					assert.Equal(t, int32(100), *params.MaxResults, "MaxResults should be 100")

					// Verify NextToken handling
					if callCount == 0 {
						// First call should have no NextToken
						assert.Nil(t, params.NextToken, "First call should not have NextToken")
					} else if callCount < len(tt.mockResponses)-1 {
						// Middle calls should have NextToken from previous response
						expectedToken := tt.mockResponses[callCount-1].NextToken
						require.NotNil(t, params.NextToken, "NextToken should be set for paginated request")
						assert.Equal(t, *expectedToken, *params.NextToken, "NextToken should match previous response")
					}

					if callCount >= len(tt.mockResponses) {
						t.Fatalf("Unexpected call to ListClientVpcConnections, call count: %d", callCount)
					}

					response := tt.mockResponses[callCount]
					callCount++
					return response, nil
				},
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, mockClusterScannerMSKClient, nil, false)
			result, err := clusterScanner.scanClusterVpcConnections(context.Background(), aws.String("test-cluster-arn"), 100)

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(result), "Total VPC connections should match expected count")
			assert.Equal(t, tt.wantCallCount, callCount, "Should have made the expected number of API calls")

			// Verify all VPC connections from all pages are returned
			expectedArns := []string{"vpc-conn-1", "vpc-conn-2", "vpc-conn-3"}
			actualArns := make([]string, len(result))
			for i, conn := range result {
				actualArns[i] = *conn.VpcConnectionArn
			}
			assert.ElementsMatch(t, expectedArns, actualArns, "All VPC connections from all pages should be returned")
		})
	}
}

func TestClusterScanner_OperationsPaginationTokenHandling(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListClusterOperationsV2Output
		wantCallCount int
		wantTotal     int
	}{
		{
			name: "verifies NextToken is properly passed between requests for operations",
			mockResponses: []*kafka.ListClusterOperationsV2Output{
				{
					ClusterOperationInfoList: []kafkatypes.ClusterOperationV2Summary{
						{OperationArn: aws.String("operation-1")},
					},
					NextToken: aws.String("operations-token-page-1"),
				},
				{
					ClusterOperationInfoList: []kafkatypes.ClusterOperationV2Summary{
						{OperationArn: aws.String("operation-2")},
					},
					NextToken: aws.String("operations-token-page-2"),
				},
				{
					ClusterOperationInfoList: []kafkatypes.ClusterOperationV2Summary{
						{OperationArn: aws.String("operation-3")},
					},
					// No NextToken - end of pagination
				},
			},
			wantCallCount: 3,
			wantTotal:     3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockClusterScannerMSKClient := &MockClusterScannerMSKClient{
				ListClusterOperationsV2Func: func(ctx context.Context, params *kafka.ListClusterOperationsV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClusterOperationsV2Output, error) {
					// Verify parameters
					assert.NotNil(t, params.ClusterArn, "ClusterArn should be set")
					assert.NotNil(t, params.MaxResults, "MaxResults should be set")
					assert.Equal(t, int32(100), *params.MaxResults, "MaxResults should be 100")

					// Verify NextToken handling
					if callCount == 0 {
						// First call should have no NextToken
						assert.Nil(t, params.NextToken, "First call should not have NextToken")
					} else if callCount < len(tt.mockResponses)-1 {
						// Middle calls should have NextToken from previous response
						expectedToken := tt.mockResponses[callCount-1].NextToken
						require.NotNil(t, params.NextToken, "NextToken should be set for paginated request")
						assert.Equal(t, *expectedToken, *params.NextToken, "NextToken should match previous response")
					}

					if callCount >= len(tt.mockResponses) {
						t.Fatalf("Unexpected call to ListClusterOperationsV2, call count: %d", callCount)
					}

					response := tt.mockResponses[callCount]
					callCount++
					return response, nil
				},
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, mockClusterScannerMSKClient, nil, false)
			result, err := clusterScanner.scanClusterOperations(context.Background(), aws.String("test-cluster-arn"), 100)

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(result), "Total operations should match expected count")
			assert.Equal(t, tt.wantCallCount, callCount, "Should have made the expected number of API calls")

			// Verify all operations from all pages are returned
			expectedArns := []string{"operation-1", "operation-2", "operation-3"}
			actualArns := make([]string, len(result))
			for i, op := range result {
				actualArns[i] = *op.OperationArn
			}
			assert.ElementsMatch(t, expectedArns, actualArns, "All operations from all pages should be returned")
		})
	}
}

func TestClusterScanner_NodesPaginationTokenHandling(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListNodesOutput
		wantCallCount int
		wantTotal     int
	}{
		{
			name: "verifies NextToken is properly passed between requests for nodes",
			mockResponses: []*kafka.ListNodesOutput{
				{
					NodeInfoList: []kafkatypes.NodeInfo{
						{NodeARN: aws.String("node-1")},
					},
					NextToken: aws.String("nodes-token-page-1"),
				},
				{
					NodeInfoList: []kafkatypes.NodeInfo{
						{NodeARN: aws.String("node-2")},
						{NodeARN: aws.String("node-3")},
					},
					NextToken: aws.String("nodes-token-page-2"),
				},
				{
					NodeInfoList: []kafkatypes.NodeInfo{
						{NodeARN: aws.String("node-4")},
					},
					// No NextToken - end of pagination
				},
			},
			wantCallCount: 3,
			wantTotal:     4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockMSK := &MockClusterScannerMSKClient{
				ListNodesFunc: func(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error) {
					// Verify parameters
					assert.NotNil(t, params.ClusterArn, "ClusterArn should be set")
					assert.NotNil(t, params.MaxResults, "MaxResults should be set")
					assert.Equal(t, int32(100), *params.MaxResults, "MaxResults should be 100")

					// Verify NextToken handling
					if callCount == 0 {
						// First call should have no NextToken
						assert.Nil(t, params.NextToken, "First call should not have NextToken")
					} else if callCount < len(tt.mockResponses)-1 {
						// Middle calls should have NextToken from previous response
						expectedToken := tt.mockResponses[callCount-1].NextToken
						require.NotNil(t, params.NextToken, "NextToken should be set for paginated request")
						assert.Equal(t, *expectedToken, *params.NextToken, "NextToken should match previous response")
					}

					if callCount >= len(tt.mockResponses) {
						t.Fatalf("Unexpected call to ListNodes, call count: %d", callCount)
					}

					response := tt.mockResponses[callCount]
					callCount++
					return response, nil
				},
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, mockMSK, nil, false)
			result, err := clusterScanner.scanClusterNodes(context.Background(), aws.String("test-cluster-arn"), 100)

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(result), "Total nodes should match expected count")
			assert.Equal(t, tt.wantCallCount, callCount, "Should have made the expected number of API calls")

			// Verify all nodes from all pages are returned
			expectedArns := []string{"node-1", "node-2", "node-3", "node-4"}
			actualArns := make([]string, len(result))
			for i, node := range result {
				actualArns[i] = *node.NodeARN
			}
			assert.ElementsMatch(t, expectedArns, actualArns, "All nodes from all pages should be returned")
		})
	}
}

func TestClusterScanner_ScramSecretsPaginationTokenHandling(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListScramSecretsOutput
		wantCallCount int
		wantTotal     int
	}{
		{
			name: "verifies NextToken is properly passed between requests for SCRAM secrets",
			mockResponses: []*kafka.ListScramSecretsOutput{
				{
					SecretArnList: []string{"secret-1", "secret-2"},
					NextToken:     aws.String("secrets-token-page-1"),
				},
				{
					SecretArnList: []string{"secret-3"},
					NextToken:     aws.String("secrets-token-page-2"),
				},
				{
					SecretArnList: []string{"secret-4", "secret-5", "secret-6"},
					// No NextToken - end of pagination
				},
			},
			wantCallCount: 3,
			wantTotal:     6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockClusterScannerMSKClient := &MockClusterScannerMSKClient{
				ListScramSecretsFunc: func(ctx context.Context, params *kafka.ListScramSecretsInput, optFns ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error) {
					// Verify parameters
					assert.NotNil(t, params.ClusterArn, "ClusterArn should be set")
					assert.NotNil(t, params.MaxResults, "MaxResults should be set")
					assert.Equal(t, int32(100), *params.MaxResults, "MaxResults should be 100")

					// Verify NextToken handling
					if callCount == 0 {
						// First call should have no NextToken
						assert.Nil(t, params.NextToken, "First call should not have NextToken")
					} else if callCount < len(tt.mockResponses)-1 {
						// Middle calls should have NextToken from previous response
						expectedToken := tt.mockResponses[callCount-1].NextToken
						require.NotNil(t, params.NextToken, "NextToken should be set for paginated request")
						assert.Equal(t, *expectedToken, *params.NextToken, "NextToken should match previous response")
					}

					if callCount >= len(tt.mockResponses) {
						t.Fatalf("Unexpected call to ListScramSecrets, call count: %d", callCount)
					}

					response := tt.mockResponses[callCount]
					callCount++
					return response, nil
				},
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, mockClusterScannerMSKClient, nil, false)
			result, err := clusterScanner.scanClusterScramSecrets(context.Background(), aws.String("test-cluster-arn"), 100)

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(result), "Total SCRAM secrets should match expected count")
			assert.Equal(t, tt.wantCallCount, callCount, "Should have made the expected number of API calls")

			// Verify all secrets from all pages are returned
			expectedSecrets := []string{"secret-1", "secret-2", "secret-3", "secret-4", "secret-5", "secret-6"}
			assert.ElementsMatch(t, expectedSecrets, result, "All SCRAM secrets from all pages should be returned")
		})
	}
}

func TestClusterScanner_DescribeKafkaCluster(t *testing.T) {
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
				ListAclsFunc: func() ([]sarama.ResourceAcls, error) {
					return []sarama.ResourceAcls{}, nil
				},
				CloseFunc: func() error { return nil },
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, nil, nil, false)
			result, err := clusterScanner.describeKafkaCluster(mockAdmin)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				assert.Nil(t, result)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)

			// Verify cluster metadata
			assert.Equal(t, tt.expectedBrokerCount, len(result.Brokers), "Broker count should match")
			assert.Equal(t, tt.expectedControllerID, result.ControllerID, "Controller ID should match")
			assert.Equal(t, tt.expectedClusterID, result.ClusterID, "Cluster ID should match")

			// Verify brokers slice is properly initialized
			assert.NotNil(t, result.Brokers, "Brokers slice should not be nil")
		})
	}
}

func TestClusterScanner_DescribeKafkaCluster_Integration(t *testing.T) {
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
		{
			name:              "integration test - describe cluster error propagates to scanKafkaResources",
			mockDescribeError: errors.New("admin client error"),
			wantError:         "❌ Failed to describe kafka cluster: admin client error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAdmin := &mocks.MockKafkaAdmin{
				ListTopicsFunc: func() (map[string]sarama.TopicDetail, error) {
					return map[string]sarama.TopicDetail{"test-topic": {}}, nil
				},
				GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
					return tt.mockClusterMetadata, tt.mockDescribeError
				},
				ListAclsFunc: func() ([]sarama.ResourceAcls, error) {
					return []sarama.ResourceAcls{}, nil
				},
				CloseFunc: func() error { return nil },
			}

			adminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
				return mockAdmin, nil
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, nil, adminFactory, false)

			// Create ClusterInformation with bootstrap brokers
			clusterInfo := &types.ClusterInformation{
				Timestamp: time.Now(),
				Region:    defaultRegion,

				BootstrapBrokers: kafka.GetBootstrapBrokersOutput{
					BootstrapBrokerStringSaslIam: aws.String("broker1:9098,broker2:9098"),
				},
			}

			err := clusterScanner.scanKafkaResources(clusterInfo)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantClusterID, clusterInfo.ClusterID, "ClusterID should be properly stored")
		})
	}
}

func TestClusterScanner_ParseBrokerAddresses_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		brokers     kafka.GetBootstrapBrokersOutput
		wantBrokers []string
		wantError   string
	}{
		{
			name: "handles single broker with spaces in public brokers",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslIam: aws.String("broker1:9098"),
			},
			wantBrokers: []string{"broker1:9098"}, // Split preserves spaces
		},
		{
			name: "handles single broker with spaces in private brokers (fallback)",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("broker1:9098"),
			},
			wantBrokers: []string{"broker1:9098"}, // Split preserves spaces
		},
		{
			name: "handles multiple brokers with trailing comma in public brokers",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslIam: aws.String("broker1:9098,broker2:9098,"),
			},
			wantBrokers: []string{"broker1:9098", "broker2:9098"},
		},
		{
			name: "handles multiple brokers with trailing comma in private brokers (fallback)",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("broker1:9098,broker2:9098,"),
			},
			wantBrokers: []string{"broker1:9098", "broker2:9098"},
		},
		{
			name: "handles empty public broker list but has private brokers",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslIam: aws.String(""),
				BootstrapBrokerStringSaslIam:       aws.String("private-broker1:9098"),
			},
			wantBrokers: []string{"private-broker1:9098"},
		},
		{
			name: "handles nil public broker field but has private brokers",
			brokers: kafka.GetBootstrapBrokersOutput{
				// BootstrapBrokerStringPublicSaslIam is nil
				BootstrapBrokerStringSaslIam: aws.String("private-broker1:9098"),
			},
			wantBrokers: []string{"private-broker1:9098"},
		},
		{
			name:    "returns error when both public and private broker fields are nil",
			brokers: kafka.GetBootstrapBrokersOutput{
				// Both BootstrapBrokerStringPublicSaslIam and BootstrapBrokerStringSaslIam are nil
			},
			wantError: "❌ No SASL/IAM brokers found in the cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusterScanner := newTestClusterScanner("test-cluster", defaultRegion, nil, nil, false)
			brokers, err := clusterScanner.parseBrokerAddresses(tt.brokers)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantBrokers, brokers)
		})
	}
}

func TestClusterScanner_GetClusterPolicy_ErrorHandling(t *testing.T) {
	tests := []struct {
		name          string
		mockError     error
		wantError     bool
		wantNilResult bool
	}{
		{
			name:          "correctly returns non-NotFoundException errors after fix",
			mockError:     errors.New("access denied"),
			wantError:     true, // FIXED: Now correctly returns the error
			wantNilResult: true, // Should return nil result when there's an error
		},
		{
			name: "handles NotFoundException correctly",
			mockError: &kafkatypes.NotFoundException{
				Message: aws.String("Policy not found"),
			},
			wantError:     false, // Should return empty policy without error
			wantNilResult: false, // Should return empty but valid policy object
		},
		{
			name:          "handles nil error successfully",
			mockError:     nil,
			wantError:     false,
			wantNilResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockMSKClient := &MockClusterScannerMSKClient{
				GetClusterPolicyFunc: func(ctx context.Context, params *kafka.GetClusterPolicyInput, optFns ...func(*kafka.Options)) (*kafka.GetClusterPolicyOutput, error) {
					if tt.mockError != nil {
						return nil, tt.mockError
					}
					return &kafka.GetClusterPolicyOutput{
						CurrentVersion: aws.String("v1"),
						Policy:         aws.String("test-policy"),
					}, nil
				},
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, mockMSKClient, nil, false)
			result, err := clusterScanner.getClusterPolicy(context.Background(), aws.String("test-cluster-arn"))

			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.wantNilResult {
				assert.Nil(t, result, "Expected nil result when there's an error")
			} else {
				assert.NotNil(t, result, "Expected non-nil result")
			}
		})
	}
}

func TestClusterScanner_AdminClose_Failures(t *testing.T) {
	tests := []struct {
		name           string
		adminCloseErr  error
		expectLogError bool // We'd expect this to be logged but not fail the operation
	}{
		{
			name:           "handles admin close failure gracefully",
			adminCloseErr:  errors.New("failed to close admin client"),
			expectLogError: true,
		},
		{
			name:           "handles successful admin close",
			adminCloseErr:  nil,
			expectLogError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAdmin := &mocks.MockKafkaAdmin{
				ListTopicsFunc: func() (map[string]sarama.TopicDetail, error) {
					return map[string]sarama.TopicDetail{"topic1": {}}, nil
				},
				GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
					return &client.ClusterKafkaMetadata{
						Brokers:      []*sarama.Broker{},
						ControllerID: 1,
						ClusterID:    "test-cluster-id",
					}, nil
				},
				ListAclsFunc: func() ([]sarama.ResourceAcls, error) {
					return []sarama.ResourceAcls{}, nil
				},
				CloseFunc: func() error {
					return tt.adminCloseErr
				},
			}

			adminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
				return mockAdmin, nil
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, nil, adminFactory, false)
			clusterInfo := &types.ClusterInformation{
				Timestamp: time.Now(),
				Region:    defaultRegion,
				BootstrapBrokers: kafka.GetBootstrapBrokersOutput{
					BootstrapBrokerStringSaslIam: aws.String("broker1:9098"),
				},
			}

			// The operation should succeed even if admin.Close() fails
			err := clusterScanner.scanKafkaResources(clusterInfo)
			assert.NoError(t, err, "scanKafkaResources should succeed even if admin.Close() fails")
			assert.Equal(t, []string{"topic1"}, clusterInfo.Topics)
		})
	}
}

func TestClusterScanner_GetClusterPolicy_FixIntegration(t *testing.T) {
	// This test demonstrates how the bug fix improves error handling
	// in the broader context of scanning AWS resources
	tests := []struct {
		name              string
		policyError       error
		expectScanFailure bool
		expectedErrorMsg  string
	}{
		{
			name:              "scan continues successfully when policy not found (expected behavior)",
			policyError:       &kafkatypes.NotFoundException{Message: aws.String("Policy not found")},
			expectScanFailure: false,
		},
		{
			name:              "scan fails properly when policy access is denied (fixed behavior)",
			policyError:       errors.New("access denied - insufficient permissions"),
			expectScanFailure: true,
			expectedErrorMsg:  "access denied - insufficient permissions",
		},
		{
			name:              "scan fails properly when policy service is unavailable (fixed behavior)",
			policyError:       errors.New("service temporarily unavailable"),
			expectScanFailure: true,
			expectedErrorMsg:  "service temporarily unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockMSKClient := &MockClusterScannerMSKClient{
				GetBootstrapBrokersFunc: func(ctx context.Context, params *kafka.GetBootstrapBrokersInput, optFns ...func(*kafka.Options)) (*kafka.GetBootstrapBrokersOutput, error) {
					return &kafka.GetBootstrapBrokersOutput{
						BootstrapBrokerStringSaslIam: aws.String("broker1:9098"),
					}, nil
				},
				DescribeClusterV2Func: func(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error) {
					return &kafka.DescribeClusterV2Output{
						ClusterInfo: &kafkatypes.Cluster{
							ClusterName: aws.String("test-cluster"),
							ClusterArn:  aws.String("test-cluster-arn"),
							ClusterType: kafkatypes.ClusterTypeProvisioned,
							Provisioned: &kafkatypes.Provisioned{
								ClientAuthentication: &kafkatypes.ClientAuthentication{
									Sasl: &kafkatypes.Sasl{
										Iam: &kafkatypes.Iam{
											Enabled: aws.Bool(true),
										},
									},
								},
								EncryptionInfo: &kafkatypes.EncryptionInfo{
									EncryptionInTransit: &kafkatypes.EncryptionInTransit{
										ClientBroker: kafkatypes.ClientBrokerTls,
									},
								},
							},
						},
					}, nil
				},
				ListClientVpcConnectionsFunc: func(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error) {
					return &kafka.ListClientVpcConnectionsOutput{}, nil
				},
				ListClusterOperationsV2Func: func(ctx context.Context, params *kafka.ListClusterOperationsV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClusterOperationsV2Output, error) {
					return &kafka.ListClusterOperationsV2Output{}, nil
				},
				ListNodesFunc: func(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error) {
					return &kafka.ListNodesOutput{}, nil
				},
				ListScramSecretsFunc: func(ctx context.Context, params *kafka.ListScramSecretsInput, optFns ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error) {
					return &kafka.ListScramSecretsOutput{}, nil
				},
				GetClusterPolicyFunc: func(ctx context.Context, params *kafka.GetClusterPolicyInput, optFns ...func(*kafka.Options)) (*kafka.GetClusterPolicyOutput, error) {
					if tt.policyError != nil {
						return nil, tt.policyError
					}
					return &kafka.GetClusterPolicyOutput{
						CurrentVersion: aws.String("v1"),
						Policy:         aws.String("test-policy"),
					}, nil
				},
				GetCompatibleKafkaVersionsFunc: func(ctx context.Context, params *kafka.GetCompatibleKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
					return &kafka.GetCompatibleKafkaVersionsOutput{}, nil
				},
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, mockMSKClient, nil, false)
			clusterInfo := &types.ClusterInformation{
				Timestamp: time.Now(),
				Region:    defaultRegion,
			}

			err := clusterScanner.scanAWSResources(context.Background(), clusterInfo)

			if tt.expectScanFailure {
				require.Error(t, err, "Expected scanAWSResources to fail due to policy error")
				assert.Contains(t, err.Error(), tt.expectedErrorMsg, "Error should contain the original policy error message")
			} else {
				require.NoError(t, err, "Expected scanAWSResources to succeed despite policy not found")
				// For NotFoundException, the scan should continue and complete successfully
				assert.Equal(t, "test-cluster", *clusterInfo.Cluster.ClusterName)
			}
		})
	}
}

func TestClusterScanner_SkipKafka(t *testing.T) {
	tests := []struct {
		name                 string
		skipKafka            bool
		expectTopics         bool
		expectClusterID      bool
		expectKafkaAdminCall bool
	}{
		{
			name:                 "skipKafka=false should scan Kafka resources",
			skipKafka:            false,
			expectTopics:         true,
			expectClusterID:      true,
			expectKafkaAdminCall: true,
		},
		{
			name:                 "skipKafka=true should skip Kafka resources",
			skipKafka:            true,
			expectTopics:         false,
			expectClusterID:      false,
			expectKafkaAdminCall: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adminFactoryCalled := false
			adminCreated := false

			mockClusterScannerMSKClient := &MockClusterScannerMSKClient{
				GetBootstrapBrokersFunc: func(ctx context.Context, params *kafka.GetBootstrapBrokersInput, optFns ...func(*kafka.Options)) (*kafka.GetBootstrapBrokersOutput, error) {
					return &kafka.GetBootstrapBrokersOutput{
						BootstrapBrokerStringSaslIam: aws.String("broker1:9098,broker2:9098"),
					}, nil
				},
				DescribeClusterV2Func: func(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error) {
					return &kafka.DescribeClusterV2Output{
						ClusterInfo: &kafkatypes.Cluster{
							ClusterName: aws.String("test-cluster"),
							ClusterArn:  aws.String("test-cluster-arn"),
							ClusterType: kafkatypes.ClusterTypeProvisioned,
							Provisioned: &kafkatypes.Provisioned{
								ClientAuthentication: &kafkatypes.ClientAuthentication{
									Sasl: &kafkatypes.Sasl{
										Iam: &kafkatypes.Iam{
											Enabled: aws.Bool(true),
										},
									},
								},
								EncryptionInfo: &kafkatypes.EncryptionInfo{
									EncryptionInTransit: &kafkatypes.EncryptionInTransit{
										ClientBroker: kafkatypes.ClientBrokerTls,
									},
								},
							},
						},
					}, nil
				},
				ListClientVpcConnectionsFunc: func(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error) {
					return &kafka.ListClientVpcConnectionsOutput{
						ClientVpcConnections: []kafkatypes.ClientVpcConnection{
							{VpcConnectionArn: aws.String("vpc-conn-1")},
						},
					}, nil
				},
				ListClusterOperationsV2Func: func(ctx context.Context, params *kafka.ListClusterOperationsV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClusterOperationsV2Output, error) {
					return &kafka.ListClusterOperationsV2Output{
						ClusterOperationInfoList: []kafkatypes.ClusterOperationV2Summary{
							{OperationArn: aws.String("operation-1")},
						},
					}, nil
				},
				ListNodesFunc: func(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error) {
					return &kafka.ListNodesOutput{
						NodeInfoList: []kafkatypes.NodeInfo{
							{NodeARN: aws.String("node-1")},
						},
					}, nil
				},
				ListScramSecretsFunc: func(ctx context.Context, params *kafka.ListScramSecretsInput, optFns ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error) {
					return &kafka.ListScramSecretsOutput{
						SecretArnList: []string{"secret-1"},
					}, nil
				},
				GetClusterPolicyFunc: func(ctx context.Context, params *kafka.GetClusterPolicyInput, optFns ...func(*kafka.Options)) (*kafka.GetClusterPolicyOutput, error) {
					return &kafka.GetClusterPolicyOutput{
						CurrentVersion: aws.String("v1"),
						Policy:         aws.String("test-policy"),
					}, nil
				},
				GetCompatibleKafkaVersionsFunc: func(ctx context.Context, params *kafka.GetCompatibleKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
					return &kafka.GetCompatibleKafkaVersionsOutput{
						CompatibleKafkaVersions: []kafkatypes.CompatibleKafkaVersion{
							{
								SourceVersion:  aws.String("2.8.0"),
								TargetVersions: []string{"2.8.1", "2.8.2"},
							},
						},
					}, nil
				},
			}

			mockAdmin := &mocks.MockKafkaAdmin{
				ListTopicsFunc: func() (map[string]sarama.TopicDetail, error) {
					return map[string]sarama.TopicDetail{
						"test-topic-1": {},
						"test-topic-2": {},
					}, nil
				},
				GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
					return &client.ClusterKafkaMetadata{
						ClusterID: "test-cluster-id",
					}, nil
				},
				ListAclsFunc: func() ([]sarama.ResourceAcls, error) {
					return []sarama.ResourceAcls{}, nil
				},
				CloseFunc: func() error { return nil },
			}

			adminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
				adminFactoryCalled = true
				adminCreated = true
				return mockAdmin, nil
			}

			clusterScanner := newTestClusterScanner("test-cluster-arn", defaultRegion, mockClusterScannerMSKClient, adminFactory, tt.skipKafka)
			result, err := clusterScanner.scanCluster(context.Background())

			require.NoError(t, err)
			require.NotNil(t, result)

			// Verify AWS resources are always scanned regardless of skipKafka
			assert.Equal(t, "test-cluster", *result.Cluster.ClusterName)
			assert.Equal(t, "test-cluster-arn", *result.Cluster.ClusterArn)
			assert.Len(t, result.ClientVpcConnections, 1)
			assert.Equal(t, "vpc-conn-1", *result.ClientVpcConnections[0].VpcConnectionArn)
			assert.Len(t, result.ClusterOperations, 1)
			assert.Equal(t, "operation-1", *result.ClusterOperations[0].OperationArn)
			assert.Len(t, result.Nodes, 1)
			assert.Equal(t, "node-1", *result.Nodes[0].NodeARN)
			assert.Len(t, result.ScramSecrets, 1)
			assert.Equal(t, "secret-1", result.ScramSecrets[0])
			assert.Equal(t, "v1", *result.Policy.CurrentVersion)
			assert.Equal(t, "test-policy", *result.Policy.Policy)
			assert.Len(t, result.CompatibleVersions.CompatibleKafkaVersions, 1)

			// Verify Kafka admin factory call behavior
			if tt.expectKafkaAdminCall {
				assert.True(t, adminFactoryCalled, "Admin factory should be called when skipKafka=false")
				assert.True(t, adminCreated, "Admin client should be created when skipKafka=false")
			} else {
				assert.False(t, adminFactoryCalled, "Admin factory should not be called when skipKafka=true")
				assert.False(t, adminCreated, "Admin client should not be created when skipKafka=true")
			}

			// Verify Kafka-level resources behavior
			if tt.expectTopics {
				assert.NotNil(t, result.Topics, "Topics should be populated when skipKafka=false")
				assert.Len(t, result.Topics, 2, "Should have 2 topics when skipKafka=false")
				assert.ElementsMatch(t, []string{"test-topic-1", "test-topic-2"}, result.Topics)
			} else {
				assert.Nil(t, result.Topics, "Topics should be nil when skipKafka=true")
			}

			if tt.expectClusterID {
				assert.Equal(t, "test-cluster-id", result.ClusterID, "ClusterID should be populated when skipKafka=false")
			} else {
				assert.Empty(t, result.ClusterID, "ClusterID should be empty when skipKafka=true")
			}

			// Verify basic fields are always set
			assert.Equal(t, defaultRegion, result.Region)
			assert.NotZero(t, result.Timestamp)
		})
	}
}

func TestClusterScanner_ParseBrokerAddresses_Unauthenticated(t *testing.T) {
	tests := []struct {
		name        string
		brokers     kafka.GetBootstrapBrokersOutput
		wantBrokers []string
		wantError   string
	}{
		{
			name: "returns TLS brokers when available (preferred)",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringTls: aws.String("tls-broker1:9094,tls-broker2:9094"),
				BootstrapBrokerString:    aws.String("plaintext-broker1:9092,plaintext-broker2:9092"),
			},
			wantBrokers: []string{"tls-broker1:9094", "tls-broker2:9094"},
		},
		{
			name: "falls back to plaintext brokers when TLS not available",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerString: aws.String("plaintext-broker1:9092,plaintext-broker2:9092"),
			},
			wantBrokers: []string{"plaintext-broker1:9092", "plaintext-broker2:9092"},
		},
		{
			name: "returns TLS brokers even when plaintext are also available",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringTls: aws.String("tls-broker1:9094"),
				BootstrapBrokerString:    aws.String("plaintext-broker1:9092,plaintext-broker2:9092"),
			},
			wantBrokers: []string{"tls-broker1:9094"},
		},
		{
			name: "handles single TLS broker",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringTls: aws.String("tls-broker1:9094"),
			},
			wantBrokers: []string{"tls-broker1:9094"},
		},
		{
			name: "handles single plaintext broker (fallback)",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerString: aws.String("plaintext-broker1:9092"),
			},
			wantBrokers: []string{"plaintext-broker1:9092"},
		},
		{
			name: "handles brokers with spaces",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringTls: aws.String(" tls-broker1:9094 , tls-broker2:9094 "),
			},
			wantBrokers: []string{"tls-broker1:9094", "tls-broker2:9094"},
		},
		{
			name: "handles plaintext brokers with spaces (fallback)",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerString: aws.String(" plaintext-broker1:9092 , plaintext-broker2:9092 "),
			},
			wantBrokers: []string{"plaintext-broker1:9092", "plaintext-broker2:9092"},
		},
		{
			name: "returns error when no unauthenticated brokers available",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("broker1:9098"),
			},
			wantError: "❌ No Unauthenticated brokers found in the cluster",
		},
		{
			name: "returns error when both TLS and plaintext broker types are empty strings",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringTls: aws.String(""),
				BootstrapBrokerString:    aws.String(""),
			},
			wantError: "❌ No Unauthenticated brokers found in the cluster",
		},
		{
			name: "returns error when TLS is empty string and plaintext is nil",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringTls: aws.String(""),
				BootstrapBrokerString:    nil,
			},
			wantError: "❌ No Unauthenticated brokers found in the cluster",
		},
		{
			name: "returns error when TLS is nil and plaintext is empty string",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringTls: nil,
				BootstrapBrokerString:    aws.String(""),
			},
			wantError: "❌ No Unauthenticated brokers found in the cluster",
		},
		{
			name: "returns error when both TLS and plaintext broker types are nil",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringTls: nil,
				BootstrapBrokerString:    nil,
			},
			wantError: "❌ No Unauthenticated brokers found in the cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a cluster scanner with AuthTypeUnauthenticated
			discoverer := NewClusterScanner(
				nil, // msk client not needed for this test
				nil, // admin factory not needed for this test
				ClusterScannerOpts{
					Region:     defaultRegion,
					ClusterArn: "test-cluster",
					SkipKafka:  false,
					AuthType:   types.AuthTypeUnauthenticated,
				},
			)

			brokers, err := discoverer.parseBrokerAddresses(tt.brokers)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantBrokers, brokers)
		})
	}
}

func TestClusterScanner_ParseBrokerAddresses_SASLSCRAM(t *testing.T) {
	tests := []struct {
		name        string
		brokers     kafka.GetBootstrapBrokersOutput
		wantBrokers []string
		wantError   string
	}{
		{
			name: "returns Public SASL/SCRAM brokers when available (preferred)",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslScram: aws.String("public-scram-broker1:9096,public-scram-broker2:9096"),
				BootstrapBrokerStringSaslScram:       aws.String("private-scram-broker1:9096,private-scram-broker2:9096"),
			},
			wantBrokers: []string{"public-scram-broker1:9096", "public-scram-broker2:9096"},
		},
		{
			name: "falls back to private SASL/SCRAM brokers when public not available",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslScram: aws.String("private-scram-broker1:9096,private-scram-broker2:9096"),
			},
			wantBrokers: []string{"private-scram-broker1:9096", "private-scram-broker2:9096"},
		},
		{
			name: "returns public brokers even when private are also available",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslScram: aws.String("public-scram-broker1:9096"),
				BootstrapBrokerStringSaslScram:       aws.String("private-scram-broker1:9096,private-scram-broker2:9096"),
			},
			wantBrokers: []string{"public-scram-broker1:9096"},
		},
		{
			name: "handles single public SASL/SCRAM broker",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslScram: aws.String("public-scram-broker1:9096"),
			},
			wantBrokers: []string{"public-scram-broker1:9096"},
		},
		{
			name: "handles single private SASL/SCRAM broker (fallback)",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslScram: aws.String("private-scram-broker1:9096"),
			},
			wantBrokers: []string{"private-scram-broker1:9096"},
		},
		{
			name: "handles public brokers with spaces",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslScram: aws.String(" public-scram-broker1:9096 , public-scram-broker2:9096 "),
			},
			wantBrokers: []string{"public-scram-broker1:9096", "public-scram-broker2:9096"},
		},
		{
			name: "handles private brokers with spaces (fallback)",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslScram: aws.String(" private-scram-broker1:9096 , private-scram-broker2:9096 "),
			},
			wantBrokers: []string{"private-scram-broker1:9096", "private-scram-broker2:9096"},
		},
		{
			name: "handles empty public broker list but has private brokers",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslScram: aws.String(""),
				BootstrapBrokerStringSaslScram:       aws.String("private-scram-broker1:9096"),
			},
			wantBrokers: []string{"private-scram-broker1:9096"},
		},
		{
			name: "handles nil public broker field but has private brokers",
			brokers: kafka.GetBootstrapBrokersOutput{
				// BootstrapBrokerStringPublicSaslScram is nil
				BootstrapBrokerStringSaslScram: aws.String("private-scram-broker1:9096"),
			},
			wantBrokers: []string{"private-scram-broker1:9096"},
		},
		{
			name: "returns error when no SASL/SCRAM brokers available",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("broker1:9098"),
			},
			wantError: "❌ No SASL/SCRAM brokers found in the cluster",
		},
		{
			name: "returns error when both public and private broker types are empty strings",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslScram: aws.String(""),
				BootstrapBrokerStringSaslScram:       aws.String(""),
			},
			wantError: "❌ No SASL/SCRAM brokers found in the cluster",
		},
		{
			name: "returns error when public is empty string and private is nil",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslScram: aws.String(""),
				BootstrapBrokerStringSaslScram:       nil,
			},
			wantError: "❌ No SASL/SCRAM brokers found in the cluster",
		},
		{
			name: "returns error when public is nil and private is empty string",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslScram: nil,
				BootstrapBrokerStringSaslScram:       aws.String(""),
			},
			wantError: "❌ No SASL/SCRAM brokers found in the cluster",
		},
		{
			name: "returns error when both public and private broker types are nil",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslScram: nil,
				BootstrapBrokerStringSaslScram:       nil,
			},
			wantError: "❌ No SASL/SCRAM brokers found in the cluster",
		},
		{
			name: "handles multiple brokers with trailing comma in public brokers",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslScram: aws.String("public-scram-broker1:9096,public-scram-broker2:9096,"),
			},
			wantBrokers: []string{"public-scram-broker1:9096", "public-scram-broker2:9096"},
		},
		{
			name: "handles multiple brokers with trailing comma in private brokers (fallback)",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslScram: aws.String("private-scram-broker1:9096,private-scram-broker2:9096,"),
			},
			wantBrokers: []string{"private-scram-broker1:9096", "private-scram-broker2:9096"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a cluster scanner with AuthTypeSASLSCRAM
			discoverer := NewClusterScanner(
				nil, // msk client not needed for this test
				nil, // admin factory not needed for this test
				ClusterScannerOpts{
					Region:     defaultRegion,
					ClusterArn: "test-cluster",
					SkipKafka:  false,
					AuthType:   types.AuthTypeSASLSCRAM,
				})

			brokers, err := discoverer.parseBrokerAddresses(tt.brokers)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantBrokers, brokers)
		})
	}
}

func TestClusterScanner_ParseBrokerAddresses_TLS(t *testing.T) {
	tests := []struct {
		name        string
		brokers     kafka.GetBootstrapBrokersOutput
		wantBrokers []string
		wantError   string
	}{
		{
			name: "returns Public TLS brokers when available (preferred)",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicTls: aws.String("public-tls-broker1:9094,public-tls-broker2:9094"),
				BootstrapBrokerStringTls:       aws.String("private-tls-broker1:9094,private-tls-broker2:9094"),
			},
			wantBrokers: []string{"public-tls-broker1:9094", "public-tls-broker2:9094"},
		},
		{
			name: "falls back to private TLS brokers when public not available",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringTls: aws.String("private-tls-broker1:9094,private-tls-broker2:9094"),
			},
			wantBrokers: []string{"private-tls-broker1:9094", "private-tls-broker2:9094"},
		},
		{
			name: "returns public TLS brokers even when private are also available",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicTls: aws.String("public-tls-broker1:9094"),
				BootstrapBrokerStringTls:       aws.String("private-tls-broker1:9094,private-tls-broker2:9094"),
			},
			wantBrokers: []string{"public-tls-broker1:9094"},
		},
		{
			name: "handles single public TLS broker",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicTls: aws.String("public-tls-broker1:9094"),
			},
			wantBrokers: []string{"public-tls-broker1:9094"},
		},
		{
			name: "handles single private TLS broker (fallback)",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringTls: aws.String("private-tls-broker1:9094"),
			},
			wantBrokers: []string{"private-tls-broker1:9094"},
		},
		{
			name: "handles public TLS brokers with spaces",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicTls: aws.String(" public-tls-broker1:9094 , public-tls-broker2:9094 "),
			},
			wantBrokers: []string{"public-tls-broker1:9094", "public-tls-broker2:9094"},
		},
		{
			name: "handles private TLS brokers with spaces (fallback)",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringTls: aws.String(" private-tls-broker1:9094 , private-tls-broker2:9094 "),
			},
			wantBrokers: []string{"private-tls-broker1:9094", "private-tls-broker2:9094"},
		},
		{
			name: "handles empty public TLS broker list but has private brokers",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicTls: aws.String(""),
				BootstrapBrokerStringTls:       aws.String("private-tls-broker1:9094"),
			},
			wantBrokers: []string{"private-tls-broker1:9094"},
		},
		{
			name: "handles nil public TLS broker field but has private brokers",
			brokers: kafka.GetBootstrapBrokersOutput{
				// BootstrapBrokerStringPublicTls is nil
				BootstrapBrokerStringTls: aws.String("private-tls-broker1:9094"),
			},
			wantBrokers: []string{"private-tls-broker1:9094"},
		},
		{
			name: "returns error when no TLS brokers available",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("broker1:9098"),
			},
			wantError: "❌ No TLS brokers found in the cluster",
		},
		{
			name: "returns error when both public and private TLS broker types are empty strings",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicTls: aws.String(""),
				BootstrapBrokerStringTls:       aws.String(""),
			},
			wantError: "❌ No TLS brokers found in the cluster",
		},
		{
			name: "returns error when public TLS is empty string and private TLS is nil",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicTls: aws.String(""),
				BootstrapBrokerStringTls:       nil,
			},
			wantError: "❌ No TLS brokers found in the cluster",
		},
		{
			name: "returns error when public TLS is nil and private TLS is empty string",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicTls: nil,
				BootstrapBrokerStringTls:       aws.String(""),
			},
			wantError: "❌ No TLS brokers found in the cluster",
		},
		{
			name: "returns error when both public and private TLS broker types are nil",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicTls: nil,
				BootstrapBrokerStringTls:       nil,
			},
			wantError: "❌ No TLS brokers found in the cluster",
		},
		{
			name: "handles multiple brokers with trailing comma in public TLS brokers",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicTls: aws.String("public-tls-broker1:9094,public-tls-broker2:9094,"),
			},
			wantBrokers: []string{"public-tls-broker1:9094", "public-tls-broker2:9094"},
		},
		{
			name: "handles multiple brokers with trailing comma in private TLS brokers (fallback)",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringTls: aws.String("private-tls-broker1:9094,private-tls-broker2:9094,"),
			},
			wantBrokers: []string{"private-tls-broker1:9094", "private-tls-broker2:9094"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a cluster scanner with AuthTypeTLS
			discoverer := NewClusterScanner(
				nil, // msk client not needed for this test
				nil, // admin factory not needed for this test
				ClusterScannerOpts{
					Region:     defaultRegion,
					ClusterArn: "test-cluster",
					SkipKafka:  false,
					AuthType:   types.AuthTypeTLS,
				})

			brokers, err := discoverer.parseBrokerAddresses(tt.brokers)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantBrokers, brokers)
		})
	}
}
