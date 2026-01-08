package iam_acls

import (
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
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
	testClusterArn := "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abcd1234-5678-90ef-ghij-klmnopqrstuv-1"

	tests := []struct {
		name               string
		state              *types.State
		expectedPrincipals []string
		expectedError      bool
	}{
		{
			name: "valid state with IAM principals should parse correctly",
			state: &types.State{
				Regions: []types.DiscoveredRegion{
					{
						Name: "us-east-1",
						Clusters: []types.DiscoveredCluster{
							{
								Arn:    testClusterArn,
								Name:   "test-cluster",
								Region: "us-east-1",
								DiscoveredClients: []types.DiscoveredClient{
									{
										ClientId:  "TESTING_PRODUCER-1",
										Role:      "Producer",
										Topic:     "customers1",
										Auth:      "IAM",
										Principal: "arn:aws:sts::000123456789:assumed-role/kcp-testing-role/testing-sts",
										Timestamp: time.Now(),
									},
									{
										ClientId:  "producer_with_sasl_scram-9",
										Role:      "Producer",
										Topic:     "test-topic-1",
										Auth:      "SASL_SCRAM",
										Principal: "User:kafka-user-2",
										Timestamp: time.Now(),
									},
									{
										ClientId:  "kafka-client-1",
										Role:      "Consumer",
										Topic:     "orders",
										Auth:      "IAM",
										Principal: "arn:aws:sts::000123456789:assumed-role/kcp-iam-role/i-0ab123456cdef7890",
										Timestamp: time.Now(),
									},
								},
							},
						},
					},
				},
			},
			expectedPrincipals: []string{
				"arn:aws:iam::000123456789:role/kcp-testing-role",
				"arn:aws:iam::000123456789:role/kcp-iam-role",
			},
			expectedError: false,
		},
		{
			name: "state with only SASL_SCRAM principals should return empty list",
			state: &types.State{
				Regions: []types.DiscoveredRegion{
					{
						Name: "us-east-1",
						Clusters: []types.DiscoveredCluster{
							{
								Arn:    testClusterArn,
								Name:   "test-cluster",
								Region: "us-east-1",
								DiscoveredClients: []types.DiscoveredClient{
									{
										ClientId:  "producer_with_sasl_scram-1",
										Role:      "Producer",
										Topic:     "test-topic-1",
										Auth:      "SASL_SCRAM",
										Principal: "User:kafka-user-1",
										Timestamp: time.Now(),
									},
									{
										ClientId:  "producer_with_sasl_scram-2",
										Role:      "Producer",
										Topic:     "test-topic-2",
										Auth:      "SASL_SCRAM",
										Principal: "User:kafka-user-2",
										Timestamp: time.Now(),
									},
								},
							},
						},
					},
				},
			},
			expectedPrincipals: nil,
			expectedError:      false,
		},
		{
			name: "state with mixed auth types should only return IAM principals",
			state: &types.State{
				Regions: []types.DiscoveredRegion{
					{
						Name: "us-east-1",
						Clusters: []types.DiscoveredCluster{
							{
								Arn:    testClusterArn,
								Name:   "test-cluster",
								Region: "us-east-1",
								DiscoveredClients: []types.DiscoveredClient{
									{
										ClientId:  "client-1",
										Role:      "Producer",
										Topic:     "topic-1",
										Auth:      "IAM",
										Principal: "arn:aws:sts::111222333444:assumed-role/role-1/session-1",
										Timestamp: time.Now(),
									},
									{
										ClientId:  "client-2",
										Role:      "Consumer",
										Topic:     "topic-2",
										Auth:      "SASL_SCRAM",
										Principal: "User:scram-user",
										Timestamp: time.Now(),
									},
									{
										ClientId:  "client-3",
										Role:      "Producer",
										Topic:     "topic-3",
										Auth:      "TLS",
										Principal: "CN=client3",
										Timestamp: time.Now(),
									},
									{
										ClientId:  "client-4",
										Role:      "Consumer",
										Topic:     "topic-4",
										Auth:      "IAM",
										Principal: "arn:aws:iam::555666777888:user/direct-user",
										Timestamp: time.Now(),
									},
								},
							},
						},
					},
				},
			},
			expectedPrincipals: []string{
				"arn:aws:iam::111222333444:role/role-1",
				"arn:aws:iam::555666777888:user/direct-user",
			},
			expectedError: false,
		},
		{
			name: "state with empty discovered clients should return empty list",
			state: &types.State{
				Regions: []types.DiscoveredRegion{
					{
						Name: "us-east-1",
						Clusters: []types.DiscoveredCluster{
							{
								Arn:               testClusterArn,
								Name:              "test-cluster",
								Region:            "us-east-1",
								DiscoveredClients: []types.DiscoveredClient{},
							},
						},
					},
				},
			},
			expectedPrincipals: nil,
			expectedError:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the function
			result, err := parseClientDiscoveryFile(testClusterArn, tt.state)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedPrincipals, result)
			}
		})
	}
}
