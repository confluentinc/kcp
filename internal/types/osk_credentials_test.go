package types

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOSKCredentials_Validate_Valid(t *testing.T) {
	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092", "broker2:9092"},
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{
						Use:      true,
						Username: "admin",
						Password: "secret",
					},
				},
			},
		},
	}

	valid, errs := creds.Validate()
	if !valid {
		t.Errorf("expected valid credentials, got errors: %v", errs)
	}
}

func TestOSKCredentials_Validate_DuplicateID(t *testing.T) {
	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092"},
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: true, Username: "u", Password: "p"},
				},
			},
			{
				ID:               "prod-kafka-01", // Duplicate!
				BootstrapServers: []string{"broker2:9092"},
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: true, Username: "u", Password: "p"},
				},
			},
		},
	}

	valid, errs := creds.Validate()
	if valid {
		t.Error("expected validation to fail for duplicate IDs")
	}
	if len(errs) == 0 {
		t.Error("expected errors for duplicate IDs")
	}
}

func TestOSKCredentials_Validate_InvalidBootstrapServer(t *testing.T) {
	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"invalid-server"}, // Missing port
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: true, Username: "u", Password: "p"},
				},
			},
		},
	}

	valid, _ := creds.Validate()
	if valid {
		t.Error("expected validation to fail for invalid bootstrap server")
	}
}

func TestOSKCredentials_Validate_NoAuthMethod(t *testing.T) {
	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092"},
				AuthMethod:       AuthMethodConfig{}, // No auth method
			},
		},
	}

	valid, errs := creds.Validate()
	if !valid {
		t.Errorf("expected validation to pass when no auth method is enabled (cluster will be skipped during scan), got errors: %v", errs)
	}
}

func TestOSKCredentials_Validate_MultipleAuthMethods(t *testing.T) {
	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092"},
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: true, Username: "u", Password: "p"},
					TLS:       &TLSConfig{Use: true, ClientCert: "cert", ClientKey: "key"},
				},
			},
		},
	}

	valid, _ := creds.Validate()
	if valid {
		t.Error("expected validation to fail when multiple auth methods are enabled")
	}
}

func TestOSKCredentials_Validate_EmptyClusters(t *testing.T) {
	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{},
	}

	valid, errs := creds.Validate()
	if valid {
		t.Error("expected validation to fail when no clusters are defined")
	}
	if len(errs) == 0 {
		t.Error("expected errors when no clusters are defined")
	}
}

func TestOSKCredentials_Validate_MissingID(t *testing.T) {
	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "", // Missing ID
				BootstrapServers: []string{"broker1:9092"},
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: true, Username: "u", Password: "p"},
				},
			},
		},
	}

	valid, _ := creds.Validate()
	if valid {
		t.Error("expected validation to fail when ID is missing")
	}
}

func TestOSKCredentials_Validate_EmptyBootstrapServers(t *testing.T) {
	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{}, // Empty bootstrap servers
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: true, Username: "u", Password: "p"},
				},
			},
		},
	}

	valid, _ := creds.Validate()
	if valid {
		t.Error("expected validation to fail when bootstrap servers are empty")
	}
}

func TestOSKCredentials_Validate_SASLScramMissingUsername(t *testing.T) {
	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092"},
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: true, Username: "", Password: "p"}, // Missing username
				},
			},
		},
	}

	valid, _ := creds.Validate()
	if valid {
		t.Error("expected validation to fail when SASL/SCRAM username is missing")
	}
}

func TestOSKCredentials_Validate_SASLScramMissingPassword(t *testing.T) {
	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092"},
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: true, Username: "u", Password: ""}, // Missing password
				},
			},
		},
	}

	valid, _ := creds.Validate()
	if valid {
		t.Error("expected validation to fail when SASL/SCRAM password is missing")
	}
}

func TestOSKCredentials_Validate_TLSMissingClientCert(t *testing.T) {
	// Create temp files for testing
	tmpDir := t.TempDir()
	keyFile := filepath.Join(tmpDir, "client.key")
	require.NoError(t, os.WriteFile(keyFile, []byte("key"), 0644))

	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092"},
				AuthMethod: AuthMethodConfig{
					TLS: &TLSConfig{Use: true, ClientCert: "", ClientKey: keyFile}, // Missing client cert
				},
			},
		},
	}

	valid, _ := creds.Validate()
	if valid {
		t.Error("expected validation to fail when TLS client cert is missing")
	}
}

func TestOSKCredentials_Validate_TLSMissingClientKey(t *testing.T) {
	// Create temp files for testing
	tmpDir := t.TempDir()
	certFile := filepath.Join(tmpDir, "client.crt")
	require.NoError(t, os.WriteFile(certFile, []byte("cert"), 0644))

	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092"},
				AuthMethod: AuthMethodConfig{
					TLS: &TLSConfig{Use: true, ClientCert: certFile, ClientKey: ""}, // Missing client key
				},
			},
		},
	}

	valid, _ := creds.Validate()
	if valid {
		t.Error("expected validation to fail when TLS client key is missing")
	}
}

func TestOSKCredentials_Validate_TLSClientCertNotFound(t *testing.T) {
	// Create temp files for testing
	tmpDir := t.TempDir()
	keyFile := filepath.Join(tmpDir, "client.key")
	require.NoError(t, os.WriteFile(keyFile, []byte("key"), 0644))

	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092"},
				AuthMethod: AuthMethodConfig{
					TLS: &TLSConfig{Use: true, ClientCert: "/nonexistent/cert.crt", ClientKey: keyFile},
				},
			},
		},
	}

	valid, _ := creds.Validate()
	if valid {
		t.Error("expected validation to fail when TLS client cert file does not exist")
	}
}

func TestOSKCredentials_Validate_TLSClientKeyNotFound(t *testing.T) {
	// Create temp files for testing
	tmpDir := t.TempDir()
	certFile := filepath.Join(tmpDir, "client.crt")
	require.NoError(t, os.WriteFile(certFile, []byte("cert"), 0644))

	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092"},
				AuthMethod: AuthMethodConfig{
					TLS: &TLSConfig{Use: true, ClientCert: certFile, ClientKey: "/nonexistent/key.key"},
				},
			},
		},
	}

	valid, _ := creds.Validate()
	if valid {
		t.Error("expected validation to fail when TLS client key file does not exist")
	}
}

func TestOSKCredentials_Validate_UnauthenticatedTLS(t *testing.T) {
	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092"},
				AuthMethod: AuthMethodConfig{
					UnauthenticatedTLS: &UnauthenticatedTLSConfig{Use: true},
				},
			},
		},
	}

	valid, errs := creds.Validate()
	if !valid {
		t.Errorf("expected valid credentials for unauthenticated TLS, got errors: %v", errs)
	}
}

func TestOSKCredentials_Validate_UnauthenticatedPlaintext(t *testing.T) {
	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092"},
				AuthMethod: AuthMethodConfig{
					UnauthenticatedPlaintext: &UnauthenticatedPlaintextConfig{Use: true},
				},
			},
		},
	}

	valid, errs := creds.Validate()
	if !valid {
		t.Errorf("expected valid credentials for unauthenticated plaintext, got errors: %v", errs)
	}
}

func TestOSKClusterAuth_GetAuthMethods(t *testing.T) {
	tests := []struct {
		name            string
		clusterAuth     OSKClusterAuth
		expectedMethods []AuthType
	}{
		{
			name: "no auth methods enabled",
			clusterAuth: OSKClusterAuth{
				AuthMethod: AuthMethodConfig{},
			},
			expectedMethods: []AuthType{},
		},
		{
			name: "SASL/SCRAM enabled",
			clusterAuth: OSKClusterAuth{
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: true},
				},
			},
			expectedMethods: []AuthType{AuthTypeSASLSCRAM},
		},
		{
			name: "TLS enabled",
			clusterAuth: OSKClusterAuth{
				AuthMethod: AuthMethodConfig{
					TLS: &TLSConfig{Use: true},
				},
			},
			expectedMethods: []AuthType{AuthTypeTLS},
		},
		{
			name: "unauthenticated TLS enabled",
			clusterAuth: OSKClusterAuth{
				AuthMethod: AuthMethodConfig{
					UnauthenticatedTLS: &UnauthenticatedTLSConfig{Use: true},
				},
			},
			expectedMethods: []AuthType{AuthTypeUnauthenticatedTLS},
		},
		{
			name: "unauthenticated plaintext enabled",
			clusterAuth: OSKClusterAuth{
				AuthMethod: AuthMethodConfig{
					UnauthenticatedPlaintext: &UnauthenticatedPlaintextConfig{Use: true},
				},
			},
			expectedMethods: []AuthType{AuthTypeUnauthenticatedPlaintext},
		},
		{
			name: "multiple methods enabled (invalid but GetAuthMethods should return them)",
			clusterAuth: OSKClusterAuth{
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: true},
					TLS:       &TLSConfig{Use: true},
				},
			},
			expectedMethods: []AuthType{AuthTypeSASLSCRAM, AuthTypeTLS},
		},
		{
			name: "auth method configured but not enabled",
			clusterAuth: OSKClusterAuth{
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: false},
				},
			},
			expectedMethods: []AuthType{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			methods := tt.clusterAuth.GetAuthMethods()
			assert.Equal(t, tt.expectedMethods, methods)
		})
	}
}

func TestOSKClusterAuth_GetSelectedAuthType(t *testing.T) {
	tests := []struct {
		name          string
		clusterAuth   OSKClusterAuth
		expectedType  AuthType
		expectedError bool
	}{
		{
			name: "SASL/SCRAM auth type",
			clusterAuth: OSKClusterAuth{
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: true},
				},
			},
			expectedType:  AuthTypeSASLSCRAM,
			expectedError: false,
		},
		{
			name: "TLS auth type",
			clusterAuth: OSKClusterAuth{
				AuthMethod: AuthMethodConfig{
					TLS: &TLSConfig{Use: true},
				},
			},
			expectedType:  AuthTypeTLS,
			expectedError: false,
		},
		{
			name: "unauthenticated TLS auth type",
			clusterAuth: OSKClusterAuth{
				AuthMethod: AuthMethodConfig{
					UnauthenticatedTLS: &UnauthenticatedTLSConfig{Use: true},
				},
			},
			expectedType:  AuthTypeUnauthenticatedTLS,
			expectedError: false,
		},
		{
			name: "unauthenticated plaintext auth type",
			clusterAuth: OSKClusterAuth{
				AuthMethod: AuthMethodConfig{
					UnauthenticatedPlaintext: &UnauthenticatedPlaintextConfig{Use: true},
				},
			},
			expectedType:  AuthTypeUnauthenticatedPlaintext,
			expectedError: false,
		},
		{
			name: "no auth method enabled",
			clusterAuth: OSKClusterAuth{
				AuthMethod: AuthMethodConfig{},
			},
			expectedType:  "",
			expectedError: true,
		},
		{
			name: "multiple auth methods enabled returns first",
			clusterAuth: OSKClusterAuth{
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: true},
					TLS:       &TLSConfig{Use: true},
				},
			},
			expectedType:  AuthTypeSASLSCRAM,
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authType, err := tt.clusterAuth.GetSelectedAuthType()

			if tt.expectedError {
				assert.Error(t, err)
				assert.Empty(t, authType)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedType, authType)
			}
		})
	}
}

func TestNewOSKCredentialsFromFile_Valid(t *testing.T) {
	content := `
clusters:
- id: prod-kafka-01
  bootstrap_servers:
  - broker1:9092
  - broker2:9092
  auth_method:
    sasl_scram:
      use: true
      username: admin
      password: secret
`
	tmpFile := filepath.Join(t.TempDir(), "osk-credentials.yaml")
	require.NoError(t, os.WriteFile(tmpFile, []byte(content), 0644))

	creds, errs := NewOSKCredentialsFromFile(tmpFile)
	require.Empty(t, errs)
	require.NotNil(t, creds)

	assert.Len(t, creds.Clusters, 1)
	assert.Equal(t, "prod-kafka-01", creds.Clusters[0].ID)
	assert.Equal(t, []string{"broker1:9092", "broker2:9092"}, creds.Clusters[0].BootstrapServers)
}

func TestNewOSKCredentialsFromFile_FileNotFound(t *testing.T) {
	creds, errs := NewOSKCredentialsFromFile("/nonexistent/file.yaml")
	assert.Nil(t, creds)
	assert.NotEmpty(t, errs)
	assert.Len(t, errs, 1)
}

func TestNewOSKCredentialsFromFile_InvalidYAML(t *testing.T) {
	content := `invalid: yaml: content: [`
	tmpFile := filepath.Join(t.TempDir(), "invalid.yaml")
	require.NoError(t, os.WriteFile(tmpFile, []byte(content), 0644))

	creds, errs := NewOSKCredentialsFromFile(tmpFile)
	assert.Nil(t, creds)
	assert.NotEmpty(t, errs)
	assert.Len(t, errs, 1)
}

func TestNewOSKCredentialsFromFile_ValidationFailure(t *testing.T) {
	content := `
clusters:
- id: prod-kafka-01
  bootstrap_servers:
  - invalid-server
  auth_method:
    sasl_scram:
      use: true
      username: user
      password: pass
`
	tmpFile := filepath.Join(t.TempDir(), "invalid-creds.yaml")
	require.NoError(t, os.WriteFile(tmpFile, []byte(content), 0644))

	creds, errs := NewOSKCredentialsFromFile(tmpFile)
	assert.Nil(t, creds)
	assert.NotEmpty(t, errs)
}

func TestOSKCredentials_WriteToFile(t *testing.T) {
	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092", "broker2:9092"},
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{
						Use:      true,
						Username: "admin",
						Password: "secret",
					},
				},
				Metadata: OSKCredentialMetadata{
					Environment: "production",
					Location:    "us-east-1",
					Labels: map[string]string{
						"team": "platform",
					},
				},
			},
		},
	}

	tmpFile := filepath.Join(t.TempDir(), "test-osk-creds.yaml")
	err := creds.WriteToFile(tmpFile)
	require.NoError(t, err)

	// Verify file was created
	_, err = os.Stat(tmpFile)
	require.NoError(t, err)

	// Read back and verify
	readCreds, errs := NewOSKCredentialsFromFile(tmpFile)
	require.Empty(t, errs)
	require.NotNil(t, readCreds)

	assert.Equal(t, creds.Clusters[0].ID, readCreds.Clusters[0].ID)
	assert.Equal(t, creds.Clusters[0].BootstrapServers, readCreds.Clusters[0].BootstrapServers)
	assert.Equal(t, creds.Clusters[0].Metadata.Environment, readCreds.Clusters[0].Metadata.Environment)
}

func TestOSKCredentials_Validate_WithMetadata(t *testing.T) {
	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092"},
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: true, Username: "u", Password: "p"},
				},
				Metadata: OSKCredentialMetadata{
					Environment: "production",
					Location:    "us-east-1",
					Labels: map[string]string{
						"team":    "platform",
						"project": "migrations",
					},
				},
			},
		},
	}

	valid, errs := creds.Validate()
	if !valid {
		t.Errorf("expected valid credentials with metadata, got errors: %v", errs)
	}
}

func TestOSKCredentials_Validate_AllAuthMethodsDisabled(t *testing.T) {
	creds := &OSKCredentials{
		Clusters: []OSKClusterAuth{
			{
				ID:               "disabled-cluster",
				BootstrapServers: []string{"localhost:9092"},
				AuthMethod: AuthMethodConfig{
					SASLScram:                &SASLScramConfig{Use: false},
					TLS:                      &TLSConfig{Use: false},
					UnauthenticatedPlaintext: &UnauthenticatedPlaintextConfig{Use: false},
				},
			},
		},
	}

	valid, errs := creds.Validate()
	if !valid {
		t.Errorf("expected validation to pass when all auth methods are disabled (cluster will be skipped during scan), got errors: %v", errs)
	}

	// Verify GetSelectedAuthType returns error for disabled cluster
	authType, err := creds.Clusters[0].GetSelectedAuthType()
	if err == nil {
		t.Error("expected GetSelectedAuthType to fail when no auth methods enabled")
	}
	if authType != "" {
		t.Errorf("expected empty auth type, got: %s", authType)
	}
}

func TestOSKCredentials_Validate_BootstrapServerEdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		server string
		valid  bool
	}{
		{"valid with hostname and port", "broker1:9092", true},
		{"valid with IP and port", "192.168.1.1:9092", true},
		{"valid with FQDN and port", "broker1.example.com:9092", true},
		{"invalid - missing port", "broker1", false},
		{"invalid - empty string", "", false},
		{"invalid - only colon", ":", false},
		{"invalid - missing host", ":9092", false},
		{"invalid - missing port number", "broker1:", false},
		{"invalid - non-numeric port", "broker1:abc", false},
		{"invalid - multiple colons", "broker1:9092:extra", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds := &OSKCredentials{
				Clusters: []OSKClusterAuth{
					{
						ID:               "test-cluster",
						BootstrapServers: []string{tt.server},
						AuthMethod: AuthMethodConfig{
							SASLScram: &SASLScramConfig{Use: true, Username: "u", Password: "p"},
						},
					},
				},
			}

			valid, _ := creds.Validate()
			if tt.valid && !valid {
				t.Errorf("expected server '%s' to be valid, but validation failed", tt.server)
			} else if !tt.valid && valid {
				t.Errorf("expected server '%s' to be invalid, but validation passed", tt.server)
			}
		})
	}
}
