package types

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegionScanResult_AsJson(t *testing.T) {
	tests := []struct {
		name     string
		result   *RegionScanResult
		wantErr  bool
		validate func(t *testing.T, data []byte)
	}{
		{
			name: "successfully marshal empty result",
			result: &RegionScanResult{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-east-1",
			},
			wantErr: false,
			validate: func(t *testing.T, data []byte) {
				var unmarshaled RegionScanResult
				err := json.Unmarshal(data, &unmarshaled)
				require.NoError(t, err)
				assert.Equal(t, "us-east-1", unmarshaled.Region)
				assert.Equal(t, time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC), unmarshaled.Timestamp)
			},
		},
		{
			name: "successfully marshal result with clusters",
			result: &RegionScanResult{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-west-2",
				Clusters: []ClusterSummary{
					{
						ClusterName: "test-cluster-1",
						ClusterARN:  "arn:aws:kafka:us-west-2:123456789012:cluster/test-cluster-1/12345678-1234-1234-1234-123456789012",
						Status:      "ACTIVE",
						Type:        "PROVISIONED",
						Authentication: "SASL_SCRAM",
						PublicAccess: false,
						ClientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
					},
				},
			},
			wantErr: false,
			validate: func(t *testing.T, data []byte) {
				var unmarshaled RegionScanResult
				err := json.Unmarshal(data, &unmarshaled)
				require.NoError(t, err)
				assert.Equal(t, "us-west-2", unmarshaled.Region)
				assert.Len(t, unmarshaled.Clusters, 1)
				assert.Equal(t, "test-cluster-1", unmarshaled.Clusters[0].ClusterName)
			},
		},
		{
			name: "successfully marshal result with all fields populated",
			result: &RegionScanResult{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "eu-west-1",
				Clusters: []ClusterSummary{
					{
						ClusterName: "test-cluster-2",
						ClusterARN:  "arn:aws:kafka:eu-west-1:123456789012:cluster/test-cluster-2/87654321-4321-4321-4321-210987654321",
						Status:      "ACTIVE",
						Type:        "SERVERLESS",
						Authentication: "IAM",
						PublicAccess: true,
						ClientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
					},
				},
				VpcConnections: []kafkatypes.VpcConnection{
					{
						VpcConnectionArn: aws.String("arn:aws:kafka:eu-west-1:123456789012:vpc-connection/test-vpc-conn/12345678-1234-1234-1234-123456789012"),
						State:            "ACTIVE",
					},
				},
				Configurations: []kafka.DescribeConfigurationRevisionOutput{
					{
						Arn: aws.String("arn:aws:kafka:eu-west-1:123456789012:configuration/test-config/12345678-1234-1234-1234-123456789012"),
					},
				},
				KafkaVersions: []kafkatypes.KafkaVersion{
					{
						Version: aws.String("3.4.0"),
					},
				},
				Replicators: []kafka.DescribeReplicatorOutput{
					{
						ReplicatorArn: aws.String("arn:aws:kafka:eu-west-1:123456789012:replicator/test-replicator/12345678-1234-1234-1234-123456789012"),
					},
				},
			},
			wantErr: false,
			validate: func(t *testing.T, data []byte) {
				var unmarshaled RegionScanResult
				err := json.Unmarshal(data, &unmarshaled)
				require.NoError(t, err)
				assert.Equal(t, "eu-west-1", unmarshaled.Region)
				assert.Len(t, unmarshaled.Clusters, 1)
				assert.Len(t, unmarshaled.VpcConnections, 1)
				assert.Len(t, unmarshaled.Configurations, 1)
				assert.Len(t, unmarshaled.KafkaVersions, 1)
				assert.Len(t, unmarshaled.Replicators, 1)
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

func TestRegionScanResult_WriteAsJson(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	tests := []struct {
		name     string
		result   *RegionScanResult
		filePath string
		wantErr  bool
	}{
		{
			name: "successfully write to file",
			result: &RegionScanResult{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-east-1",
				Clusters: []ClusterSummary{
					{
						ClusterName: "test-cluster",
						ClusterARN:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012",
						Status:      "ACTIVE",
						Type:        "PROVISIONED",
						Authentication: "SASL_SCRAM",
						PublicAccess: false,
						ClientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
					},
				},
			},
			filePath: filepath.Join(tempDir, "test_output.json"),
			wantErr:  false,
		},
		{
			name: "write to invalid directory should fail",
			result: &RegionScanResult{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-east-1",
			},
			filePath: "/invalid/path/test.json",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.result.WriteAsJson(tt.filePath)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Verify file was created and contains expected content
			fileInfo, err := os.Stat(tt.filePath)
			require.NoError(t, err)
			assert.True(t, fileInfo.Size() > 0)

			// Read and verify content
			fileData, err := os.ReadFile(tt.filePath)
			require.NoError(t, err)

			var unmarshaled RegionScanResult
			err = json.Unmarshal(fileData, &unmarshaled)
			require.NoError(t, err)
			assert.Equal(t, tt.result.Region, unmarshaled.Region)
			assert.Equal(t, tt.result.Timestamp, unmarshaled.Timestamp)
		})
	}
}

func TestRegionScanResult_AsMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		result   *RegionScanResult
		validate func(t *testing.T, md *markdown.Markdown)
	}{
		{
			name: "generate markdown for empty result",
			result: &RegionScanResult{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-east-1",
			},
			validate: func(t *testing.T, md *markdown.Markdown) {
				// Basic validation that markdown was generated
				assert.NotNil(t, md)
			},
		},
		{
			name: "generate markdown for result with clusters",
			result: &RegionScanResult{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-west-2",
				Clusters: []ClusterSummary{
					{
						ClusterName: "test-cluster-1",
						ClusterARN:  "arn:aws:kafka:us-west-2:123456789012:cluster/test-cluster-1/12345678-1234-1234-1234-123456789012",
						Status:      "ACTIVE",
						Type:        "PROVISIONED",
						Authentication: "SASL_SCRAM",
						PublicAccess: false,
						ClientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
					},
					{
						ClusterName: "test-cluster-2",
						ClusterARN:  "arn:aws:kafka:us-west-2:123456789012:cluster/test-cluster-2/87654321-4321-4321-4321-210987654321",
						Status:      "ACTIVE",
						Type:        "SERVERLESS",
						Authentication: "IAM",
						PublicAccess: true,
						ClientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
					},
				},
				VpcConnections: []kafkatypes.VpcConnection{
					{
						VpcConnectionArn: aws.String("arn:aws:kafka:us-west-2:123456789012:vpc-connection/test-vpc-conn/12345678-1234-1234-1234-123456789012"),
						State:            "ACTIVE",
					},
				},
				Configurations: []kafka.DescribeConfigurationRevisionOutput{
					{
						Arn: aws.String("arn:aws:kafka:us-west-2:123456789012:configuration/test-config/12345678-1234-1234-1234-123456789012"),
					},
				},
				KafkaVersions: []kafkatypes.KafkaVersion{
					{
						Version: aws.String("3.4.0"),
					},
				},
				Replicators: []kafka.DescribeReplicatorOutput{
					{
						ReplicatorArn: aws.String("arn:aws:kafka:us-west-2:123456789012:replicator/test-replicator/12345678-1234-1234-1234-123456789012"),
					},
				},
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

func TestRegionScanResult_WriteAsMarkdown(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	tests := []struct {
		name     string
		result   *RegionScanResult
		filePath string
		wantErr  bool
	}{
		{
			name: "successfully write markdown to file",
			result: &RegionScanResult{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-east-1",
				Clusters: []ClusterSummary{
					{
						ClusterName: "test-cluster",
						ClusterARN:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012",
						Status:      "ACTIVE",
						Type:        "PROVISIONED",
						Authentication: "SASL_SCRAM",
						PublicAccess: false,
						ClientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
					},
				},
			},
			filePath: filepath.Join(tempDir, "test_output.md"),
			wantErr:  false,
		},
		{
			name: "write to invalid directory should fail",
			result: &RegionScanResult{
				Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				Region:    "us-east-1",
			},
			filePath: "/invalid/path/test.md",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.result.WriteAsMarkdown(tt.filePath)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Verify file was created and contains expected content
			fileInfo, err := os.Stat(tt.filePath)
			require.NoError(t, err)
			assert.True(t, fileInfo.Size() > 0)

			// Read and verify content contains markdown
			fileData, err := os.ReadFile(tt.filePath)
			require.NoError(t, err)
			content := string(fileData)
			assert.Contains(t, content, "# MSK Region Scan Report")
			assert.Contains(t, content, tt.result.Region)
		})
	}
}

func TestRegionScanResult_addSummarySection(t *testing.T) {
	tests := []struct {
		name     string
		result   *RegionScanResult
		validate func(t *testing.T, md *markdown.Markdown)
	}{
		{
			name: "summary with mixed cluster types",
			result: &RegionScanResult{
				Region: "us-east-1",
				Clusters: []ClusterSummary{
					{Type: "PROVISIONED", Authentication: "SASL_SCRAM", Status: "ACTIVE"},
					{Type: "PROVISIONED", Authentication: "IAM", Status: "ACTIVE"},
					{Type: "SERVERLESS", Authentication: "IAM", Status: "ACTIVE"},
					{Type: "PROVISIONED", Authentication: "TLS", Status: "CREATING"},
				},
				VpcConnections: []kafkatypes.VpcConnection{{}},
				Configurations: []kafka.DescribeConfigurationRevisionOutput{{}},
				KafkaVersions:  []kafkatypes.KafkaVersion{{}},
				Replicators:    []kafka.DescribeReplicatorOutput{{}},
			},
			validate: func(t *testing.T, md *markdown.Markdown) {
				assert.NotNil(t, md)
			},
		},
		{
			name: "summary with empty clusters",
			result: &RegionScanResult{
				Region:         "us-west-2",
				Clusters:       []ClusterSummary{},
				VpcConnections: []kafkatypes.VpcConnection{},
				Configurations: []kafka.DescribeConfigurationRevisionOutput{},
				KafkaVersions:  []kafkatypes.KafkaVersion{},
				Replicators:    []kafka.DescribeReplicatorOutput{},
			},
			validate: func(t *testing.T, md *markdown.Markdown) {
				assert.NotNil(t, md)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md := markdown.New()
			tt.result.addSummarySection(md)
			assert.NotNil(t, md)

			if tt.validate != nil {
				tt.validate(t, md)
			}
		})
	}
}

func TestRegionScanResult_addClustersSection(t *testing.T) {
	tests := []struct {
		name     string
		result   *RegionScanResult
		validate func(t *testing.T, md *markdown.Markdown)
	}{
		{
			name: "clusters table with data",
			result: &RegionScanResult{
				Clusters: []ClusterSummary{
					{
						ClusterName: "cluster-1",
						Status:      "ACTIVE",
						Type:        "PROVISIONED",
						Authentication: "SASL_SCRAM",
						PublicAccess: false,
						ClientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
					},
					{
						ClusterName: "cluster-2",
						Status:      "ACTIVE",
						Type:        "SERVERLESS",
						Authentication: "IAM",
						PublicAccess: true,
						ClientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
					},
				},
			},
			validate: func(t *testing.T, md *markdown.Markdown) {
				assert.NotNil(t, md)
			},
		},
		{
			name: "clusters table with no data",
			result: &RegionScanResult{
				Clusters: []ClusterSummary{},
			},
			validate: func(t *testing.T, md *markdown.Markdown) {
				assert.NotNil(t, md)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md := markdown.New()
			tt.result.addClustersSection(md)
			assert.NotNil(t, md)

			if tt.validate != nil {
				tt.validate(t, md)
			}
		})
	}
}

func TestRegionScanResult_addClusterArnsSection(t *testing.T) {
	tests := []struct {
		name     string
		result   *RegionScanResult
		validate func(t *testing.T, md *markdown.Markdown)
	}{
		{
			name: "cluster ARNs table with data",
			result: &RegionScanResult{
				Clusters: []ClusterSummary{
					{
						ClusterName: "cluster-1",
						ClusterARN:  "arn:aws:kafka:us-east-1:123456789012:cluster/cluster-1/12345678-1234-1234-1234-123456789012",
					},
					{
						ClusterName: "cluster-2",
						ClusterARN:  "arn:aws:kafka:us-east-1:123456789012:cluster/cluster-2/87654321-4321-4321-4321-210987654321",
					},
				},
			},
			validate: func(t *testing.T, md *markdown.Markdown) {
				assert.NotNil(t, md)
			},
		},
		{
			name: "cluster ARNs table with no data",
			result: &RegionScanResult{
				Clusters: []ClusterSummary{},
			},
			validate: func(t *testing.T, md *markdown.Markdown) {
				assert.NotNil(t, md)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md := markdown.New()
			tt.result.addClusterArnsSection(md)
			assert.NotNil(t, md)

			if tt.validate != nil {
				tt.validate(t, md)
			}
		})
	}
}

func TestRegionScanResult_Integration(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Create a comprehensive test result
	result := &RegionScanResult{
		Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		Region:    "us-east-1",
		Clusters: []ClusterSummary{
			{
				ClusterName: "integration-test-cluster",
				ClusterARN:  "arn:aws:kafka:us-east-1:123456789012:cluster/integration-test-cluster/12345678-1234-1234-1234-123456789012",
				Status:      "ACTIVE",
				Type:        "PROVISIONED",
				Authentication: "SASL_SCRAM",
				PublicAccess: false,
				ClientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
			},
		},
		VpcConnections: []kafkatypes.VpcConnection{
			{
				VpcConnectionArn: aws.String("arn:aws:kafka:us-east-1:123456789012:vpc-connection/test-vpc-conn/12345678-1234-1234-1234-123456789012"),
				State:            "ACTIVE",
			},
		},
		Configurations: []kafka.DescribeConfigurationRevisionOutput{
			{
				Arn: aws.String("arn:aws:kafka:us-east-1:123456789012:configuration/test-config/12345678-1234-1234-1234-123456789012"),
			},
		},
		KafkaVersions: []kafkatypes.KafkaVersion{
			{
				Version: aws.String("3.4.0"),
			},
		},
		Replicators: []kafka.DescribeReplicatorOutput{
			{
				ReplicatorArn: aws.String("arn:aws:kafka:us-east-1:123456789012:replicator/test-replicator/12345678-1234-1234-1234-123456789012"),
			},
		},
	}

	// Test JSON serialization
	t.Run("JSON serialization", func(t *testing.T) {
		jsonData, err := result.AsJson()
		require.NoError(t, err)
		assert.NotNil(t, jsonData)

		// Verify we can unmarshal it back
		var unmarshaled RegionScanResult
		err = json.Unmarshal(jsonData, &unmarshaled)
		require.NoError(t, err)
		assert.Equal(t, result.Region, unmarshaled.Region)
		assert.Equal(t, result.Timestamp, unmarshaled.Timestamp)
		assert.Len(t, unmarshaled.Clusters, 1)
	})

	// Test JSON file writing
	t.Run("JSON file writing", func(t *testing.T) {
		jsonFilePath := filepath.Join(tempDir, "integration_test.json")
		err := result.WriteAsJson(jsonFilePath)
		require.NoError(t, err)

		// Verify file exists and has content
		fileInfo, err := os.Stat(jsonFilePath)
		require.NoError(t, err)
		assert.True(t, fileInfo.Size() > 0)
	})

	// Test Markdown generation
	t.Run("Markdown generation", func(t *testing.T) {
		md := result.AsMarkdown()
		require.NotNil(t, md)
	})

	// Test Markdown file writing
	t.Run("Markdown file writing", func(t *testing.T) {
		mdFilePath := filepath.Join(tempDir, "integration_test.md")
		err := result.WriteAsMarkdown(mdFilePath)
		require.NoError(t, err)

		// Verify file exists and has content
		fileInfo, err := os.Stat(mdFilePath)
		require.NoError(t, err)
		assert.True(t, fileInfo.Size() > 0)

		// Verify content contains expected markdown
		fileData, err := os.ReadFile(mdFilePath)
		require.NoError(t, err)
		content := string(fileData)
		assert.Contains(t, content, "# MSK Region Scan Report")
		assert.Contains(t, content, "us-east-1")
		assert.Contains(t, content, "integration-test-cluster")
	})
} 