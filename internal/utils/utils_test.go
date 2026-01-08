package utils

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConvertKafkaVersion(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedOutput string
	}{
		{
			name:           "4.0.x.kraft should convert to 4.0.0",
			input:          "4.0.x.kraft",
			expectedOutput: "4.0.0",
		},
		{
			name:           "3.9.x should convert to 3.9.0",
			input:          "3.9.x",
			expectedOutput: "3.9.0",
		},
		{
			name:           "3.9.x.kraft should convert to 3.9.0",
			input:          "3.9.x.kraft",
			expectedOutput: "3.9.0",
		},
		{
			name:           "3.7.x.kraft should convert to 3.7.0",
			input:          "3.7.x.kraft",
			expectedOutput: "3.7.0",
		},
		{
			name:           "3.6.0.1 should convert to 3.6.0",
			input:          "3.6.0.1",
			expectedOutput: "3.6.0",
		},
		{
			name:           "3.6.0 should remain 3.6.0",
			input:          "3.6.0",
			expectedOutput: "3.6.0",
		},
		{
			name:           "2.8.2.tiered should convert to 2.8.2",
			input:          "2.8.2.tiered",
			expectedOutput: "2.8.2",
		},
		{
			name:           "2.6.0 should remain 2.6.0",
			input:          "2.6.0",
			expectedOutput: "2.6.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertKafkaVersion(&tt.input)
			assert.Equal(t, tt.expectedOutput, result, "convertKafkaVersion should return expected output")
		})
	}
}

func TestURLToFolderName(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedOutput string
	}{
		{
			name:           "http://localhost:8081 should convert to localhost_8081",
			input:          "http://localhost:8081",
			expectedOutput: "localhost_8081",
		},
		{
			name:           "",
			input:          "https://psrc-ab123.us-east-2.aws.confluent.cloud",
			expectedOutput: "psrc-ab123_us-east-2_aws_confluent_cloud",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := URLToFolderName(tt.input)
			assert.Equal(t, tt.expectedOutput, result, "URLToFolderName should return expected output")
		})
	}
}

func TestExtractRegionFromS3Uri(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedOutput string
		expectedError  error
	}{
		{
			name:           "valid s3 uri",
			input:          "s3://kcp-demo-logs/AWSLogs/635910096382/KafkaBrokerLogs/eu-west-1/kcp-demo-cluster-bb10f1f7-7557-4f1d-a75a-a2241429a5e8-5/2025-08-23-04/",
			expectedOutput: "eu-west-1",
		},
		{
			name:          "invalid s3 uri",
			input:         "s3://kcp-demo-logs/AWSLogs/",
			expectedError: fmt.Errorf("invalid S3 URI format: expected at least 5 path segments"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExtractRegionFromS3Uri(tt.input)
			if tt.expectedError != nil {
				assert.Error(t, err, "ExtractRegionFromS3Uri should return an error")
				assert.Equal(t, tt.expectedError, err, "ExtractRegionFromS3Uri should return expected error")
			} else {
				assert.NoError(t, err, "ExtractRegionFromS3Uri should not return an error")
			}
			assert.Equal(t, tt.expectedOutput, result, "ExtractRegionFromS3Uri should return expected output")
		})
	}
}

func TestExtractClusterNameFromS3Uri(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedOutput string
		expectedError  error
	}{
		{
			name:           "valid s3 uri - extracts mytestcluster",
			input:          "s3://kcp-demo-test-logs/AWSLogs/635910096382/KafkaBrokerLogs/us-east-1/mytestcluster-a5adc230-1179-4e38-83bc-bf96b62377b3-2/2025-12-01-10/",
			expectedOutput: "mytestcluster",
		},
		{
			name:          "invalid s3 uri - too few segments",
			input:         "s3://kcp-demo-logs/AWSLogs/635910096382/KafkaBrokerLogs/eu-west-1/",
			expectedError: fmt.Errorf("invalid S3 URI format: expected at least 5 path segments (AWSLogs/account/KafkaBrokerLogs/region/cluster-name/...)"),
		},
		{
			name:          "invalid s3 uri - missing cluster name",
			input:         "s3://kcp-demo-logs/AWSLogs/",
			expectedError: fmt.Errorf("invalid S3 URI format: expected at least 5 path segments (AWSLogs/account/KafkaBrokerLogs/region/cluster-name/...)"),
		},
		{
			name:          "invalid s3 uri - cluster segment without UUID pattern",
			input:         "s3://kcp-demo-logs/AWSLogs/635910096382/KafkaBrokerLogs/eu-west-1/just-a-cluster-name/2025-08-26-23/",
			expectedError: fmt.Errorf("invalid S3 URI format: cluster segment 'just-a-cluster-name' does not contain a valid UUID pattern"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExtractClusterNameFromS3Uri(tt.input)
			if tt.expectedError != nil {
				assert.Error(t, err, "ExtractClusterNameFromS3Uri should return an error")
				assert.Equal(t, tt.expectedError.Error(), err.Error(), "ExtractClusterNameFromS3Uri should return expected error")
			} else {
				assert.NoError(t, err, "ExtractClusterNameFromS3Uri should not return an error")
			}
			assert.Equal(t, tt.expectedOutput, result, "ExtractClusterNameFromS3Uri should return expected output")
		})
	}
}
