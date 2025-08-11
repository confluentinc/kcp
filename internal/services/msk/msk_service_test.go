package msk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/aws/aws-sdk-go-v2/service/kafka/types"
	internaltypes "github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// KafkaClientInterface defines the interface for Kafka client operations used by MSK service
type KafkaClientInterface interface {
	DescribeClusterV2(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error)
	GetBootstrapBrokers(ctx context.Context, params *kafka.GetBootstrapBrokersInput, optFns ...func(*kafka.Options)) (*kafka.GetBootstrapBrokersOutput, error)
	DescribeConfigurationRevision(ctx context.Context, params *kafka.DescribeConfigurationRevisionInput, optFns ...func(*kafka.Options)) (*kafka.DescribeConfigurationRevisionOutput, error)
	GetCompatibleKafkaVersions(ctx context.Context, params *kafka.GetCompatibleKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.GetCompatibleKafkaVersionsOutput, error)
	GetClusterPolicy(ctx context.Context, params *kafka.GetClusterPolicyInput, optFns ...func(*kafka.Options)) (*kafka.GetClusterPolicyOutput, error)
	ListClientVpcConnections(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error)
	ListClusterOperationsV2(ctx context.Context, params *kafka.ListClusterOperationsV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClusterOperationsV2Output, error)
	ListNodes(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error)
	ListScramSecrets(ctx context.Context, params *kafka.ListScramSecretsInput, optFns ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error)
}

// TestMSKService is a test-specific version of MSKService that can accept a mock client
type TestMSKService struct {
	client KafkaClientInterface
}

// NewTestMSKService creates a new test MSK service with a mock client
func NewTestMSKService(client KafkaClientInterface) *TestMSKService {
	return &TestMSKService{client: client}
}

// DescribeCluster implements the same logic as the real MSK service
func (ms *TestMSKService) DescribeCluster(ctx context.Context, clusterArn *string) (*types.Cluster, error) {
	cluster, err := ms.client.DescribeClusterV2(ctx, &kafka.DescribeClusterV2Input{
		ClusterArn: clusterArn,
	})
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to describe cluster: %v", err)
	}
	return cluster.ClusterInfo, nil
}

// DescribeClusterV2 implements the same logic as the real MSK service
func (ms *TestMSKService) DescribeClusterV2(ctx context.Context, clusterArn *string) (*kafka.DescribeClusterV2Output, error) {
	cluster, err := ms.client.DescribeClusterV2(ctx, &kafka.DescribeClusterV2Input{
		ClusterArn: clusterArn,
	})
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to describe cluster: %v", err)
	}
	return cluster, nil
}

// GetBootstrapBrokers implements the same logic as the real MSK service
func (ms *TestMSKService) GetBootstrapBrokers(ctx context.Context, clusterArn *string) (*kafka.GetBootstrapBrokersOutput, error) {
	brokers, err := ms.client.GetBootstrapBrokers(ctx, &kafka.GetBootstrapBrokersInput{
		ClusterArn: clusterArn,
	})
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to get bootstrap brokers: %v", err)
	}
	return brokers, nil
}

// ParseBrokerAddresses implements the same logic as the real MSK service
func (ms *TestMSKService) ParseBrokerAddresses(brokers kafka.GetBootstrapBrokersOutput, authType internaltypes.AuthType) ([]string, error) {
	var brokerList string

	switch authType {
	case internaltypes.AuthTypeIAM:
		brokerList = aws.ToString(brokers.BootstrapBrokerStringPublicSaslIam)
		if brokerList == "" {
			brokerList = aws.ToString(brokers.BootstrapBrokerStringSaslIam)
		}
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No SASL/IAM brokers found in the cluster")
		}
	case internaltypes.AuthTypeSASLSCRAM:
		brokerList = aws.ToString(brokers.BootstrapBrokerStringPublicSaslScram)
		if brokerList == "" {
			brokerList = aws.ToString(brokers.BootstrapBrokerStringSaslScram)
		}
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No SASL/SCRAM brokers found in the cluster")
		}
	case internaltypes.AuthTypeUnauthenticated:
		brokerList = aws.ToString(brokers.BootstrapBrokerStringTls)
		if brokerList == "" {
			brokerList = aws.ToString(brokers.BootstrapBrokerString)
		}
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No Unauthenticated brokers found in the cluster")
		}
	case internaltypes.AuthTypeTLS:
		brokerList = aws.ToString(brokers.BootstrapBrokerStringPublicTls)
		if brokerList == "" {
			brokerList = aws.ToString(brokers.BootstrapBrokerStringTls)
		}
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No TLS brokers found in the cluster")
		}
	default:
		return nil, fmt.Errorf("❌ Auth type: %v not yet supported", authType)
	}

	// Split by comma and trim whitespace from each address, filter out empty strings
	rawAddresses := strings.Split(brokerList, ",")
	addresses := make([]string, 0, len(rawAddresses))
	for _, addr := range rawAddresses {
		trimmedAddr := strings.TrimSpace(addr)
		if trimmedAddr != "" {
			addresses = append(addresses, trimmedAddr)
		}
	}
	return addresses, nil
}

// IsFetchFromFollowerEnabled implements the same logic as the real MSK service
func (ms *TestMSKService) IsFetchFromFollowerEnabled(ctx context.Context, cluster types.Cluster) (*bool, error) {
	if cluster.Provisioned == nil ||
		cluster.Provisioned.CurrentBrokerSoftwareInfo == nil ||
		cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationArn == nil ||
		cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationRevision == nil {
		return nil, nil
	}

	configurationArn := cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationArn
	configurationRevision := cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationRevision

	describeConfigurationRevisionInput := &kafka.DescribeConfigurationRevisionInput{
		Arn:      configurationArn,
		Revision: configurationRevision,
	}

	revision, err := ms.client.DescribeConfigurationRevision(ctx, describeConfigurationRevisionInput)
	if err != nil {
		return nil, fmt.Errorf("failed to describe configuration revision: %v", err)
	}

	serverProperties := revision.ServerProperties
	propertiesText := string(serverProperties)

	if strings.Contains(propertiesText, "replica.selector.class=org.apache.kafka.common.replica.RackAwareReplicaSelector") {
		return aws.Bool(true), nil
	}
	return aws.Bool(false), nil
}

// GetCompatibleKafkaVersions implements the same logic as the real MSK service
func (ms *TestMSKService) GetCompatibleKafkaVersions(ctx context.Context, clusterArn *string) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
	versions, err := ms.client.GetCompatibleKafkaVersions(ctx, &kafka.GetCompatibleKafkaVersionsInput{
		ClusterArn: clusterArn,
	})
	if err != nil {
		if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
			return &kafka.GetCompatibleKafkaVersionsOutput{
				CompatibleKafkaVersions: []types.CompatibleKafkaVersion{},
			}, nil
		}
		return nil, fmt.Errorf("❌ Failed to get compatible versions: %v", err)
	}
	return versions, nil
}

// GetClusterPolicy implements the same logic as the real MSK service
func (ms *TestMSKService) GetClusterPolicy(ctx context.Context, clusterArn *string) (*kafka.GetClusterPolicyOutput, error) {
	policy, err := ms.client.GetClusterPolicy(ctx, &kafka.GetClusterPolicyInput{
		ClusterArn: clusterArn,
	})
	if err == nil {
		return policy, nil
	}

	var notFoundErr *types.NotFoundException
	if errors.As(err, &notFoundErr) {
		return new(kafka.GetClusterPolicyOutput), nil
	}
	return nil, err
}

// ListClientVpcConnections implements the same pagination logic as the real MSK service
func (ms *TestMSKService) ListClientVpcConnections(ctx context.Context, clusterArn *string) ([]types.ClientVpcConnection, error) {
	var connections []types.ClientVpcConnection
	var nextToken *string
	maxResults := int32(100)

	for {
		output, err := ms.client.ListClientVpcConnections(ctx, &kafka.ListClientVpcConnectionsInput{
			MaxResults: &maxResults,
			ClusterArn: clusterArn,
			NextToken:  nextToken,
		})
		if err != nil {
			if strings.Contains(err.Error(), "This Region doesn't currently support VPC connectivity with Amazon MSK Serverless clusters") {
				return []types.ClientVpcConnection{}, nil
			}
			return nil, fmt.Errorf("❌ Failed listing client vpc connections: %v", err)
		}
		connections = append(connections, output.ClientVpcConnections...)
		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return connections, nil
}

// ListClusterOperationsV2 implements the same pagination logic as the real MSK service
func (ms *TestMSKService) ListClusterOperationsV2(ctx context.Context, clusterArn *string) ([]types.ClusterOperationV2Summary, error) {
	var operations []types.ClusterOperationV2Summary
	var nextToken *string
	maxResults := int32(100)

	for {
		output, err := ms.client.ListClusterOperationsV2(ctx, &kafka.ListClusterOperationsV2Input{
			MaxResults: &maxResults,
			ClusterArn: clusterArn,
			NextToken:  nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("❌ Failed listing operations: %v", err)
		}
		operations = append(operations, output.ClusterOperationInfoList...)
		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return operations, nil
}

// ListNodes implements the same pagination logic as the real MSK service
func (ms *TestMSKService) ListNodes(ctx context.Context, clusterArn *string) ([]types.NodeInfo, error) {
	var nodes []types.NodeInfo
	var nextToken *string
	maxResults := int32(100)

	for {
		output, err := ms.client.ListNodes(ctx, &kafka.ListNodesInput{
			MaxResults: &maxResults,
			ClusterArn: clusterArn,
			NextToken:  nextToken,
		})
		if err != nil {
			if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
				return []types.NodeInfo{}, nil
			}
			return nil, fmt.Errorf("❌ Failed listing nodes: %v", err)
		}
		nodes = append(nodes, output.NodeInfoList...)
		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return nodes, nil
}

// ListScramSecrets implements the same pagination logic as the real MSK service
func (ms *TestMSKService) ListScramSecrets(ctx context.Context, clusterArn *string) ([]string, error) {
	var secrets []string
	var nextToken *string
	maxResults := int32(100)

	for {
		output, err := ms.client.ListScramSecrets(ctx, &kafka.ListScramSecretsInput{
			MaxResults: &maxResults,
			ClusterArn: clusterArn,
			NextToken:  nextToken,
		})
		if err != nil {
			if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
				return []string{}, nil
			}
			return nil, fmt.Errorf("❌ Failed listing scram secrets: %v", err)
		}
		secrets = append(secrets, output.SecretArnList...)
		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return secrets, nil
}

// MockKafkaClient is a mock implementation of the KafkaClientInterface for testing
type MockKafkaClient struct {
	DescribeClusterV2Func             func(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error)
	GetBootstrapBrokersFunc           func(ctx context.Context, params *kafka.GetBootstrapBrokersInput, optFns ...func(*kafka.Options)) (*kafka.GetBootstrapBrokersOutput, error)
	DescribeConfigurationRevisionFunc func(ctx context.Context, params *kafka.DescribeConfigurationRevisionInput, optFns ...func(*kafka.Options)) (*kafka.DescribeConfigurationRevisionOutput, error)
	GetCompatibleKafkaVersionsFunc    func(ctx context.Context, params *kafka.GetCompatibleKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.GetCompatibleKafkaVersionsOutput, error)
	GetClusterPolicyFunc              func(ctx context.Context, params *kafka.GetClusterPolicyInput, optFns ...func(*kafka.Options)) (*kafka.GetClusterPolicyOutput, error)
	ListClientVpcConnectionsFunc      func(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error)
	ListClusterOperationsV2Func       func(ctx context.Context, params *kafka.ListClusterOperationsV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClusterOperationsV2Output, error)
	ListNodesFunc                     func(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error)
	ListScramSecretsFunc              func(ctx context.Context, params *kafka.ListScramSecretsInput, optFns ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error)
}

func (m *MockKafkaClient) DescribeClusterV2(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error) {
	return m.DescribeClusterV2Func(ctx, params, optFns...)
}

func (m *MockKafkaClient) GetBootstrapBrokers(ctx context.Context, params *kafka.GetBootstrapBrokersInput, optFns ...func(*kafka.Options)) (*kafka.GetBootstrapBrokersOutput, error) {
	return m.GetBootstrapBrokersFunc(ctx, params, optFns...)
}

func (m *MockKafkaClient) DescribeConfigurationRevision(ctx context.Context, params *kafka.DescribeConfigurationRevisionInput, optFns ...func(*kafka.Options)) (*kafka.DescribeConfigurationRevisionOutput, error) {
	return m.DescribeConfigurationRevisionFunc(ctx, params, optFns...)
}

func (m *MockKafkaClient) GetCompatibleKafkaVersions(ctx context.Context, params *kafka.GetCompatibleKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
	return m.GetCompatibleKafkaVersionsFunc(ctx, params, optFns...)
}

func (m *MockKafkaClient) GetClusterPolicy(ctx context.Context, params *kafka.GetClusterPolicyInput, optFns ...func(*kafka.Options)) (*kafka.GetClusterPolicyOutput, error) {
	return m.GetClusterPolicyFunc(ctx, params, optFns...)
}

func (m *MockKafkaClient) ListClientVpcConnections(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error) {
	return m.ListClientVpcConnectionsFunc(ctx, params, optFns...)
}

func (m *MockKafkaClient) ListClusterOperationsV2(ctx context.Context, params *kafka.ListClusterOperationsV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClusterOperationsV2Output, error) {
	return m.ListClusterOperationsV2Func(ctx, params, optFns...)
}

func (m *MockKafkaClient) ListNodes(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error) {
	return m.ListNodesFunc(ctx, params, optFns...)
}

func (m *MockKafkaClient) ListScramSecrets(ctx context.Context, params *kafka.ListScramSecretsInput, optFns ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error) {
	return m.ListScramSecretsFunc(ctx, params, optFns...)
}

func TestMSKService_ListClientVpcConnections_Pagination(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListClientVpcConnectionsOutput
		wantTotal     int
		wantError     bool
	}{
		{
			name: "handles pagination with multiple pages",
			mockResponses: []*kafka.ListClientVpcConnectionsOutput{
				{
					ClientVpcConnections: []types.ClientVpcConnection{
						{VpcConnectionArn: aws.String("vpc-conn-1")},
					},
					NextToken: aws.String("token-page-1"),
				},
				{
					ClientVpcConnections: []types.ClientVpcConnection{
						{VpcConnectionArn: aws.String("vpc-conn-2")},
					},
					NextToken: aws.String("token-page-2"),
				},
				{
					ClientVpcConnections: []types.ClientVpcConnection{
						{VpcConnectionArn: aws.String("vpc-conn-3")},
					},
					// No NextToken - end of pagination
				},
			},
			wantTotal: 3,
			wantError: false,
		},
		{
			name: "handles single page with no NextToken",
			mockResponses: []*kafka.ListClientVpcConnectionsOutput{
				{
					ClientVpcConnections: []types.ClientVpcConnection{
						{VpcConnectionArn: aws.String("vpc-conn-1")},
						{VpcConnectionArn: aws.String("vpc-conn-2")},
					},
					// No NextToken - single page
				},
			},
			wantTotal: 2,
			wantError: false,
		},
		{
			name: "handles empty results",
			mockResponses: []*kafka.ListClientVpcConnectionsOutput{
				{
					ClientVpcConnections: []types.ClientVpcConnection{},
					// No NextToken - empty page
				},
			},
			wantTotal: 0,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockClient := &MockKafkaClient{
				ListClientVpcConnectionsFunc: func(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error) {
					// Verify parameters
					assert.NotNil(t, params.ClusterArn, "ClusterArn should be set")
					assert.Equal(t, int32(100), *params.MaxResults, "MaxResults should be 100")

					// Return the appropriate response based on call count
					if callCount < len(tt.mockResponses) {
						response := tt.mockResponses[callCount]
						callCount++
						return response, nil
					}

					// Should not reach here
					t.Fatal("Unexpected call to ListClientVpcConnections")
					return nil, nil
				},
			}

			mskService := NewTestMSKService(mockClient)
			result, err := mskService.ListClientVpcConnections(context.Background(), aws.String("test-cluster-arn"))

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(result), "Total VPC connections should match expected count")

			// Verify all VPC connections from all pages are returned
			expectedArns := make([]string, 0)
			for _, response := range tt.mockResponses {
				for _, conn := range response.ClientVpcConnections {
					expectedArns = append(expectedArns, *conn.VpcConnectionArn)
				}
			}

			actualArns := make([]string, len(result))
			for i, conn := range result {
				actualArns[i] = *conn.VpcConnectionArn
			}
			assert.ElementsMatch(t, expectedArns, actualArns, "All VPC connections from all pages should be returned")
		})
	}
}

func TestMSKService_ListClusterOperationsV2_Pagination(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListClusterOperationsV2Output
		wantTotal     int
		wantError     bool
	}{
		{
			name: "handles pagination with multiple pages",
			mockResponses: []*kafka.ListClusterOperationsV2Output{
				{
					ClusterOperationInfoList: []types.ClusterOperationV2Summary{
						{OperationArn: aws.String("operation-1")},
					},
					NextToken: aws.String("operations-token-page-1"),
				},
				{
					ClusterOperationInfoList: []types.ClusterOperationV2Summary{
						{OperationArn: aws.String("operation-2")},
					},
					NextToken: aws.String("operations-token-page-2"),
				},
				{
					ClusterOperationInfoList: []types.ClusterOperationV2Summary{
						{OperationArn: aws.String("operation-3")},
					},
					// No NextToken - end of pagination
				},
			},
			wantTotal: 3,
			wantError: false,
		},
		{
			name: "handles single page with no NextToken",
			mockResponses: []*kafka.ListClusterOperationsV2Output{
				{
					ClusterOperationInfoList: []types.ClusterOperationV2Summary{
						{OperationArn: aws.String("operation-1")},
						{OperationArn: aws.String("operation-2")},
					},
					// No NextToken - single page
				},
			},
			wantTotal: 2,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockClient := &MockKafkaClient{
				ListClusterOperationsV2Func: func(ctx context.Context, params *kafka.ListClusterOperationsV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClusterOperationsV2Output, error) {
					// Verify parameters
					assert.NotNil(t, params.ClusterArn, "ClusterArn should be set")
					assert.Equal(t, int32(100), *params.MaxResults, "MaxResults should be 100")

					// Return the appropriate response based on call count
					if callCount < len(tt.mockResponses) {
						response := tt.mockResponses[callCount]
						callCount++
						return response, nil
					}

					// Should not reach here
					t.Fatal("Unexpected call to ListClusterOperationsV2")
					return nil, nil
				},
			}

			mskService := NewTestMSKService(mockClient)
			result, err := mskService.ListClusterOperationsV2(context.Background(), aws.String("test-cluster-arn"))

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(result), "Total operations should match expected count")

			// Verify all operations from all pages are returned
			expectedArns := make([]string, 0)
			for _, response := range tt.mockResponses {
				for _, op := range response.ClusterOperationInfoList {
					expectedArns = append(expectedArns, *op.OperationArn)
				}
			}

			actualArns := make([]string, len(result))
			for i, op := range result {
				actualArns[i] = *op.OperationArn
			}
			assert.ElementsMatch(t, expectedArns, actualArns, "All operations from all pages should be returned")
		})
	}
}

func TestMSKService_ListNodes_Pagination(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListNodesOutput
		wantTotal     int
		wantError     bool
	}{
		{
			name: "handles pagination with multiple pages",
			mockResponses: []*kafka.ListNodesOutput{
				{
					NodeInfoList: []types.NodeInfo{
						{NodeARN: aws.String("node-1")},
					},
					NextToken: aws.String("nodes-token-page-1"),
				},
				{
					NodeInfoList: []types.NodeInfo{
						{NodeARN: aws.String("node-2")},
						{NodeARN: aws.String("node-3")},
					},
					NextToken: aws.String("nodes-token-page-2"),
				},
				{
					NodeInfoList: []types.NodeInfo{
						{NodeARN: aws.String("node-4")},
					},
					// No NextToken - end of pagination
				},
			},
			wantTotal: 4,
			wantError: false,
		},
		{
			name: "handles single page with no NextToken",
			mockResponses: []*kafka.ListNodesOutput{
				{
					NodeInfoList: []types.NodeInfo{
						{NodeARN: aws.String("node-1")},
						{NodeARN: aws.String("node-2")},
					},
					// No NextToken - single page
				},
			},
			wantTotal: 2,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockClient := &MockKafkaClient{
				ListNodesFunc: func(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error) {
					// Verify parameters
					assert.NotNil(t, params.ClusterArn, "ClusterArn should be set")
					assert.Equal(t, int32(100), *params.MaxResults, "MaxResults should be 100")

					// Return the appropriate response based on call count
					if callCount < len(tt.mockResponses) {
						response := tt.mockResponses[callCount]
						callCount++
						return response, nil
					}

					// Should not reach here
					t.Fatal("Unexpected call to ListNodes")
					return nil, nil
				},
			}

			mskService := NewTestMSKService(mockClient)
			result, err := mskService.ListNodes(context.Background(), aws.String("test-cluster-arn"))

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(result), "Total nodes should match expected count")

			// Verify all nodes from all pages are returned
			expectedArns := make([]string, 0)
			for _, response := range tt.mockResponses {
				for _, node := range response.NodeInfoList {
					expectedArns = append(expectedArns, *node.NodeARN)
				}
			}

			actualArns := make([]string, len(result))
			for i, node := range result {
				actualArns[i] = *node.NodeARN
			}
			assert.ElementsMatch(t, expectedArns, actualArns, "All nodes from all pages should be returned")
		})
	}
}

func TestMSKService_ListScramSecrets_Pagination(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListScramSecretsOutput
		wantTotal     int
		wantError     bool
	}{
		{
			name: "handles pagination with multiple pages",
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
			wantTotal: 6,
			wantError: false,
		},
		{
			name: "handles single page with no NextToken",
			mockResponses: []*kafka.ListScramSecretsOutput{
				{
					SecretArnList: []string{"secret-1", "secret-2"},
					// No NextToken - single page
				},
			},
			wantTotal: 2,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockClient := &MockKafkaClient{
				ListScramSecretsFunc: func(ctx context.Context, params *kafka.ListScramSecretsInput, optFns ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error) {
					// Verify parameters
					assert.NotNil(t, params.ClusterArn, "ClusterArn should be set")
					assert.Equal(t, int32(100), *params.MaxResults, "MaxResults should be 100")

					// Return the appropriate response based on call count
					if callCount < len(tt.mockResponses) {
						response := tt.mockResponses[callCount]
						callCount++
						return response, nil
					}

					// Should not reach here
					t.Fatal("Unexpected call to ListScramSecrets")
					return nil, nil
				},
			}

			mskService := NewTestMSKService(mockClient)
			result, err := mskService.ListScramSecrets(context.Background(), aws.String("test-cluster-arn"))

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(result), "Total SCRAM secrets should match expected count")

			// Verify all secrets from all pages are returned
			expectedSecrets := make([]string, 0)
			for _, response := range tt.mockResponses {
				expectedSecrets = append(expectedSecrets, response.SecretArnList...)
			}

			assert.ElementsMatch(t, expectedSecrets, result, "All SCRAM secrets from all pages should be returned")
		})
	}
}

func TestMSKService_Pagination_NextTokenHandling(t *testing.T) {
	t.Run("verifies NextToken is properly passed between requests for VPC connections", func(t *testing.T) {
		callCount := 0
		expectedTokens := []*string{nil, aws.String("token-1"), aws.String("token-2")}

		mockClient := &MockKafkaClient{
			ListClientVpcConnectionsFunc: func(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error) {
				// Verify NextToken parameter matches expected sequence
				expectedToken := expectedTokens[callCount]
				if expectedToken == nil {
					assert.Nil(t, params.NextToken, "First call should have nil NextToken")
				} else {
					assert.Equal(t, *expectedToken, *params.NextToken, "NextToken should match expected sequence")
				}

				callCount++

				// Return response with NextToken for first two calls
				if callCount <= 2 {
					return &kafka.ListClientVpcConnectionsOutput{
						ClientVpcConnections: []types.ClientVpcConnection{
							{VpcConnectionArn: aws.String(fmt.Sprintf("vpc-conn-%d", callCount))},
						},
						NextToken: aws.String(fmt.Sprintf("token-%d", callCount)),
					}, nil
				}

				// Final call - no NextToken
				return &kafka.ListClientVpcConnectionsOutput{
					ClientVpcConnections: []types.ClientVpcConnection{
						{VpcConnectionArn: aws.String("vpc-conn-3")},
					},
					// No NextToken - end of pagination
				}, nil
			},
		}

		mskService := NewTestMSKService(mockClient)
		result, err := mskService.ListClientVpcConnections(context.Background(), aws.String("test-cluster-arn"))

		require.NoError(t, err)
		assert.Equal(t, 3, len(result), "Should return all VPC connections from all pages")
		assert.Equal(t, 3, callCount, "Should make exactly 3 API calls")
	})
}

func TestMSKService_DescribeCluster(t *testing.T) {
	tests := []struct {
		name        string
		clusterArn  string
		mockCluster *kafka.DescribeClusterV2Output
		mockError   error
		wantError   bool
		wantCluster *types.Cluster
	}{
		{
			name:       "successful cluster description",
			clusterArn: "test-cluster-arn",
			mockCluster: &kafka.DescribeClusterV2Output{
				ClusterInfo: &types.Cluster{
					ClusterArn:  aws.String("test-cluster-arn"),
					ClusterName: aws.String("test-cluster"),
					State:       types.ClusterStateActive,
				},
			},
			mockError:   nil,
			wantError:   false,
			wantCluster: &types.Cluster{ClusterArn: aws.String("test-cluster-arn"), ClusterName: aws.String("test-cluster"), State: types.ClusterStateActive},
		},
		{
			name:        "API error",
			clusterArn:  "test-cluster-arn",
			mockCluster: nil,
			mockError:   fmt.Errorf("AWS API error"),
			wantError:   true,
			wantCluster: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockKafkaClient{
				DescribeClusterV2Func: func(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error) {
					assert.Equal(t, tt.clusterArn, *params.ClusterArn)
					return tt.mockCluster, tt.mockError
				},
			}

			mskService := NewTestMSKService(mockClient)
			result, err := mskService.DescribeCluster(context.Background(), aws.String(tt.clusterArn))

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantCluster.ClusterArn, result.ClusterArn)
			assert.Equal(t, tt.wantCluster.ClusterName, result.ClusterName)
			assert.Equal(t, tt.wantCluster.State, result.State)
		})
	}
}

func TestMSKService_DescribeClusterV2(t *testing.T) {
	tests := []struct {
		name        string
		clusterArn  string
		mockCluster *kafka.DescribeClusterV2Output
		mockError   error
		wantError   bool
		wantCluster *kafka.DescribeClusterV2Output
	}{
		{
			name:       "successful cluster description",
			clusterArn: "test-cluster-arn",
			mockCluster: &kafka.DescribeClusterV2Output{
				ClusterInfo: &types.Cluster{
					ClusterArn:  aws.String("test-cluster-arn"),
					ClusterName: aws.String("test-cluster"),
					State:       types.ClusterStateActive,
				},
			},
			mockError:   nil,
			wantError:   false,
			wantCluster: &kafka.DescribeClusterV2Output{ClusterInfo: &types.Cluster{ClusterArn: aws.String("test-cluster-arn"), ClusterName: aws.String("test-cluster"), State: types.ClusterStateActive}},
		},
		{
			name:        "API error",
			clusterArn:  "test-cluster-arn",
			mockCluster: nil,
			mockError:   fmt.Errorf("AWS API error"),
			wantError:   true,
			wantCluster: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockKafkaClient{
				DescribeClusterV2Func: func(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error) {
					assert.Equal(t, tt.clusterArn, *params.ClusterArn)
					return tt.mockCluster, tt.mockError
				},
			}

			mskService := NewTestMSKService(mockClient)
			result, err := mskService.DescribeClusterV2(context.Background(), aws.String(tt.clusterArn))

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantCluster.ClusterInfo.ClusterArn, result.ClusterInfo.ClusterArn)
			assert.Equal(t, tt.wantCluster.ClusterInfo.ClusterName, result.ClusterInfo.ClusterName)
			assert.Equal(t, tt.wantCluster.ClusterInfo.State, result.ClusterInfo.State)
		})
	}
}

func TestMSKService_GetBootstrapBrokers(t *testing.T) {
	tests := []struct {
		name        string
		clusterArn  string
		mockBrokers *kafka.GetBootstrapBrokersOutput
		mockError   error
		wantError   bool
		wantBrokers *kafka.GetBootstrapBrokersOutput
	}{
		{
			name:       "successful bootstrap brokers retrieval",
			clusterArn: "test-cluster-arn",
			mockBrokers: &kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerString: aws.String("broker1:9092,broker2:9092"),
			},
			mockError:   nil,
			wantError:   false,
			wantBrokers: &kafka.GetBootstrapBrokersOutput{BootstrapBrokerString: aws.String("broker1:9092,broker2:9092")},
		},
		{
			name:        "API error",
			clusterArn:  "test-cluster-arn",
			mockBrokers: nil,
			mockError:   fmt.Errorf("AWS API error"),
			wantError:   true,
			wantBrokers: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockKafkaClient{
				GetBootstrapBrokersFunc: func(ctx context.Context, params *kafka.GetBootstrapBrokersInput, optFns ...func(*kafka.Options)) (*kafka.GetBootstrapBrokersOutput, error) {
					assert.Equal(t, tt.clusterArn, *params.ClusterArn)
					return tt.mockBrokers, tt.mockError
				},
			}

			mskService := NewTestMSKService(mockClient)
			result, err := mskService.GetBootstrapBrokers(context.Background(), aws.String(tt.clusterArn))

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantBrokers.BootstrapBrokerString, result.BootstrapBrokerString)
		})
	}
}

func TestMSKService_ParseBrokerAddresses(t *testing.T) {
	tests := []struct {
		name      string
		brokers   kafka.GetBootstrapBrokersOutput
		authType  internaltypes.AuthType
		wantAddrs []string
		wantError bool
	}{
		{
			name: "SASL/IAM with public brokers",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslIam: aws.String("broker1:9098,broker2:9098"),
			},
			authType:  internaltypes.AuthTypeIAM,
			wantAddrs: []string{"broker1:9098", "broker2:9098"},
			wantError: false,
		},
		{
			name: "SASL/IAM with private brokers (fallback)",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("broker1:9098,broker2:9098"),
			},
			authType:  internaltypes.AuthTypeIAM,
			wantAddrs: []string{"broker1:9098", "broker2:9098"},
			wantError: false,
		},
		{
			name: "SASL/SCRAM with public brokers",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslScram: aws.String("broker1:9096,broker2:9096"),
			},
			authType:  internaltypes.AuthTypeSASLSCRAM,
			wantAddrs: []string{"broker1:9096", "broker2:9096"},
			wantError: false,
		},
		{
			name: "TLS with public brokers",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicTls: aws.String("broker1:9094,broker2:9094"),
			},
			authType:  internaltypes.AuthTypeTLS,
			wantAddrs: []string{"broker1:9094", "broker2:9094"},
			wantError: false,
		},
		{
			name: "Unauthenticated with TLS brokers",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringTls: aws.String("broker1:9092,broker2:9092"),
			},
			authType:  internaltypes.AuthTypeUnauthenticated,
			wantAddrs: []string{"broker1:9092", "broker2:9092"},
			wantError: false,
		},
		{
			name: "handles brokers with spaces",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslIam: aws.String(" broker1:9098 , broker2:9098 "),
			},
			authType:  internaltypes.AuthTypeIAM,
			wantAddrs: []string{"broker1:9098", "broker2:9098"},
			wantError: false,
		},
		{
			name: "handles empty broker list",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslIam: aws.String(""),
			},
			authType:  internaltypes.AuthTypeIAM,
			wantAddrs: nil,
			wantError: true,
		},
		{
			name: "unsupported auth type",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerString: aws.String("broker1:9092"),
			},
			authType:  internaltypes.AuthType("INVALID_AUTH_TYPE"),
			wantAddrs: nil,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mskService := NewTestMSKService(nil) // No client needed for this method
			result, err := mskService.ParseBrokerAddresses(tt.brokers, tt.authType)

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.ElementsMatch(t, tt.wantAddrs, result)
		})
	}
}

func TestMSKService_IsFetchFromFollowerEnabled(t *testing.T) {
	tests := []struct {
		name         string
		cluster      types.Cluster
		mockRevision *kafka.DescribeConfigurationRevisionOutput
		mockError    error
		wantEnabled  *bool
		wantError    bool
	}{
		{
			name: "fetch from follower enabled",
			cluster: types.Cluster{
				Provisioned: &types.Provisioned{
					CurrentBrokerSoftwareInfo: &types.BrokerSoftwareInfo{
						ConfigurationArn:      aws.String("config-arn"),
						ConfigurationRevision: aws.Int64(1),
					},
				},
			},
			mockRevision: &kafka.DescribeConfigurationRevisionOutput{
				ServerProperties: []byte("replica.selector.class=org.apache.kafka.common.replica.RackAwareReplicaSelector"),
			},
			mockError:   nil,
			wantEnabled: aws.Bool(true),
			wantError:   false,
		},
		{
			name: "fetch from follower disabled",
			cluster: types.Cluster{
				Provisioned: &types.Provisioned{
					CurrentBrokerSoftwareInfo: &types.BrokerSoftwareInfo{
						ConfigurationArn:      aws.String("config-arn"),
						ConfigurationRevision: aws.Int64(1),
					},
				},
			},
			mockRevision: &kafka.DescribeConfigurationRevisionOutput{
				ServerProperties: []byte("some.other.property=value"),
			},
			mockError:   nil,
			wantEnabled: aws.Bool(false),
			wantError:   false,
		},
		{
			name: "nil provisioned cluster",
			cluster: types.Cluster{
				Provisioned: nil,
			},
			mockRevision: nil,
			mockError:    nil,
			wantEnabled:  nil,
			wantError:    false,
		},
		{
			name: "API error",
			cluster: types.Cluster{
				Provisioned: &types.Provisioned{
					CurrentBrokerSoftwareInfo: &types.BrokerSoftwareInfo{
						ConfigurationArn:      aws.String("config-arn"),
						ConfigurationRevision: aws.Int64(1),
					},
				},
			},
			mockRevision: nil,
			mockError:    fmt.Errorf("AWS API error"),
			wantEnabled:  nil,
			wantError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockKafkaClient{
				DescribeConfigurationRevisionFunc: func(ctx context.Context, params *kafka.DescribeConfigurationRevisionInput, optFns ...func(*kafka.Options)) (*kafka.DescribeConfigurationRevisionOutput, error) {
					if tt.cluster.Provisioned != nil && tt.cluster.Provisioned.CurrentBrokerSoftwareInfo != nil {
						assert.Equal(t, *tt.cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationArn, *params.Arn)
						assert.Equal(t, *tt.cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationRevision, *params.Revision)
					}
					return tt.mockRevision, tt.mockError
				},
			}

			mskService := NewTestMSKService(mockClient)
			result, err := mskService.IsFetchFromFollowerEnabled(context.Background(), tt.cluster)

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			if tt.wantEnabled == nil {
				assert.Nil(t, result)
			} else {
				assert.Equal(t, *tt.wantEnabled, *result)
			}
		})
	}
}

func TestMSKService_GetCompatibleKafkaVersions(t *testing.T) {
	tests := []struct {
		name         string
		clusterArn   string
		mockVersions *kafka.GetCompatibleKafkaVersionsOutput
		mockError    error
		wantError    bool
		wantVersions *kafka.GetCompatibleKafkaVersionsOutput
	}{
		{
			name:       "successful versions retrieval",
			clusterArn: "test-cluster-arn",
			mockVersions: &kafka.GetCompatibleKafkaVersionsOutput{
				CompatibleKafkaVersions: []types.CompatibleKafkaVersion{
					{SourceVersion: aws.String("3.5.1")},
					{SourceVersion: aws.String("3.4.1")},
				},
			},
			mockError: nil,
			wantError: false,
			wantVersions: &kafka.GetCompatibleKafkaVersionsOutput{
				CompatibleKafkaVersions: []types.CompatibleKafkaVersion{
					{SourceVersion: aws.String("3.5.1")},
					{SourceVersion: aws.String("3.4.1")},
				},
			},
		},
		{
			name:         "serverless cluster error - returns empty list",
			clusterArn:   "test-cluster-arn",
			mockVersions: nil,
			mockError:    fmt.Errorf("This operation cannot be performed on serverless clusters."),
			wantError:    false,
			wantVersions: &kafka.GetCompatibleKafkaVersionsOutput{
				CompatibleKafkaVersions: []types.CompatibleKafkaVersion{},
			},
		},
		{
			name:         "other API error",
			clusterArn:   "test-cluster-arn",
			mockVersions: nil,
			mockError:    fmt.Errorf("Some other AWS API error"),
			wantError:    true,
			wantVersions: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockKafkaClient{
				GetCompatibleKafkaVersionsFunc: func(ctx context.Context, params *kafka.GetCompatibleKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
					assert.Equal(t, tt.clusterArn, *params.ClusterArn)
					return tt.mockVersions, tt.mockError
				},
			}

			mskService := NewTestMSKService(mockClient)
			result, err := mskService.GetCompatibleKafkaVersions(context.Background(), aws.String(tt.clusterArn))

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, len(tt.wantVersions.CompatibleKafkaVersions), len(result.CompatibleKafkaVersions))
		})
	}
}

func TestMSKService_GetClusterPolicy(t *testing.T) {
	tests := []struct {
		name       string
		clusterArn string
		mockPolicy *kafka.GetClusterPolicyOutput
		mockError  error
		wantError  bool
		wantPolicy *kafka.GetClusterPolicyOutput
	}{
		{
			name:       "successful policy retrieval",
			clusterArn: "test-cluster-arn",
			mockPolicy: &kafka.GetClusterPolicyOutput{
				Policy: aws.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"*"},"Action":"kafka-cluster:Connect","Resource":"*"}]}`),
			},
			mockError:  nil,
			wantError:  false,
			wantPolicy: &kafka.GetClusterPolicyOutput{Policy: aws.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"*"},"Action":"kafka-cluster:Connect","Resource":"*"}]}`)},
		},
		{
			name:       "policy not found - returns empty policy",
			clusterArn: "test-cluster-arn",
			mockPolicy: nil,
			mockError:  &types.NotFoundException{},
			wantError:  false,
			wantPolicy: &kafka.GetClusterPolicyOutput{},
		},
		{
			name:       "other API error",
			clusterArn: "test-cluster-arn",
			mockPolicy: nil,
			mockError:  fmt.Errorf("Some other AWS API error"),
			wantError:  true,
			wantPolicy: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockKafkaClient{
				GetClusterPolicyFunc: func(ctx context.Context, params *kafka.GetClusterPolicyInput, optFns ...func(*kafka.Options)) (*kafka.GetClusterPolicyOutput, error) {
					assert.Equal(t, tt.clusterArn, *params.ClusterArn)
					return tt.mockPolicy, tt.mockError
				},
			}

			mskService := NewTestMSKService(mockClient)
			result, err := mskService.GetClusterPolicy(context.Background(), aws.String(tt.clusterArn))

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			if tt.wantPolicy.Policy != nil {
				assert.Equal(t, *tt.wantPolicy.Policy, *result.Policy)
			} else {
				assert.Nil(t, result.Policy)
			}
		})
	}
}

func TestMSKService_ListClientVpcConnections_ServerlessCluster(t *testing.T) {
	tests := []struct {
		name        string
		clusterArn  string
		mockError   error
		wantError   bool
		wantResults []types.ClientVpcConnection
	}{
		{
			name:        "serverless cluster vpc connectivity not supported",
			clusterArn:  "test-serverless-cluster-arn",
			mockError:   fmt.Errorf("This Region doesn't currently support VPC connectivity with Amazon MSK Serverless clusters"),
			wantError:   false,
			wantResults: []types.ClientVpcConnection{},
		},
		{
			name:        "other API error",
			clusterArn:  "test-cluster-arn",
			mockError:   fmt.Errorf("Some other AWS API error"),
			wantError:   true,
			wantResults: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockKafkaClient{
				ListClientVpcConnectionsFunc: func(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error) {
					assert.Equal(t, tt.clusterArn, *params.ClusterArn)
					return nil, tt.mockError
				},
			}

			mskService := NewTestMSKService(mockClient)
			result, err := mskService.ListClientVpcConnections(context.Background(), aws.String(tt.clusterArn))

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantResults, result)
		})
	}
}

func TestMSKService_ListNodes_ServerlessCluster(t *testing.T) {
	tests := []struct {
		name        string
		clusterArn  string
		mockError   error
		wantError   bool
		wantResults []types.NodeInfo
	}{
		{
			name:        "serverless cluster nodes not supported",
			clusterArn:  "test-serverless-cluster-arn",
			mockError:   fmt.Errorf("This operation cannot be performed on serverless clusters."),
			wantError:   false,
			wantResults: []types.NodeInfo{},
		},
		{
			name:        "other API error",
			clusterArn:  "test-cluster-arn",
			mockError:   fmt.Errorf("Some other AWS API error"),
			wantError:   true,
			wantResults: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockKafkaClient{
				ListNodesFunc: func(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error) {
					assert.Equal(t, tt.clusterArn, *params.ClusterArn)
					return nil, tt.mockError
				},
			}

			mskService := NewTestMSKService(mockClient)
			result, err := mskService.ListNodes(context.Background(), aws.String(tt.clusterArn))

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantResults, result)
		})
	}
}

func TestMSKService_ListScramSecrets_ServerlessCluster(t *testing.T) {
	tests := []struct {
		name        string
		clusterArn  string
		mockError   error
		wantError   bool
		wantResults []string
	}{
		{
			name:        "serverless cluster scram secrets not supported",
			clusterArn:  "test-serverless-cluster-arn",
			mockError:   fmt.Errorf("This operation cannot be performed on serverless clusters."),
			wantError:   false,
			wantResults: []string{},
		},
		{
			name:        "other API error",
			clusterArn:  "test-cluster-arn",
			mockError:   fmt.Errorf("Some other AWS API error"),
			wantError:   true,
			wantResults: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockKafkaClient{
				ListScramSecretsFunc: func(ctx context.Context, params *kafka.ListScramSecretsInput, optFns ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error) {
					assert.Equal(t, tt.clusterArn, *params.ClusterArn)
					return nil, tt.mockError
				},
			}

			mskService := NewTestMSKService(mockClient)
			result, err := mskService.ListScramSecrets(context.Background(), aws.String(tt.clusterArn))

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantResults, result)
		})
	}
}

func TestMSKService_ParseBrokerAddresses_EdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		brokers   kafka.GetBootstrapBrokersOutput
		authType  internaltypes.AuthType
		wantAddrs []string
		wantError bool
	}{
		{
			name: "SASL/IAM with private brokers only",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslIam: aws.String("broker1:9098,broker2:9098"),
			},
			authType:  internaltypes.AuthTypeIAM,
			wantAddrs: []string{"broker1:9098", "broker2:9098"},
			wantError: false,
		},
		{
			name: "SASL/SCRAM with private brokers only",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringSaslScram: aws.String("broker1:9096,broker2:9096"),
			},
			authType:  internaltypes.AuthTypeSASLSCRAM,
			wantAddrs: []string{"broker1:9096", "broker2:9096"},
			wantError: false,
		},
		{
			name: "TLS with private brokers only",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringTls: aws.String("broker1:9094,broker2:9094"),
			},
			authType:  internaltypes.AuthTypeTLS,
			wantAddrs: []string{"broker1:9094", "broker2:9094"},
			wantError: false,
		},
		{
			name: "Unauthenticated with plain brokers only",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerString: aws.String("broker1:9092,broker2:9092"),
			},
			authType:  internaltypes.AuthTypeUnauthenticated,
			wantAddrs: []string{"broker1:9092", "broker2:9092"},
			wantError: false,
		},
		{
			name: "handles single broker",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslIam: aws.String("broker1:9098"),
			},
			authType:  internaltypes.AuthTypeIAM,
			wantAddrs: []string{"broker1:9098"},
			wantError: false,
		},
		{
			name: "handles brokers with extra spaces and commas",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslIam: aws.String(" , broker1:9098 , , broker2:9098 , "),
			},
			authType:  internaltypes.AuthTypeIAM,
			wantAddrs: []string{"broker1:9098", "broker2:9098"},
			wantError: false,
		},
		{
			name: "handles brokers with mixed spacing",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslIam: aws.String("broker1:9098, broker2:9098 ,broker3:9098"),
			},
			authType:  internaltypes.AuthTypeIAM,
			wantAddrs: []string{"broker1:9098", "broker2:9098", "broker3:9098"},
			wantError: false,
		},
		{
			name: "handles nil broker strings",
			brokers: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringPublicSaslIam: nil,
			},
			authType:  internaltypes.AuthTypeIAM,
			wantAddrs: nil,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mskService := NewTestMSKService(nil) // No client needed for this method
			result, err := mskService.ParseBrokerAddresses(tt.brokers, tt.authType)

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.ElementsMatch(t, tt.wantAddrs, result)
		})
	}
}

func TestMSKService_ContextHandling(t *testing.T) {
	t.Run("handles cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		mockClient := &MockKafkaClient{
			DescribeClusterV2Func: func(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error) {
				// Check if context is cancelled
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
					return &kafka.DescribeClusterV2Output{
						ClusterInfo: &types.Cluster{
							ClusterArn: aws.String("test-cluster-arn"),
						},
					}, nil
				}
			},
		}

		mskService := NewTestMSKService(mockClient)
		_, err := mskService.DescribeCluster(ctx, aws.String("test-cluster-arn"))

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "context canceled")
	})

	t.Run("handles timeout context", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 0) // Immediate timeout
		defer cancel()

		mockClient := &MockKafkaClient{
			DescribeClusterV2Func: func(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
					return &kafka.DescribeClusterV2Output{
						ClusterInfo: &types.Cluster{
							ClusterArn: aws.String("test-cluster-arn"),
						},
					}, nil
				}
			},
		}

		mskService := NewTestMSKService(mockClient)
		_, err := mskService.DescribeCluster(ctx, aws.String("test-cluster-arn"))

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "context deadline exceeded")
	})
}

func TestMSKService_NilInputHandling(t *testing.T) {
	t.Run("handles nil cluster ARN in DescribeCluster", func(t *testing.T) {
		mockClient := &MockKafkaClient{
			DescribeClusterV2Func: func(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error) {
				assert.Nil(t, params.ClusterArn)
				return nil, fmt.Errorf("expected error for nil cluster ARN")
			},
		}

		mskService := NewTestMSKService(mockClient)
		_, err := mskService.DescribeCluster(context.Background(), nil)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expected error for nil cluster ARN")
	})

	t.Run("handles nil cluster ARN in GetBootstrapBrokers", func(t *testing.T) {
		mockClient := &MockKafkaClient{
			GetBootstrapBrokersFunc: func(ctx context.Context, params *kafka.GetBootstrapBrokersInput, optFns ...func(*kafka.Options)) (*kafka.GetBootstrapBrokersOutput, error) {
				assert.Nil(t, params.ClusterArn)
				return nil, fmt.Errorf("expected error for nil cluster ARN")
			},
		}

		mskService := NewTestMSKService(mockClient)
		_, err := mskService.GetBootstrapBrokers(context.Background(), nil)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expected error for nil cluster ARN")
	})
}

func TestMSKService_IsFetchFromFollowerEnabled_EdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		cluster      types.Cluster
		mockRevision *kafka.DescribeConfigurationRevisionOutput
		mockError    error
		wantEnabled  *bool
		wantError    bool
	}{
		{
			name: "nil provisioned cluster",
			cluster: types.Cluster{
				Provisioned: nil,
			},
			mockRevision: nil,
			mockError:    nil,
			wantEnabled:  nil,
			wantError:    false,
		},
		{
			name: "nil current broker software info",
			cluster: types.Cluster{
				Provisioned: &types.Provisioned{
					CurrentBrokerSoftwareInfo: nil,
				},
			},
			mockRevision: nil,
			mockError:    nil,
			wantEnabled:  nil,
			wantError:    false,
		},
		{
			name: "nil configuration ARN",
			cluster: types.Cluster{
				Provisioned: &types.Provisioned{
					CurrentBrokerSoftwareInfo: &types.BrokerSoftwareInfo{
						ConfigurationArn:      nil,
						ConfigurationRevision: aws.Int64(1),
					},
				},
			},
			mockRevision: nil,
			mockError:    nil,
			wantEnabled:  nil,
			wantError:    false,
		},
		{
			name: "nil configuration revision",
			cluster: types.Cluster{
				Provisioned: &types.Provisioned{
					CurrentBrokerSoftwareInfo: &types.BrokerSoftwareInfo{
						ConfigurationArn:      aws.String("config-arn"),
						ConfigurationRevision: nil,
					},
				},
			},
			mockRevision: nil,
			mockError:    nil,
			wantEnabled:  nil,
			wantError:    false,
		},
		{
			name: "empty server properties",
			cluster: types.Cluster{
				Provisioned: &types.Provisioned{
					CurrentBrokerSoftwareInfo: &types.BrokerSoftwareInfo{
						ConfigurationArn:      aws.String("config-arn"),
						ConfigurationRevision: aws.Int64(1),
					},
				},
			},
			mockRevision: &kafka.DescribeConfigurationRevisionOutput{
				ServerProperties: []byte(""),
			},
			mockError:   nil,
			wantEnabled: aws.Bool(false),
			wantError:   false,
		},
		{
			name: "server properties with different replica selector",
			cluster: types.Cluster{
				Provisioned: &types.Provisioned{
					CurrentBrokerSoftwareInfo: &types.BrokerSoftwareInfo{
						ConfigurationArn:      aws.String("config-arn"),
						ConfigurationRevision: aws.Int64(1),
					},
				},
			},
			mockRevision: &kafka.DescribeConfigurationRevisionOutput{
				ServerProperties: []byte("replica.selector.class=org.apache.kafka.common.replica.SomeOtherSelector"),
			},
			mockError:   nil,
			wantEnabled: aws.Bool(false),
			wantError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockKafkaClient{
				DescribeConfigurationRevisionFunc: func(ctx context.Context, params *kafka.DescribeConfigurationRevisionInput, optFns ...func(*kafka.Options)) (*kafka.DescribeConfigurationRevisionOutput, error) {
					if tt.cluster.Provisioned != nil &&
						tt.cluster.Provisioned.CurrentBrokerSoftwareInfo != nil &&
						tt.cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationArn != nil &&
						tt.cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationRevision != nil {
						assert.Equal(t, *tt.cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationArn, *params.Arn)
						assert.Equal(t, *tt.cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationRevision, *params.Revision)
					}
					return tt.mockRevision, tt.mockError
				},
			}

			mskService := NewTestMSKService(mockClient)
			result, err := mskService.IsFetchFromFollowerEnabled(context.Background(), tt.cluster)

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			if tt.wantEnabled == nil {
				assert.Nil(t, result)
			} else {
				assert.Equal(t, *tt.wantEnabled, *result)
			}
		})
	}
}

func TestMSKService_Integration_ClusterScan(t *testing.T) {
	t.Run("complete cluster scan workflow", func(t *testing.T) {
		clusterArn := "test-cluster-arn"

		mockClient := &MockKafkaClient{
			DescribeClusterV2Func: func(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error) {
				assert.Equal(t, clusterArn, *params.ClusterArn)
				return &kafka.DescribeClusterV2Output{
					ClusterInfo: &types.Cluster{
						ClusterArn:  aws.String(clusterArn),
						ClusterName: aws.String("test-cluster"),
						State:       types.ClusterStateActive,
						Provisioned: &types.Provisioned{
							CurrentBrokerSoftwareInfo: &types.BrokerSoftwareInfo{
								ConfigurationArn:      aws.String("config-arn"),
								ConfigurationRevision: aws.Int64(1),
							},
						},
					},
				}, nil
			},
			GetBootstrapBrokersFunc: func(ctx context.Context, params *kafka.GetBootstrapBrokersInput, optFns ...func(*kafka.Options)) (*kafka.GetBootstrapBrokersOutput, error) {
				assert.Equal(t, clusterArn, *params.ClusterArn)
				return &kafka.GetBootstrapBrokersOutput{
					BootstrapBrokerStringPublicSaslIam: aws.String("broker1:9098,broker2:9098"),
				}, nil
			},
			DescribeConfigurationRevisionFunc: func(ctx context.Context, params *kafka.DescribeConfigurationRevisionInput, optFns ...func(*kafka.Options)) (*kafka.DescribeConfigurationRevisionOutput, error) {
				return &kafka.DescribeConfigurationRevisionOutput{
					ServerProperties: []byte("replica.selector.class=org.apache.kafka.common.replica.RackAwareReplicaSelector"),
				}, nil
			},
			ListClientVpcConnectionsFunc: func(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error) {
				assert.Equal(t, clusterArn, *params.ClusterArn)
				return &kafka.ListClientVpcConnectionsOutput{
					ClientVpcConnections: []types.ClientVpcConnection{
						{VpcConnectionArn: aws.String("vpc-conn-1")},
					},
				}, nil
			},
			ListNodesFunc: func(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error) {
				assert.Equal(t, clusterArn, *params.ClusterArn)
				return &kafka.ListNodesOutput{
					NodeInfoList: []types.NodeInfo{
						{NodeARN: aws.String("node-1")},
					},
				}, nil
			},
		}

		mskService := NewTestMSKService(mockClient)

		// Test complete workflow
		cluster, err := mskService.DescribeCluster(context.Background(), aws.String(clusterArn))
		require.NoError(t, err)
		assert.Equal(t, clusterArn, *cluster.ClusterArn)

		brokers, err := mskService.GetBootstrapBrokers(context.Background(), aws.String(clusterArn))
		require.NoError(t, err)
		assert.NotNil(t, brokers.BootstrapBrokerStringPublicSaslIam)

		brokerAddrs, err := mskService.ParseBrokerAddresses(*brokers, internaltypes.AuthTypeIAM)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"broker1:9098", "broker2:9098"}, brokerAddrs)

		fetchEnabled, err := mskService.IsFetchFromFollowerEnabled(context.Background(), *cluster)
		require.NoError(t, err)
		assert.True(t, *fetchEnabled)

		vpcConnections, err := mskService.ListClientVpcConnections(context.Background(), aws.String(clusterArn))
		require.NoError(t, err)
		assert.Len(t, vpcConnections, 1)

		nodes, err := mskService.ListNodes(context.Background(), aws.String(clusterArn))
		require.NoError(t, err)
		assert.Len(t, nodes, 1)
	})
}

func TestMSKService_ErrorMessages(t *testing.T) {
	t.Run("error messages contain expected prefixes", func(t *testing.T) {
		mockClient := &MockKafkaClient{
			DescribeClusterV2Func: func(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error) {
				return nil, fmt.Errorf("AWS API error")
			},
			GetBootstrapBrokersFunc: func(ctx context.Context, params *kafka.GetBootstrapBrokersInput, optFns ...func(*kafka.Options)) (*kafka.GetBootstrapBrokersOutput, error) {
				return nil, fmt.Errorf("AWS API error")
			},
		}

		mskService := NewTestMSKService(mockClient)

		// Test error message format
		_, err := mskService.DescribeCluster(context.Background(), aws.String("test-arn"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "❌ Failed to describe cluster:")

		_, err = mskService.GetBootstrapBrokers(context.Background(), aws.String("test-arn"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "❌ Failed to get bootstrap brokers:")
	})

	t.Run("ParseBrokerAddresses error messages", func(t *testing.T) {
		mskService := NewTestMSKService(nil)

		// Test unsupported auth type error
		_, err := mskService.ParseBrokerAddresses(kafka.GetBootstrapBrokersOutput{}, internaltypes.AuthType("INVALID"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "❌ Auth type: INVALID not yet supported")

		// Test no brokers found error
		_, err = mskService.ParseBrokerAddresses(kafka.GetBootstrapBrokersOutput{}, internaltypes.AuthTypeIAM)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "❌ No SASL/IAM brokers found in the cluster")
	})
}
