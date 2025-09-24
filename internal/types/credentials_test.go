package types

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCredentials(t *testing.T) {
	tests := []struct {
		name           string
		setupFile      func() string
		expectedError  bool
		expectedErrors int
	}{
		{
			name: "valid credentials file",
			setupFile: func() string {
				content := `
regions:
- name: us-east-1
  clusters:
  - name: test-cluster
    arn: arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012-1
    auth_method:
      iam:
        use: true
`
				tmpFile := createTempFile(t, content)
				return tmpFile
			},
			expectedError: false,
		},
		{
			name: "file not found",
			setupFile: func() string {
				return "/nonexistent/file.yaml"
			},
			expectedError:  true,
			expectedErrors: 1,
		},
		{
			name: "invalid YAML",
			setupFile: func() string {
				content := `invalid: yaml: content: [`
				tmpFile := createTempFile(t, content)
				return tmpFile
			},
			expectedError:  true,
			expectedErrors: 1,
		},
		{
			name: "multiple auth methods enabled",
			setupFile: func() string {
				content := `
regions:
- name: us-east-1
  clusters:
  - name: test-cluster
    arn: arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012-1
    auth_method:
      iam:
        use: true
	  tls:
	    use: true
`
				tmpFile := createTempFile(t, content)
				return tmpFile
			},
			expectedError:  true,
			expectedErrors: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := tt.setupFile()
			creds, errs := NewCredentials(filePath)

			if tt.expectedError {
				assert.Nil(t, creds)
				assert.NotEmpty(t, errs)
				if tt.expectedErrors > 0 {
					assert.Len(t, errs, tt.expectedErrors)
				}
			} else {
				assert.NotNil(t, creds)
				assert.Empty(t, errs)
			}
		})
	}
}

func TestCredentials_Validate(t *testing.T) {
	tests := []struct {
		name           string
		credentials    Credentials
		expectedValid  bool
		expectedErrors int
	}{
		{
			name: "valid single auth method",
			credentials: Credentials{
				Regions: []RegionEntry{
					{
						Name: "us-east-1",
						Clusters: []ClusterEntry{
							{
								Name: "test-cluster",
								Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012-1",
								AuthMethod: AuthMethodConfig{
									IAM: &IAMConfig{Use: true},
								},
							},
						},
					},
				},
			},
			expectedValid:  true,
			expectedErrors: 0,
		},
		{
			name: "multiple auth methods selected for a cluster is not allowed",
			credentials: Credentials{
				Regions: []RegionEntry{
					{
						Name: "us-east-1",
						Clusters: []ClusterEntry{
							{
								Name: "test-cluster",
								Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012-1",
								AuthMethod: AuthMethodConfig{
									IAM: &IAMConfig{Use: true},
									TLS: &TLSConfig{Use: true},
								},
							},
						},
					},
				},
			},
			expectedValid:  false,
			expectedErrors: 1,
		},
		{
			name: "no auth methods enabled is valid - means skip this cluster",
			credentials: Credentials{
				Regions: []RegionEntry{
					{
						Name: "us-east-1",
						Clusters: []ClusterEntry{
							{
								Name:       "test-cluster",
								Arn:        "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012-1",
								AuthMethod: AuthMethodConfig{},
							},
						},
					},
				},
			},
			expectedValid:  true,
			expectedErrors: 0,
		},
		{
			name: "multiple clusters with mixed validity",
			credentials: Credentials{
				Regions: []RegionEntry{
					{
						Name: "us-east-1",
						Clusters: []ClusterEntry{
							{
								Name: "test-cluster",
								Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012-1",
								AuthMethod: AuthMethodConfig{
									IAM: &IAMConfig{Use: true},
								},
							},
							{
								Name: "invalid-cluster-with-two-auth",
								Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster-2/12345678-1234-1234-1234-123456789012-2",
								AuthMethod: AuthMethodConfig{
									IAM: &IAMConfig{Use: true},
									TLS: &TLSConfig{Use: true},
								},
							},
						},
					},
				},
			},
			expectedValid:  false,
			expectedErrors: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid, errs := tt.credentials.Validate()
			assert.Equal(t, tt.expectedValid, valid)
			assert.Len(t, errs, tt.expectedErrors)
		})
	}
}

func TestClusterEntry_GetAuthMethods(t *testing.T) {
	tests := []struct {
		name            string
		clusterEntry    ClusterEntry
		expectedMethods []AuthType
	}{
		{
			name: "no auth methods enabled",
			clusterEntry: ClusterEntry{
				AuthMethod: AuthMethodConfig{},
			},
			expectedMethods: []AuthType{},
		},
		{
			name: "unauthenticated TLS enabled",
			clusterEntry: ClusterEntry{
				AuthMethod: AuthMethodConfig{
					UnauthenticatedTLS: &UnauthenticatedTLSConfig{Use: true},
				},
			},
			expectedMethods: []AuthType{AuthTypeUnauthenticatedTLS},
		},
		{
			name: "unauthenticated plaintext enabled",
			clusterEntry: ClusterEntry{
				AuthMethod: AuthMethodConfig{
					UnauthenticatedPlaintext: &UnauthenticatedPlaintextConfig{Use: true},
				},
			},
			expectedMethods: []AuthType{AuthTypeUnauthenticatedPlaintext},
		},
		{
			name: "IAM enabled",
			clusterEntry: ClusterEntry{
				AuthMethod: AuthMethodConfig{
					IAM: &IAMConfig{Use: true},
				},
			},
			expectedMethods: []AuthType{AuthTypeIAM},
		},
		{
			name: "SASL/SCRAM enabled",
			clusterEntry: ClusterEntry{
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: true},
				},
			},
			expectedMethods: []AuthType{AuthTypeSASLSCRAM},
		},
		{
			name: "TLS enabled",
			clusterEntry: ClusterEntry{
				AuthMethod: AuthMethodConfig{
					TLS: &TLSConfig{Use: true},
				},
			},
			expectedMethods: []AuthType{AuthTypeTLS},
		},
		{
			name: "multiple methods enabled",
			clusterEntry: ClusterEntry{
				AuthMethod: AuthMethodConfig{
					IAM: &IAMConfig{Use: true},
					TLS: &TLSConfig{Use: true},
				},
			},
			expectedMethods: []AuthType{AuthTypeIAM, AuthTypeTLS},
		},
		{
			name: "auth method configured but not enabled",
			clusterEntry: ClusterEntry{
				AuthMethod: AuthMethodConfig{
					IAM: &IAMConfig{Use: false},
				},
			},
			expectedMethods: []AuthType{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			methods := tt.clusterEntry.GetAuthMethods()
			assert.Equal(t, tt.expectedMethods, methods)
		})
	}
}

func TestClusterEntry_GetSelectedAuthType(t *testing.T) {
	tests := []struct {
		name          string
		clusterEntry  ClusterEntry
		expectedType  AuthType
		expectedError bool
	}{
		{
			name: "unauthenticated TLS auth type",
			clusterEntry: ClusterEntry{
				AuthMethod: AuthMethodConfig{
					UnauthenticatedTLS: &UnauthenticatedTLSConfig{Use: true},
				},
			},
			expectedType:  AuthTypeUnauthenticatedTLS,
			expectedError: false,
		},
		{
			name: "unauthenticated plaintext auth type",
			clusterEntry: ClusterEntry{
				AuthMethod: AuthMethodConfig{
					UnauthenticatedPlaintext: &UnauthenticatedPlaintextConfig{Use: true},
				},
			},
			expectedType:  AuthTypeUnauthenticatedPlaintext,
			expectedError: false,
		},
		{
			name: "IAM auth type",
			clusterEntry: ClusterEntry{
				AuthMethod: AuthMethodConfig{
					IAM: &IAMConfig{Use: true},
				},
			},
			expectedType:  AuthTypeIAM,
			expectedError: false,
		},
		{
			name: "SASL/SCRAM auth type",
			clusterEntry: ClusterEntry{
				AuthMethod: AuthMethodConfig{
					SASLScram: &SASLScramConfig{Use: true},
				},
			},
			expectedType:  AuthTypeSASLSCRAM,
			expectedError: false,
		},
		{
			name: "TLS auth type",
			clusterEntry: ClusterEntry{
				AuthMethod: AuthMethodConfig{
					TLS: &TLSConfig{Use: true},
				},
			},
			expectedType:  AuthTypeTLS,
			expectedError: false,
		},
		{
			name: "no auth method enabled",
			clusterEntry: ClusterEntry{
				AuthMethod: AuthMethodConfig{},
			},
			expectedType:  "",
			expectedError: true,
		},
		{
			name: "multiple auth methods enabled",
			clusterEntry: ClusterEntry{
				AuthMethod: AuthMethodConfig{
					IAM: &IAMConfig{Use: true},
					TLS: &TLSConfig{Use: true},
				},
			},
			expectedType:  AuthTypeIAM, // Should return the first one
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authType, err := tt.clusterEntry.GetSelectedAuthType()

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

func TestCredentials_ToYaml(t *testing.T) {
	creds := &Credentials{
		Regions: []RegionEntry{
			{
				Name: "us-east-1",
				Clusters: []ClusterEntry{
					{
						Name: "test-cluster",
						Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012-1",
						AuthMethod: AuthMethodConfig{
							IAM: &IAMConfig{Use: true},
						},
					},
				},
			},
		},
	}

	yamlData, err := creds.ToYaml()
	require.NoError(t, err)
	assert.NotEmpty(t, yamlData)

	// Verify it's valid YAML by unmarshaling it back
	var unmarshaled Credentials
	err = yaml.Unmarshal(yamlData, &unmarshaled)
	require.NoError(t, err)
	assert.Equal(t, creds.Regions, unmarshaled.Regions)
}

func TestCredentials_WriteToFile(t *testing.T) {
	creds := &Credentials{
		Regions: []RegionEntry{
			{
				Name: "us-east-1",
				Clusters: []ClusterEntry{
					{
						Name: "test-cluster",
						Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/12345678-1234-1234-1234-123456789012-1",
						AuthMethod: AuthMethodConfig{
							IAM: &IAMConfig{Use: true},
						},
					},
				},
			},
		},
	}

	tmpFile := filepath.Join(t.TempDir(), "test-creds.yaml")
	err := creds.WriteToFile(tmpFile)
	require.NoError(t, err)

	// Verify file was created and contains expected content
	_, err = os.Stat(tmpFile)
	require.NoError(t, err)

	// Read and verify content
	content, err := os.ReadFile(tmpFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "regions:")
	assert.Contains(t, string(content), "- name: us-east-1")
	assert.Contains(t, string(content), "clusters:")
	assert.Contains(t, string(content), "- name: test-cluster")
}

func TestCredentials_WriteToFile_InvalidPath(t *testing.T) {
	creds := &Credentials{
		Regions: []RegionEntry{},
	}

	// Try to write to a directory (should fail)
	err := creds.WriteToFile("/tmp")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write YAML file")
}

func TestAuthMethodConfigs(t *testing.T) {
	t.Run("UnauthenticatedTLSConfig", func(t *testing.T) {
		config := &UnauthenticatedTLSConfig{Use: true}
		assert.True(t, config.Use)
	})

	t.Run("UnauthenticatedPlaintextConfig", func(t *testing.T) {
		config := &UnauthenticatedPlaintextConfig{Use: true}
		assert.True(t, config.Use)
	})

	t.Run("IAMConfig", func(t *testing.T) {
		config := &IAMConfig{Use: true}
		assert.True(t, config.Use)
	})

	t.Run("TLSConfig", func(t *testing.T) {
		config := &TLSConfig{
			Use:        true,
			CACert:     "ca-cert",
			ClientCert: "client-cert",
			ClientKey:  "client-key",
		}
		assert.True(t, config.Use)
		assert.Equal(t, "ca-cert", config.CACert)
		assert.Equal(t, "client-cert", config.ClientCert)
		assert.Equal(t, "client-key", config.ClientKey)
	})

	t.Run("SASLScramConfig", func(t *testing.T) {
		config := &SASLScramConfig{
			Use:      true,
			Username: "testuser",
			Password: "testpass",
		}
		assert.True(t, config.Use)
		assert.Equal(t, "testuser", config.Username)
		assert.Equal(t, "testpass", config.Password)
	})
}

func TestCredentials_Integration(t *testing.T) {
	// Test a complete round-trip: create, marshal, write, read, unmarshal
	originalCreds := &Credentials{
		Regions: []RegionEntry{
			{
				Name: "us-east-1",
				Clusters: []ClusterEntry{
					{
						Name: "cluster1",
						Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/cluster1/12345678-1234-1234-1234-123456789012-1",
						AuthMethod: AuthMethodConfig{
							IAM: &IAMConfig{Use: true},
						},
					},
					{
						Name: "cluster2",
						Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/cluster2/12345678-1234-1234-1234-123456789012-2",
						AuthMethod: AuthMethodConfig{
							TLS: &TLSConfig{
								Use:        true,
								CACert:     "ca-cert",
								ClientCert: "client-cert",
								ClientKey:  "client-key",
							},
						},
					},
				},
			},
			{
				Name: "us-west-2",
				Clusters: []ClusterEntry{
					{
						Name: "cluster3",
						Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/cluster3/12345678-1234-1234-1234-123456789012-3",
					},
					{
						Name: "cluster3",
						Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/cluster3/12345678-1234-1234-1234-123456789012-3",
						AuthMethod: AuthMethodConfig{
							SASLScram: &SASLScramConfig{
								Use:      true,
								Username: "user",
								Password: "pass",
							},
						},
					},
				},
			},
		},
	}

	// Write to file
	tmpFile := filepath.Join(t.TempDir(), "integration-test.yaml")
	err := originalCreds.WriteToFile(tmpFile)
	require.NoError(t, err)

	// Read back from file
	readCreds, errs := NewCredentials(tmpFile)
	require.Empty(t, errs)
	require.NotNil(t, readCreds)

	// Verify structure
	assert.Equal(t, len(originalCreds.Regions), len(readCreds.Regions))
	assert.Equal(t, originalCreds.Regions, readCreds.Regions)

	// Verify validation passes
	valid, errs := readCreds.Validate()
	assert.True(t, valid)
	assert.Empty(t, errs)
}

// Helper function to create temporary files for testing
func createTempFile(t *testing.T, content string) string {
	tmpFile, err := os.CreateTemp("", "test-creds-*.yaml")
	require.NoError(t, err)
	defer tmpFile.Close()

	_, err = tmpFile.WriteString(content)
	require.NoError(t, err)

	return tmpFile.Name()
}
