package client

import (
	"crypto/ecdsa"
	"crypto/elliptic"
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
			option: WithSASLSCRAMAuth("test-user", "test-pass", "SHA256", "", false),
			expectedConfig: AdminConfig{
				authType:      types.AuthTypeSASLSCRAM,
				username:      "test-user",
				password:      "test-pass",
				saslMechanism: "SHA256",
			},
		},
		{
			name:   "WithUnauthenticatedAuth sets unauthenticated auth",
			option: WithUnauthenticatedTlsAuth("", false),
			expectedConfig: AdminConfig{
				authType: types.AuthTypeUnauthenticatedTLS,
			},
		},
		{
			name:   "WithTLSAuth sets TLS auth with certificate files",
			option: WithTLSAuth("ca.crt", "client.crt", "client.key", false),
			expectedConfig: AdminConfig{
				authType:       types.AuthTypeTLS,
				caCertFile:     "ca.crt",
				clientCertFile: "client.crt",
				clientKeyFile:  "client.key",
			},
		},
		{
			name:   "WithSASLPlainAuth sets SASL/PLAIN auth with TLS",
			option: WithSASLPlainAuth("test-user", "test-pass", "", false),
			expectedConfig: AdminConfig{
				authType: types.AuthTypeSASLPlain,
				username: "test-user",
				password: "test-pass",
			},
		},
		{
			name:   "WithSASLPlainAuthNoTLS sets SASL/PLAIN auth without TLS",
			option: WithSASLPlainAuthNoTLS("test-user", "test-pass"),
			expectedConfig: AdminConfig{
				authType:   types.AuthTypeSASLPlain,
				username:   "test-user",
				password:   "test-pass",
				disableTLS: true,
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
			assert.Equal(t, tt.expectedConfig.saslMechanism, config.saslMechanism)
			assert.Equal(t, tt.expectedConfig.awsAccessKey, config.awsAccessKey)
			assert.Equal(t, tt.expectedConfig.awsAccessSecret, config.awsAccessSecret)
			assert.Equal(t, tt.expectedConfig.caCertFile, config.caCertFile)
			assert.Equal(t, tt.expectedConfig.clientCertFile, config.clientCertFile)
			assert.Equal(t, tt.expectedConfig.clientKeyFile, config.clientKeyFile)
			assert.Equal(t, tt.expectedConfig.disableTLS, config.disableTLS)
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

	configureSASLTypeOAuthAuthentication(config, region, false)

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
	tests := []struct {
		name              string
		mechanism         string
		expectedMechanism sarama.SASLMechanism
	}{
		{
			name:              "SHA256 mechanism",
			mechanism:         "SHA256",
			expectedMechanism: sarama.SASLTypeSCRAMSHA256,
		},
		{
			name:              "SHA512 mechanism",
			mechanism:         "SHA512",
			expectedMechanism: sarama.SASLTypeSCRAMSHA512,
		},
		{
			name:              "empty mechanism defaults to SHA256",
			mechanism:         "",
			expectedMechanism: sarama.SASLTypeSCRAMSHA256,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := sarama.NewConfig()
			username := "test-user"
			password := "test-pass"

			require.NoError(t, configureSASLTypeSCRAMAuthentication(config, username, password, tt.mechanism, "", false))

			// Verify SASL/SCRAM configuration
			assert.True(t, config.Net.TLS.Enable)
			assert.NotNil(t, config.Net.TLS.Config)
			assert.True(t, config.Net.SASL.Enable)
			assert.Equal(t, username, config.Net.SASL.User)
			assert.Equal(t, password, config.Net.SASL.Password)
			assert.True(t, config.Net.SASL.Handshake)
			assert.Equal(t, string(tt.expectedMechanism), string(config.Net.SASL.Mechanism))
			assert.NotNil(t, config.Net.SASL.SCRAMClientGeneratorFunc)

			// Verify SCRAM client generator function
			scramClient := config.Net.SASL.SCRAMClientGeneratorFunc()
			assert.NotNil(t, scramClient)
		})
	}
}

func TestConfigureSASLTypePlainAuthentication(t *testing.T) {
	tests := []struct {
		name               string
		withTLSEncryption  bool
		expectedTLSEnabled bool
	}{
		{
			name:               "with TLS encryption (SASL_SSL)",
			withTLSEncryption:  true,
			expectedTLSEnabled: true,
		},
		{
			name:               "without TLS encryption (SASL_PLAINTEXT)",
			withTLSEncryption:  false,
			expectedTLSEnabled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := sarama.NewConfig()
			err := configureSASLTypePlainAuthentication(config, "user", "pass", tt.withTLSEncryption, "", false)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedTLSEnabled, config.Net.TLS.Enable)
			assert.True(t, config.Net.SASL.Enable)
			assert.Equal(t, "user", config.Net.SASL.User)
			assert.Equal(t, "pass", config.Net.SASL.Password)
			assert.Equal(t, string(sarama.SASLTypePlaintext), string(config.Net.SASL.Mechanism))
		})
	}
}

func TestAdminOptionForAuth_SASLPlain(t *testing.T) {
	clusterAuth := types.ClusterAuth{}
	clusterAuth.AuthMethod.SASLPlain = &types.SASLPlainConfig{Use: true, Username: "u", Password: "p"}

	opt, err := AdminOptionForAuthMethod(types.AuthTypeSASLPlain, clusterAuth.AuthMethod, false)
	require.NoError(t, err)

	cfg := &AdminConfig{}
	opt(cfg)
	require.Equal(t, types.AuthTypeSASLPlain, cfg.authType)
	require.Equal(t, "u", cfg.username)
	require.Equal(t, "p", cfg.password)
	// No ca_cert → SASL_PLAINTEXT (TLS disabled). Preserves the SASL_PLAINTEXT
	// listener path (e.g. the osk-scan kafka-sasl-plain test on port 9098).
	require.True(t, cfg.disableTLS)
}

// Regression for R2-H1: a sasl_plain credential carrying a ca_cert must connect
// over SASL_SSL (TLS), trusting the supplied CA — not silently fall back to
// cleartext SASL_PLAINTEXT and discard the CA.
func TestAdminOptionForAuth_SASLPlainWithCACertEnablesTLS(t *testing.T) {
	caCertFile, _, _ := createTestCertificates(t)

	amc := types.AuthMethodConfig{
		SASLPlain: &types.SASLPlainConfig{Use: true, Username: "u", Password: "p", CACert: caCertFile},
	}

	cfg := &AdminConfig{}
	opt, err := AdminOptionForAuthMethod(types.AuthTypeSASLPlain, amc, false)
	require.NoError(t, err)
	opt(cfg)

	// Routing: ca_cert present → TLS variant (disableTLS stays false), CA captured.
	require.Equal(t, types.AuthTypeSASLPlain, cfg.authType)
	require.False(t, cfg.disableTLS, "ca_cert present must select the TLS (SASL_SSL) variant")
	require.Equal(t, caCertFile, cfg.caCertFile)

	// End-to-end: the build path enables TLS and trusts the supplied CA.
	sc := sarama.NewConfig()
	err = configureSASLTypePlainAuthentication(sc, cfg.username, cfg.password, !cfg.disableTLS, cfg.caCertFile, cfg.insecureSkipTLSVerify)
	require.NoError(t, err)
	require.True(t, sc.Net.TLS.Enable, "SASL/PLAIN + ca_cert must enable TLS")
	require.NotNil(t, sc.Net.TLS.Config)
	require.NotNil(t, sc.Net.TLS.Config.RootCAs, "the supplied CA must be trusted, not discarded")
	require.True(t, sc.Net.SASL.Enable)
	require.Equal(t, string(sarama.SASLTypePlaintext), string(sc.Net.SASL.Mechanism))
}

// A bad ca_cert path must surface an error rather than silently connecting in
// cleartext — proving the CA is actually read on the SASL/PLAIN + ca_cert path.
func TestAdminOptionForAuth_SASLPlainBadCACertErrors(t *testing.T) {
	amc := types.AuthMethodConfig{
		SASLPlain: &types.SASLPlainConfig{Use: true, Username: "u", Password: "p", CACert: "/no/such/ca.pem"},
	}
	cfg := &AdminConfig{}
	opt, err := AdminOptionForAuthMethod(types.AuthTypeSASLPlain, amc, false)
	require.NoError(t, err)
	opt(cfg)
	require.False(t, cfg.disableTLS)

	sc := sarama.NewConfig()
	err = configureSASLTypePlainAuthentication(sc, cfg.username, cfg.password, !cfg.disableTLS, cfg.caCertFile, cfg.insecureSkipTLSVerify)
	require.Error(t, err, "an unreadable ca_cert must error, not be silently ignored")
}

func TestAdminOptionForAuth_IAM(t *testing.T) {
	opt, err := AdminOptionForAuthMethod(types.AuthTypeIAM, types.AuthMethodConfig{IAM: &types.IAMConfig{Use: true}}, false)
	require.NoError(t, err)
	cfg := &AdminConfig{}
	opt(cfg)
	require.Equal(t, types.AuthTypeIAM, cfg.authType)
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

			// Determine if TLS should be enabled based on the encryption type
			withTLSEncryption := tt.clientBrokerEncryptionInTransit != kafkatypes.ClientBrokerPlaintext
			err := configureUnauthenticatedAuthentication(config, withTLSEncryption, "", false)
			require.NoError(t, err)

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
			errorContains:  "reading CA certificate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := sarama.NewConfig()

			err := configureTLSAuth(config, tt.caCertFile, tt.clientCertFile, tt.clientKeyFile, false)

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

// writeTestCAPEM generates a self-signed CA, PEM-encodes it, writes it to a
// file in t.TempDir(), and returns the path. No checked-in fixture needed.
func writeTestCAPEM(t *testing.T) string {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(100),
		Subject:               pkix.Name{Organization: []string{"KCP Test CA"}},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	require.NoError(t, err)

	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	path := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(path, pemBytes, 0600))
	return path
}

func TestTLSConfigWithCA_PopulatesRootCAs(t *testing.T) {
	caPath := writeTestCAPEM(t)

	cfg, err := tlsConfigWithCA(caPath, false)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.NotNil(t, cfg.RootCAs)
	assert.False(t, cfg.InsecureSkipVerify)
}

func TestTLSConfigWithCA_EmptyUsesSystemRoots(t *testing.T) {
	cfg, err := tlsConfigWithCA("", false)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	// Empty CA must fall back to system roots (nil RootCAs) — identical to prior behavior.
	assert.Nil(t, cfg.RootCAs)
}

func TestTLSConfigWithCA_BadFile(t *testing.T) {
	cfg, err := tlsConfigWithCA(filepath.Join(t.TempDir(), "does-not-exist.pem"), false)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "reading CA certificate")
}

func TestTLSConfigWithCA_GarbagePEM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garbage.pem")
	require.NoError(t, os.WriteFile(path, []byte("not a pem certificate"), 0600))

	cfg, err := tlsConfigWithCA(path, false)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "contains no valid PEM certificate")
}

func TestConfigureSCRAM_WithCA_SetsRootCAs(t *testing.T) {
	caPath := writeTestCAPEM(t)
	config := sarama.NewConfig()

	err := configureSASLTypeSCRAMAuthentication(config, "user", "pass", "SHA256", caPath, false)
	require.NoError(t, err)
	require.NotNil(t, config.Net.TLS.Config)
	assert.NotNil(t, config.Net.TLS.Config.RootCAs)
}

func TestConfigureUnauthenticatedTLS_WithCA_SetsRootCAs(t *testing.T) {
	caPath := writeTestCAPEM(t)
	config := sarama.NewConfig()

	err := configureUnauthenticatedAuthentication(config, true, caPath, false)
	require.NoError(t, err)
	require.NotNil(t, config.Net.TLS.Config)
	assert.NotNil(t, config.Net.TLS.Config.RootCAs)
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
			opts:                            []AdminOption{WithSASLSCRAMAuth("user", "pass", "SHA256", "", false)},
			expectError:                     false,
		},
		{
			name:                            "successful unauthenticated auth creation",
			brokerAddresses:                 []string{"broker1:9094"},
			clientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
			region:                          "us-west-2",
			opts:                            []AdminOption{WithUnauthenticatedTlsAuth("", false)},
			expectError:                     false,
		},
		{
			name:                            "successful unauthenticated auth creation",
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
			opts:                            []AdminOption{WithTLSAuth(caCertFile, clientCertFile, clientKeyFile, false)},
			expectError:                     false,
		},
		{
			name:                            "TLS auth with invalid certificate files",
			brokerAddresses:                 []string{"broker1:9094"},
			clientBrokerEncryptionInTransit: kafkatypes.ClientBrokerTls,
			region:                          "us-west-2",
			opts:                            []AdminOption{WithTLSAuth("invalid.crt", "invalid.crt", "invalid.key", false)},
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
			errorContains:                   "failed to create admin client",
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
				_ = admin.Close()
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
		assert.Contains(t, err.Error(), "failed to create admin client")
	} else {
		require.NotNil(t, admin)
		_ = admin.Close()
	}
}

func TestNewKafkaAdmin_MultipleOptions(t *testing.T) {
	t.Skip("skipping integration test that requires real Kafka brokers")
	// Test that multiple options can be applied
	opts := []AdminOption{
		WithIAMAuth(),
		WithSASLSCRAMAuth("user", "pass", "SHA256", "", false), // This should override the IAM auth
	}

	admin, err := NewKafkaAdmin([]string{"broker1:9096"}, kafkatypes.ClientBrokerTls, "us-west-2", "4.0.0", opts...)

	// This will likely fail due to network/credentials, but we can verify the error message
	if err != nil {
		// The error should be related to creating the admin client, not auth type
		assert.NotContains(t, err.Error(), "Auth type")
		assert.Contains(t, err.Error(), "failed to create admin client")
	} else {
		require.NotNil(t, admin)
		_ = admin.Close()
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

func TestAdminOptionForAuthMethod(t *testing.T) {
	t.Run("IAM", func(t *testing.T) {
		opt, err := AdminOptionForAuthMethod(types.AuthTypeIAM, types.AuthMethodConfig{
			IAM: &types.IAMConfig{Use: true},
		}, false)
		require.NoError(t, err)
		cfg := AdminConfig{}
		opt(&cfg)
		assert.Equal(t, types.AuthTypeIAM, cfg.authType)
	})

	t.Run("SASL/SCRAM honors skipTLSVerify=false", func(t *testing.T) {
		opt, err := AdminOptionForAuthMethod(types.AuthTypeSASLSCRAM, types.AuthMethodConfig{
			SASLScram: &types.SASLScramConfig{Use: true, Username: "u", Password: "p", Mechanism: "SHA512"},
		}, false)
		require.NoError(t, err)
		cfg := AdminConfig{}
		opt(&cfg)
		assert.Equal(t, types.AuthTypeSASLSCRAM, cfg.authType)
		assert.Equal(t, "u", cfg.username)
		assert.Equal(t, "p", cfg.password)
		assert.Equal(t, "SHA512", cfg.saslMechanism)
		assert.False(t, cfg.insecureSkipTLSVerify)
	})

	t.Run("SASL/SCRAM honors skipTLSVerify=true", func(t *testing.T) {
		opt, err := AdminOptionForAuthMethod(types.AuthTypeSASLSCRAM, types.AuthMethodConfig{
			SASLScram: &types.SASLScramConfig{Use: true, Username: "u", Password: "p", Mechanism: "SHA256"},
		}, true)
		require.NoError(t, err)
		cfg := AdminConfig{}
		opt(&cfg)
		assert.True(t, cfg.insecureSkipTLSVerify)
	})

	t.Run("SASL/PLAIN disables TLS", func(t *testing.T) {
		opt, err := AdminOptionForAuthMethod(types.AuthTypeSASLPlain, types.AuthMethodConfig{
			SASLPlain: &types.SASLPlainConfig{Use: true, Username: "u", Password: "p"},
		}, false)
		require.NoError(t, err)
		cfg := AdminConfig{}
		opt(&cfg)
		assert.Equal(t, types.AuthTypeSASLPlain, cfg.authType)
		assert.True(t, cfg.disableTLS)
	})

	t.Run("TLS", func(t *testing.T) {
		opt, err := AdminOptionForAuthMethod(types.AuthTypeTLS, types.AuthMethodConfig{
			TLS: &types.TLSConfig{Use: true, CACert: "ca.pem", ClientCert: "cert.pem", ClientKey: "key.pem"},
		}, false)
		require.NoError(t, err)
		cfg := AdminConfig{}
		opt(&cfg)
		assert.Equal(t, types.AuthTypeTLS, cfg.authType)
		assert.Equal(t, "ca.pem", cfg.caCertFile)
		assert.Equal(t, "cert.pem", cfg.clientCertFile)
		assert.Equal(t, "key.pem", cfg.clientKeyFile)
	})

	t.Run("UnauthenticatedTLS", func(t *testing.T) {
		opt, err := AdminOptionForAuthMethod(types.AuthTypeUnauthenticatedTLS, types.AuthMethodConfig{
			UnauthenticatedTLS: &types.UnauthenticatedTLSConfig{Use: true},
		}, false)
		require.NoError(t, err)
		cfg := AdminConfig{}
		opt(&cfg)
		assert.Equal(t, types.AuthTypeUnauthenticatedTLS, cfg.authType)
	})

	t.Run("UnauthenticatedPlaintext", func(t *testing.T) {
		opt, err := AdminOptionForAuthMethod(types.AuthTypeUnauthenticatedPlaintext, types.AuthMethodConfig{
			UnauthenticatedPlaintext: &types.UnauthenticatedPlaintextConfig{Use: true},
		}, false)
		require.NoError(t, err)
		cfg := AdminConfig{}
		opt(&cfg)
		assert.Equal(t, types.AuthTypeUnauthenticatedPlaintext, cfg.authType)
	})

	t.Run("unknown auth type returns error", func(t *testing.T) {
		_, err := AdminOptionForAuthMethod(types.AuthType("bogus"), types.AuthMethodConfig{}, false)
		require.Error(t, err)
	})

	t.Run("SASL/SCRAM with nil config returns error", func(t *testing.T) {
		_, err := AdminOptionForAuthMethod(types.AuthTypeSASLSCRAM, types.AuthMethodConfig{}, false)
		require.Error(t, err)
	})

	t.Run("SASL/PLAIN with nil config returns error", func(t *testing.T) {
		_, err := AdminOptionForAuthMethod(types.AuthTypeSASLPlain, types.AuthMethodConfig{}, false)
		require.Error(t, err)
	})

	t.Run("TLS with nil config returns error", func(t *testing.T) {
		_, err := AdminOptionForAuthMethod(types.AuthTypeTLS, types.AuthMethodConfig{}, false)
		require.Error(t, err)
	})

	// Regression for review finding #2: skipTLSVerify must be threaded into EVERY
	// TLS-bearing branch (not just SASL/SCRAM), and a supplied ca_cert must be
	// captured on each — so callers need no separate WithInsecureSkipVerify() override.
	t.Run("skipTLSVerify and ca_cert thread through every TLS branch", func(t *testing.T) {
		cases := []struct {
			name string
			amc  types.AuthMethodConfig
		}{
			{"SASL/SCRAM", types.AuthMethodConfig{SASLScram: &types.SASLScramConfig{Use: true, Username: "u", Password: "p", Mechanism: "SHA512", CACert: "ca.pem"}}},
			{"SASL/PLAIN over TLS", types.AuthMethodConfig{SASLPlain: &types.SASLPlainConfig{Use: true, Username: "u", Password: "p", CACert: "ca.pem"}}},
			{"unauthenticated TLS", types.AuthMethodConfig{UnauthenticatedTLS: &types.UnauthenticatedTLSConfig{Use: true, CACert: "ca.pem"}}},
			{"mTLS", types.AuthMethodConfig{TLS: &types.TLSConfig{Use: true, CACert: "ca.pem", ClientCert: "cert.pem", ClientKey: "key.pem"}}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				authType, err := tc.amc.SelectedAuthType(false)
				require.NoError(t, err)
				opt, err := AdminOptionForAuthMethod(authType, tc.amc, true)
				require.NoError(t, err)
				cfg := AdminConfig{}
				opt(&cfg)
				assert.True(t, cfg.insecureSkipTLSVerify, "skipTLSVerify must reach %s", tc.name)
				assert.Equal(t, "ca.pem", cfg.caCertFile, "ca_cert must reach %s", tc.name)
				assert.False(t, cfg.disableTLS, "%s must stay TLS-enabled", tc.name)
			})
		}
	})

	// SASL/PLAIN with NO ca_cert is cleartext SASL_PLAINTEXT; skipTLSVerify is
	// irrelevant there and TLS stays disabled.
	t.Run("SASL/PLAIN without ca_cert stays plaintext regardless of skipTLSVerify", func(t *testing.T) {
		opt, err := AdminOptionForAuthMethod(types.AuthTypeSASLPlain, types.AuthMethodConfig{
			SASLPlain: &types.SASLPlainConfig{Use: true, Username: "u", Password: "p"},
		}, true)
		require.NoError(t, err)
		cfg := AdminConfig{}
		opt(&cfg)
		assert.True(t, cfg.disableTLS)
	})
}
