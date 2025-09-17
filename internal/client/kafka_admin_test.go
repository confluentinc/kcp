package client

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/IBM/sarama"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockClusterAdmin implements sarama.ClusterAdmin for testing
type MockClusterAdmin struct {
	topics        map[string]sarama.TopicDetail
	topicsError   error
	brokers       []*sarama.Broker
	controllerID  int32
	describeError error
	closeError    error
}

func (m *MockClusterAdmin) ListTopics() (map[string]sarama.TopicDetail, error) {
	return m.topics, m.topicsError
}

func (m *MockClusterAdmin) DescribeCluster() ([]*sarama.Broker, int32, error) {
	return m.brokers, m.controllerID, m.describeError
}

func (m *MockClusterAdmin) Close() error {
	return m.closeError
}

// Implement missing methods from sarama.ClusterAdmin interface
func (m *MockClusterAdmin) AlterClientQuotas(components []sarama.QuotaEntityComponent, op sarama.ClientQuotasOp, validateOnly bool) error {
	return fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) AlterConfig(resource sarama.ConfigResourceType, name string, entries map[string]*string, validateOnly bool) error {
	return fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) AlterPartitionReassignments(topic string, assignment [][]int32) error {
	return fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) AlterUserScramCredentials(delete []sarama.AlterUserScramCredentialsDelete, upsert []sarama.AlterUserScramCredentialsUpsert) (*sarama.AlterUserScramCredentialsResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) CreateACL(resource sarama.Resource, acl sarama.Acl) error {
	return fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) CreatePartitions(topic string, count int32, assignment [][]int32, validateOnly bool) error {
	return fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) CreateTopic(topic string, detail *sarama.TopicDetail, validateOnly bool) error {
	return fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) DeleteACL(filter []sarama.AclFilter, validateOnly bool) ([]sarama.MatchingAcl, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) DeleteRecords(topic string, partitionOffsets map[int32]int64) error {
	return fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) DeleteTopic(topic string) error {
	return fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) DescribeACL(filter []sarama.AclFilter) ([]sarama.ResourceAcls, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) DescribeClientQuotas(components []sarama.QuotaFilterComponent, strict bool) ([]sarama.DescribeClientQuotasEntry, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) DescribeConfig(resource sarama.ConfigResourceType, name string) ([]sarama.ConfigEntry, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) DescribeLogDirs(brokers []int32) (map[int32][]sarama.DescribeLogDirsResponseDirMetadata, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) DescribeUserScramCredentials(users []string) ([]*sarama.DescribeUserScramCredentialsResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) IncrementalAlterConfig(resource sarama.ConfigResourceType, name string, entries map[string]sarama.IncrementalAlterConfigsEntry, validateOnly bool) error {
	return fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) ListPartitionReassignments(topics map[string][]int32) (map[string]map[int32]*sarama.PartitionReplicaReassignmentsStatus, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) Metadata() (*sarama.MetadataResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) RemoveMemberFromConsumerGroup(groupID string, groupInstanceIds []string) (*sarama.LeaveGroupResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) ResourceName(resource sarama.ConfigResourceType, name string) string {
	return fmt.Sprintf("%v:%s", resource, name)
}

func (m *MockClusterAdmin) SupportedFeatures() map[sarama.ConfigResourceType]bool {
	return nil
}

func (m *MockClusterAdmin) ValidateOnlySupported() bool {
	return false
}

func (m *MockClusterAdmin) Controller() (*sarama.Broker, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *MockClusterAdmin) Coordinator(consumerGroup string) (*sarama.Broker, error) {
	return nil, fmt.Errorf("not implemented")
}

// MockBroker implements sarama.Broker for testing
type MockBroker struct {
	addr          string
	metadata      *sarama.MetadataResponse
	metadataError error
	openError     error
	closeError    error
}

func (m *MockBroker) Addr() string {
	return m.addr
}

func (m *MockBroker) Open(config *sarama.Config) error {
	return m.openError
}

func (m *MockBroker) Close() error {
	return m.closeError
}

func (m *MockBroker) GetMetadata(request *sarama.MetadataRequest) (*sarama.MetadataResponse, error) {
	return m.metadata, m.metadataError
}

// Helper function to create test certificates
func createTestCertificates(t *testing.T) (string, string, string) {
	// Create CA certificate
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test CA"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour * 24),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caPrivKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &caPrivKey.PublicKey, caPrivKey)
	require.NoError(t, err)

	// Create client certificate
	client := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization: []string{"Test Client"},
		},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(time.Hour * 24),
		DNSNames:    []string{"localhost"},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		KeyUsage:    x509.KeyUsageDigitalSignature,
	}

	clientPrivKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	clientBytes, err := x509.CreateCertificate(rand.Reader, client, ca, &clientPrivKey.PublicKey, caPrivKey)
	require.NoError(t, err)

	// Create temporary files
	tempDir := t.TempDir()
	caCertFile := filepath.Join(tempDir, "ca.crt")
	clientCertFile := filepath.Join(tempDir, "client.crt")
	clientKeyFile := filepath.Join(tempDir, "client.key")

	// Write CA certificate
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caBytes})
	err = os.WriteFile(caCertFile, caPEM, 0644)
	require.NoError(t, err)

	// Write client certificate
	clientPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientBytes})
	err = os.WriteFile(clientCertFile, clientPEM, 0644)
	require.NoError(t, err)

	// Write client private key
	clientKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(clientPrivKey),
	})
	err = os.WriteFile(clientKeyFile, clientKeyPEM, 0600)
	require.NoError(t, err)

	return caCertFile, clientCertFile, clientKeyFile
}

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
			name:   "WithUnauthenticatedTlsAuth sets unauthenticated TLS auth",
			option: WithUnauthenticatedTlsAuth(),
			expectedConfig: AdminConfig{
				authType: types.AuthTypeUnauthenticatedTLS,
			},
		},
		{
			name:   "WithUnauthenticatedPlaintextAuth sets unauthenticated plaintext auth",
			option: WithUnauthenticatedPlaintextAuth(),
			expectedConfig: AdminConfig{
				authType: types.AuthTypeUnauthenticatedPlaintext,
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

func TestConfigureCommonSettings(t *testing.T) {
	config := sarama.NewConfig()
	clientID := "test-client"

	configureCommonSettings(config, clientID, sarama.V4_0_0_0)

	// Verify common settings
	assert.Equal(t, sarama.V4_0_0_0, config.Version)
	assert.Equal(t, clientID, config.ClientID)
	assert.Equal(t, 10*time.Second, config.Net.DialTimeout)
	assert.Equal(t, 30*time.Second, config.Net.ReadTimeout)
	assert.Equal(t, 30*time.Second, config.Net.KeepAlive)
	assert.Equal(t, 15*time.Second, config.Metadata.Timeout)
	assert.Equal(t, 3, config.Metadata.Retry.Max)
	assert.Equal(t, 250*time.Millisecond, config.Metadata.Retry.Backoff)
}

func TestConfigureSASLTypeOAuthAuthentication(t *testing.T) {
	config := sarama.NewConfig()
	region := "us-west-2"

	configureSASLTypeOAuthAuthentication(config, region)

	// Verify SASL/OAuth configuration
	assert.True(t, config.Net.TLS.Enable)
	assert.NotNil(t, config.Net.TLS.Config)
	assert.True(t, config.Net.SASL.Enable)
	assert.Equal(t, string(sarama.SASLTypeOAuth), string(config.Net.SASL.Mechanism))
	assert.NotNil(t, config.Net.SASL.TokenProvider)

	// Verify token provider is correctly configured
	tokenProvider, ok := config.Net.SASL.TokenProvider.(*MSKAccessTokenProvider)
	assert.True(t, ok)
	assert.Equal(t, region, tokenProvider.region)
}

func TestConfigureSASLTypeSCRAMAuthentication(t *testing.T) {
	config := sarama.NewConfig()
	username := "test-user"
	password := "test-pass"

	configureSASLTypeSCRAMAuthentication(config, username, password)

	// Verify SASL/SCRAM configuration
	assert.True(t, config.Net.TLS.Enable)
	assert.NotNil(t, config.Net.TLS.Config)
	assert.True(t, config.Net.SASL.Enable)
	assert.Equal(t, username, config.Net.SASL.User)
	assert.Equal(t, password, config.Net.SASL.Password)
	assert.True(t, config.Net.SASL.Handshake)
	assert.Equal(t, string(sarama.SASLTypeSCRAMSHA512), string(config.Net.SASL.Mechanism))
	assert.NotNil(t, config.Net.SASL.SCRAMClientGeneratorFunc)

	// Verify SCRAM client generator function
	scramClient := config.Net.SASL.SCRAMClientGeneratorFunc()
	assert.NotNil(t, scramClient)
}

func TestConfigureUnauthenticatedAuthentication(t *testing.T) {
	tests := []struct {
		name                            string
		clientBrokerEncryptionInTransit kafkatypes.ClientBroker
		expectedTLSEnabled              bool
	}{
		{
			name:                            "TLS encryption enabled for TLS",
			clientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
			expectedTLSEnabled:              true,
		},
		{
			name:                            "TLS encryption enabled for TLS_PLAINTEXT",
			clientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTlsPlaintext,
			expectedTLSEnabled:              true,
		},
		{
			name:                            "TLS encryption disabled for PLAINTEXT",
			clientBrokerEncryptionInTransit: kafkatypes.ClientBrokerPlaintext,
			expectedTLSEnabled:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := sarama.NewConfig()

			// Convert ClientBroker to boolean for the function call
			withTLSEncryption := tt.clientBrokerEncryptionInTransit == kafkatypes.ClientBrokerTls ||
				tt.clientBrokerEncryptionInTransit == kafkatypes.ClientBrokerTlsPlaintext
			configureUnauthenticatedAuthentication(config, withTLSEncryption)

			assert.Equal(t, tt.expectedTLSEnabled, config.Net.TLS.Enable)
			if tt.expectedTLSEnabled {
				assert.NotNil(t, config.Net.TLS.Config)
			}
		})
	}
}

func TestConfigureTLSAuth(t *testing.T) {
	caCertFile, clientCertFile, clientKeyFile := createTestCertificates(t)

	tests := []struct {
		name           string
		caCertFile     string
		clientCertFile string
		clientKeyFile  string
		expectError    bool
		errorContains  string
	}{
		{
			name:           "successful TLS configuration",
			caCertFile:     caCertFile,
			clientCertFile: clientCertFile,
			clientKeyFile:  clientKeyFile,
			expectError:    false,
		},
		{
			name:           "client certificate file not found",
			caCertFile:     caCertFile,
			clientCertFile: "nonexistent.crt",
			clientKeyFile:  clientKeyFile,
			expectError:    true,
			errorContains:  "failed to load client certificate",
		},
		{
			name:           "client key file not found",
			caCertFile:     caCertFile,
			clientCertFile: clientCertFile,
			clientKeyFile:  "nonexistent.key",
			expectError:    true,
			errorContains:  "failed to load client certificate",
		},
		{
			name:           "CA certificate file not found",
			caCertFile:     "nonexistent-ca.crt",
			clientCertFile: clientCertFile,
			clientKeyFile:  clientKeyFile,
			expectError:    true,
			errorContains:  "failed to read CA certificate file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := sarama.NewConfig()

			err := configureTLSAuth(config, tt.caCertFile, tt.clientCertFile, tt.clientKeyFile)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				require.NoError(t, err)
				assert.True(t, config.Net.TLS.Enable)
				assert.NotNil(t, config.Net.TLS.Config)

				// Verify TLS config has certificates
				tlsConfig := config.Net.TLS.Config
				assert.Len(t, tlsConfig.Certificates, 1)
				assert.NotNil(t, tlsConfig.RootCAs)
			}
		})
	}
}

func TestMSKAccessTokenProvider_Token(t *testing.T) {

	t.Skip("skipping integration test that requires credentials configuration")

	// TODO: Fix this test to not require credentials configuration

	provider := &MSKAccessTokenProvider{
		region: "us-west-2",
	}

	// Note: This test will fail if AWS credentials are not properly configured
	// or if there are network issues. In a real test environment, you might
	// want to mock the AWS signer.
	token, err := provider.Token()

	// We can't easily test the actual token generation without AWS credentials,
	// but we can test the structure and error handling
	if err != nil {
		// If there's an error (e.g., no AWS credentials), that's expected
		assert.Contains(t, err.Error(), "NoCredentialProviders")
	} else {
		// If successful, verify token structure
		assert.NotNil(t, token)
		assert.NotEmpty(t, token.Token)
	}
}

func TestNewKafkaAdmin(t *testing.T) {
	t.Skip("skipping integration test that requires real Kafka brokers")
	caCertFile, clientCertFile, clientKeyFile := createTestCertificates(t)

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
			brokerAddresses:                 []string{"broker1:9092"},
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
			name:                            "successful TLS auth creation",
			brokerAddresses:                 []string{"broker1:9094"},
			clientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
			region:                          "us-west-2",
			opts:                            []AdminOption{WithTLSAuth(caCertFile, clientCertFile, clientKeyFile)},
			expectError:                     false,
		},
		{
			name:                            "TLS auth with invalid certificate files",
			brokerAddresses:                 []string{"broker1:9094"},
			clientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
			region:                          "us-west-2",
			opts:                            []AdminOption{WithTLSAuth("invalid.crt", "invalid.crt", "invalid.key")},
			expectError:                     true,
			errorContains:                   "Failed to configure TLS authentication",
		},
		{
			name:                            "unsupported auth type",
			brokerAddresses:                 []string{"broker1:9092"},
			clientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
			region:                          "us-west-2",
			opts:                            []AdminOption{},
			expectError:                     false, // Defaults to IAM auth
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
	// Test that NewKafkaAdmin uses IAM auth by default
	admin, err := NewKafkaAdmin([]string{"broker1:9098"}, kafkatypes.ClientBrokerTls, "us-west-2", "4.0.0")

	// This will likely fail due to network/credentials, but we can verify the error message
	if err != nil {
		// The error should be related to creating the admin client, not auth type
		assert.NotContains(t, err.Error(), "Auth type")
		assert.Contains(t, err.Error(), "Failed to create admin client")
	} else {
		require.NotNil(t, admin)
		admin.Close()
	}
}

func TestNewKafkaAdmin_MultipleOptions(t *testing.T) {
	t.Skip("skipping integration test that requires real Kafka brokers")
	// Test that multiple options can be applied
	opts := []AdminOption{
		WithIAMAuth(),
		WithSASLSCRAMAuth("user", "pass"), // This should override the IAM auth
	}

	admin, err := NewKafkaAdmin([]string{"broker1:9096"}, kafkatypes.ClientBrokerTls, "us-west-2", "4.0.0", opts...)

	// This will likely fail due to network/credentials, but we can verify the error message
	if err != nil {
		// The error should be related to creating the admin client, not auth type
		assert.NotContains(t, err.Error(), "Auth type")
		assert.Contains(t, err.Error(), "Failed to create admin client")
	} else {
		require.NotNil(t, admin)
		admin.Close()
	}
}

func TestKafkaAdminInterface(t *testing.T) {
	// Test that KafkaAdminClient properly implements the KafkaAdmin interface
	var _ KafkaAdmin = (*KafkaAdminClient)(nil)
}

func TestClusterKafkaMetadata_Structure(t *testing.T) {
	// Test the ClusterKafkaMetadata structure
	metadata := &ClusterKafkaMetadata{
		Brokers: []*sarama.Broker{
			sarama.NewBroker("broker1:9092"),
			sarama.NewBroker("broker2:9092"),
		},
		ControllerID: 1,
		ClusterID:    "test-cluster",
	}

	assert.Len(t, metadata.Brokers, 2)
	assert.Equal(t, int32(1), metadata.ControllerID)
	assert.Equal(t, "test-cluster", metadata.ClusterID)
}

func TestSaramaKafkaVersionParsing(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedOutput sarama.KafkaVersion
	}{
		{
			name:           "4.0.x.kraft should convert to sarama.V4_0_0_0",
			input:          "4.0.x.kraft",
			expectedOutput: sarama.V4_0_0_0,
		},
		{
			name:           "3.9.x should convert to sarama.V3_9_0_0",
			input:          "3.9.x",
			expectedOutput: sarama.V3_9_0_0,
		},
		{
			name:           "3.9.x.kraft should convert to sarama.V3_9_0_0",
			input:          "3.9.x.kraft",
			expectedOutput: sarama.V3_9_0_0,
		},
		{
			name:           "3.7.x.kraft should convert to sarama.V3_7_0_0",
			input:          "3.7.x.kraft",
			expectedOutput: sarama.V3_7_0_0,
		},
		{
			name:           "3.6.0.1 should convert to sarama.V3_6_0_0",
			input:          "3.6.0.1",
			expectedOutput: sarama.V3_6_0_0,
		},
		{
			name:           "3.6.0 should remain sarama.V3_6_0_0",
			input:          "3.6.0",
			expectedOutput: sarama.V3_6_0_0,
		},
		{
			name:           "2.8.2.tiered should convert to sarama.V2_8_2_0",
			input:          "2.8.2.tiered",
			expectedOutput: sarama.V2_8_2_0,
		},
		{
			name:           "2.6.0 should remain sarama.V2_6_0_0",
			input:          "2.6.0",
			expectedOutput: sarama.V2_6_0_0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := sarama.ParseKafkaVersion(utils.ConvertKafkaVersion(&tt.input))
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedOutput, result)
		})
	}
}
