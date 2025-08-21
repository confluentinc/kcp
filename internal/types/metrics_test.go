package types

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClusterMetrics_AsJson(t *testing.T) {
	tests := []struct {
		name     string
		metrics  *ClusterMetrics
		wantErr  bool
		validate func(t *testing.T, data []byte)
	}{
		{
			name: "successfully marshal empty metrics",
			metrics: &ClusterMetrics{
				ClusterArn:   "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012",
				ClusterName:  "test-cluster",
				ClusterType:  "PROVISIONED",
				StartDate:    time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				EndDate:      time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
				NodesMetrics: []NodeMetrics{},
			},
			wantErr: false,
			validate: func(t *testing.T, data []byte) {
				var unmarshaled ClusterMetrics
				err := json.Unmarshal(data, &unmarshaled)
				require.NoError(t, err)
				assert.Equal(t, "test-cluster", unmarshaled.ClusterName)
				assert.Equal(t, "PROVISIONED", unmarshaled.ClusterType)
				assert.Equal(t, time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC), unmarshaled.StartDate)
				assert.Equal(t, time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC), unmarshaled.EndDate)
			},
		},
		{
			name: "successfully marshal metrics with node data",
			metrics: &ClusterMetrics{
				ClusterArn:   "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012",
				ClusterName:  "test-cluster",
				ClusterType:  "PROVISIONED",
				StartDate:    time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				EndDate:      time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
				KafkaVersion: stringPtr("3.4.0"),
				NodesMetrics: []NodeMetrics{
					{
						NodeID:                   1,
						InstanceType:             stringPtr("kafka.m5.large"),
						VolumeSizeGB:             intPtr(100),
						BytesInPerSecAvg:         1024.0,
						BytesOutPerSecAvg:        512.0,
						MessagesInPerSecAvg:      100.0,
						KafkaDataLogsDiskUsedAvg: 1073741824.0, // 1GB
						RemoteLogSizeBytesAvg:    2147483648.0, // 2GB
					},
				},
				ClusterMetricsSummary: ClusterMetricsSummary{
					AvgIngressThroughputMegabytesPerSecond:  float64Ptr(1.0),
					PeakIngressThroughputMegabytesPerSecond: float64Ptr(2.0),
					Partitions:                              float64Ptr(100.0),
					ReplicationFactor:                       float64Ptr(3.0),
				},
			},
			wantErr: false,
			validate: func(t *testing.T, data []byte) {
				var unmarshaled ClusterMetrics
				err := json.Unmarshal(data, &unmarshaled)
				require.NoError(t, err)
				assert.Equal(t, "test-cluster", unmarshaled.ClusterName)
				assert.Equal(t, "3.4.0", *unmarshaled.KafkaVersion)
				assert.Len(t, unmarshaled.NodesMetrics, 1)
				assert.Equal(t, 1, unmarshaled.NodesMetrics[0].NodeID)
				assert.Equal(t, "kafka.m5.large", *unmarshaled.NodesMetrics[0].InstanceType)
				assert.Equal(t, 100.0, *unmarshaled.ClusterMetricsSummary.Partitions)
			},
		},
		{
			name: "successfully marshal metrics with all fields populated",
			metrics: &ClusterMetrics{
				ClusterArn:           "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012",
				ClusterName:          "test-cluster-full",
				ClusterType:          "SERVERLESS",
				BrokerAZDistribution: stringPtr("MULTI_AZ"),
				Authentication:       map[string]any{"SASL": "SCRAM-SHA-512"},
				KafkaVersion:         stringPtr("3.5.0"),
				EnhancedMonitoring:   stringPtr("DEFAULT"),
				StartDate:            time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				EndDate:              time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
				NodesMetrics:         []NodeMetrics{},
				ClusterMetricsSummary: ClusterMetricsSummary{
					AvgIngressThroughputMegabytesPerSecond:  float64Ptr(5.5),
					PeakIngressThroughputMegabytesPerSecond: float64Ptr(10.2),
					AvgEgressThroughputMegabytesPerSecond:   float64Ptr(3.1),
					PeakEgressThroughputMegabytesPerSecond:  float64Ptr(8.7),
					RetentionDays:                           float64Ptr(7.0),
					Partitions:                              float64Ptr(200.0),
					ReplicationFactor:                       float64Ptr(3.0),
					FollowerFetching:                        boolPtr(true),
					TieredStorage:                           boolPtr(true),
					LocalRetentionInPrimaryStorageHours:     float64Ptr(24.0),
					InstanceType:                            stringPtr("kafka.m5.xlarge"),
				},
			},
			wantErr: false,
			validate: func(t *testing.T, data []byte) {
				var unmarshaled ClusterMetrics
				err := json.Unmarshal(data, &unmarshaled)
				require.NoError(t, err)
				assert.Equal(t, "SERVERLESS", unmarshaled.ClusterType)
				assert.Equal(t, "MULTI_AZ", *unmarshaled.BrokerAZDistribution)
				assert.Equal(t, "SCRAM-SHA-512", unmarshaled.Authentication["SASL"])
				assert.Equal(t, 5.5, *unmarshaled.ClusterMetricsSummary.AvgIngressThroughputMegabytesPerSecond)
				assert.Equal(t, true, *unmarshaled.ClusterMetricsSummary.FollowerFetching)
				assert.Equal(t, "kafka.m5.xlarge", *unmarshaled.ClusterMetricsSummary.InstanceType)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.metrics.AsJson()
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

func TestClusterMetrics_WriteAsJson(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	tests := []struct {
		name    string
		metrics *ClusterMetrics
		wantErr bool
	}{
		{
			name: "successfully write to file",
			metrics: &ClusterMetrics{
				Region:       "us-east-1",
				ClusterArn:   "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012",
				ClusterName:  "test-cluster",
				ClusterType:  "PROVISIONED",
				StartDate:    time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				EndDate:      time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
				NodesMetrics: []NodeMetrics{},
			},
			wantErr: false,
		},
		{
			name: "write with empty cluster name should succeed",
			metrics: &ClusterMetrics{
				Region:       "us-east-1",
				ClusterArn:   "arn:aws:kafka:us-east-1:123456789012:cluster//12345678-1234-1234-1234-123456789012",
				ClusterName:  "",
				ClusterType:  "PROVISIONED",
				StartDate:    time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				EndDate:      time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
				NodesMetrics: []NodeMetrics{},
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

			err = tt.metrics.WriteAsJson()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Verify file was created and contains expected content
			expectedPath := tt.metrics.GetJsonPath()
			fileInfo, err := os.Stat(expectedPath)
			require.NoError(t, err)
			assert.True(t, fileInfo.Size() > 0)

			// Read and verify content
			fileData, err := os.ReadFile(expectedPath)
			require.NoError(t, err)

			var unmarshaled ClusterMetrics
			err = json.Unmarshal(fileData, &unmarshaled)
			require.NoError(t, err)
			assert.Equal(t, tt.metrics.ClusterName, unmarshaled.ClusterName)
			assert.Equal(t, tt.metrics.ClusterType, unmarshaled.ClusterType)
			assert.Equal(t, tt.metrics.StartDate, unmarshaled.StartDate)
		})
	}
}

func TestClusterMetrics_AsMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		metrics  *ClusterMetrics
		validate func(t *testing.T, md interface{})
	}{
		{
			name: "generate markdown for empty metrics",
			metrics: &ClusterMetrics{
				ClusterArn:   "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012",
				ClusterName:  "test-cluster",
				ClusterType:  "PROVISIONED",
				StartDate:    time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				EndDate:      time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
				NodesMetrics: []NodeMetrics{},
			},
			validate: func(t *testing.T, md interface{}) {
				// Basic validation that markdown was generated
				assert.NotNil(t, md)
			},
		},
		{
			name: "generate markdown for metrics with node data",
			metrics: &ClusterMetrics{
				ClusterArn:   "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012",
				ClusterName:  "test-cluster",
				ClusterType:  "PROVISIONED",
				StartDate:    time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				EndDate:      time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
				KafkaVersion: stringPtr("3.4.0"),
				NodesMetrics: []NodeMetrics{
					{
						NodeID:                   1,
						InstanceType:             stringPtr("kafka.m5.large"),
						VolumeSizeGB:             intPtr(100),
						BytesInPerSecAvg:         1024.0,
						BytesOutPerSecAvg:        512.0,
						MessagesInPerSecAvg:      100.0,
						KafkaDataLogsDiskUsedAvg: 1073741824.0,
					},
				},
				ClusterMetricsSummary: ClusterMetricsSummary{
					Partitions:        float64Ptr(100.0),
					ReplicationFactor: float64Ptr(3.0),
				},
			},
			validate: func(t *testing.T, md interface{}) {
				assert.NotNil(t, md)
			},
		},
		{
			name: "generate markdown for metrics with all nil ClusterMetricsSummary fields",
			metrics: &ClusterMetrics{
				ClusterArn:   "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012",
				ClusterName:  "test-cluster-nil",
				ClusterType:  "PROVISIONED",
				StartDate:    time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				EndDate:      time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
				NodesMetrics: []NodeMetrics{},
				ClusterMetricsSummary: ClusterMetricsSummary{
					AvgIngressThroughputMegabytesPerSecond:  nil,
					PeakIngressThroughputMegabytesPerSecond: nil,
					AvgEgressThroughputMegabytesPerSecond:   nil,
					PeakEgressThroughputMegabytesPerSecond:  nil,
					RetentionDays:                           nil,
					Partitions:                              nil,
					ReplicationFactor:                       nil,
					FollowerFetching:                        nil,
					TieredStorage:                           nil,
					LocalRetentionInPrimaryStorageHours:     nil,
					InstanceType:                            nil,
				},
			},
			validate: func(t *testing.T, md interface{}) {
				assert.NotNil(t, md)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md := tt.metrics.AsMarkdown()
			assert.NotNil(t, md)

			if tt.validate != nil {
				tt.validate(t, md)
			}
		})
	}
}

func TestClusterMetrics_WriteAsMarkdown(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	tests := []struct {
		name    string
		metrics *ClusterMetrics
		wantErr bool
	}{
		{
			name: "successfully write markdown to file",
			metrics: &ClusterMetrics{
				Region:       "us-east-1",
				ClusterArn:   "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012",
				ClusterName:  "test-cluster",
				ClusterType:  "PROVISIONED",
				StartDate:    time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				EndDate:      time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
				NodesMetrics: []NodeMetrics{},
			},
			wantErr: false,
		},
		{
			name: "write with empty cluster name should succeed",
			metrics: &ClusterMetrics{
				Region:       "us-east-1",
				ClusterArn:   "arn:aws:kafka:us-east-1:123456789012:cluster//12345678-1234-1234-1234-123456789012",
				ClusterName:  "",
				ClusterType:  "PROVISIONED",
				StartDate:    time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				EndDate:      time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
				NodesMetrics: []NodeMetrics{},
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

			err = tt.metrics.WriteAsMarkdown()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Verify file was created and contains expected content
			expectedPath := tt.metrics.GetMarkdownPath()
			fileInfo, err := os.Stat(expectedPath)
			require.NoError(t, err)
			assert.True(t, fileInfo.Size() > 0)

			// Read and verify content contains markdown
			fileData, err := os.ReadFile(expectedPath)
			require.NoError(t, err)
			content := string(fileData)
			assert.Contains(t, content, "# MSK Cluster Metrics Report")
			assert.Contains(t, content, tt.metrics.ClusterName)
		})
	}
}

func TestFormatInstanceTypeOverride(t *testing.T) {
	tests := []struct {
		name         string
		instanceType *string
		expected     string
	}{
		{
			name:         "nil instance type",
			instanceType: nil,
			expected:     "",
		},
		{
			name:         "instance type with dot",
			instanceType: stringPtr("kafka.m5.large"),
			expected:     "M5.large",
		},
		{
			name:         "instance type without dot",
			instanceType: stringPtr("m5large"),
			expected:     "M5large",
		},
		{
			name:         "empty string",
			instanceType: stringPtr(""),
			expected:     "",
		},
		{
			name:         "single character",
			instanceType: stringPtr("a"),
			expected:     "A",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatInstanceTypeOverride(tt.instanceType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClusterMetrics_AddIndividualClusterSections(t *testing.T) {
	metrics := &ClusterMetrics{
		ClusterArn:   "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012",
		ClusterName:  "test-cluster",
		ClusterType:  "PROVISIONED",
		StartDate:    time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		EndDate:      time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
		NodesMetrics: []NodeMetrics{},
	}

	// This test verifies that the method doesn't panic and returns
	// The actual markdown generation is tested in the AsMarkdown tests
	t.Run("does not panic with empty metrics", func(t *testing.T) {
		assert.NotPanics(t, func() {
			metrics.AsMarkdown()
		})
	})
}

func TestClusterMetrics_AsMarkdown_WithNilValues_NoPanics(t *testing.T) {
	// Create a cluster with all nil ClusterMetricsSummary fields
	metrics := &ClusterMetrics{
		ClusterArn:   "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012",
		ClusterName:  "test-cluster-nil",
		ClusterType:  "PROVISIONED",
		StartDate:    time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		EndDate:      time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
		NodesMetrics: []NodeMetrics{},
		ClusterMetricsSummary: ClusterMetricsSummary{
			AvgIngressThroughputMegabytesPerSecond:  nil,
			PeakIngressThroughputMegabytesPerSecond: nil,
			AvgEgressThroughputMegabytesPerSecond:   nil,
			PeakEgressThroughputMegabytesPerSecond:  nil,
			RetentionDays:                           nil,
			Partitions:                              nil,
			ReplicationFactor:                       nil,
			FollowerFetching:                        nil,
			TieredStorage:                           nil,
			LocalRetentionInPrimaryStorageHours:     nil,
			InstanceType:                            nil,
		},
	}

	// Test that markdown generation doesn't panic with nil values
	assert.NotPanics(t, func() {
		metrics.AsMarkdown()
	}, "Did not expect panic when ClusterMetricsSummary members are nil")
}

// Helper functions for creating pointers to primitive types
func stringPtr(s string) *string {
	return &s
}

func intPtr(i int) *int {
	return &i
}

func float64Ptr(f float64) *float64 {
	return &f
}

func boolPtr(b bool) *bool {
	return &b
}
