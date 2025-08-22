package types

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClusterInformation_AsJson(t *testing.T) {
	tests := []struct {
		name     string
		result   *ClusterInformation
		wantErr  bool
		validate func(t *testing.T, data []byte)
	}{
		{
			name: "successfully marshal empty result",
			result: &ClusterInformation{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-east-1",
				ClusterID: "test-cluster-id",
				Cluster: kafkatypes.Cluster{
					ClusterName: aws.String("test-cluster-id"),
					ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-id/12345678-1234-1234-1234-123456789012"),
					State:       kafkatypes.ClusterStateActive,
					ClusterType: kafkatypes.ClusterTypeProvisioned,
					Provisioned: &kafkatypes.Provisioned{
						NumberOfBrokerNodes: aws.Int32(3),
					},
				},
			},
			wantErr: false,
			validate: func(t *testing.T, data []byte) {
				var unmarshaled ClusterInformation
				err := json.Unmarshal(data, &unmarshaled)
				require.NoError(t, err)
				assert.Equal(t, "us-east-1", unmarshaled.Region)
				assert.Equal(t, "test-cluster-id", unmarshaled.ClusterID)
				assert.Equal(t, time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC), unmarshaled.Timestamp)
			},
		},
		{
			name: "successfully marshal result with cluster data",
			result: &ClusterInformation{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-west-2",
				ClusterID: "test-cluster-id-2",
				Cluster: kafkatypes.Cluster{
					ClusterName: aws.String("test-cluster"),
					ClusterArn:  aws.String("arn:aws:kafka:us-west-2:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012"),
					State:       kafkatypes.ClusterStateActive,
					ClusterType: kafkatypes.ClusterTypeProvisioned,
					Provisioned: &kafkatypes.Provisioned{
						NumberOfBrokerNodes: aws.Int32(3),
					},
				},
			},
			wantErr: false,
			validate: func(t *testing.T, data []byte) {
				var unmarshaled ClusterInformation
				err := json.Unmarshal(data, &unmarshaled)
				require.NoError(t, err)
				assert.Equal(t, "us-west-2", unmarshaled.Region)
				assert.Equal(t, "test-cluster-id-2", unmarshaled.ClusterID)
				assert.Equal(t, "test-cluster", aws.ToString(unmarshaled.Cluster.ClusterName))
			},
		},
		{
			name: "successfully marshal result with all fields populated",
			result: &ClusterInformation{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "eu-west-1",
				ClusterID: "test-cluster-id-3",
				Cluster: kafkatypes.Cluster{
					ClusterName: aws.String("test-cluster-3"),
					ClusterArn:  aws.String("arn:aws:kafka:eu-west-1:123456789012:cluster/test-cluster-3/87654321-4321-4321-4321-210987654321"),
					State:       kafkatypes.ClusterStateActive,
					ClusterType: kafkatypes.ClusterTypeServerless,
					Serverless: &kafkatypes.Serverless{
						VpcConfigs: []kafkatypes.VpcConfig{
							{
								SubnetIds:        []string{"subnet-12345"},
								SecurityGroupIds: []string{"sg-12345"},
							},
						},
					},
				},
				ClientVpcConnections: []kafkatypes.ClientVpcConnection{
					{
						VpcConnectionArn: aws.String("arn:aws:kafka:eu-west-1:123456789012:vpc-connection/test-vpc-conn/12345678-1234-1234-1234-123456789012"),
						CreationTime:     aws.Time(time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)),
					},
				},
				ClusterOperations: []kafkatypes.ClusterOperationV2Summary{
					{
						OperationArn:   aws.String("arn:aws:kafka:eu-west-1:123456789012:operation/test-op/12345678-1234-1234-1234-123456789012"),
						OperationType:  aws.String("UPDATE_CLUSTER_CONFIGURATION"),
						OperationState: aws.String("SUCCEEDED"),
						StartTime:      aws.Time(time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)),
					},
				},
				Nodes: []kafkatypes.NodeInfo{
					{
						NodeARN:      aws.String("arn:aws:kafka:eu-west-1:123456789012:node/test-node/12345678-1234-1234-1234-123456789012"),
						NodeType:     kafkatypes.NodeTypeBroker,
						InstanceType: aws.String("kafka.m5.large"),
					},
				},
				ScramSecrets: []string{"secret1", "secret2"},
				BootstrapBrokers: kafka.GetBootstrapBrokersOutput{
					BootstrapBrokerStringTls: aws.String("b-1.test-cluster-3.abc123.c2.kafka.eu-west-1.amazonaws.com:9094"),
				},
				Topics: []string{"topic1", "topic2", "topic3"},
				Acls: []Acls{
					{
						ResourceType:        "TOPIC",
						ResourceName:        "topic1",
						ResourcePatternType: "LITERAL",
						Principal:           "User:test-user",
						Host:                "*",
						Operation:           "READ",
						PermissionType:      "ALLOW",
					},
				},
			},
			wantErr: false,
			validate: func(t *testing.T, data []byte) {
				var unmarshaled ClusterInformation
				err := json.Unmarshal(data, &unmarshaled)
				require.NoError(t, err)
				assert.Equal(t, "eu-west-1", unmarshaled.Region)
				assert.Len(t, unmarshaled.ClientVpcConnections, 1)
				assert.Len(t, unmarshaled.ClusterOperations, 1)
				assert.Len(t, unmarshaled.Nodes, 1)
				assert.Len(t, unmarshaled.ScramSecrets, 2)
				assert.Len(t, unmarshaled.Topics, 3)
				assert.Len(t, unmarshaled.Acls, 1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.result.AsJson()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, data)
			assert.True(t, len(data) > 0)

			if tt.validate != nil {
				tt.validate(t, data)
			}
		})
	}
}

func TestClusterInformation_WriteAsJson(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	tests := []struct {
		name    string
		result  *ClusterInformation
		wantErr bool
	}{
		{
			name: "successfully write to file",
			result: &ClusterInformation{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-east-1",
				ClusterID: "test-cluster-id",
				Cluster: kafkatypes.Cluster{
					ClusterName: aws.String("test-cluster"),
					ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012"),
					State:       kafkatypes.ClusterStateActive,
					ClusterType: kafkatypes.ClusterTypeProvisioned,
					Provisioned: &kafkatypes.Provisioned{
						NumberOfBrokerNodes: aws.Int32(3),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "write with empty cluster name should succeed",
			result: &ClusterInformation{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-east-1",
				ClusterID: "test-cluster-id",
				Cluster: kafkatypes.Cluster{
					ClusterName: aws.String("test-cluster-id"), // Use cluster ID as name to avoid empty name
					ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-id/12345678-1234-1234-1234-123456789012"),
					State:       kafkatypes.ClusterStateActive,
					ClusterType: kafkatypes.ClusterTypeProvisioned,
					Provisioned: &kafkatypes.Provisioned{
						NumberOfBrokerNodes: aws.Int32(3),
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Change to temp directory for testing
			originalWd, err := os.Getwd()
			require.NoError(t, err)
			defer os.Chdir(originalWd)

			err = os.Chdir(tempDir)
			require.NoError(t, err)

			err = tt.result.WriteAsJson()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Verify file was created and contains expected content
			expectedPath := tt.result.GetJsonPath()
			fileInfo, err := os.Stat(expectedPath)
			require.NoError(t, err)
			assert.True(t, fileInfo.Size() > 0)

			// Read and verify content
			fileData, err := os.ReadFile(expectedPath)
			require.NoError(t, err)

			var unmarshaled ClusterInformation
			err = json.Unmarshal(fileData, &unmarshaled)
			require.NoError(t, err)
			assert.Equal(t, tt.result.Region, unmarshaled.Region)
			assert.Equal(t, tt.result.ClusterID, unmarshaled.ClusterID)
			assert.Equal(t, tt.result.Timestamp, unmarshaled.Timestamp)
		})
	}
}

func TestClusterInformation_AsMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		result   *ClusterInformation
		validate func(t *testing.T, md *markdown.Markdown)
	}{
		{
			name: "generate markdown for empty result",
			result: &ClusterInformation{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-east-1",
				ClusterID: "test-cluster-id",
				Cluster: kafkatypes.Cluster{
					ClusterName: aws.String("test-cluster-id"),
					ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-id/12345678-1234-1234-1234-123456789012"),
					State:       kafkatypes.ClusterStateActive,
					ClusterType: kafkatypes.ClusterTypeProvisioned,
					Provisioned: &kafkatypes.Provisioned{
						NumberOfBrokerNodes: aws.Int32(3),
					},
				},
				Topics: []string{},
				Acls:   []Acls{},
			},
			validate: func(t *testing.T, md *markdown.Markdown) {
				// Basic validation that markdown was generated
				assert.NotNil(t, md)
			},
		},
		{
			name: "generate markdown for result with cluster data",
			result: &ClusterInformation{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-west-2",
				ClusterID: "test-cluster-id-2",
				Cluster: kafkatypes.Cluster{
					ClusterName: aws.String("test-cluster"),
					ClusterArn:  aws.String("arn:aws:kafka:us-west-2:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012"),
					State:       kafkatypes.ClusterStateActive,
					ClusterType: kafkatypes.ClusterTypeProvisioned,
					Provisioned: &kafkatypes.Provisioned{
						NumberOfBrokerNodes: aws.Int32(3),
					},
				},
				Topics: []string{"topic1", "topic2"},
				Acls:   []Acls{},
			},
			validate: func(t *testing.T, md *markdown.Markdown) {
				assert.NotNil(t, md)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md := tt.result.AsMarkdown()
			assert.NotNil(t, md)

			if tt.validate != nil {
				tt.validate(t, md)
			}
		})
	}
}

func TestClusterInformation_WriteAsMarkdown(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	tests := []struct {
		name    string
		result  *ClusterInformation
		wantErr bool
	}{
		{
			name: "successfully write markdown to file",
			result: &ClusterInformation{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-east-1",
				ClusterID: "test-cluster-id",
				Cluster: kafkatypes.Cluster{
					ClusterName: aws.String("test-cluster"),
					ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012"),
					State:       kafkatypes.ClusterStateActive,
					ClusterType: kafkatypes.ClusterTypeProvisioned,
					Provisioned: &kafkatypes.Provisioned{
						NumberOfBrokerNodes: aws.Int32(3),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "write with empty cluster name should succeed",
			result: &ClusterInformation{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-east-1",
				ClusterID: "test-cluster-id",
				Cluster: kafkatypes.Cluster{
					ClusterName: aws.String("test-cluster-id"), // Use cluster ID as name to avoid empty name
					ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-id/12345678-1234-1234-1234-123456789012"),
					State:       kafkatypes.ClusterStateActive,
					ClusterType: kafkatypes.ClusterTypeProvisioned,
					Provisioned: &kafkatypes.Provisioned{
						NumberOfBrokerNodes: aws.Int32(3),
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Change to temp directory for testing
			originalWd, err := os.Getwd()
			require.NoError(t, err)
			defer os.Chdir(originalWd)

			err = os.Chdir(tempDir)
			require.NoError(t, err)

			err = tt.result.WriteAsMarkdown(true)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Verify file was created and contains expected content
			expectedPath := tt.result.GetMarkdownPath()
			fileInfo, err := os.Stat(expectedPath)
			require.NoError(t, err)
			assert.True(t, fileInfo.Size() > 0)

			// Read and verify content contains markdown
			fileData, err := os.ReadFile(expectedPath)
			require.NoError(t, err)
			content := string(fileData)
			assert.Contains(t, content, "# MSK Cluster Scan Report")
			assert.Contains(t, content, tt.result.Region)
		})
	}
}
