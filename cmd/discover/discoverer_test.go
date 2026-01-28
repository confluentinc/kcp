package discover

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestDiscoverer_getAvailableClusterAuthOptions(t *testing.T) {
	d := &Discoverer{}

	tests := []struct {
		name         string
		cluster      kafkatypes.Cluster
		expectedAuth types.AuthType
		expectedUse  bool
		description  string
	}{
		{
			name: "only unauthenticated_plaintext available",
			cluster: createProvisionedCluster(
				withUnauthenticatedPlaintext(),
			),
			expectedAuth: types.AuthTypeUnauthenticatedPlaintext,
			expectedUse:  true,
			description:  "only one auth available --> choose it",
		},
		{
			name: "only iam available",
			cluster: createProvisionedCluster(
				withIAM(),
			),
			expectedAuth: types.AuthTypeIAM,
			expectedUse:  true,
			description:  "only one auth available --> choose it",
		},
		{
			name: "only sasl_scram available",
			cluster: createProvisionedCluster(
				withSASLScram(),
			),
			expectedAuth: types.AuthTypeSASLSCRAM,
			expectedUse:  true,
			description:  "only one auth available --> choose it",
		},
		{
			name: "only unauthenticated_tls available",
			cluster: createProvisionedCluster(
				withUnauthenticatedTLS(),
			),
			expectedAuth: types.AuthTypeUnauthenticatedTLS,
			expectedUse:  true,
			description:  "only one auth available --> choose it",
		},
		{
			name: "only tls available",
			cluster: createProvisionedCluster(
				withTLS(),
			),
			expectedAuth: types.AuthTypeTLS,
			expectedUse:  true,
			description:  "only one auth available --> choose it",
		},
		{
			name: "sasl_scram, tls and iam available --> choose iam",
			cluster: createProvisionedCluster(
				withSASLScram(),
				withTLS(),
				withIAM(),
			),
			expectedAuth: types.AuthTypeIAM,
			expectedUse:  true,
			description:  "sasl_scram, tls and iam available --> choose iam (second priority)",
		},
		{
			name: "all auth methods available --> choose unauthenticated_plaintext",
			cluster: createProvisionedCluster(
				withUnauthenticatedPlaintext(),
				withIAM(),
				withSASLScram(),
				withUnauthenticatedTLS(),
				withTLS(),
			),
			expectedAuth: types.AuthTypeUnauthenticatedPlaintext,
			expectedUse:  true,
			description:  "all available --> choose unauthenticated_plaintext (first priority)",
		},
		{
			name: "iam and sasl_scram available --> choose iam",
			cluster: createProvisionedCluster(
				withIAM(),
				withSASLScram(),
			),
			expectedAuth: types.AuthTypeIAM,
			expectedUse:  true,
			description:  "iam and sasl_scram available --> choose iam (higher priority)",
		},
		{
			name: "sasl_scram and unauthenticated_tls available --> choose sasl_scram",
			cluster: createProvisionedCluster(
				withSASLScram(),
				withUnauthenticatedTLS(),
			),
			expectedAuth: types.AuthTypeSASLSCRAM,
			expectedUse:  true,
			description:  "sasl_scram and unauthenticated_tls available --> choose sasl_scram (higher priority)",
		},
		{
			name: "unauthenticated_tls and tls available --> choose unauthenticated_tls",
			cluster: createProvisionedCluster(
				withUnauthenticatedTLS(),
				withTLS(),
			),
			expectedAuth: types.AuthTypeUnauthenticatedTLS,
			expectedUse:  true,
			description:  "unauthenticated_tls and tls available --> choose unauthenticated_tls (higher priority)",
		},
		{
			name: "unauthenticated_plaintext and iam available --> choose unauthenticated_plaintext",
			cluster: createProvisionedCluster(
				withUnauthenticatedPlaintext(),
				withIAM(),
			),
			expectedAuth: types.AuthTypeUnauthenticatedPlaintext,
			expectedUse:  true,
			description:  "unauthenticated_plaintext and iam available --> choose unauthenticated_plaintext (higher priority)",
		},
		{
			name:         "serverless cluster --> choose iam",
			cluster:      createServerlessCluster(),
			expectedAuth: types.AuthTypeIAM,
			expectedUse:  true,
			description:  "serverless clusters only support IAM authentication",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := d.getAvailableClusterAuthOptions(tt.cluster)
			assert.NoError(t, err)
			assert.NotNil(t, result)

			// Verify the expected auth method is selected
			selectedAuth, err := result.GetSelectedAuthType()
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedAuth, selectedAuth, tt.description)

			// Verify only one auth method has Use=true
			enabledMethods := result.GetAuthMethods()
			assert.Len(t, enabledMethods, 1, "should have exactly one enabled auth method")
			assert.Equal(t, tt.expectedAuth, enabledMethods[0])

			// Verify the Use flag is set correctly
			switch tt.expectedAuth {
			case types.AuthTypeUnauthenticatedPlaintext:
				assert.NotNil(t, result.AuthMethod.UnauthenticatedPlaintext)
				assert.Equal(t, tt.expectedUse, result.AuthMethod.UnauthenticatedPlaintext.Use)
			case types.AuthTypeIAM:
				assert.NotNil(t, result.AuthMethod.IAM)
				assert.Equal(t, tt.expectedUse, result.AuthMethod.IAM.Use)
			case types.AuthTypeSASLSCRAM:
				assert.NotNil(t, result.AuthMethod.SASLScram)
				assert.Equal(t, tt.expectedUse, result.AuthMethod.SASLScram.Use)
			case types.AuthTypeUnauthenticatedTLS:
				assert.NotNil(t, result.AuthMethod.UnauthenticatedTLS)
				assert.Equal(t, tt.expectedUse, result.AuthMethod.UnauthenticatedTLS.Use)
			case types.AuthTypeTLS:
				assert.NotNil(t, result.AuthMethod.TLS)
				assert.Equal(t, tt.expectedUse, result.AuthMethod.TLS.Use)
			}

			// Verify other auth methods are either nil or have Use=false
			if tt.expectedAuth != types.AuthTypeUnauthenticatedPlaintext {
				if result.AuthMethod.UnauthenticatedPlaintext != nil {
					assert.False(t, result.AuthMethod.UnauthenticatedPlaintext.Use)
				}
			}
			if tt.expectedAuth != types.AuthTypeIAM {
				if result.AuthMethod.IAM != nil {
					assert.False(t, result.AuthMethod.IAM.Use)
				}
			}
			if tt.expectedAuth != types.AuthTypeSASLSCRAM {
				if result.AuthMethod.SASLScram != nil {
					assert.False(t, result.AuthMethod.SASLScram.Use)
				}
			}
			if tt.expectedAuth != types.AuthTypeUnauthenticatedTLS {
				if result.AuthMethod.UnauthenticatedTLS != nil {
					assert.False(t, result.AuthMethod.UnauthenticatedTLS.Use)
				}
			}
			if tt.expectedAuth != types.AuthTypeTLS {
				if result.AuthMethod.TLS != nil {
					assert.False(t, result.AuthMethod.TLS.Use)
				}
			}
		})
	}
}

// Helper functions to create test clusters

type clusterOption func(*kafkatypes.Cluster)

func createProvisionedCluster(opts ...clusterOption) kafkatypes.Cluster {
	cluster := kafkatypes.Cluster{
		ClusterType: kafkatypes.ClusterTypeProvisioned,
		ClusterName: aws.String("test-cluster"),
		ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123"),
		Provisioned: &kafkatypes.Provisioned{
			ClientAuthentication: &kafkatypes.ClientAuthentication{},
			EncryptionInfo: &kafkatypes.EncryptionInfo{
				EncryptionInTransit: &kafkatypes.EncryptionInTransit{},
			},
		},
	}

	for _, opt := range opts {
		opt(&cluster)
	}

	return cluster
}

func createServerlessCluster() kafkatypes.Cluster {
	return kafkatypes.Cluster{
		ClusterType: kafkatypes.ClusterTypeServerless,
		ClusterName: aws.String("test-serverless-cluster"),
		ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-serverless-cluster/abc-123"),
	}
}

func withIAM() clusterOption {
	return func(c *kafkatypes.Cluster) {
		if c.Provisioned.ClientAuthentication.Sasl == nil {
			c.Provisioned.ClientAuthentication.Sasl = &kafkatypes.Sasl{}
		}
		c.Provisioned.ClientAuthentication.Sasl.Iam = &kafkatypes.Iam{
			Enabled: aws.Bool(true),
		}
	}
}

func withSASLScram() clusterOption {
	return func(c *kafkatypes.Cluster) {
		if c.Provisioned.ClientAuthentication.Sasl == nil {
			c.Provisioned.ClientAuthentication.Sasl = &kafkatypes.Sasl{}
		}
		c.Provisioned.ClientAuthentication.Sasl.Scram = &kafkatypes.Scram{
			Enabled: aws.Bool(true),
		}
	}
}

func withTLS() clusterOption {
	return func(c *kafkatypes.Cluster) {
		c.Provisioned.ClientAuthentication.Tls = &kafkatypes.Tls{
			Enabled: aws.Bool(true),
		}
	}
}

func withUnauthenticatedPlaintext() clusterOption {
	return func(c *kafkatypes.Cluster) {
		c.Provisioned.ClientAuthentication.Unauthenticated = &kafkatypes.Unauthenticated{
			Enabled: aws.Bool(true),
		}
		// If TLS is already set, use TlsPlaintext to enable both
		if c.Provisioned.EncryptionInfo.EncryptionInTransit.ClientBroker == kafkatypes.ClientBrokerTls {
			c.Provisioned.EncryptionInfo.EncryptionInTransit.ClientBroker = kafkatypes.ClientBrokerTlsPlaintext
		} else {
			c.Provisioned.EncryptionInfo.EncryptionInTransit.ClientBroker = kafkatypes.ClientBrokerPlaintext
		}
	}
}

func withUnauthenticatedTLS() clusterOption {
	return func(c *kafkatypes.Cluster) {
		c.Provisioned.ClientAuthentication.Unauthenticated = &kafkatypes.Unauthenticated{
			Enabled: aws.Bool(true),
		}
		// If Plaintext is already set, use TlsPlaintext to enable both
		if c.Provisioned.EncryptionInfo.EncryptionInTransit.ClientBroker == kafkatypes.ClientBrokerPlaintext {
			c.Provisioned.EncryptionInfo.EncryptionInTransit.ClientBroker = kafkatypes.ClientBrokerTlsPlaintext
		} else {
			c.Provisioned.EncryptionInfo.EncryptionInTransit.ClientBroker = kafkatypes.ClientBrokerTls
		}
	}
}
