package client

import (
	"testing"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminOptionFunctions(t *testing.T) {
	tests := []struct {
		name           string
		option         AdminOption
		expectedConfig AdminConfig
	}{
		{
			name:   "WithIAMAuth sets IAM auth type",
			option: WithIAMAuth(),
			expectedConfig: AdminConfig{
				authType: types.AuthTypeIAM,
			},
		},
		{
			name:   "WithSASLSCRAMAuth sets SASL/SCRAM auth",
			option: WithSASLSCRAMAuth("test-user", "test-pass"),
			expectedConfig: AdminConfig{
				authType: types.AuthTypeSASLSCRAM,
				username: "test-user",
				password: "test-pass",
			},
		},
		{
			name:   "WithUnauthenticatedAuth sets unauthenticated auth",
			option: WithUnauthenticatedTlsAuth(),
			expectedConfig: AdminConfig{
				authType: types.AuthTypeUnauthenticatedTLS,
			},
		},
		{
			name:   "WithTLSAuth sets TLS auth with certificate files",
			option: WithTLSAuth("ca.crt", "client.crt", "client.key"),
			expectedConfig: AdminConfig{
				authType:       types.AuthTypeTLS,
				caCertFile:     "ca.crt",
				clientCertFile: "client.crt",
				clientKeyFile:  "client.key",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := AdminConfig{}
			tt.option(&config)

			assert.Equal(t, tt.expectedConfig.authType, config.authType)
			assert.Equal(t, tt.expectedConfig.username, config.username)
			assert.Equal(t, tt.expectedConfig.password, config.password)
			assert.Equal(t, tt.expectedConfig.awsAccessKey, config.awsAccessKey)
			assert.Equal(t, tt.expectedConfig.awsAccessSecret, config.awsAccessSecret)
			assert.Equal(t, tt.expectedConfig.caCertFile, config.caCertFile)
			assert.Equal(t, tt.expectedConfig.clientCertFile, config.clientCertFile)
			assert.Equal(t, tt.expectedConfig.clientKeyFile, config.clientKeyFile)
		})
	}
}

func TestBuildConfigMap(t *testing.T) {
	tests := []struct {
		name             string
		brokerAddresses  []string
		config           AdminConfig
		expectError      bool
		expectedKeys     map[string]string
		unexpectedKeys   []string
	}{
		{
			name:            "IAM auth produces correct config",
			brokerAddresses: []string{"broker1:9098", "broker2:9098"},
			config: AdminConfig{
				authType: types.AuthTypeIAM,
			},
			expectedKeys: map[string]string{
				"bootstrap.servers": "broker1:9098,broker2:9098",
				"client.id":         "kcp-cli",
				"security.protocol": "SASL_SSL",
				"sasl.mechanisms":    "OAUTHBEARER",
			},
		},
		{
			name:            "SASL/SCRAM auth produces correct config",
			brokerAddresses: []string{"broker1:9096"},
			config: AdminConfig{
				authType: types.AuthTypeSASLSCRAM,
				username: "myuser",
				password: "mypass",
			},
			expectedKeys: map[string]string{
				"bootstrap.servers": "broker1:9096",
				"security.protocol": "SASL_SSL",
				"sasl.mechanisms":    "SCRAM-SHA-512",
				"sasl.username":      "myuser",
				"sasl.password":      "mypass",
			},
		},
		{
			name:            "Unauthenticated TLS produces correct config",
			brokerAddresses: []string{"broker1:9094"},
			config: AdminConfig{
				authType: types.AuthTypeUnauthenticatedTLS,
			},
			expectedKeys: map[string]string{
				"security.protocol": "SSL",
			},
			unexpectedKeys: []string{"sasl.mechanisms", "sasl.username"},
		},
		{
			name:            "Unauthenticated Plaintext produces correct config",
			brokerAddresses: []string{"broker1:9092"},
			config: AdminConfig{
				authType: types.AuthTypeUnauthenticatedPlaintext,
			},
			expectedKeys: map[string]string{
				"security.protocol": "PLAINTEXT",
			},
			unexpectedKeys: []string{"sasl.mechanisms"},
		},
		{
			name:            "TLS auth produces correct config",
			brokerAddresses: []string{"broker1:9094"},
			config: AdminConfig{
				authType:       types.AuthTypeTLS,
				caCertFile:     "/path/to/ca.crt",
				clientCertFile: "/path/to/client.crt",
				clientKeyFile:  "/path/to/client.key",
			},
			expectedKeys: map[string]string{
				"security.protocol":      "SSL",
				"ssl.ca.location":        "/path/to/ca.crt",
				"ssl.certificate.location": "/path/to/client.crt",
				"ssl.key.location":       "/path/to/client.key",
			},
		},
		{
			name:            "Unsupported auth type returns error",
			brokerAddresses: []string{"broker1:9092"},
			config: AdminConfig{
				authType: types.AuthType("UNSUPPORTED"),
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configMap, err := buildConfigMap(tt.brokerAddresses, tt.config)

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			for key, expectedValue := range tt.expectedKeys {
				val, err := configMap.Get(key, "")
				require.NoError(t, err, "key %s should exist", key)
				assert.Equal(t, expectedValue, val, "key %s should have correct value", key)
			}

			for _, key := range tt.unexpectedKeys {
				val, err := configMap.Get(key, nil)
				require.NoError(t, err)
				assert.Nil(t, val, "key %s should not be set", key)
			}
		})
	}
}

func TestKafkaAdminInterface(t *testing.T) {
	// Test that KafkaAdminClient properly implements the KafkaAdmin interface
	var _ KafkaAdmin = (*KafkaAdminClient)(nil)
}

func TestClusterKafkaMetadata_Structure(t *testing.T) {
	// Test the ClusterKafkaMetadata structure
	metadata := &ClusterKafkaMetadata{
		Brokers: []types.BrokerInfo{
			{ID: 1, Address: "broker1:9092"},
			{ID: 2, Address: "broker2:9092"},
		},
		ControllerID: 1,
		ClusterID:    "test-cluster",
	}

	assert.Len(t, metadata.Brokers, 2)
	assert.Equal(t, int32(1), metadata.ControllerID)
	assert.Equal(t, "test-cluster", metadata.ClusterID)
}

func TestNewKafkaAdmin(t *testing.T) {
	t.Skip("skipping integration test that requires real Kafka brokers")

	tests := []struct {
		name                            string
		brokerAddresses                 []string
		clientBrokerEncryptionInTransit kafkatypes.ClientBroker
		region                          string
		opts                            []AdminOption
		expectError                     bool
		errorContains                   string
	}{
		{
			name:                            "successful IAM auth creation",
			brokerAddresses:                 []string{"broker1:9098", "broker2:9098"},
			clientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
			region:                          "us-west-2",
			opts:                            []AdminOption{WithIAMAuth()},
			expectError:                     false,
		},
		{
			name:                            "successful SASL/SCRAM auth creation",
			brokerAddresses:                 []string{"broker1:9096"},
			clientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
			region:                          "us-west-2",
			opts:                            []AdminOption{WithSASLSCRAMAuth("user", "pass")},
			expectError:                     false,
		},
		{
			name:                            "successful unauthenticated TLS auth creation",
			brokerAddresses:                 []string{"broker1:9094"},
			clientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
			region:                          "us-west-2",
			opts:                            []AdminOption{WithUnauthenticatedTlsAuth()},
			expectError:                     false,
		},
		{
			name:                            "successful unauthenticated plaintext auth creation",
			brokerAddresses:                 []string{"broker1:9092"},
			clientBrokerEncryptionInTransit: kafkatypes.ClientBrokerPlaintext,
			region:                          "us-west-2",
			opts:                            []AdminOption{WithUnauthenticatedPlaintextAuth()},
			expectError:                     false,
		},
		{
			name:                            "empty broker addresses",
			brokerAddresses:                 []string{},
			clientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
			region:                          "us-west-2",
			opts:                            []AdminOption{WithIAMAuth()},
			expectError:                     true,
			errorContains:                   "Failed to create admin client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			admin, err := NewKafkaAdmin(tt.brokerAddresses, tt.clientBrokerEncryptionInTransit, tt.region, "4.0.0", tt.opts...)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
				assert.Nil(t, admin)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, admin)
				admin.Close()
			}
		})
	}
}

func TestNewKafkaAdmin_DefaultConfiguration(t *testing.T) {
	t.Skip("skipping integration test that requires real Kafka brokers")
	admin, err := NewKafkaAdmin([]string{"broker1:9098"}, kafkatypes.ClientBrokerTls, "us-west-2", "4.0.0")

	if err != nil {
		assert.NotContains(t, err.Error(), "Auth type")
		assert.Contains(t, err.Error(), "Failed to create admin client")
	} else {
		require.NotNil(t, admin)
		admin.Close()
	}
}

func TestNewKafkaAdmin_MultipleOptions(t *testing.T) {
	t.Skip("skipping integration test that requires real Kafka brokers")
	opts := []AdminOption{
		WithIAMAuth(),
		WithSASLSCRAMAuth("user", "pass"),
	}

	admin, err := NewKafkaAdmin([]string{"broker1:9096"}, kafkatypes.ClientBrokerTls, "us-west-2", "4.0.0", opts...)

	if err != nil {
		assert.NotContains(t, err.Error(), "Auth type")
		assert.Contains(t, err.Error(), "Failed to create admin client")
	} else {
		require.NotNil(t, admin)
		admin.Close()
	}
}
