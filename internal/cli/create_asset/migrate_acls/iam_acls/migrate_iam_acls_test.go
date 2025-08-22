package iam_acls

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluatePrincipal(t *testing.T) {
	tests := []struct {
		name                 string
		discoveredPrincipals []string
		expectedPrincipals   []string
		expectedError        bool
	}{
		{
			name: "single STS assumed-role ARN should convert to IAM role ARN",
			discoveredPrincipals: []string{
				"arn:aws:sts::000123456789:assumed-role/kcp-iam-role/i-0ab123456cdef7890",
			},
			expectedPrincipals: []string{
				"arn:aws:iam::000123456789:role/kcp-iam-role",
			},
			expectedError: false,
		},
		{
			name: "multiple STS assumed-role ARNs should convert to IAM role ARNs",
			discoveredPrincipals: []string{
				"arn:aws:sts::000123456789:assumed-role/kcp-iam-role/i-0ab123456cdef7890",
				"arn:aws:sts::111222333444:assumed-role/another-role/i-0xyz987654fedcba21",
			},
			expectedPrincipals: []string{
				"arn:aws:iam::000123456789:role/kcp-iam-role",
				"arn:aws:iam::111222333444:role/another-role",
			},
			expectedError: false,
		},
		{
			name: "STS assumed-role with session name should convert correctly",
			discoveredPrincipals: []string{
				"arn:aws:sts::000123456789:assumed-role/msk-assumed-role/testing-sts",
			},
			expectedPrincipals: []string{
				"arn:aws:iam::000123456789:role/msk-assumed-role",
			},
			expectedError: false,
		},
		{
			name: "IAM role ARN should remain unchanged",
			discoveredPrincipals: []string{
				"arn:aws:iam::000123456789:role/kcp-iam-role",
			},
			expectedPrincipals: []string{
				"arn:aws:iam::000123456789:role/kcp-iam-role",
			},
			expectedError: false,
		},
		{
			name: "IAM user ARN should remain unchanged",
			discoveredPrincipals: []string{
				"arn:aws:iam::000123456789:user/kcp-iam-user",
			},
			expectedPrincipals: []string{
				"arn:aws:iam::000123456789:user/kcp-iam-user",
			},
			expectedError: false,
		},
		{
			name: "mixed ARN types should be processed correctly",
			discoveredPrincipals: []string{
				"arn:aws:sts::000123456789:assumed-role/kcp-iam-role/i-0ab123456cdef7890",
				"arn:aws:iam::111222333444:role/direct-role",
				"arn:aws:iam::555666777888:user/kafka-user",
			},
			expectedPrincipals: []string{
				"arn:aws:iam::000123456789:role/kcp-iam-role",
				"arn:aws:iam::111222333444:role/direct-role",
				"arn:aws:iam::555666777888:user/kafka-user",
			},
			expectedError: false,
		},
		{
			name:                 "empty list should return empty list",
			discoveredPrincipals: []string{},
			expectedPrincipals:   nil,
			expectedError:        false,
		},
		{
			name: "multiple of the same role should be deduplicated",
			discoveredPrincipals: []string{
				"arn:aws:sts::000123456789:assumed-role/kcp-iam-role/i-0ab123456cdef7890",
				"arn:aws:sts::000123456789:assumed-role/kcp-iam-role/i-0ab123456cdef7890",
			},
			expectedPrincipals: []string{
				"arn:aws:iam::000123456789:role/kcp-iam-role",
			},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evaluatePrincipal(tt.discoveredPrincipals)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedPrincipals, result)
			}
		})
	}
}

func TestParseClientDiscoveryFile(t *testing.T) {
	tests := []struct {
		name               string
		csvContent         string
		expectedPrincipals []string
		expectedError      bool
	}{
		{
			name: "valid CSV with IAM principals should parse correctly",
			csvContent: `Client ID,Role,Topic,Auth,Principal,Timestamp
TESTING_PRODUCER-1,Producer,customers1,IAM,arn:aws:sts::000123456789:assumed-role/kcp-testing-role/testing-sts,2025-07-25 14:45:53
producer_with_sasl_scram-9,Producer,test-topic-1,SASL_SCRAM,User:kafka-user-2,2025-08-07 14:34:27
kafka-client-1,Consumer,orders,IAM,arn:aws:sts::000123456789:assumed-role/kcp-iam-role/i-0ab123456cdef7890,2025-07-26 10:15:30`,
			expectedPrincipals: []string{
				"arn:aws:iam::000123456789:role/kcp-testing-role",
				"arn:aws:iam::000123456789:role/kcp-iam-role",
			},
			expectedError: false,
		},
		{
			name: "CSV with only SASL_SCRAM principals should return empty list",
			csvContent: `Client ID,Role,Topic,Auth,Principal,Timestamp
producer_with_sasl_scram-1,Producer,test-topic-1,SASL_SCRAM,User:kafka-user-1,2025-08-07 14:34:27
producer_with_sasl_scram-2,Producer,test-topic-2,SASL_SCRAM,User:kafka-user-2,2025-08-07 14:35:27`,
			expectedPrincipals: nil,
			expectedError:      false,
		},
		{
			name: "CSV with mixed auth types should only return IAM principals",
			csvContent: `Client ID,Role,Topic,Auth,Principal,Timestamp
client-1,Producer,topic-1,IAM,arn:aws:sts::111222333444:assumed-role/role-1/session-1,2025-07-25 14:45:53
client-2,Consumer,topic-2,SASL_SCRAM,User:scram-user,2025-07-25 14:46:53
client-3,Producer,topic-3,TLS,CN=client3,2025-07-25 14:47:53
client-4,Consumer,topic-4,IAM,arn:aws:iam::555666777888:user/direct-user,2025-07-25 14:48:53`,
			expectedPrincipals: []string{
				"arn:aws:iam::111222333444:role/role-1",
				"arn:aws:iam::555666777888:user/direct-user",
			},
			expectedError: false,
		},
		{
			name:               "empty CSV file (only header) should return empty list",
			csvContent:         `Client ID,Role,Topic,Auth,Principal,Timestamp`,
			expectedPrincipals: nil,
			expectedError:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary CSV file
			tmpDir := t.TempDir()
			csvFile := filepath.Join(tmpDir, "test-client-discovery.csv")

			err := os.WriteFile(csvFile, []byte(tt.csvContent), 0644)
			require.NoError(t, err, "Failed to create test CSV file")

			// Test the function
			result, err := parseClientDiscoveryFile(csvFile)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedPrincipals, result)
			}
		})
	}
}

func TestParseClientDiscoveryFile_FileErrors(t *testing.T) {
	tests := []struct {
		name          string
		fileName      string
		expectedError string
	}{
		{
			name:          "non-existent file should return error",
			fileName:      "/path/to/non-existent-file.csv",
			expectedError: "failed to read client discovery file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseClientDiscoveryFile(tt.fileName)

			assert.Error(t, err)
			assert.Nil(t, result)
			assert.Contains(t, err.Error(), tt.expectedError)
		})
	}
}

func TestParseClientDiscoveryFile_MalformedCSV(t *testing.T) {
	tests := []struct {
		name          string
		csvContent    string
		expectedError string
	}{
		{
			name: "malformed CSV with unmatched quotes should return error",
			csvContent: `Client ID,Role,Topic,Auth,Principal,Timestamp
"client-1,Producer,topic-1,IAM,arn:aws:sts::111222333444:assumed-role/role-1/session-1,2025-07-25 14:45:53`,
			expectedError: "failed to read all records from client discovery file",
		},
		{
			name: "CSV with inconsistent number of columns should return error",
			csvContent: `Client ID,Role,Topic,Auth,Principal,Timestamp
client-1,Producer,topic-1,IAM
client-2,Consumer,topic-2,SASL_SCRAM,User:scram-user,2025-07-25 14:46:53`,
			expectedError: "failed to read all records from client discovery file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary CSV file
			tmpDir := t.TempDir()
			csvFile := filepath.Join(tmpDir, "test-malformed.csv")

			err := os.WriteFile(csvFile, []byte(tt.csvContent), 0644)
			require.NoError(t, err, "Failed to create test CSV file")

			// Test the function
			result, err := parseClientDiscoveryFile(csvFile)

			assert.Error(t, err)
			assert.Nil(t, result)
			assert.Contains(t, err.Error(), tt.expectedError)
		})
	}
}
