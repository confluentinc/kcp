package region

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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp-internal/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	defaultMaxResults = 100
	defaultRegion     = "us-west-2"
)

type MockRegionScannerMSKClient struct {
	ListClustersV2Func                func(ctx context.Context, params *kafka.ListClustersV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClustersV2Output, error)
	ListVpcConnectionsFunc            func(ctx context.Context, params *kafka.ListVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListVpcConnectionsOutput, error)
	ListConfigurationsFunc            func(ctx context.Context, params *kafka.ListConfigurationsInput, optFns ...func(*kafka.Options)) (*kafka.ListConfigurationsOutput, error)
	DescribeConfigurationRevisionFunc func(ctx context.Context, params *kafka.DescribeConfigurationRevisionInput, optFns ...func(*kafka.Options)) (*kafka.DescribeConfigurationRevisionOutput, error)
	ListKafkaVersionsFunc             func(ctx context.Context, params *kafka.ListKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.ListKafkaVersionsOutput, error)
	ListReplicatorsFunc               func(ctx context.Context, params *kafka.ListReplicatorsInput, optFns ...func(*kafka.Options)) (*kafka.ListReplicatorsOutput, error)
	DescribeReplicatorFunc            func(ctx context.Context, params *kafka.DescribeReplicatorInput, optFns ...func(*kafka.Options)) (*kafka.DescribeReplicatorOutput, error)
}

// MockAuthenticationSummarizer provides a simple mock for testing
type MockAuthenticationSummarizer struct {
	SummariseAuthenticationFunc func(cluster kafkatypes.Cluster) string
}

func (m *MockAuthenticationSummarizer) SummariseAuthentication(cluster kafkatypes.Cluster) string {
	if m.SummariseAuthenticationFunc != nil {
		return m.SummariseAuthenticationFunc(cluster)
	}
	return "MOCKED_AUTH"
}

func (m *MockRegionScannerMSKClient) ListClustersV2(ctx context.Context, params *kafka.ListClustersV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClustersV2Output, error) {
	return m.ListClustersV2Func(ctx, params, optFns...)
}

func (m *MockRegionScannerMSKClient) ListVpcConnections(ctx context.Context, params *kafka.ListVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListVpcConnectionsOutput, error) {
	return m.ListVpcConnectionsFunc(ctx, params, optFns...)
}

func (m *MockRegionScannerMSKClient) ListConfigurations(ctx context.Context, params *kafka.ListConfigurationsInput, optFns ...func(*kafka.Options)) (*kafka.ListConfigurationsOutput, error) {
	return m.ListConfigurationsFunc(ctx, params, optFns...)
}

func (m *MockRegionScannerMSKClient) DescribeConfigurationRevision(ctx context.Context, params *kafka.DescribeConfigurationRevisionInput, optFns ...func(*kafka.Options)) (*kafka.DescribeConfigurationRevisionOutput, error) {
	return m.DescribeConfigurationRevisionFunc(ctx, params, optFns...)
}

func (m *MockRegionScannerMSKClient) ListKafkaVersions(ctx context.Context, params *kafka.ListKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.ListKafkaVersionsOutput, error) {
	return m.ListKafkaVersionsFunc(ctx, params, optFns...)
}

func (m *MockRegionScannerMSKClient) ListReplicators(ctx context.Context, params *kafka.ListReplicatorsInput, optFns ...func(*kafka.Options)) (*kafka.ListReplicatorsOutput, error) {
	return m.ListReplicatorsFunc(ctx, params, optFns...)
}

func (m *MockRegionScannerMSKClient) DescribeReplicator(ctx context.Context, params *kafka.DescribeReplicatorInput, optFns ...func(*kafka.Options)) (*kafka.DescribeReplicatorOutput, error) {
	return m.DescribeReplicatorFunc(ctx, params, optFns...)
}

func TestScanner_ListClusters(t *testing.T) {
	tests := []struct {
		name       string
		mockOutput *kafka.ListClustersV2Output
		mockError  error
		wantCount  int
		wantError  string
	}{
		{
			name: "lists_clusters_successfully",
			mockOutput: &kafka.ListClustersV2Output{
				ClusterInfoList: []kafkatypes.Cluster{
					{
						ClusterName: aws.String("test-cluster-1"),
						ClusterArn:  aws.String("test-arn-1"),
						State:       kafkatypes.ClusterStateActive,
						ClusterType: kafkatypes.ClusterTypeProvisioned,
						Provisioned: &kafkatypes.Provisioned{
							BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
								BrokerAZDistribution: kafkatypes.BrokerAZDistributionDefault,
								InstanceType:         aws.String("kafka.m5.large"),
								ConnectivityInfo: &kafkatypes.ConnectivityInfo{
									PublicAccess: &kafkatypes.PublicAccess{
										Type: aws.String("DISABLED"),
									},
								},
							},
							CurrentBrokerSoftwareInfo: &kafkatypes.BrokerSoftwareInfo{
								KafkaVersion: aws.String("2.8.1"),
							},
							EncryptionInfo: &kafkatypes.EncryptionInfo{
								EncryptionInTransit: &kafkatypes.EncryptionInTransit{
									ClientBroker: kafkatypes.ClientBrokerTls,
								},
							},
							ClientAuthentication: &kafkatypes.ClientAuthentication{
								Sasl: &kafkatypes.Sasl{
									Scram: &kafkatypes.Scram{
										Enabled: aws.Bool(true),
									},
									Iam: &kafkatypes.Iam{
										Enabled: aws.Bool(false),
									},
								},
								Tls: &kafkatypes.Tls{
									Enabled: aws.Bool(false),
								},
								Unauthenticated: &kafkatypes.Unauthenticated{
									Enabled: aws.Bool(false),
								},
							},
						},
						CreationTime: aws.Time(time.Now()),
					},
					{
						ClusterName: aws.String("test-cluster-2"),
						ClusterArn:  aws.String("test-arn-2"),
						State:       kafkatypes.ClusterStateActive,
						ClusterType: kafkatypes.ClusterTypeProvisioned,
						Provisioned: &kafkatypes.Provisioned{
							BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
								BrokerAZDistribution: kafkatypes.BrokerAZDistributionDefault,
								InstanceType:         aws.String("kafka.m5.large"),
								ConnectivityInfo: &kafkatypes.ConnectivityInfo{
									PublicAccess: &kafkatypes.PublicAccess{
										Type: aws.String("DISABLED"),
									},
								},
							},
							CurrentBrokerSoftwareInfo: &kafkatypes.BrokerSoftwareInfo{
								KafkaVersion: aws.String("2.8.1"),
							},
							EncryptionInfo: &kafkatypes.EncryptionInfo{
								EncryptionInTransit: &kafkatypes.EncryptionInTransit{
									ClientBroker: kafkatypes.ClientBrokerTls,
								},
							},
							ClientAuthentication: &kafkatypes.ClientAuthentication{
								Sasl: &kafkatypes.Sasl{
									Scram: &kafkatypes.Scram{
										Enabled: aws.Bool(false),
									},
									Iam: &kafkatypes.Iam{
										Enabled: aws.Bool(true),
									},
								},
								Tls: &kafkatypes.Tls{
									Enabled: aws.Bool(true),
								},
								Unauthenticated: &kafkatypes.Unauthenticated{
									Enabled: aws.Bool(false),
								},
							},
						},
						CreationTime: aws.Time(time.Now()),
					},
				},
			},
			wantCount: 2,
		},
		{
			name:       "handles empty cluster list",
			mockOutput: &kafka.ListClustersV2Output{},
			wantCount:  0,
		},
		{
			name:      "handles AWS API error",
			mockError: errors.New("AWS API error"),
			wantError: "❌ Failed to list clusters: AWS API error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRegionScannerMSKClient := &MockRegionScannerMSKClient{
				ListClustersV2Func: func(ctx context.Context, params *kafka.ListClustersV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClustersV2Output, error) {
					return tt.mockOutput, tt.mockError
				},
			}

			opts := ScanRegionOpts{
				Region: defaultRegion,
			}
			regionScanner := NewRegionScanner(mockRegionScannerMSKClient, opts)
			clusters, err := regionScanner.listClusters(context.Background(), defaultMaxResults)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantCount, len(clusters))
		})
	}
}

func TestScanner_Run(t *testing.T) {
	// Create a read-only directory for testing file write errors
	readOnlyDir := filepath.Join(os.TempDir(), "readonly_test_dir")
	err := os.MkdirAll(readOnlyDir, 0400)
	require.NoError(t, err)
	defer os.RemoveAll(readOnlyDir)

	tests := []struct {
		name       string
		region     string
		mockOutput *kafka.ListClustersV2Output
		mockError  error
		wantError  string
	}{
		{
			name:   "successful end-to-end scan",
			region: defaultRegion,
			mockOutput: &kafka.ListClustersV2Output{
				ClusterInfoList: []kafkatypes.Cluster{
					{
						ClusterName: aws.String("test-cluster-1"),
						ClusterArn:  aws.String("test-arn-1"),
						State:       kafkatypes.ClusterStateActive,
						ClusterType: kafkatypes.ClusterTypeProvisioned,
						Provisioned: &kafkatypes.Provisioned{
							BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
								BrokerAZDistribution: kafkatypes.BrokerAZDistributionDefault,
								InstanceType:         aws.String("kafka.m5.large"),
								ConnectivityInfo: &kafkatypes.ConnectivityInfo{
									PublicAccess: &kafkatypes.PublicAccess{
										Type: aws.String("DISABLED"),
									},
								},
							},
							CurrentBrokerSoftwareInfo: &kafkatypes.BrokerSoftwareInfo{
								KafkaVersion: aws.String("2.8.1"),
							},
							EncryptionInfo: &kafkatypes.EncryptionInfo{
								EncryptionInTransit: &kafkatypes.EncryptionInTransit{
									ClientBroker: kafkatypes.ClientBrokerTls,
								},
							},
							NumberOfBrokerNodes: aws.Int32(3),
							ClientAuthentication: &kafkatypes.ClientAuthentication{
								Sasl: &kafkatypes.Sasl{
									Scram: &kafkatypes.Scram{
										Enabled: aws.Bool(false),
									},
									Iam: &kafkatypes.Iam{
										Enabled: aws.Bool(false),
									},
								},
								Tls: &kafkatypes.Tls{
									Enabled: aws.Bool(false),
								},
								Unauthenticated: &kafkatypes.Unauthenticated{
									Enabled: aws.Bool(false),
								},
							},
						},
						CreationTime: aws.Time(time.Now()),
					},
				},
			},
		},
		{
			name:      "handles AWS API error",
			region:    defaultRegion,
			mockError: errors.New("AWS API error"),
			wantError: "❌ Failed to list clusters: AWS API error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRegionScannerMSKClient := &MockRegionScannerMSKClient{
				ListClustersV2Func: func(ctx context.Context, params *kafka.ListClustersV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClustersV2Output, error) {
					return tt.mockOutput, tt.mockError
				},
				ListVpcConnectionsFunc: func(ctx context.Context, params *kafka.ListVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListVpcConnectionsOutput, error) {
					return &kafka.ListVpcConnectionsOutput{}, nil
				},
				ListConfigurationsFunc: func(ctx context.Context, params *kafka.ListConfigurationsInput, optFns ...func(*kafka.Options)) (*kafka.ListConfigurationsOutput, error) {
					return &kafka.ListConfigurationsOutput{}, nil
				},
				DescribeConfigurationRevisionFunc: func(ctx context.Context, params *kafka.DescribeConfigurationRevisionInput, optFns ...func(*kafka.Options)) (*kafka.DescribeConfigurationRevisionOutput, error) {
					return &kafka.DescribeConfigurationRevisionOutput{}, nil
				},
				ListKafkaVersionsFunc: func(ctx context.Context, params *kafka.ListKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.ListKafkaVersionsOutput, error) {
					return &kafka.ListKafkaVersionsOutput{}, nil
				},
				ListReplicatorsFunc: func(ctx context.Context, params *kafka.ListReplicatorsInput, optFns ...func(*kafka.Options)) (*kafka.ListReplicatorsOutput, error) {
					return &kafka.ListReplicatorsOutput{}, nil
				},
				DescribeReplicatorFunc: func(ctx context.Context, params *kafka.DescribeReplicatorInput, optFns ...func(*kafka.Options)) (*kafka.DescribeReplicatorOutput, error) {
					return &kafka.DescribeReplicatorOutput{}, nil
				},
			}

			opts := ScanRegionOpts{
				Region: tt.region,
			}
			regionScanner := NewRegionScanner(mockRegionScannerMSKClient, opts)
			err := regionScanner.Run()

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)

			// Verify the output file exists and contains valid JSON
			jsonFilePath := fmt.Sprintf("region_scan_%s.json", tt.region)
			data, err := os.ReadFile(jsonFilePath)
			require.NoError(t, err)

			var result types.RegionScanResult
			err = json.Unmarshal(data, &result)
			require.NoError(t, err)

			assert.Equal(t, tt.region, result.Region)
			assert.NotZero(t, result.Timestamp)
			assert.Equal(t, len(tt.mockOutput.ClusterInfoList), len(result.Clusters))

			// Cleanup test file
			markDownFilePath := fmt.Sprintf("region_scan_%s.md", tt.region)
			os.Remove(jsonFilePath)
			os.Remove(markDownFilePath)
		})
	}
}

func TestScanner_HandlePagination(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListClustersV2Output
		wantTotal     int
		wantError     string
	}{
		{
			name: "handles pagination with multiple pages",
			mockResponses: []*kafka.ListClustersV2Output{
				{
					ClusterInfoList: []kafkatypes.Cluster{
						{
							ClusterName: aws.String("cluster-page-1-item-1"),
							ClusterArn:  aws.String("arn-page-1-item-1"),
							State:       kafkatypes.ClusterStateActive,
							ClusterType: kafkatypes.ClusterTypeProvisioned,
							Provisioned: &kafkatypes.Provisioned{
								ClientAuthentication: &kafkatypes.ClientAuthentication{
									Sasl: &kafkatypes.Sasl{
										Scram: &kafkatypes.Scram{Enabled: aws.Bool(false)},
										Iam:   &kafkatypes.Iam{Enabled: aws.Bool(false)},
									},
									Tls: &kafkatypes.Tls{Enabled: aws.Bool(false)},
								},
							},
						},
						{
							ClusterName: aws.String("cluster-page-1-item-2"),
							ClusterArn:  aws.String("arn-page-1-item-2"),
							State:       kafkatypes.ClusterStateActive,
							ClusterType: kafkatypes.ClusterTypeProvisioned,
							Provisioned: &kafkatypes.Provisioned{
								ClientAuthentication: &kafkatypes.ClientAuthentication{
									Sasl: &kafkatypes.Sasl{
										Scram: &kafkatypes.Scram{Enabled: aws.Bool(false)},
										Iam:   &kafkatypes.Iam{Enabled: aws.Bool(false)},
									},
									Tls: &kafkatypes.Tls{Enabled: aws.Bool(false)},
								},
							},
						},
					},
					NextToken: aws.String("next-page-token"),
				},
				{
					ClusterInfoList: []kafkatypes.Cluster{
						{
							ClusterName: aws.String("cluster-page-2-item-1"),
							ClusterArn:  aws.String("arn-page-2-item-1"),
							State:       kafkatypes.ClusterStateActive,
							ClusterType: kafkatypes.ClusterTypeProvisioned,
							Provisioned: &kafkatypes.Provisioned{
								ClientAuthentication: &kafkatypes.ClientAuthentication{
									Sasl: &kafkatypes.Sasl{
										Scram: &kafkatypes.Scram{Enabled: aws.Bool(false)},
										Iam:   &kafkatypes.Iam{Enabled: aws.Bool(false)},
									},
									Tls: &kafkatypes.Tls{Enabled: aws.Bool(false)},
								},
							},
						},
					},
					// No NextToken indicates last page
				},
			},
			wantTotal: 3,
		},
		{
			name: "handles single page with no NextToken",
			mockResponses: []*kafka.ListClustersV2Output{
				{
					ClusterInfoList: []kafkatypes.Cluster{
						{
							ClusterName: aws.String("single-page-cluster"),
							ClusterArn:  aws.String("single-page-arn"),
							State:       kafkatypes.ClusterStateActive,
							ClusterType: kafkatypes.ClusterTypeProvisioned,
							Provisioned: &kafkatypes.Provisioned{
								ClientAuthentication: &kafkatypes.ClientAuthentication{
									Sasl: &kafkatypes.Sasl{
										Scram: &kafkatypes.Scram{Enabled: aws.Bool(false)},
										Iam:   &kafkatypes.Iam{Enabled: aws.Bool(false)},
									},
									Tls: &kafkatypes.Tls{Enabled: aws.Bool(false)},
								},
							},
						},
					},
					// No NextToken
				},
			},
			wantTotal: 1,
		},
		{
			name: "handles empty first page with NextToken",
			mockResponses: []*kafka.ListClustersV2Output{
				{
					ClusterInfoList: []kafkatypes.Cluster{},
					NextToken:       aws.String("next-page-token"),
				},
				{
					ClusterInfoList: []kafkatypes.Cluster{
						{
							ClusterName: aws.String("cluster-on-second-page"),
							ClusterArn:  aws.String("arn-on-second-page"),
							State:       kafkatypes.ClusterStateActive,
							ClusterType: kafkatypes.ClusterTypeProvisioned,
							Provisioned: &kafkatypes.Provisioned{
								ClientAuthentication: &kafkatypes.ClientAuthentication{
									Sasl: &kafkatypes.Sasl{
										Scram: &kafkatypes.Scram{Enabled: aws.Bool(false)},
										Iam:   &kafkatypes.Iam{Enabled: aws.Bool(false)},
									},
									Tls: &kafkatypes.Tls{Enabled: aws.Bool(false)},
								},
							},
						},
					},
				},
			},
			wantTotal: 1,
		},
		{
			name: "handles multiple empty pages",
			mockResponses: []*kafka.ListClustersV2Output{
				{
					ClusterInfoList: []kafkatypes.Cluster{},
					NextToken:       aws.String("token-1"),
				},
				{
					ClusterInfoList: []kafkatypes.Cluster{},
					NextToken:       aws.String("token-2"),
				},
				{
					ClusterInfoList: []kafkatypes.Cluster{},
					// No NextToken - end of pages
				},
			},
			wantTotal: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockRegionScannerMSKClient := &MockRegionScannerMSKClient{
				ListClustersV2Func: func(ctx context.Context, params *kafka.ListClustersV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClustersV2Output, error) {
					if callCount >= len(tt.mockResponses) {
						t.Fatalf("Unexpected call to ListClustersV2, call count: %d, available responses: %d", callCount, len(tt.mockResponses))
					}

					response := tt.mockResponses[callCount]
					callCount++

					// Verify NextToken handling - if this isn't the first call, params should have NextToken
					if callCount > 1 {
						expectedToken := tt.mockResponses[callCount-2].NextToken
						if expectedToken != nil {
							require.NotNil(t, params.NextToken, "Expected NextToken to be set for paginated request")
							assert.Equal(t, *expectedToken, *params.NextToken, "NextToken should match previous response")
						}
					}

					return response, nil
				},
			}

			opts := ScanRegionOpts{
				Region: defaultRegion,
			}
			regionScanner := NewRegionScanner(mockRegionScannerMSKClient, opts)
			clusters, err := regionScanner.listClusters(context.Background(), defaultMaxResults)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(clusters), "Total clusters should match expected count")
			assert.Equal(t, len(tt.mockResponses), callCount, "Should have made the expected number of API calls")

			// Verify that all clusters from all pages are returned
			expectedClusterNames := make([]string, 0)
			for _, response := range tt.mockResponses {
				for _, cluster := range response.ClusterInfoList {
					if cluster.ClusterName != nil {
						expectedClusterNames = append(expectedClusterNames, *cluster.ClusterName)
					}
				}
			}

			actualClusterNames := make([]string, 0)
			for _, cluster := range clusters {
				actualClusterNames = append(actualClusterNames, cluster.ClusterName)
			}

			assert.ElementsMatch(t, expectedClusterNames, actualClusterNames, "All clusters from all pages should be returned")
		})
	}
}

func TestScanner_HandleVpcConnectionsPagination(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListVpcConnectionsOutput
		wantTotal     int
		wantError     string
	}{
		{
			name: "handles VPC connections pagination with multiple pages",
			mockResponses: []*kafka.ListVpcConnectionsOutput{
				{
					VpcConnections: []kafkatypes.VpcConnection{
						{
							VpcConnectionArn: aws.String("vpc-conn-1"),
							TargetClusterArn: aws.String("cluster-arn-1"),
						},
						{
							VpcConnectionArn: aws.String("vpc-conn-2"),
							TargetClusterArn: aws.String("cluster-arn-2"),
						},
					},
					NextToken: aws.String("vpc-next-token"),
				},
				{
					VpcConnections: []kafkatypes.VpcConnection{
						{
							VpcConnectionArn: aws.String("vpc-conn-3"),
							TargetClusterArn: aws.String("cluster-arn-3"),
						},
					},
				},
			},
			wantTotal: 3,
		},
		{
			name: "handles empty VPC connections with pagination",
			mockResponses: []*kafka.ListVpcConnectionsOutput{
				{
					VpcConnections: []kafkatypes.VpcConnection{},
					NextToken:      aws.String("empty-page-token"),
				},
				{
					VpcConnections: []kafkatypes.VpcConnection{
						{
							VpcConnectionArn: aws.String("vpc-conn-after-empty"),
							TargetClusterArn: aws.String("cluster-arn-after-empty"),
						},
					},
				},
			},
			wantTotal: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockRegionScannerMSKClient := &MockRegionScannerMSKClient{
				ListVpcConnectionsFunc: func(ctx context.Context, params *kafka.ListVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListVpcConnectionsOutput, error) {
					if callCount >= len(tt.mockResponses) {
						t.Fatalf("Unexpected call to ListVpcConnections, call count: %d, available responses: %d", callCount, len(tt.mockResponses))
					}

					response := tt.mockResponses[callCount]
					callCount++

					if callCount > 1 {
						expectedToken := tt.mockResponses[callCount-2].NextToken
						if expectedToken != nil {
							require.NotNil(t, params.NextToken, "Expected NextToken to be set for paginated request")
							assert.Equal(t, *expectedToken, *params.NextToken, "NextToken should match previous response")
						}
					}

					return response, nil
				},
			}

			opts := ScanRegionOpts{
				Region: defaultRegion,
			}
			regionScanner := NewRegionScanner(mockRegionScannerMSKClient, opts)
			connections, err := regionScanner.scanVpcConnections(context.Background(), defaultMaxResults)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(connections), "Total VPC connections should match expected count")
			assert.Equal(t, len(tt.mockResponses), callCount, "Should have made the expected number of API calls")
		})
	}
}

func TestScanner_HandleConfigurationsPagination(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListConfigurationsOutput
		mockRevisions map[string]*kafka.DescribeConfigurationRevisionOutput
		wantTotal     int
		wantError     string
	}{
		{
			name: "handles configurations pagination with multiple pages",
			mockResponses: []*kafka.ListConfigurationsOutput{
				{
					Configurations: []kafkatypes.Configuration{
						{
							Arn:  aws.String("config-arn-1"),
							Name: aws.String("config-1"),
							LatestRevision: &kafkatypes.ConfigurationRevision{
								Revision: aws.Int64(1),
							},
						},
					},
					NextToken: aws.String("config-next-token"),
				},
				{
					Configurations: []kafkatypes.Configuration{
						{
							Arn:  aws.String("config-arn-2"),
							Name: aws.String("config-2"),
							LatestRevision: &kafkatypes.ConfigurationRevision{
								Revision: aws.Int64(2),
							},
						},
					},
				},
			},
			mockRevisions: map[string]*kafka.DescribeConfigurationRevisionOutput{
				"config-arn-1": {
					Arn:              aws.String("config-arn-1"),
					Revision:         aws.Int64(1),
					ServerProperties: []byte("config.1=value1"),
				},
				"config-arn-2": {
					Arn:              aws.String("config-arn-2"),
					Revision:         aws.Int64(2),
					ServerProperties: []byte("config.2=value2"),
				},
			},
			wantTotal: 2,
		},
		{
			name: "handles configuration revision error during pagination",
			mockResponses: []*kafka.ListConfigurationsOutput{
				{
					Configurations: []kafkatypes.Configuration{
						{
							Arn:  aws.String("config-arn-error"),
							Name: aws.String("config-error"),
							LatestRevision: &kafkatypes.ConfigurationRevision{
								Revision: aws.Int64(1),
							},
						},
					},
				},
			},
			wantError: "error describing configuration revision",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockRegionScannerMSKClient := &MockRegionScannerMSKClient{
				ListConfigurationsFunc: func(ctx context.Context, params *kafka.ListConfigurationsInput, optFns ...func(*kafka.Options)) (*kafka.ListConfigurationsOutput, error) {
					if callCount >= len(tt.mockResponses) {
						t.Fatalf("Unexpected call to ListConfigurations, call count: %d, available responses: %d", callCount, len(tt.mockResponses))
					}

					response := tt.mockResponses[callCount]
					callCount++

					if callCount > 1 {
						expectedToken := tt.mockResponses[callCount-2].NextToken
						if expectedToken != nil {
							require.NotNil(t, params.NextToken, "Expected NextToken to be set for paginated request")
							assert.Equal(t, *expectedToken, *params.NextToken, "NextToken should match previous response")
						}
					}

					return response, nil
				},
				DescribeConfigurationRevisionFunc: func(ctx context.Context, params *kafka.DescribeConfigurationRevisionInput, optFns ...func(*kafka.Options)) (*kafka.DescribeConfigurationRevisionOutput, error) {
					if tt.mockRevisions != nil {
						if revision, exists := tt.mockRevisions[*params.Arn]; exists {
							return revision, nil
						}
					}
					return nil, errors.New("configuration revision not found")
				},
			}

			opts := ScanRegionOpts{
				Region: defaultRegion,
			}
			regionScanner := NewRegionScanner(mockRegionScannerMSKClient, opts)
			configurations, err := regionScanner.scanConfigurations(context.Background(), defaultMaxResults)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(configurations), "Total configurations should match expected count")
			assert.Equal(t, len(tt.mockResponses), callCount, "Should have made the expected number of API calls")
		})
	}
}

func TestScanner_HandleKafkaVersionsPagination(t *testing.T) {
	tests := []struct {
		name          string
		mockResponses []*kafka.ListKafkaVersionsOutput
		wantTotal     int
		wantError     string
	}{
		{
			name: "handles Kafka versions pagination with multiple pages",
			mockResponses: []*kafka.ListKafkaVersionsOutput{
				{
					KafkaVersions: []kafkatypes.KafkaVersion{
						{
							Version: aws.String("2.8.1"),
							Status:  kafkatypes.KafkaVersionStatusActive,
						},
						{
							Version: aws.String("2.8.2"),
							Status:  kafkatypes.KafkaVersionStatusActive,
						},
					},
					NextToken: aws.String("kafka-version-token"),
				},
				{
					KafkaVersions: []kafkatypes.KafkaVersion{
						{
							Version: aws.String("3.0.0"),
							Status:  kafkatypes.KafkaVersionStatusActive,
						},
					},
				},
			},
			wantTotal: 3,
		},
		{
			name: "handles empty Kafka versions page",
			mockResponses: []*kafka.ListKafkaVersionsOutput{
				{
					KafkaVersions: []kafkatypes.KafkaVersion{},
					NextToken:     aws.String("empty-versions-token"),
				},
				{
					KafkaVersions: []kafkatypes.KafkaVersion{
						{
							Version: aws.String("2.8.1"),
							Status:  kafkatypes.KafkaVersionStatusActive,
						},
					},
				},
			},
			wantTotal: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockRegionScannerMSKClient := &MockRegionScannerMSKClient{
				ListKafkaVersionsFunc: func(ctx context.Context, params *kafka.ListKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.ListKafkaVersionsOutput, error) {
					if callCount >= len(tt.mockResponses) {
						t.Fatalf("Unexpected call to ListKafkaVersions, call count: %d, available responses: %d", callCount, len(tt.mockResponses))
					}

					response := tt.mockResponses[callCount]
					callCount++

					if callCount > 1 {
						expectedToken := tt.mockResponses[callCount-2].NextToken
						if expectedToken != nil {
							require.NotNil(t, params.NextToken, "Expected NextToken to be set for paginated request")
							assert.Equal(t, *expectedToken, *params.NextToken, "NextToken should match previous response")
						}
					}

					return response, nil
				},
			}

			opts := ScanRegionOpts{
				Region: defaultRegion,
			}
			regionScanner := NewRegionScanner(mockRegionScannerMSKClient, opts)
			versions, err := regionScanner.scanKafkaVersions(context.Background(), defaultMaxResults)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(versions), "Total Kafka versions should match expected count")
			assert.Equal(t, len(tt.mockResponses), callCount, "Should have made the expected number of API calls")
		})
	}
}

func TestScanner_HandleReplicatorsPagination(t *testing.T) {
	tests := []struct {
		name             string
		mockResponses    []*kafka.ListReplicatorsOutput
		mockDescriptions map[string]*kafka.DescribeReplicatorOutput
		wantTotal        int
		wantError        string
	}{
		{
			name: "handles replicators pagination with multiple pages",
			mockResponses: []*kafka.ListReplicatorsOutput{
				{
					Replicators: []kafkatypes.ReplicatorSummary{
						{
							ReplicatorArn:  aws.String("replicator-arn-1"),
							ReplicatorName: aws.String("replicator-1"),
						},
					},
					NextToken: aws.String("replicator-next-token"),
				},
				{
					Replicators: []kafkatypes.ReplicatorSummary{
						{
							ReplicatorArn:  aws.String("replicator-arn-2"),
							ReplicatorName: aws.String("replicator-2"),
						},
					},
				},
			},
			mockDescriptions: map[string]*kafka.DescribeReplicatorOutput{
				"replicator-arn-1": {
					ReplicatorArn:         aws.String("replicator-arn-1"),
					ReplicatorName:        aws.String("replicator-1"),
					ReplicatorDescription: aws.String("Test replicator 1"),
				},
				"replicator-arn-2": {
					ReplicatorArn:         aws.String("replicator-arn-2"),
					ReplicatorName:        aws.String("replicator-2"),
					ReplicatorDescription: aws.String("Test replicator 2"),
				},
			},
			wantTotal: 2,
		},
		{
			name: "handles replicator description error during pagination",
			mockResponses: []*kafka.ListReplicatorsOutput{
				{
					Replicators: []kafkatypes.ReplicatorSummary{
						{
							ReplicatorArn:  aws.String("replicator-arn-error"),
							ReplicatorName: aws.String("replicator-error"),
						},
					},
				},
			},
			wantError: "error describing replicator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			mockRegionScannerMSKClient := &MockRegionScannerMSKClient{
				ListReplicatorsFunc: func(ctx context.Context, params *kafka.ListReplicatorsInput, optFns ...func(*kafka.Options)) (*kafka.ListReplicatorsOutput, error) {
					if callCount >= len(tt.mockResponses) {
						t.Fatalf("Unexpected call to ListReplicators, call count: %d, available responses: %d", callCount, len(tt.mockResponses))
					}

					response := tt.mockResponses[callCount]
					callCount++

					if callCount > 1 {
						expectedToken := tt.mockResponses[callCount-2].NextToken
						if expectedToken != nil {
							require.NotNil(t, params.NextToken, "Expected NextToken to be set for paginated request")
							assert.Equal(t, *expectedToken, *params.NextToken, "NextToken should match previous response")
						}
					}

					return response, nil
				},
				DescribeReplicatorFunc: func(ctx context.Context, params *kafka.DescribeReplicatorInput, optFns ...func(*kafka.Options)) (*kafka.DescribeReplicatorOutput, error) {
					if tt.mockDescriptions != nil {
						if description, exists := tt.mockDescriptions[*params.ReplicatorArn]; exists {
							return description, nil
						}
					}
					return nil, errors.New("replicator not found")
				},
			}

			opts := ScanRegionOpts{
				Region: defaultRegion,
			}
			regionScanner := NewRegionScanner(mockRegionScannerMSKClient, opts)
			replicators, err := regionScanner.scanReplicators(context.Background(), defaultMaxResults)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, len(replicators), "Total replicators should match expected count")
			assert.Equal(t, len(tt.mockResponses), callCount, "Should have made the expected number of API calls")
		})
	}
}

func TestScanner_SummariseAuthentication(t *testing.T) {
	tests := []struct {
		name     string
		cluster  kafkatypes.Cluster
		expected string
	}{
		{
			name: "serverless cluster with SASL/IAM enabled",
			cluster: kafkatypes.Cluster{
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
			},
			expected: "SASL/IAM",
		},
		{
			name: "serverless cluster with SASL/IAM disabled",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeServerless,
				Serverless: &kafkatypes.Serverless{
					ClientAuthentication: &kafkatypes.ServerlessClientAuthentication{
						Sasl: &kafkatypes.ServerlessSasl{
							Iam: &kafkatypes.Iam{
								Enabled: aws.Bool(false),
							},
						},
					},
				},
			},
			expected: "Unauthenticated",
		},
		{
			name: "provisioned cluster with only SASL/SCRAM enabled",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Scram: &kafkatypes.Scram{
								Enabled: aws.Bool(true),
							},
							Iam: &kafkatypes.Iam{
								Enabled: aws.Bool(false),
							},
						},
						Tls: &kafkatypes.Tls{
							Enabled: aws.Bool(false),
						},
					},
				},
			},
			expected: "SASL/SCRAM",
		},
		{
			name: "provisioned cluster with only SASL/IAM enabled",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Scram: &kafkatypes.Scram{
								Enabled: aws.Bool(false),
							},
							Iam: &kafkatypes.Iam{
								Enabled: aws.Bool(true),
							},
						},
						Tls: &kafkatypes.Tls{
							Enabled: aws.Bool(false),
						},
					},
				},
			},
			expected: "SASL/IAM",
		},
		{
			name: "provisioned cluster with only TLS enabled",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Scram: &kafkatypes.Scram{
								Enabled: aws.Bool(false),
							},
							Iam: &kafkatypes.Iam{
								Enabled: aws.Bool(false),
							},
						},
						Tls: &kafkatypes.Tls{
							Enabled: aws.Bool(true),
						},
					},
				},
			},
			expected: "TLS",
		},
		{
			name: "provisioned cluster with SASL/SCRAM and SASL/IAM enabled",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Scram: &kafkatypes.Scram{
								Enabled: aws.Bool(true),
							},
							Iam: &kafkatypes.Iam{
								Enabled: aws.Bool(true),
							},
						},
						Tls: &kafkatypes.Tls{
							Enabled: aws.Bool(false),
						},
					},
				},
			},
			expected: "SASL/SCRAM,SASL/IAM",
		},
		{
			name: "provisioned cluster with SASL/IAM and TLS enabled",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Scram: &kafkatypes.Scram{
								Enabled: aws.Bool(false),
							},
							Iam: &kafkatypes.Iam{
								Enabled: aws.Bool(true),
							},
						},
						Tls: &kafkatypes.Tls{
							Enabled: aws.Bool(true),
						},
					},
				},
			},
			expected: "SASL/IAM,TLS",
		},
		{
			name: "provisioned cluster with all authentication types enabled",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Scram: &kafkatypes.Scram{
								Enabled: aws.Bool(true),
							},
							Iam: &kafkatypes.Iam{
								Enabled: aws.Bool(true),
							},
						},
						Tls: &kafkatypes.Tls{
							Enabled: aws.Bool(true),
						},
					},
				},
			},
			expected: "SASL/SCRAM,SASL/IAM,TLS",
		},
		{
			name: "provisioned cluster with all authentication types disabled",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Scram: &kafkatypes.Scram{
								Enabled: aws.Bool(false),
							},
							Iam: &kafkatypes.Iam{
								Enabled: aws.Bool(false),
							},
						},
						Tls: &kafkatypes.Tls{
							Enabled: aws.Bool(false),
						},
					},
				},
			},
			expected: "Unauthenticated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ScanRegionOpts{
				Region: defaultRegion,
			}
			regionScanner := NewRegionScanner(nil, opts) // No client needed for this test
			result := regionScanner.authSummarizer.SummariseAuthentication(tt.cluster)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestScanner_ListClusters_Simplified demonstrates how tests can be simplified with mocked authentication
func TestScanner_ListClusters_Simplified(t *testing.T) {
	tests := []struct {
		name       string
		mockOutput *kafka.ListClustersV2Output
		mockError  error
		wantCount  int
		wantError  string
	}{
		{
			name: "lists clusters successfully with simplified mocks",
			mockOutput: &kafka.ListClustersV2Output{
				ClusterInfoList: []kafkatypes.Cluster{
					{
						ClusterName: aws.String("test-cluster-1"),
						ClusterArn:  aws.String("test-arn-1"),
						State:       kafkatypes.ClusterStateActive,
						ClusterType: kafkatypes.ClusterTypeProvisioned,
						// No need for complex authentication structures!
					},
					{
						ClusterName: aws.String("test-cluster-2"),
						ClusterArn:  aws.String("test-arn-2"),
						State:       kafkatypes.ClusterStateActive,
						ClusterType: kafkatypes.ClusterTypeServerless,
						// No need for complex authentication structures!
					},
				},
			},
			wantCount: 2,
		},
		{
			name:       "handles empty cluster list",
			mockOutput: &kafka.ListClustersV2Output{},
			wantCount:  0,
		},
		{
			name:      "handles AWS API error",
			mockError: errors.New("AWS API error"),
			wantError: "❌ Failed to list clusters: AWS API error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockMSKClient := &MockRegionScannerMSKClient{
				ListClustersV2Func: func(ctx context.Context, params *kafka.ListClustersV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClustersV2Output, error) {
					return tt.mockOutput, tt.mockError
				},
			}

			// Mock authentication summarizer to avoid complex cluster structures
			mockAuthSummarizer := &MockAuthenticationSummarizer{
				SummariseAuthenticationFunc: func(cluster kafkatypes.Cluster) string {
					// Simple logic based on cluster name for testing
					if strings.Contains(*cluster.ClusterName, "1") {
						return "SASL/SCRAM"
					}
					return "SASL/IAM"
				},
			}

			regionScanner := NewRegionScannerWithAuthSummarizer(defaultRegion, mockMSKClient, mockAuthSummarizer)
			clusters, err := regionScanner.listClusters(context.Background(), defaultMaxResults)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantCount, len(clusters))

			// Verify that authentication was properly mocked
			if len(clusters) > 0 {
				assert.Equal(t, "SASL/SCRAM", clusters[0].Authentication) // test-cluster-1
				if len(clusters) > 1 {
					assert.Equal(t, "SASL/IAM", clusters[1].Authentication) // test-cluster-2
				}
			}
		})
	}
}

func TestScanner_SummariseAuthentication_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		cluster  kafkatypes.Cluster
		expected string
	}{
		{
			name: "serverless cluster with nil authentication chain",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeServerless,
				Serverless:  &kafkatypes.Serverless{
					// ClientAuthentication is nil
				},
			},
			expected: "Unauthenticated",
		},
		{
			name: "serverless cluster with nil serverless config",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeServerless,
				// Serverless is nil
			},
			expected: "Unauthenticated",
		},
		{
			name: "provisioned cluster with nil provisioned config",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				// Provisioned is nil
			},
			expected: "Unauthenticated",
		},
		{
			name: "provisioned cluster with nil client authentication",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					// ClientAuthentication is nil
				},
			},
			expected: "Unauthenticated",
		},
		{
			name: "provisioned cluster with only unauthenticated enabled",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Scram: &kafkatypes.Scram{Enabled: aws.Bool(false)},
							Iam:   &kafkatypes.Iam{Enabled: aws.Bool(false)},
						},
						Tls: &kafkatypes.Tls{Enabled: aws.Bool(false)},
						Unauthenticated: &kafkatypes.Unauthenticated{
							Enabled: aws.Bool(true),
						},
					},
				},
			},
			expected: "Unauthenticated",
		},
		{
			name: "provisioned cluster with mixed authentication including unauthenticated",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Scram: &kafkatypes.Scram{Enabled: aws.Bool(true)},
							Iam:   &kafkatypes.Iam{Enabled: aws.Bool(false)},
						},
						Tls: &kafkatypes.Tls{Enabled: aws.Bool(false)},
						Unauthenticated: &kafkatypes.Unauthenticated{
							Enabled: aws.Bool(true),
						},
					},
				},
			},
			expected: "SASL/SCRAM,Unauthenticated",
		},
		{
			name: "unknown cluster type",
			cluster: kafkatypes.Cluster{
				ClusterType: "UNKNOWN_TYPE", // Invalid cluster type
			},
			expected: "Unauthenticated",
		},
		{
			name: "provisioned cluster with nil enabled fields",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Scram: &kafkatypes.Scram{
								// Enabled is nil
							},
							Iam: &kafkatypes.Iam{
								// Enabled is nil
							},
						},
						Tls: &kafkatypes.Tls{
							// Enabled is nil
						},
						Unauthenticated: &kafkatypes.Unauthenticated{
							// Enabled is nil
						},
					},
				},
			},
			expected: "Unauthenticated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ScanRegionOpts{
				Region: defaultRegion,
			}
			regionScanner := NewRegionScanner(nil, opts)
			result := regionScanner.authSummarizer.SummariseAuthentication(tt.cluster)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestScanner_ScanConfigurations_ErrorHandling(t *testing.T) {
	tests := []struct {
		name                       string
		listConfigsError           error
		describeRevisionError      error
		mockConfigurations         []kafkatypes.Configuration
		wantError                  string
		expectDescribeRevisionCall bool
	}{
		{
			name:             "handles ListConfigurations error",
			listConfigsError: errors.New("list configurations failed"),
			wantError:        "error listing configurations: list configurations failed",
		},
		{
			name: "handles DescribeConfigurationRevision error",
			mockConfigurations: []kafkatypes.Configuration{
				{
					Arn: aws.String("config-arn-1"),
					LatestRevision: &kafkatypes.ConfigurationRevision{
						Revision: aws.Int64(1),
					},
				},
			},
			describeRevisionError:      errors.New("describe revision failed"),
			expectDescribeRevisionCall: true,
			wantError:                  "error describing configuration revision: describe revision failed",
		},
		{
			name:               "handles empty configurations list",
			mockConfigurations: []kafkatypes.Configuration{},
			wantError:          "", // Should succeed with empty list
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			describeRevisionCalled := false
			mockMSKClient := &MockRegionScannerMSKClient{
				ListConfigurationsFunc: func(ctx context.Context, params *kafka.ListConfigurationsInput, optFns ...func(*kafka.Options)) (*kafka.ListConfigurationsOutput, error) {
					if tt.listConfigsError != nil {
						return nil, tt.listConfigsError
					}
					return &kafka.ListConfigurationsOutput{
						Configurations: tt.mockConfigurations,
					}, nil
				},
				DescribeConfigurationRevisionFunc: func(ctx context.Context, params *kafka.DescribeConfigurationRevisionInput, optFns ...func(*kafka.Options)) (*kafka.DescribeConfigurationRevisionOutput, error) {
					describeRevisionCalled = true
					if tt.describeRevisionError != nil {
						return nil, tt.describeRevisionError
					}
					return &kafka.DescribeConfigurationRevisionOutput{
						Arn:      params.Arn,
						Revision: params.Revision,
					}, nil
				},
			}

			opts := ScanRegionOpts{
				Region: defaultRegion,
			}

			regionScanner := NewRegionScanner(mockMSKClient, opts)
			_, err := regionScanner.scanConfigurations(context.Background(), 100)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
			} else {
				require.NoError(t, err)
			}

			if tt.expectDescribeRevisionCall {
				assert.True(t, describeRevisionCalled, "Expected DescribeConfigurationRevision to be called")
			}
		})
	}
}

func TestScanner_ScanReplicators_ErrorHandling(t *testing.T) {
	tests := []struct {
		name                    string
		listReplicatorsError    error
		describeReplicatorError error
		mockReplicators         []kafkatypes.ReplicatorSummary
		wantError               string
		expectDescribeCall      bool
	}{
		{
			name:                 "handles ListReplicators error",
			listReplicatorsError: errors.New("list replicators failed"),
			wantError:            "error listing replicators: list replicators failed",
		},
		{
			name: "handles DescribeReplicator error",
			mockReplicators: []kafkatypes.ReplicatorSummary{
				{
					ReplicatorArn: aws.String("replicator-arn-1"),
				},
			},
			describeReplicatorError: errors.New("describe replicator failed"),
			expectDescribeCall:      true,
			wantError:               "error describing replicator: describe replicator failed",
		},
		{
			name:            "handles empty replicators list",
			mockReplicators: []kafkatypes.ReplicatorSummary{},
			wantError:       "", // Should succeed with empty list
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			describeReplicatorCalled := false
			mockMSKClient := &MockRegionScannerMSKClient{
				ListReplicatorsFunc: func(ctx context.Context, params *kafka.ListReplicatorsInput, optFns ...func(*kafka.Options)) (*kafka.ListReplicatorsOutput, error) {
					if tt.listReplicatorsError != nil {
						return nil, tt.listReplicatorsError
					}
					return &kafka.ListReplicatorsOutput{
						Replicators: tt.mockReplicators,
					}, nil
				},
				DescribeReplicatorFunc: func(ctx context.Context, params *kafka.DescribeReplicatorInput, optFns ...func(*kafka.Options)) (*kafka.DescribeReplicatorOutput, error) {
					describeReplicatorCalled = true
					if tt.describeReplicatorError != nil {
						return nil, tt.describeReplicatorError
					}
					return &kafka.DescribeReplicatorOutput{
						ReplicatorArn: params.ReplicatorArn,
					}, nil
				},
			}

			opts := ScanRegionOpts{
				Region: defaultRegion,
			}
			regionScanner := NewRegionScanner(mockMSKClient, opts)
			_, err := regionScanner.scanReplicators(context.Background(), 100)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantError)
			} else {
				require.NoError(t, err)
			}

			if tt.expectDescribeCall {
				assert.True(t, describeReplicatorCalled, "Expected DescribeReplicator to be called")
			}
		})
	}
}

func TestScanner_PublicAccess_EdgeCases(t *testing.T) {
	tests := []struct {
		name           string
		cluster        kafkatypes.Cluster
		expectedAccess bool
	}{
		{
			name: "serverless cluster (should default to false)",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeServerless,
				Serverless:  &kafkatypes.Serverless{},
			},
			expectedAccess: false,
		},
		{
			name: "provisioned cluster with nil BrokerNodeGroupInfo",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					// BrokerNodeGroupInfo is nil
				},
			},
			expectedAccess: false,
		},
		{
			name: "provisioned cluster with nil ConnectivityInfo",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
						// ConnectivityInfo is nil
					},
				},
			},
			expectedAccess: false,
		},
		{
			name: "provisioned cluster with nil PublicAccess",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
						ConnectivityInfo: &kafkatypes.ConnectivityInfo{
							// PublicAccess is nil
						},
					},
				},
			},
			expectedAccess: false,
		},
		{
			name: "provisioned cluster with nil PublicAccess Type",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
						ConnectivityInfo: &kafkatypes.ConnectivityInfo{
							PublicAccess: &kafkatypes.PublicAccess{
								// Type is nil
							},
						},
					},
				},
			},
			expectedAccess: false,
		},
		{
			name: "provisioned cluster with DISABLED public access",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
						ConnectivityInfo: &kafkatypes.ConnectivityInfo{
							PublicAccess: &kafkatypes.PublicAccess{
								Type: aws.String("DISABLED"),
							},
						},
					},
				},
			},
			expectedAccess: false,
		},
		{
			name: "provisioned cluster with SERVICE_PROVIDED_EIPS public access",
			cluster: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
						ConnectivityInfo: &kafkatypes.ConnectivityInfo{
							PublicAccess: &kafkatypes.PublicAccess{
								Type: aws.String("SERVICE_PROVIDED_EIPS"),
							},
						},
					},
				},
			},
			expectedAccess: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockMSKClient := &MockRegionScannerMSKClient{
				ListClustersV2Func: func(ctx context.Context, params *kafka.ListClustersV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClustersV2Output, error) {
					return &kafka.ListClustersV2Output{
						ClusterInfoList: []kafkatypes.Cluster{tt.cluster},
					}, nil
				},
			}

			opts := ScanRegionOpts{
				Region: defaultRegion,
			}
			regionScanner := NewRegionScanner(mockMSKClient, opts)
			clusters, err := regionScanner.listClusters(context.Background(), 100)

			require.NoError(t, err)
			require.Len(t, clusters, 1)
			assert.Equal(t, tt.expectedAccess, clusters[0].PublicAccess)
		})
	}
}
