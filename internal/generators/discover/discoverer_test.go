package discover

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverer_getClusterEntry(t *testing.T) {
	discoverer := &Discoverer{}

	tests := []struct {
		name     string
		cluster  kafkatypes.Cluster
		expected types.ClusterEntry
	}{
		{
			name: "Provisioned cluster with all authentication methods enabled",
			cluster: kafkatypes.Cluster{
				ClusterName: aws.String("test-cluster-all-auth"),
				ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-all-auth/12345678-1234-1234-1234-123456789012-1"),
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Iam: &kafkatypes.Iam{
								Enabled: aws.Bool(true),
							},
							Scram: &kafkatypes.Scram{
								Enabled: aws.Bool(true),
							},
						},
						Tls: &kafkatypes.Tls{
							Enabled: aws.Bool(true),
						},
						Unauthenticated: &kafkatypes.Unauthenticated{
							Enabled: aws.Bool(true),
						},
					},
				},
			},
			expected: types.ClusterEntry{
				Name: "test-cluster-all-auth",
				Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-all-auth/12345678-1234-1234-1234-123456789012-1",
				AuthMethod: types.AuthMethodConfig{
					Unauthenticated: &types.UnauthenticatedConfig{Use: true},                                 // highest priority, selected
					IAM:             &types.IAMConfig{Use: false},                                            // not selected due to priority
					SASLScram:       &types.SASLScramConfig{Use: false, Username: "", Password: ""},          // not selected due to priority
					TLS:             &types.TLSConfig{Use: false, CACert: "", ClientCert: "", ClientKey: ""}, // not selected due to priority
				},
			},
		},
		{
			name: "Provisioned cluster with only IAM enabled",
			cluster: kafkatypes.Cluster{
				ClusterName: aws.String("test-cluster-iam-only"),
				ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-iam-only/12345678-1234-1234-1234-123456789012-2"),
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Iam: &kafkatypes.Iam{
								Enabled: aws.Bool(true),
							},
						},
					},
				},
			},
			expected: types.ClusterEntry{
				Name: "test-cluster-iam-only",
				Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-iam-only/12345678-1234-1234-1234-123456789012-2",
				AuthMethod: types.AuthMethodConfig{
					IAM: &types.IAMConfig{Use: true}, // only auth method available, selected
				},
			},
		},
		{
			name: "Provisioned cluster with only SASL/SCRAM enabled",
			cluster: kafkatypes.Cluster{
				ClusterName: aws.String("test-cluster-scram-only"),
				ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-scram-only/12345678-1234-1234-1234-123456789012-3"),
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Scram: &kafkatypes.Scram{
								Enabled: aws.Bool(true),
							},
						},
					},
				},
			},
			expected: types.ClusterEntry{
				Name: "test-cluster-scram-only",
				Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-scram-only/12345678-1234-1234-1234-123456789012-3",
				AuthMethod: types.AuthMethodConfig{
					SASLScram: &types.SASLScramConfig{Use: true, Username: "", Password: ""}, // only auth method available, selected
				},
			},
		},
		{
			name: "Provisioned cluster with only TLS enabled",
			cluster: kafkatypes.Cluster{
				ClusterName: aws.String("test-cluster-tls-only"),
				ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-tls-only/12345678-1234-1234-1234-123456789012-4"),
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Tls: &kafkatypes.Tls{
							Enabled: aws.Bool(true),
						},
					},
				},
			},
			expected: types.ClusterEntry{
				Name: "test-cluster-tls-only",
				Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-tls-only/12345678-1234-1234-1234-123456789012-4",
				AuthMethod: types.AuthMethodConfig{
					TLS: &types.TLSConfig{Use: true, CACert: "", ClientCert: "", ClientKey: ""}, // only auth method available, selected
				},
			},
		},
		{
			name: "Serverless cluster",
			cluster: kafkatypes.Cluster{
				ClusterName: aws.String("test-cluster-serverless"),
				ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-serverless/12345678-1234-1234-1234-123456789012-5"),
				ClusterType: kafkatypes.ClusterTypeServerless,
			},
			expected: types.ClusterEntry{
				Name: "test-cluster-serverless",
				Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-serverless/12345678-1234-1234-1234-123456789012-5",
				AuthMethod: types.AuthMethodConfig{
					IAM: &types.IAMConfig{Use: true}, // serverless defaults to IAM
				},
			},
		},
		{
			name: "Provisioned cluster with IAM and SCRAM (IAM priority)",
			cluster: kafkatypes.Cluster{
				ClusterName: aws.String("test-cluster-iam-scram"),
				ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-iam-scram/12345678-1234-1234-1234-123456789012-6"),
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Iam: &kafkatypes.Iam{
								Enabled: aws.Bool(true),
							},
							Scram: &kafkatypes.Scram{
								Enabled: aws.Bool(true),
							},
						},
					},
				},
			},
			expected: types.ClusterEntry{
				Name: "test-cluster-iam-scram",
				Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-iam-scram/12345678-1234-1234-1234-123456789012-6",
				AuthMethod: types.AuthMethodConfig{
					IAM:       &types.IAMConfig{Use: true},                                    // higher priority than SCRAM
					SASLScram: &types.SASLScramConfig{Use: false, Username: "", Password: ""}, // lower priority
				},
			},
		},
		{
			name: "Provisioned cluster with SCRAM and TLS (SCRAM priority)",
			cluster: kafkatypes.Cluster{
				ClusterName: aws.String("test-cluster-scram-tls"),
				ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-scram-tls/12345678-1234-1234-1234-123456789012-7"),
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Scram: &kafkatypes.Scram{
								Enabled: aws.Bool(true),
							},
						},
						Tls: &kafkatypes.Tls{
							Enabled: aws.Bool(true),
						},
					},
				},
			},
			expected: types.ClusterEntry{
				Name: "test-cluster-scram-tls",
				Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-scram-tls/12345678-1234-1234-1234-123456789012-7",
				AuthMethod: types.AuthMethodConfig{
					SASLScram: &types.SASLScramConfig{Use: true, Username: "", Password: ""},           // higher priority than TLS
					TLS:       &types.TLSConfig{Use: false, CACert: "", ClientCert: "", ClientKey: ""}, // lower priority
				},
			},
		},
		{
			name: "Provisioned cluster with disabled authentication methods",
			cluster: kafkatypes.Cluster{
				ClusterName: aws.String("test-cluster-disabled"),
				ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-disabled/12345678-1234-1234-1234-123456789012-8"),
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{
							Iam: &kafkatypes.Iam{
								Enabled: aws.Bool(false),
							},
							Scram: &kafkatypes.Scram{
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
			},
			expected: types.ClusterEntry{
				Name:       "test-cluster-disabled",
				Arn:        "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-disabled/12345678-1234-1234-1234-123456789012-8",
				AuthMethod: types.AuthMethodConfig{
					// no auth methods should be configured since all are disabled
				},
			},
		},
		{
			name: "Provisioned cluster with nil client authentication",
			cluster: kafkatypes.Cluster{
				ClusterName: aws.String("test-cluster-nil-auth"),
				ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-nil-auth/12345678-1234-1234-1234-123456789012-9"),
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: nil,
				},
			},
			expected: types.ClusterEntry{
				Name:       "test-cluster-nil-auth",
				Arn:        "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-nil-auth/12345678-1234-1234-1234-123456789012-9",
				AuthMethod: types.AuthMethodConfig{
					// no auth methods should be configured
				},
			},
		},
		{
			name: "Provisioned cluster with nil provisioned section",
			cluster: kafkatypes.Cluster{
				ClusterName: aws.String("test-cluster-nil-provisioned"),
				ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-nil-provisioned/12345678-1234-1234-1234-123456789012-10"),
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: nil,
			},
			expected: types.ClusterEntry{
				Name:       "test-cluster-nil-provisioned",
				Arn:        "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-nil-provisioned/12345678-1234-1234-1234-123456789012-10",
				AuthMethod: types.AuthMethodConfig{
					// no auth methods should be configured
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := discoverer.getClusterEntry(tt.cluster)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
